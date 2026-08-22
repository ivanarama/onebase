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

// Документы.X.МойМетод() → процедура из модуля менеджера (X.manager.os)
// вызвана, аргумент проброшен, результат возврата возвращён.
func TestDocsRoot_ManagerMethod(t *testing.T) {
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
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	mgrSrc := `Функция Удвоить(Х)
  Возврат Х * 2;
КонецФункции`
	mgrProg := mustParse(t, mgrSrc)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:        []*metadata.Entity{doc},
		ManagerPrograms: map[string]*ast.Program{"Счёт": mgrProg},
	})

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

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	proxy := root.Get("Счёт").(*docProxy)
	got := proxy.CallMethod("Удвоить", []any{21.0})
	if toFloat(got) != 42 {
		t.Fatalf("Документы.Счёт.Удвоить(21) → %v (%T), ожидалось 42", got, got)
	}
}

// Справочники.X.МойМетод() — тот же сценарий для CatalogProxy: процедура
// из модуля менеджера вызвана через ManagerCaller.
func TestCatalogsRoot_ManagerMethod(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cat := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
		t.Fatal(err)
	}

	mgrSrc := `Функция Привет(Имя)
  Возврат "Здравствуйте, " + Имя;
КонецФункции`
	mgrProg := mustParse(t, mgrSrc)

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:        []*metadata.Entity{cat},
		ManagerPrograms: map[string]*ast.Program{"Контрагент": mgrProg},
	})

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

	// Реальный сценарий: vars из buildDSLVars содержит Справочники с
	// подключённым ManagerCaller. Тянем catalogs из vars вместо
	// прямого создания CatalogsRoot — это гарантирует, что caller
	// подключён в проде так же, как в тесте.
	vars, _ := s.buildDSLVarsTx(ctx, runtime.NewMovementsCollector(cat.Name, [16]byte{}))
	catsAny := vars["Справочники"]
	cats, ok := catsAny.(*interpreter.CatalogsRoot)
	if !ok {
		t.Fatalf("vars[Справочники] → %T, ожидался *CatalogsRoot", catsAny)
	}
	proxy := cats.Get("Контрагент").(*interpreter.CatalogProxy)
	got := proxy.CallMethod("Привет", []any{"мир"})
	if got != "Здравствуйте, мир" {
		t.Fatalf("Справочники.Контрагент.Привет → %v, ожидалось 'Здравствуйте, мир'", got)
	}
}
