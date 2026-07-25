package widget

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReferencePresentationsMaskFirstStringField(t *testing.T) {
	ctx := context.Background()
	entity := &metadata.Entity{
		Name: "Клиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widget-mask.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	const secret = "+79161234455"
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Телефон": secret}, entity); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	runner := &Runner{
		Reg:   reg,
		Store: db,
		User: &auth.User{Login: "operator", Roles: []*auth.Role{{
			Permissions: auth.Permission{
				Catalogs: map[string][]string{entity.Name: {"read"}},
				FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
					entity.Name: {"Телефон": {Read: "mask_all"}},
				}},
			},
		}}},
	}

	rows := []map[string]any{{"Клиент": id.String()}}
	runner.resolveUUIDs(ctx, rows)
	if got := rows[0]["Клиент"]; got == secret || got != "••••••" {
		t.Fatalf("widget UUID label = %q, want fixed mask", got)
	}
	if got := recordPresentation(ctx, runner, entity.Name, id.String()); got == secret || got != "••••••" {
		t.Fatalf("recent presentation = %q, want fixed mask", got)
	}
}
