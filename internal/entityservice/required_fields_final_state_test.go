package entityservice

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func requiredFinalStateEntity() *metadata.Entity {
	ent := numberedCatalog()
	ent.Name = "RequiredFinalState"
	ent.Fields[0].Required = true // auto-numbered Code
	ent.Fields = append(ent.Fields,
		metadata.Field{Name: "RequiredText", Type: metadata.FieldTypeString, Required: true},
	)
	ent.TableParts = []metadata.TablePart{{
		Name: "Lines",
		Fields: []metadata.Field{
			{Name: "Item", Type: metadata.FieldTypeString, Required: true},
			{Name: "Quantity", Type: metadata.FieldTypeNumber},
		},
	}}
	return ent
}

func loadRequiredProgram(t *testing.T, svc *Service, ent *metadata.Entity, source string) {
	t.Helper()
	options := runtime.LoadOptions{Entities: []*metadata.Entity{ent}}
	if source != "" {
		options.Programs = map[string]*ast.Program{ent.Name: mustParseProgramT(t, source)}
	}
	svc.Reg.Load(options)
}

func TestSaveRequiredFields_FinalPersistedStateMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := requiredFinalStateEntity()
		svc := newNumberingService(t, db, []*metadata.Entity{ent})

		t.Run("auto number fills required field before validation", func(t *testing.T) {
			id := uuid.New()
			result, err := svc.Save(ctx, SaveRequest{
				Entity: ent, ID: id, IsNew: true,
				Fields: map[string]any{"RequiredText": "direct"},
			})
			if err != nil || result.DSLError != "" {
				t.Fatalf("Save: result=%+v err=%v", result, err)
			}
			row, err := db.GetByID(ctx, ent.Name, id, ent)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(fmt.Sprint(row[metadata.StandardCodeField])); got == "" || got == "<nil>" {
				t.Fatalf("required auto-numbered Code was not persisted: %#v", row)
			}
		})

		t.Run("preflight fills required field before validation", func(t *testing.T) {
			id := uuid.New()
			result, err := svc.Save(ctx, SaveRequest{
				Entity: ent, ID: id, IsNew: true, Fields: map[string]any{},
				Preflight: func(_ context.Context, obj *runtime.Object) error {
					obj.Set("RequiredText", "from preflight")
					return nil
				},
			})
			if err != nil || result.DSLError != "" {
				t.Fatalf("Save: result=%+v err=%v", result, err)
			}
			row, err := db.GetByID(ctx, ent.Name, id, ent)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(row["RequiredText"]); got != "from preflight" {
				t.Fatalf("RequiredText = %q, want preflight value", got)
			}
		})

		t.Run("OnWrite fills required field after provisional insert", func(t *testing.T) {
			loadRequiredProgram(t, svc, ent, `
Процедура ПриЗаписи()
  ЭтотОбъект.RequiredText = "from hook";
КонецПроцедуры`)
			id := uuid.New()
			result, err := svc.Save(ctx, SaveRequest{
				Entity: ent, ID: id, IsNew: true, Fields: map[string]any{},
			})
			if err != nil || result.DSLError != "" {
				t.Fatalf("Save: result=%+v err=%v", result, err)
			}
			row, err := db.GetByID(ctx, ent.Name, id, ent)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(row["RequiredText"]); got != "from hook" {
				t.Fatalf("RequiredText = %q, want hook value", got)
			}
			if version, err := db.EntityVersion(ctx, ent.Name, id); err != nil || version != 1 {
				t.Fatalf("version after provisional/final write = %d, err=%v; want 1", version, err)
			}
		})

		t.Run("OnWrite clear is rejected and provisional row rolls back", func(t *testing.T) {
			loadRequiredProgram(t, svc, ent, `
Процедура ПриЗаписи()
  ЭтотОбъект.RequiredText = "   ";
КонецПроцедуры`)
			id := uuid.New()
			result, err := svc.Save(ctx, SaveRequest{
				Entity: ent, ID: id, IsNew: true,
				Fields: map[string]any{"RequiredText": "valid before hook"},
			})
			if err != nil {
				t.Fatalf("required rejection became technical error: %v", err)
			}
			if !strings.Contains(result.DSLError, ent.Name+".RequiredText") {
				t.Fatalf("DSLError = %q", result.DSLError)
			}
			if _, err := db.GetByID(ctx, ent.Name, id, ent); !storage.IsNotFound(err) {
				t.Fatalf("provisional row survived rejection: %v", err)
			}

			// The rejected transaction must not consume its generated number.
			loadRequiredProgram(t, svc, ent, "")
			retryID := uuid.New()
			retryFields := map[string]any{"RequiredText": "retry"}
			result, err = svc.Save(ctx, SaveRequest{Entity: ent, ID: retryID, IsNew: true, Fields: retryFields})
			if err != nil || result.DSLError != "" {
				t.Fatalf("retry Save: result=%+v err=%v", result, err)
			}
			retryRow, err := db.GetByID(ctx, ent.Name, retryID, ent)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprint(retryRow[metadata.StandardCodeField]); got != "К-000004" {
				t.Fatalf("required rollback consumed number: retry Code = %q, want К-000004", got)
			}
		})

		t.Run("required table part is rejected before header persistence", func(t *testing.T) {
			id := uuid.New()
			result, err := svc.Save(ctx, SaveRequest{
				Entity: ent, ID: id, IsNew: true,
				Fields: map[string]any{"RequiredText": "valid"},
				TablePartRows: map[string][]map[string]any{
					"Lines": {
						{"Item": "first", "Quantity": 1},
						{"Item": "   ", "Quantity": 2},
					},
				},
			})
			if err != nil {
				t.Fatalf("required rejection became technical error: %v", err)
			}
			if !strings.Contains(result.DSLError, ent.Name+".Lines[2].Item") {
				t.Fatalf("DSLError = %q", result.DSLError)
			}
			if _, err := db.GetByID(ctx, ent.Name, id, ent); !storage.IsNotFound(err) {
				t.Fatalf("header persisted despite invalid table part: %v", err)
			}
		})
	})
}
