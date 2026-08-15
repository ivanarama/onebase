package entityservice

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestSaveCarriesExplicitPresentationIntoRuntimeObject(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "presentation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entity := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Presentation: []string{"Артикул", "Описание"},
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Описание", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	seen := ""
	svc := &Service{
		Store: db, Reg: reg,
		PrepareHook: func(_ context.Context, _ *metadata.Entity, obj *runtime.Object) {
			seen = obj.String()
		},
	}
	_, err = svc.Save(ctx, SaveRequest{
		Entity: entity, ID: uuid.New(), IsNew: true,
		Fields: map[string]any{
			"Наименование": "Старое имя", "Артикул": "", "Описание": "Витринное имя",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != "Витринное имя" {
		t.Fatalf("runtime Object.String() in save hook = %q, want presentation fallback", seen)
	}
}
