package entityservice

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dslvars"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestUnpostRunsRussianHookAfterStateChange(t *testing.T) {
	svc, db, ctx, doc, reg, id := newUnpostFixture(t, `
Процедура ОбработкаУдаленияПроведения()
  Если ЭтотОбъект.Posted Тогда
    ВызватьИсключение("хук увидел проведённый документ");
  КонецЕсли;
  Сообщить("хук отмены вызван");
КонецПроцедуры`)

	result, err := svc.Unpost(ctx, doc, id)
	if err != nil {
		t.Fatalf("Unpost: %v", err)
	}
	if result.DSLError != "" {
		t.Fatalf("неожиданная DSL-ошибка: %s", result.DSLError)
	}
	if len(result.DSLMessages) != 1 || result.DSLMessages[0] != "хук отмены вызван" {
		t.Fatalf("сообщения хука = %v", result.DSLMessages)
	}
	assertUnpostState(t, db, ctx, doc, reg, id, false, 0)
}

func TestUnpostHookErrorRollsBackFlagAndMovements(t *testing.T) {
	svc, db, ctx, doc, reg, id := newUnpostFixture(t, `
Процедура ОбработкаУдаленияПроведения()
  ВызватьИсключение("отмена запрещена");
КонецПроцедуры`)

	result, err := svc.Unpost(ctx, doc, id)
	if err != nil {
		t.Fatalf("DSL-ошибка должна возвращаться через результат, получено: %v", err)
	}
	if !strings.Contains(result.DSLError, "отмена запрещена") {
		t.Fatalf("DSLError = %q", result.DSLError)
	}
	assertUnpostState(t, db, ctx, doc, reg, id, true, 1)
}

func TestSaveExistingPostedDocumentRunsUnpostHook(t *testing.T) {
	svc, db, ctx, doc, reg, id := newUnpostFixture(t, `
Процедура ОбработкаУдаленияПроведения()
  Если ЭтотОбъект.Posted Тогда
    ВызватьИсключение("хук увидел проведённый документ");
  КонецЕсли;
  Сообщить("обычная запись вызвала отмену");
КонецПроцедуры`)

	result, err := svc.Save(ctx, SaveRequest{
		Entity: doc,
		ID:     id,
		IsNew:  false,
		Fields: map[string]any{"Номер": "2"},
		Action: "",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.DSLError != "" {
		t.Fatalf("неожиданная DSL-ошибка: %s", result.DSLError)
	}
	if len(result.DSLMessages) != 1 || result.DSLMessages[0] != "обычная запись вызвала отмену" {
		t.Fatalf("сообщения хука = %v", result.DSLMessages)
	}
	assertUnpostState(t, db, ctx, doc, reg, id, false, 0)
}

func TestSaveExistingDraftDoesNotRunUnpostHook(t *testing.T) {
	svc, db, ctx, doc, reg, id := newUnpostFixture(t, `
Процедура ОбработкаУдаленияПроведения()
  ВызватьИсключение("OnUnpost не должен вызываться для черновика");
КонецПроцедуры`)
	if err := db.WriteMovements(ctx, reg.Name, doc.Name, id, nil, reg, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPosted(ctx, doc.Name, id, false); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Save(ctx, SaveRequest{
		Entity: doc,
		ID:     id,
		IsNew:  false,
		Fields: map[string]any{"Номер": "2"},
		Action: "",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.DSLError != "" {
		t.Fatalf("обычная запись черновика вызвала OnUnpost: %s", result.DSLError)
	}
	assertUnpostState(t, db, ctx, doc, reg, id, false, 0)
}

func newUnpostFixture(t *testing.T, hookSource string) (*Service, *storage.DB, context.Context, *metadata.Entity, *metadata.Register, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "unpost.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "Поступление",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	reg := &metadata.Register{
		Name:       "Остатки",
		Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: []*metadata.Register{reg},
		Programs:  map[string]*ast.Program{doc.Name: mustParseProgramT(t, hookSource)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &Service{
		Store:  db,
		Reg:    registry,
		Interp: interp,
		BuildVars: func(c context.Context, mc *runtime.MovementsCollector, msgs *[]string) (map[string]any, *interpreter.TxState) {
			vars := dslvars.Common{Ctx: c, Reg: registry, Store: db, Movements: mc}.Build()
			vars["Сообщить"] = interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
				if len(args) > 0 && msgs != nil {
					*msgs = append(*msgs, args[0].(string))
				}
				return nil, nil
			})
			return vars, nil
		},
	}

	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": "1"}, doc); err != nil {
		t.Fatal(err)
	}
	period := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := db.WriteMovements(ctx, reg.Name, doc.Name, id, []map[string]any{{
		"Товар": "Молоток", "Количество": float64(5),
	}}, reg, &period); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPosted(ctx, doc.Name, id, true); err != nil {
		t.Fatal(err)
	}
	return svc, db, ctx, doc, reg, id
}

func assertUnpostState(t *testing.T, db *storage.DB, ctx context.Context, doc *metadata.Entity, reg *metadata.Register, id uuid.UUID, posted bool, movements int) {
	t.Helper()
	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := repostBool(row["posted"]); got != posted {
		t.Fatalf("posted = %v, ожидалось %v", got, posted)
	}
	rows, err := db.GetMovements(ctx, reg.Name, reg, storage.RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != movements {
		t.Fatalf("движений = %d, ожидалось %d: %v", len(rows), movements, rows)
	}
}
