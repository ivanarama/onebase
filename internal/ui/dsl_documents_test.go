package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestDocWriter_DisplayNameUsesExplicitPresentationFallback(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Шифр", Type: metadata.FieldTypeString},
			{Name: "Тема", Type: metadata.FieldTypeString},
		},
		Presentation: []string{"Шифр", "Тема"},
	}
	newWriter := func(code, subject string) *docWriter {
		obj := runtime.NewObject(entity.Name, entity.Kind)
		obj.Fields["шифр"] = code
		obj.Fields["тема"] = subject
		obj.Fields["номер"] = "ЗК-LEGACY"
		return &docWriter{entity: entity, obj: obj}
	}

	if got := newWriter("EXT-1", "Поставка").displayName(); got != "EXT-1" {
		t.Fatalf("primary displayName = %q, ожидалось EXT-1", got)
	}
	if got := newWriter(" ", "Поставка").displayName(); got != "Поставка" {
		t.Fatalf("fallback displayName = %q, ожидалось Поставка", got)
	}
	empty := newWriter("", "")
	if got, want := empty.displayName(), entity.Name+":"+empty.obj.ID.String()[:8]; got != want {
		t.Fatalf("empty explicit displayName = %q, ожидался id fallback %q", got, want)
	}

	legacyEntity := &metadata.Entity{Name: "Заказ", Kind: metadata.KindDocument}
	legacyObj := runtime.NewObject(legacyEntity.Name, legacyEntity.Kind)
	legacyObj.Fields["номер"] = "ЗК-001"
	if got := (&docWriter{entity: legacyEntity, obj: legacyObj}).displayName(); got != "ЗК-001" {
		t.Fatalf("legacy displayName = %q, ожидалось ЗК-001", got)
	}
}

// создание/проведение документов из обработки.
// Полный сценарий: Документы.X.Создать() → заполнить шапку и ТЧ →
// Записать() → Провести(). Проверяем что документ, его ТЧ и движения
// регистра реально записались.
func TestDocsRoot_CreateWritePost(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Документ ПоступлениеТоваров с ТЧ Товары.
	doc := &metadata.Entity{
		Name:    "ПоступлениеТоваров",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
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
	if err := db.EnsureExchangeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	plan := &metadata.ExchangePlan{
		Name: "Документы", Content: []string{"Документ.ПоступлениеТоваров"},
		Nodes: []metadata.ExchangeNode{{Code: "center"}, {Code: "fil01"}},
	}
	plan.Normalize()
	if err := db.SaveExchangeThisNode(ctx, plan.Name, "center"); err != nil {
		t.Fatal(err)
	}

	// OnPost: пишем приход в регистр по строкам ТЧ.
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
	registry.LoadExchangePlans([]*metadata.ExchangePlan{plan})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	s := &Server{
		store:    db,
		reg:      registry,
		interp:   interp,
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	s.entitySvc = s.newEntityService(nil)

	// Сценарий обработки: создать документ, заполнить, записать, провести.
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	proxy := root.Get("ПоступлениеТоваров")
	if proxy == nil {
		t.Fatal("Документы.ПоступлениеТоваров → nil")
	}
	dp, ok := proxy.(*docProxy)
	if !ok {
		t.Fatalf("ожидался *docProxy, получили %T", proxy)
	}

	rec := dp.CallMethod("создать", nil)
	w, ok := rec.(*docWriter)
	if !ok {
		t.Fatalf("Создать → %T, ожидался *docWriter", rec)
	}
	w.Set("Номер", "ПОС-001")

	// Док.Товары.Добавить()
	tpAny := w.Get("Товары")
	tp, ok := tpAny.(*tpProxy)
	if !ok {
		t.Fatalf("Док.Товары → %T, ожидался *tpProxy", tpAny)
	}
	row1 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	row1.Set("Номенклатура", "Тумбочка")
	row1.Set("Количество", float64(100))
	row2 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	row2.Set("Номенклатура", "Стул")
	row2.Set("Количество", float64(50))

	// Записать + Провести
	if res := w.CallMethod("записать", nil); res == nil {
		t.Fatal("Записать вернул nil")
	}
	if res := w.CallMethod("провести", nil); res == nil {
		t.Fatal("Провести вернул nil")
	}

	// Проверки: документ записан
	var docCount int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM поступлениетоваров").Scan(&docCount); err != nil {
		t.Fatal(err)
	}
	if docCount != 1 {
		t.Errorf("ожидался 1 документ, получили %d", docCount)
	}
	// ТЧ записана
	var tpCount int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM поступлениетоваров_товары").Scan(&tpCount); err != nil {
		t.Fatal(err)
	}
	if tpCount != 2 {
		t.Errorf("ожидалось 2 строки ТЧ, получили %d", tpCount)
	}
	// движения регистра записаны
	var movCount int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&movCount); err != nil {
		t.Fatal(err)
	}
	if movCount != 2 {
		t.Errorf("ожидалось 2 движения, получили %d", movCount)
	}
	// posted = true
	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров LIMIT 1").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Error("документ не помечен проведённым")
	}

	// Финальный Upsert после OnPost повышает версию. Очередь должна содержать
	// именно её, иначе BuildPackage пропустит документ как stale change.
	version, err := db.EntityVersion(ctx, doc.Name, w.obj.ID)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := db.PendingExchangeChanges(ctx, plan.Name, "fil01")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Version != version {
		t.Fatalf("очередь=%+v, финальная версия=%d", changes, version)
	}
	data, err := exchange.BuildPackage(ctx, db, registry, plan, "fil01")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := exchange.ParsePackage(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Objects) != 1 || !pkg.Objects[0].Posted || pkg.Objects[0].Version != version {
		t.Fatalf("DSL-проведение не попало в пакет: %+v", pkg.Objects)
	}
}

