package entityservice

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestSaveNewObjectWithHookWritesSingleCreateAudit(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.EnsureAuditSchema(ctx); err != nil {
		t.Fatal(err)
	}

	entity := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Нормализовано", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}

	program := mustParseProgramT(t, `
Процедура ПриЗаписи()
  ЭтотОбъект.Нормализовано = "готово";
КонецПроцедуры`)
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{entity},
		Programs: map[string]*ast.Program{entity.Name: program},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	svc := &Service{Store: db, Reg: registry, Interp: interp}

	id := uuid.New()
	result, err := svc.Save(ctx, SaveRequest{
		Entity: entity,
		ID:     id,
		IsNew:  true,
		Fields: map[string]any{"Наименование": "Тест"},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if result.DSLError != "" {
		t.Fatalf("DSLError: %s", result.DSLError)
	}

	var creates, updates int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _audit WHERE record_id = ? AND action = 'create'`, id.String()).Scan(&creates); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _audit WHERE record_id = ? AND action = 'update'`, id.String()).Scan(&updates); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || updates != 0 {
		t.Fatalf("аудит нового объекта: create=%d update=%d, ожидалось 1/0", creates, updates)
	}
}
