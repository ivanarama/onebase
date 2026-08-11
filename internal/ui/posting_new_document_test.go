package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// newSelfRefPostingServer поднимает Server с проводимым документом «Прибор», чей
// ОбработкаПроведения создаёт связанный справочник «СобытиеПрибора» со ссылкой
// обратно на сам документ (reference:Прибор → ЭтотОбъект.Ссылка).
func newSelfRefPostingServer(t *testing.T) (context.Context, *storage.DB, *Server, *metadata.Entity, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Прибор",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	cat := &metadata.Entity{
		Name: "СобытиеПрибора",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Прибор", Type: "reference:Прибор", RefEntity: "Прибор"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc, cat}); err != nil {
		t.Fatal(err)
	}

	onPostSrc := `Процедура ОбработкаПроведения()
  Соб = Справочники.СобытиеПрибора.Создать();
  Соб.Прибор = ЭтотОбъект.Ссылка;
  Соб.Записать();
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc, cat},
		Programs: map[string]*ast.Program{"Прибор": mustParse(t, onPostSrc)},
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
	return ctx, db, s, doc, cat
}

// Регрессия issue #360: создание+проведение нового документа одним действием
// (post/post_and_close на /new), чей ОбработкаПроведения создаёт связанный
// объект со ссылкой обратно на сам документ. До фикса шапка документа
// записывалась ПОСЛЕ хука, поэтому FK-ссылка «на себя» падала с
// FOREIGN KEY constraint failed. Теперь шапка нового объекта пишется до хука.
func TestSaveNewDocument_PostWithSelfReference(t *testing.T) {
	ctx, db, s, doc, cat := newSelfRefPostingServer(t)

	docID := uuid.New()
	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     docID,
		IsNew:  true,
		Fields: map[string]any{"Номер": "П-001"},
		Action: "post",
	})
	if err != nil {
		t.Fatalf("Save вернул технический сбой: %v", err)
	}
	if res.DSLError != "" {
		t.Fatalf("Save.DSLError = %q (ожидалось пусто) — FK-ссылка на себя при создании+проведении", res.DSLError)
	}

	// Документ существует и проведён.
	row, err := db.GetByID(ctx, "Прибор", docID, doc)
	if err != nil || row == nil {
		t.Fatalf("документ не найден: %v", err)
	}
	if !asBool(row["posted"]) {
		t.Errorf("документ не помечен проведённым: %v", row["posted"])
	}
	if got := fmt.Sprint(row["_version"]); got != "1" {
		t.Errorf("новый объект после atomic hook имеет _version=%s, want 1", got)
	}

	// Связанный объект создан и ссылается на документ.
	events, err := db.List(ctx, "СобытиеПрибора", cat, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("ожидался 1 СобытиеПрибора, создано %d", len(events))
	}
	if ref := refValueString(events[0]["Прибор"]); ref != docID.String() {
		t.Errorf("СобытиеПрибора.Прибор = %q, ожидался %q", ref, docID.String())
	}
}

func TestSaveNewDocument_HookChildAndFailureRollBackTogether(t *testing.T) {
	ctx, db, s, doc, cat := newSelfRefPostingServer(t)
	failing := `Процедура ОбработкаПроведения()
  Соб = Справочники.СобытиеПрибора.Создать();
  Соб.Прибор = ЭтотОбъект.Ссылка;
  Соб.Записать();
  ВызватьИсключение("откатить всё");
КонецПроцедуры`
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc, cat},
		Programs: map[string]*ast.Program{"Прибор": mustParse(t, failing)},
	})

	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: uuid.New(), IsNew: true,
		Fields: map[string]any{"Номер": "П-ROLLBACK"}, Action: "post",
	})
	if err != nil {
		t.Fatalf("Save technical error: %v", err)
	}
	if res.DSLError == "" {
		t.Fatal("expected hook DSLError")
	}
	parents, err := db.List(ctx, doc.Name, doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	children, err := db.List(ctx, cat.Name, cat, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 0 || len(children) != 0 {
		t.Fatalf("atomic rollback left parent=%d child=%d", len(parents), len(children))
	}
}

// A Save called from an already open DSL transaction owns a savepoint. A hook
// failure must roll back its provisional parent and child writes without
// poisoning or rolling back unrelated work in the caller's transaction.
func TestSaveNewDocument_BorrowedTransactionHookFailureUsesSavepoint(t *testing.T) {
	ctx, db, s, doc, cat := newSelfRefPostingServer(t)
	failing := `Процедура ОбработкаПроведения()
  Соб = Справочники.СобытиеПрибора.Создать();
  Соб.Прибор = ЭтотОбъект.Ссылка;
  Соб.Записать();
  ВызватьИсключение("откатить внутреннюю запись");
