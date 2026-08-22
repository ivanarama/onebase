package ui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Чтение регистра накопления из DSL: после проведения документа
// РегистрыНакопления.X.Движения()/Остатки()/ВыбратьПоРегистратору(Док)
// возвращают записанные движения.
func TestAccumRegProxy_ReadSide(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name:    "ПоступлениеТоваров",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	onPostSrc := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Дв = Движения.ОстаткиТоваров.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Количество = Стр.Количество;
  КонецЦикла;
КонецПроцедуры`
	prog := mustParse(t, onPostSrc)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{"ПоступлениеТоваров": prog},
		Registers: []*metadata.Register{reg},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	// Создаём и проводим документ с двумя строками.
	docsRoot := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := docsRoot.Get("ПоступлениеТоваров").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "ПОС-001")
	tp := w.Get("Товары").(*tpProxy)
	r1 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r1.Set("Номенклатура", "Тумбочка")
	r1.Set("Количество", float64(100))
	r2 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r2.Set("Номенклатура", "Стул")
	r2.Set("Количество", float64(50))
	w.CallMethod("записать", nil)
	docRef := w.CallMethod("провести", nil).(*interpreter.Ref)

	// Читаем регистр.
	regRoot := newAccumRegsRoot(s, interpreter.NewTxState(ctx))
	rp := regRoot.Get("ОстаткиТоваров").(*accumRegProxy)

	moves := rp.CallMethod("движения", nil).(*interpreter.Array)
	if got := moves.CallMethod("количество", nil); got != float64(2) {
		t.Errorf("Движения: ожидалось 2, got %v", got)
	}

	bal := rp.CallMethod("остатки", nil).(*interpreter.Array)
	if got := bal.CallMethod("количество", nil); got != float64(2) {
		t.Errorf("Остатки: ожидалось 2 строки, got %v", got)
	}

	byRec := rp.CallMethod("выбратьпорегистратору", []any{docRef}).(*interpreter.Array)
	if got := byRec.CallMethod("количество", nil); got != float64(2) {
		t.Errorf("ВыбратьПоРегистратору: ожидалось 2, got %v", got)
	}

	// Неизвестный регистр → nil.
	if v := regRoot.Get("НетТакого"); v != nil {
		t.Errorf("неизвестный регистр → nil, got %v", v)
	}
}