func TestDocsRoot_DirectPostCreatesSingleVersion(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "version.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	s := &Server{store: db, reg: registry, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	writer := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy).
		CallMethod("создать", nil).(*docWriter)
	writer.Set("Номер", "З-1")
	if err := writer.conduct(); err != nil {
		t.Fatal(err)
	}
	if version, err := db.EntityVersion(ctx, doc.Name, writer.obj.ID); err != nil || version != 1 {
		t.Fatalf("new direct post version = %d, err=%v, want 1", version, err)
	}

	writer.Set("Номер", "З-2")
	if err := writer.conduct(); err != nil {
		t.Fatal(err)
	}
	if version, err := db.EntityVersion(ctx, doc.Name, writer.obj.ID); err != nil || version != 2 {
		t.Fatalf("second logical post version = %d, err=%v, want 2", version, err)
	}
}

func TestDocsRoot_PostRollsBackOnPostSideEffectsWhenFinalWriteFails(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "post-atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	doc := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	child := &metadata.Entity{
		Name: "Событие", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	missingRegister := &metadata.Register{
		Name:      "НеСозданныйРегистр",
		Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	// Deliberately do not migrate the register: saving its movement fails after
	// OnPost has already created a child catalog record.
	if err := db.Migrate(ctx, []*metadata.Entity{doc, child}); err != nil {
		t.Fatal(err)
	}
	onPost := `Процедура ОбработкаПроведения()
  Соб = Справочники.Событие.Создать();
  Соб.Наименование = "побочная запись";
  Соб.Записать();
  Дв = Движения.НеСозданныйРегистр.Добавить();
  Дв.Количество = 1;
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc, child},
		Programs:  map[string]*ast.Program{doc.Name: mustParse(t, onPost)},
		Registers: []*metadata.Register{missingRegister},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	writer := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy).
		CallMethod("создать", nil).(*docWriter)
	writer.Set("Номер", "З-FAIL")
	if err := writer.conduct(); err == nil {
		t.Fatal("post unexpectedly succeeded without register table")
	}
	parents, err := db.List(ctx, doc.Name, doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	children, err := db.List(ctx, child.Name, child, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 0 || len(children) != 0 {
		t.Fatalf("failed post left parent=%d child=%d", len(parents), len(children))
	}
	if writer.saved || writer.expectedVersion != nil {
		t.Fatalf("rolled-back writer state: saved=%v version=%v", writer.saved, writer.expectedVersion)
	}
}

func TestDocsRoot_LoadedWritersUseOptimisticLock(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "dsl-lock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	task := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Состояние", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{task}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, task.Name, id, map[string]any{"Номер": "ЗД-1", "Состояние": "Открыта"}, task); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{task}})
	s := &Server{store: db, reg: registry, interp: interpreter.New(), lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	proxy := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(task.Name).(*docProxy)

	firstAny, err := proxy.LoadObject(id.String())
	if err != nil {
		t.Fatal(err)
	}
	secondAny, err := proxy.LoadObject(id.String())
	if err != nil {
		t.Fatal(err)
	}
	first, second := firstAny.(*docWriter), secondAny.(*docWriter)
	first.Set("Состояние", "Выполнена")
	if err := first.write(); err != nil {
		t.Fatal(err)
	}
	second.Set("Состояние", "Отклонена")
	if err := second.write(); !errors.Is(err, storage.ErrVersionConflict) {
		t.Fatalf("stale DSL writer error = %v, want ErrVersionConflict", err)
	}
	row, err := db.GetByID(ctx, task.Name, id, task)
	if err != nil {
		t.Fatal(err)
	}
	if row["Состояние"] != "Выполнена" {
		t.Fatalf("stale writer overwrote task: %#v", row)
	}
}

// Удаление проведённого документа из обработки должно очищать его движения
// по регистрам — иначе остаются осиротевшие движения (recorder на удалённый
// документ), раздувающие остатки. Регрессия на накопление движений при
// повторных запусках обработки заполнения демоданных.
func TestDocsRoot_DeleteClearsMovements(t *testing.T) {
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
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{"ПоступлениеТоваров": mustParse(t, onPostSrc)},
		Registers: []*metadata.Register{reg},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("ПоступлениеТоваров").(*docProxy)

	// Создать → заполнить ТЧ → провести.
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "ПОС-001")
	tp := w.Get("Товары").(*tpProxy)
	r := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r.Set("Номенклатура", "Тумбочка")
	r.Set("Количество", float64(100))
	w.CallMethod("провести", nil)

	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 1 {
		t.Fatalf("до удаления ожидалось 1 движение, получили %d", mov)
	}

	// Удалить документ из обработки → движения должны исчезнуть.
	ref := dp.CallMethod("найтипономеру", []any{"ПОС-001"}).(*interpreter.Ref)
	dp.CallMethod("удалить", []any{ref})

	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 0 {
		t.Errorf("после удаления документа движений должно быть 0, получили %d (осиротевшие)", mov)
	}
	var docCnt int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM поступлениетоваров").Scan(&docCnt); err != nil {
		t.Fatal(err)
	}
	if docCnt != 0 {
		t.Errorf("документ не удалён: осталось %d", docCnt)
	}
}

// Документы.X.НайтиПоНомеру() находит документ по номеру и возвращает
// ссылку, по которой работает Удалить() (и через менеджер, и напрямую).
func TestDocsRoot_FindByNumberAndDelete(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "ЗаказПокупателя",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	s := &Server{store: db, reg: registry, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("ЗаказПокупателя").(*docProxy)

	// Создаём два документа.
	for _, num := range []string{"ЗП-001", "ЗП-002"} {
		w := dp.CallMethod("создать", nil).(*docWriter)
		w.Set("Номер", num)
		w.CallMethod("записать", nil)
	}

	// НайтиПоНомеру: существующий номер.
	res := dp.CallMethod("найтипономеру", []any{"ЗП-001"})
	ref, ok := res.(*interpreter.Ref)
	if !ok {
		t.Fatalf("НайтиПоНомеру → %T, ожидался *interpreter.Ref", res)
	}
	if ref.Name != "ЗП-001" || ref.Type != "ЗаказПокупателя" {
		t.Errorf("неверная ссылка: name=%q type=%q", ref.Name, ref.Type)
	}

	// НайтиПоНомеру: несуществующий номер → nil.
	if v := dp.CallMethod("найтипономеру", []any{"НЕТ"}); v != nil {
		t.Errorf("НайтиПоНомеру(несуществующий) → %v, ожидался nil", v)
	}

	// Ссылка.Удалить() — удаление через привязанный менеджер.
	ref.CallMethod("удалить", nil)
	var cnt int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM заказпокупателя").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("после Ссылка.Удалить() ожидался 1 документ, получили %d", cnt)
	}

	// Менеджерный вариант: Документы.X.Удалить(Ссылка).
	ref2 := dp.CallMethod("найтипономеру", []any{"ЗП-002"}).(*interpreter.Ref)
	dp.CallMethod("удалить", []any{ref2})
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM заказпокупателя").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Errorf("после Документы.X.Удалить() ожидалось 0 документов, получили %d", cnt)
	}
}

// ЭтоНовый(): Истина у созданного, Ложь после Записать и у загруженного.
// Прочитать(): откатывает несохранённые изменения к значениям из БД.
func TestDocWriter_IsNewAndRead(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "Заметка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Текст", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	s := &Server{store: db, reg: registry, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("Заметка").(*docProxy)

	w := dp.CallMethod("создать", nil).(*docWriter)
	if w.CallMethod("этоновый", nil) != true {
		t.Error("новый документ: ЭтоНовый должно быть Истина")
	}
	w.Set("Номер", "З-1")
	w.Set("Текст", "оригинал")
	ref := w.CallMethod("записать", nil).(*interpreter.Ref)
	if w.CallMethod("этоновый", nil) != false {
		t.Error("после Записать: ЭтоНовый должно быть Ложь")
	}

	loaded := ref.CallMethod("получитьобъект", nil).(*docWriter)
	if loaded.CallMethod("этоновый", nil) != false {
		t.Error("загруженный объект не должен быть новым")
	}
	loaded.Set("Текст", "черновик")
	loaded.CallMethod("прочитать", nil)
	if got := loaded.Get("Текст"); got != "оригинал" {
		t.Errorf("после Прочитать ожидался 'оригинал', got %v", got)
	}
}

func TestDocWriter_RollbackRestoresIsNew(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name:   "Заметка",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	s := &Server{store: db, reg: registry, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	txState := interpreter.NewTxState(ctx)
	root := newDocsRoot(s, txState)
	dp := root.Get("Заметка").(*docProxy)
	txFns := interpreter.NewTxFunctions(txState, db)
	callTx := func(name string) {
		t.Helper()
		if _, err := txFns[name].(interpreter.BuiltinFunc)(nil, "", 0); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "ОТКАТ")
	callTx("НачатьТранзакцию")
	w.CallMethod("записать", nil)
	callTx("ОтменитьТранзакцию")
	if got := w.CallMethod("этоновый", nil); got != true {
		t.Fatalf("после rollback ЭтоНовый = %v, ожидалось true", got)
	}

	callTx("НачатьТранзакцию")
	w.CallMethod("записать", nil)
	callTx("ЗафиксироватьТранзакцию")
	var count int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM заметка").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("повторная запись после rollback оставила %d строк, ожидалась 1", count)
	}
}

// Ссылка.ПолучитьОбъект() для существующего документа возвращает docWriter
// с загруженной шапкой и табличными частями: можно прочитать значения,
// изменить и Записать() — обновится та же запись по UUID, ТЧ перезапишется.
func TestDocsRoot_GetObject_UpdateExisting(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "ВходящееПисьмо",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{Name: "Вложения", Fields: []metadata.Field{
				{Name: "Имя", Type: metadata.FieldTypeString},
			}},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	s := &Server{store: db, reg: registry, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("ВходящееПисьмо").(*docProxy)

	// Создаём документ через Создать().Записать().
	created := dp.CallMethod("создать", nil).(*docWriter)
	created.Set("Номер", "ВП-001")
	created.Set("Статус", "Новое")
	tp := created.Get("Вложения").(*tpProxy)
	tp.CallMethod("добавить", nil).(*interpreter.MapThis).M["Имя"] = "scan.pdf"
	createdRef := created.CallMethod("записать", nil).(*interpreter.Ref)
	createdID := createdRef.UUID

	// НайтиПоНомеру → Ссылка → ПолучитьОбъект().
	foundRef := dp.CallMethod("найтипономеру", []any{"ВП-001"}).(*interpreter.Ref)
	obj := foundRef.CallMethod("получитьобъект", nil)
	w, ok := obj.(*docWriter)
	if !ok {
		t.Fatalf("ПолучитьОбъект вернул %T, ожидался *docWriter", obj)
	}
	if w.obj.ID.String() != createdID {
		t.Errorf("writer.ID = %s, want %s", w.obj.ID, createdID)
	}
	// Поле шапки прочиталось.
	if v := fmt.Sprint(w.Get("Статус")); v != "Новое" {
		t.Errorf("Get(Статус) = %q, want \"Новое\"", v)
	}
	// Табличная часть прочиталась.
	tpRows := w.obj.TablePartRows["Вложения"]
	if len(tpRows) != 1 {
		t.Fatalf("Вложения.количество = %d, want 1", len(tpRows))
	}
	if name := fmt.Sprint(tpRows[0]["Имя"]); name != "scan.pdf" {
		t.Errorf("Вложения[0].Имя = %q, want \"scan.pdf\"", name)
	}

	// Изменение и запись — обновится та же запись.
	w.Set("Статус", "Исполнено")
	w.CallMethod("записать", nil)

	row, err := db.GetByID(ctx, "ВходящееПисьмо", w.obj.ID, doc)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got := fmt.Sprint(row["Статус"]); got != "Исполнено" {
		t.Errorf("после Записать(): Статус = %q, want \"Исполнено\"", got)
	}

	// Запись не плодит дублей — это UPDATE, не INSERT.
	var cnt int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM входящееписьмо").Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("после Записать() через ПолучитьОбъект — записей %d, want 1", cnt)
	}
}

// Сценарий из issue #8: документ-исходящий ссылается на документ-входящий
// через реквизит ОснованиеВходящее. При проведении исходящего нужно дёрнуть
// ИсходящийОбъект.ОснованиеВходящее.ПолучитьОбъект() — ссылка пришла из БД
// через enrichHeaderRefs, без Manager она бы дала «не привязана к менеджеру».
// Тест проверяет что обогащение проставляет Manager и сценарий проходит.
func TestRefField_FromHeader_GetObjectWorks(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	inbox := &metadata.Entity{
		Name: "ВходящееПисьмо",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString},
		},
	}
	outbox := &metadata.Entity{
		Name: "ИсходящееПисьмо",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "ОснованиеВходящее", RefEntity: "ВходящееПисьмо"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{inbox, outbox}); err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{inbox, outbox}})
	s := &Server{store: db, reg: registry, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	// Создаём ВходящееПисьмо.
	docsRoot := newDocsRoot(s, interpreter.NewTxState(ctx))
	inDp := docsRoot.Get("ВходящееПисьмо").(*docProxy)
	inW := inDp.CallMethod("создать", nil).(*docWriter)
	inW.Set("Номер", "ВП-001")
	inW.Set("Статус", "Новое")
	inRef := inW.CallMethod("записать", nil).(*interpreter.Ref)

	// Создаём ИсходящееПисьмо, в шапке ОснованиеВходящее = inRef.
	outDp := docsRoot.Get("ИсходящееПисьмо").(*docProxy)
	outW := outDp.CallMethod("создать", nil).(*docWriter)
	outW.Set("Номер", "ИП-001")
	outW.Set("ОснованиеВходящее", inRef)
	outRef := outW.CallMethod("записать", nil).(*interpreter.Ref)

	_ = outRef // outRef в этом тесте не нужен, важен сам факт записи

	// Полный путь как в DSL пользователя:
	// Документы.ИсходящееПисьмо.НайтиПоНомеру("ИП-001").ПолучитьОбъект()
	// — за кулисами это docProxy.LoadObject, который должен обогатить
	// шапку: ОснованиеВходящее → *Ref{Manager}, а не голая строка UUID.
	outFound := outDp.CallMethod("найтипономеру", []any{"ИП-001"}).(*interpreter.Ref)
	outObj := outFound.CallMethod("получитьобъект", nil)
	outDocW, ok := outObj.(*docWriter)
	if !ok {
		t.Fatalf("ПолучитьОбъект исходящего → %T, ожидался *docWriter", outObj)
	}

	headerRef, ok := outDocW.Get("ОснованиеВходящее").(*interpreter.Ref)
	if !ok {
		t.Fatalf("Док.ОснованиеВходящее = %T, ожидался *Ref (обогащение шапки не сработало)", outDocW.Get("ОснованиеВходящее"))
	}
	if headerRef.Manager == nil {
		t.Fatal("у ссылки шапки нет Manager — ПолучитьОбъект упадёт")
	}

	// Тот самый сценарий из issue:
	// ИсходящийОбъект.ОснованиеВходящее.ПолучитьОбъект().Статус = "Исполнено"
	loaded := headerRef.CallMethod("получитьобъект", nil)
	w, ok := loaded.(*docWriter)
	if !ok {
		t.Fatalf("ПолучитьОбъект ссылки шапки → %T, ожидался *docWriter", loaded)
	}
	w.Set("Статус", "Исполнено")
	w.CallMethod("записать", nil)

	updated, err := db.GetByID(ctx, "ВходящееПисьмо", uuid.MustParse(inRef.UUID), inbox)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(updated["Статус"]); got != "Исполнено" {
		t.Errorf("Статус входящего после Записать через ПолучитьОбъект = %q, want \"Исполнено\"", got)
	}
}

// ПриЗаписи (OnWrite) вызывается при Записать() из обработки (docWriter):
// расчётные реквизиты документа вычисляются перед сохранением.
func TestDocsRoot_OnWriteRunsOnSave(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "Счёт",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "СуммаДокумента", Type: metadata.FieldTypeNumber},
		},
		TableParts: []metadata.TablePart{
			{Name: "Товары", Fields: []metadata.Field{
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Цена", Type: metadata.FieldTypeNumber},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			}},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	// ПриЗаписи считает Сумму строк и итог документа.
	onWriteSrc := `Процедура ПриЗаписи()
  Итого = 0;
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Стр.Сумма = Стр.Количество * Стр.Цена;
    Итого = Итого + Стр.Сумма;
  КонецЦикла;
  ЭтотОбъект.СуммаДокумента = Итого;
КонецПроцедуры`
	prog := mustParse(t, onWriteSrc)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{"Счёт": prog},
	})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("Счёт").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "С-1")
	tp := w.Get("Товары").(*tpProxy)
	r1 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r1.Set("Количество", float64(3))
	r1.Set("Цена", float64(100))
	r2 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r2.Set("Количество", float64(2))
	r2.Set("Цена", float64(50))

	// Записать() — без явного вызова ПриЗаписи; хук должен сработать сам.
	w.CallMethod("записать", nil)

	var total float64
	if err := db.QueryRow(ctx, "SELECT суммадокумента FROM счёт LIMIT 1").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 400 {
		t.Errorf("СуммаДокумента = %v, ожидалось 400 (ПриЗаписи не отработала)", total)
	}
	// Сумма строк табличной части тоже вычислена в ПриЗаписи и сохранена.
	rows, err := db.QueryAll(ctx, "SELECT строка, сумма FROM счёт_товары ORDER BY строка")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("ожидалось 2 строки ТЧ, получено %d", len(rows))
	}
	want := map[int]float64{1: 300, 2: 100}
	for _, row := range rows {
		line := int(parseNum(row["строка"]))
		if got := parseNum(row["сумма"]); got != want[line] {
			t.Errorf("строка %d: Сумма = %v, ожидалось %v", line, row["сумма"], want[line])
		}
	}
}

// ПриЗаписи (OnWrite) на DSL-пути записи должен видеть заполненный псевдо-реквизит
// «Ссылка» самого документа — симметрично OnPost и entityservice.Save. Регресс:
// ensureSelfRef ранее вызывался только перед OnPost, из-за чего this.Ссылка в
// ПриЗаписи был пуст (запись ссылки на себя и чтение пре-образа по своей ссылке
// в хуке не работали).
func TestDocsRoot_OnWriteHasSelfRef(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "СамоДок",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "ЕстьСсылка", Type: metadata.FieldTypeNumber},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	onWriteSrc := `Процедура ПриЗаписи()
  Если ЗначениеЗаполнено(ЭтотОбъект.Ссылка) Тогда
    ЭтотОбъект.ЕстьСсылка = 1;
  Иначе
    ЭтотОбъект.ЕстьСсылка = 0;
  КонецЕсли;
КонецПроцедуры`
	prog := mustParse(t, onWriteSrc)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{"СамоДок": prog},
	})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("СамоДок").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "СД-1")
	w.CallMethod("записать", nil)

	var flag float64
	if err := db.QueryRow(ctx, "SELECT естьссылка FROM самодок LIMIT 1").Scan(&flag); err != nil {
		t.Fatal(err)
	}
	if flag != 1 {
		t.Errorf("this.Ссылка в ПриЗаписи не заполнена (ЕстьСсылка=%v, ожидалось 1) — ensureSelfRef не вызван на пути записи", flag)
	}
}

// parseNum приводит значение из БД (число или строка вида "300.0") к float64.
func parseNum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

// При записи документа из обработки (docWriter) срабатывает автонумерация:
// пустой реквизит Номер заполняется нумератором, явно заданный — сохраняется.
func TestDocsRoot_AutoNumberOnWrite(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name:      "Заявка",
		Kind:      metadata.KindDocument,
		Numerator: &metadata.Numerator{Prefix: "ЗВ-", Length: 4, Period: "none"},
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{doc}})
	s := &Server{store: db, reg: registry, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("Заявка").(*docProxy)

	// Два документа без явного номера → автонумерация.
	dp.CallMethod("создать", nil).(*docWriter).CallMethod("записать", nil)
	dp.CallMethod("создать", nil).(*docWriter).CallMethod("записать", nil)
	// Пробельный номер тоже считается пустым.
	wWhitespace := dp.CallMethod("создать", nil).(*docWriter)
	wWhitespace.Set("Номер", " \t ")
	wWhitespace.CallMethod("записать", nil)
	// Явно заданный номер сохраняется без изменений.
	wManual := dp.CallMethod("создать", nil).(*docWriter)
	wManual.Set("Номер", "РУЧНОЙ-1")
	wManual.CallMethod("записать", nil)

	rows, err := db.QueryAll(ctx, "SELECT номер FROM заявка")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, row := range rows {
		got[fmt.Sprint(row["номер"])] = true
	}
	for _, want := range []string{"ЗВ-0001", "ЗВ-0002", "ЗВ-0003", "РУЧНОЙ-1"} {
		if !got[want] {
			t.Errorf("ожидался номер %q, получены: %v", want, got)
		}
	}
}

func TestDocWriter_AutoNumberRollbackAllowsRetrySameWriter(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name: "ЗаявкаRetry", Kind: metadata.KindDocument,
		Numerator: &metadata.Numerator{Prefix: "ЗВ-", Length: 4, Period: "none"},
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Отказ", Type: metadata.FieldTypeBool},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	program := mustParse(t, `
Процедура ПриЗаписи()
	Если ЭтотОбъект.Отказ Тогда
		ВызватьИсключение("отказ после номера");
	КонецЕсли;
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: program},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.obj.Fields["Номер"] = ""
	w.obj.Fields["НОМЕР"] = nil
	w.Set("Отказ", true)
	before := map[string]any{"Номер": "", "НОМЕР": nil}

	if err := w.write(); err == nil || !strings.Contains(err.Error(), "отказ после номера") {
		t.Fatalf("первый write error = %v", err)
	}
	after := map[string]any{}
	for key, value := range w.obj.Fields {
		if strings.EqualFold(key, "Номер") {
			after[key] = value
		}
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rollback не восстановил поля writer: до=%#v после=%#v", before, after)
	}
	if _, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc); err == nil {
		t.Fatal("документ сохранился после исключения хука")
	}

	w.Set("Отказ", false)
	if err := w.write(); err != nil {
		t.Fatalf("retry того же writer: %v", err)
	}
	if got := fmt.Sprint(w.obj.Get("Номер")); got != "ЗВ-0001" {
		t.Fatalf("retry того же writer получил номер %q", got)
	}

	next := dp.CallMethod("создать", nil).(*docWriter)
	next.obj.Fields["Номер"] = ""
	if err := next.write(); err != nil {
		t.Fatalf("write следующего writer: %v", err)
	}
	if got := fmt.Sprint(next.obj.Get("Номер")); got != "ЗВ-0002" {
		t.Fatalf("следующий writer получил номер %q", got)
	}
	if _, duplicate := next.obj.Fields["номер"]; duplicate {
		t.Fatalf("у следующего writer появился lowercase-дубликат: %#v", next.obj.Fields)
	}
}

// Документы.X для несуществующего/несдокументного — nil.
func TestDocsRoot_UnknownDocument(t *testing.T) {
	s := &Server{reg: runtime.NewRegistry()}
	s.entitySvc = s.newEntityService(nil)
	root := newDocsRoot(s, interpreter.NewTxState(context.Background()))
	if v := root.Get("НетТакого"); v != nil {
		t.Errorf("Документы.НетТакого → %v, ожидался nil", v)
	}
}

func mustParse(t *testing.T, src string) *ast.Program {
	t.Helper()
	l := lexer.New(src, "<test>")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return prog
}