КонецПроцедуры`
	s.reg.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc, cat},
		Programs: map[string]*ast.Program{"Прибор": mustParse(t, failing)},
	})

	outerChildID := uuid.New()
	if err := db.WithTx(ctx, func(outerCtx context.Context) error {
		if err := db.Upsert(outerCtx, cat.Name, outerChildID, map[string]any{}, cat); err != nil {
			return err
		}
		result, err := s.entitySvc.Save(outerCtx, entityservice.SaveRequest{
			Entity: doc, ID: uuid.New(), IsNew: true,
			Fields: map[string]any{"Номер": "П-SAVEPOINT"}, Action: "post",
		})
		if err != nil {
			return err
		}
		if result.DSLError == "" {
			return fmt.Errorf("expected hook DSLError")
		}
		parents, err := db.List(outerCtx, doc.Name, doc, storage.ListParams{})
		if err != nil {
			return err
		}
		children, err := db.List(outerCtx, cat.Name, cat, storage.ListParams{})
		if err != nil {
			return err
		}
		if len(parents) != 0 || len(children) != 1 {
			return fmt.Errorf("savepoint rollback left parent=%d children=%d", len(parents), len(children))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	children, err := db.List(ctx, cat.Name, cat, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || refValueString(children[0]["id"]) != outerChildID.String() {
		t.Fatalf("outer transaction result = %#v, want only unrelated child", children)
	}
}

// Регрессия issue #381: ссылка в шапке обогащалась до открытия транзакции
// проведения и сохраняла нетранзакционный context. Поэтому
// this.Счётчик.ПолучитьОбъект().Записать() из OnPost пытался открыть вторую
// транзакцию и навсегда ждал первую (на SQLite — единственное соединение).
func TestSaveNewDocument_OnPostUpdatesExistingReferencedCatalog(t *testing.T) {
	baseCtx := context.Background()
	db, err := storage.ConnectSQLite(baseCtx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	counter := &metadata.Entity{
		Name: "Счётчик",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString},
		},
	}
	dismantling := &metadata.Entity{
		Name:    "ДемонтажСчётчика",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Счётчик", Type: "reference:Счётчик", RefEntity: counter.Name},
		},
	}
	if err := db.Migrate(baseCtx, []*metadata.Entity{counter, dismantling}); err != nil {
		t.Fatal(err)
	}

	counterID := uuid.New()
	if err := db.Upsert(baseCtx, counter.Name, counterID, map[string]any{
		"Наименование": "Счётчик №1",
		"Статус":       "Установлен",
	}, counter); err != nil {
		t.Fatal(err)
	}

	onPostSrc := `Процедура ОбработкаПроведения()
  СчётчикОбъект = this.Счётчик.ПолучитьОбъект();
  СчётчикОбъект.Статус = "Демонтирован";
  СчётчикОбъект.Записать();
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{counter, dismantling},
		Programs: map[string]*ast.Program{dismantling.Name: mustParse(t, onPostSrc)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)

	ctx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
	defer cancel()
	result, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: dismantling,
		ID:     uuid.New(),
		IsNew:  true,
		Fields: map[string]any{"Номер": "ДС-001", "Счётчик": counterID.String()},
		Action: "post",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.DSLError != "" {
		t.Fatalf("OnPost: %s", result.DSLError)
	}

	row, err := db.GetByID(baseCtx, counter.Name, counterID, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(row["Статус"]); got != "Демонтирован" {
		t.Fatalf("Счётчик.Статус = %q, ожидался %q", got, "Демонтирован")
	}
}

// Неудачное бизнес-правило при создании+проведении не должно оставлять
// документ-сироту: пред-запись шапки нового объекта откатывается.
func TestSaveNewDocument_PostHookFailureLeavesNothing(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Прибор",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	onPostSrc := `Процедура ОбработкаПроведения()
  ВызватьИсключение("нельзя проводить");
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{"Прибор": mustParse(t, onPostSrc)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{
		Store: db, Reg: registry, Interp: interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}

	docID := uuid.New()
	res, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc, ID: docID, IsNew: true,
		Fields: map[string]any{"Номер": "П-001"}, Action: "post",
	})
	if err != nil {
		t.Fatalf("Save технический сбой: %v", err)
	}
	if res.DSLError == "" {
		t.Fatal("ожидалась DSLError от ВызватьИсключение")
	}
	rows, err := db.List(ctx, "Прибор", doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("документ-сирота не откачен: %d строк", len(rows))
	}
}
