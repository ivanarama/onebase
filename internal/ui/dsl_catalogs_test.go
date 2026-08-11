package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// setupCatTest поднимает SQLite + Server с фабрикой объектов справочников.
// Справочник Клиенты: ТЧ Контакты + хук ПриЗаписи, который собирает
// нормализованное поле из строк ТЧ (проверка, что хук видит ТЧ через this).
func setupCatTest(t *testing.T) (context.Context, *storage.DB, *Server, *interpreter.CatalogProxy, *interpreter.TxState) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cat := &metadata.Entity{
		Name: "Клиенты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Сводка", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{Name: "Контакты", Fields: []metadata.Field{
				{Name: "Вид", Type: metadata.FieldTypeString},
				{Name: "Значение", Type: metadata.FieldTypeString},
			}},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}

	onWriteSrc := `Процедура ПриЗаписи()
  Сводка = "";
  Для Каждого Стр Из this.Контакты Цикл
    Сводка = Сводка + Стр.Значение + ";";
  КонецЦикла;
  this.Сводка = Сводка;
КонецПроцедуры`

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{cat},
		Programs: map[string]*ast.Program{"Клиенты": mustParse(t, onWriteSrc)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{
		Store:        db,
		Reg:          registry,
		Interp:       interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}

	txState := interpreter.NewTxState(ctx)
	root := interpreter.NewCatalogsRoot(txState, db, registry).
		WithObjectFactory(s.catObjectFactory(txState))
	proxy, ok := root.Get("Клиенты").(*interpreter.CatalogProxy)
	if !ok {
		t.Fatalf("Справочники.Клиенты → не *CatalogProxy")
	}
	return ctx, db, s, proxy, txState
}

// Создание справочника с ТЧ из DSL: строки ТЧ сохраняются, хук ПриЗаписи
// исполняется и видит табличную часть.
func TestCatWriter_CreateWithTableParts(t *testing.T) {
	ctx, db, _, proxy, _ := setupCatTest(t)

	rec := proxy.CallMethod("создать", nil)
	w, ok := rec.(*catWriter)
	if !ok {
		t.Fatalf("Создать → %T, ожидался *catWriter", rec)
	}
	w.Set("Наименование", "Тест Клиент")

	tp, ok := w.Get("Контакты").(*tpProxy)
	if !ok {
		t.Fatalf("Кл.Контакты → %T, ожидался *tpProxy", w.Get("Контакты"))
	}
	r1 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r1.Set("Вид", "Email")
	r1.Set("Значение", "a@b.ru")
	r2 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r2.Set("Вид", "Телефон")
	r2.Set("Значение", "79001112233")

	res := w.CallMethod("записать", nil)
	ref, ok := res.(*interpreter.Ref)
	if !ok {
		t.Fatalf("Записать → %T, ожидалась *Ref", res)
	}

	var tpCount int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM клиенты_контакты").Scan(&tpCount); err != nil {
		t.Fatal(err)
	}
	if tpCount != 2 {
		t.Fatalf("строк ТЧ в БД = %d, ожидалось 2", tpCount)
	}
	// Хук ПриЗаписи собрал Сводку из строк ТЧ.
	var summary string
	if err := db.QueryRow(ctx, "SELECT сводка FROM клиенты").Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if summary != "a@b.ru;79001112233;" {
		t.Fatalf("хук ПриЗаписи не отработал: Сводка = %q", summary)
	}

	// Ссылка.ПолучитьОбъект() → catWriter с загруженной ТЧ.
	objAny, err := proxy.LoadObject(ref.UUID)
	if err != nil {
		t.Fatal(err)
	}
	w2, ok := objAny.(*catWriter)
	if !ok {
		t.Fatalf("LoadObject → %T, ожидался *catWriter", objAny)
	}
	tp2 := w2.Get("Контакты").(*tpProxy)
	if got := tp2.CallMethod("количество", nil).(float64); got != 2 {
		t.Fatalf("после загрузки строк ТЧ = %v, ожидалось 2", got)
	}

	// Обновление: очистить ТЧ, дозаписать одну строку — в БД одна строка.
	tp2.CallMethod("очистить", nil)
	r3 := tp2.CallMethod("добавить", nil).(*interpreter.MapThis)
	r3.Set("Вид", "Email")
	r3.Set("Значение", "new@b.ru")
	if res := w2.CallMethod("записать", nil); res == nil {
		t.Fatal("повторная запись вернула nil")
	}
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM клиенты_контакты").Scan(&tpCount); err != nil {
		t.Fatal(err)
	}
	if tpCount != 1 {
		t.Fatalf("после обновления строк ТЧ = %d, ожидалась 1", tpCount)
	}
	var cnt int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM клиенты").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("записей справочника = %d, ожидалась 1 (обновление, не дубль)", cnt)
	}
}

// catWriter must join an explicit DSL transaction. Before this regression
// test, entityservice.Save tried to open a second SQLite transaction and
// blocked forever on the single connection.
func TestCatWriter_ExplicitTransactionCommitAndRollback(t *testing.T) {
	ctx, db, _, proxy, txState := setupCatTest(t)
	txFns := interpreter.NewTxFunctions(txState, db)
	callTx := func(name string) {
		t.Helper()
		fn := txFns[name].(interpreter.BuiltinFunc)
		if _, err := fn(nil, "", 0); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	callTx("НачатьТранзакцию")
	rolledBack := proxy.CallMethod("создать", nil).(*catWriter)
	rolledBack.Set("Наименование", "Откат")
	rolledBack.CallMethod("записать", nil)
	callTx("ОтменитьТранзакцию")
	var count int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM клиенты").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rollback left %d catalog rows", count)
	}
	if got := rolledBack.CallMethod("ЭтоНовый", nil); got != true {
		t.Fatalf("после rollback ЭтоНовый = %v, ожидалось true", got)
	}

	// Тот же writer должен снова пройти путь создания. Иначе ПриЗаписи видит
	// ЭтоНовый=false, а созданные им FK-строки не находят provisional parent.
	rolledBack.Set("Наименование", "Повтор после отката")
	callTx("НачатьТранзакцию")
	rolledBack.CallMethod("записать", nil)
	callTx("ЗафиксироватьТранзакцию")
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM клиенты").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("повторная запись после rollback оставила %d строк, ожидалась 1", count)
	}

	callTx("НачатьТранзакцию")
	committed := proxy.CallMethod("создать", nil).(*catWriter)
	committed.Set("Наименование", "Коммит")
	committed.CallMethod("записать", nil)
	callTx("ЗафиксироватьТранзакцию")
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM клиенты").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("commit left %d catalog rows, want 2", count)
	}
}

// Без фабрики поведение прежнее: Создать() возвращает CatalogRecordWriter.
func TestCatalogsRoot_WithoutFactoryKeepsLegacyWriter(t *testing.T) {
	ctx, db, _, _, _ := setupCatTest(t)
	_ = ctx

	registry := runtime.NewRegistry()
	cat := &metadata.Entity{Name: "Простой", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat}})
	if err := db.Migrate(context.Background(), []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}
	root := interpreter.NewCatalogsRoot(interpreter.NewTxState(context.Background()), db, registry)
	proxy := root.Get("Простой").(*interpreter.CatalogProxy)
	rec := proxy.CallMethod("создать", nil)
	if _, ok := rec.(*interpreter.CatalogRecordWriter); !ok {
		t.Fatalf("без фабрики Создать → %T, ожидался *CatalogRecordWriter", rec)
	}
}
