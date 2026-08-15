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

	entity.Presentation = []string{"Телефон"}
	rows[0]["Клиент"] = id.String()
	runner.resolveUUIDs(ctx, rows)
	if got := rows[0]["Клиент"]; got == secret || got != "••••••" {
		t.Fatalf("explicit widget UUID label = %q, want fixed mask", got)
	}
	if got := recordPresentation(ctx, runner, entity.Name, id.String()); got == secret || got != "••••••" {
		t.Fatalf("explicit recent presentation = %q, want fixed mask", got)
	}
}

func TestRecordPresentationUsesExplicitFallbackOrder(t *testing.T) {
	ctx := context.Background()
	entity := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Описание", Type: metadata.FieldTypeString},
		},
		Presentation: []string{"Артикул", "Описание"},
	}
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widget-presentation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{
		"Наименование": "Старое имя", "Артикул": "", "Описание": "Витринное имя",
	}, entity); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	runner := &Runner{Reg: reg, Store: db}
	if got := recordPresentation(ctx, runner, entity.Name, id.String()); got != "Витринное имя" {
		t.Fatalf("fallback presentation = %q, ожидалось Витринное имя", got)
	}
	rows := []map[string]any{{"Ссылка": id.String()}}
	runner.resolveUUIDs(ctx, rows)
	if got := rows[0]["Ссылка"]; got != "Витринное имя" {
		t.Fatalf("resolved UUID fallback presentation = %q, ожидалось Витринное имя", got)
	}
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{
		"Наименование": "Старое имя", "Артикул": "A-1", "Описание": "Витринное имя",
	}, entity); err != nil {
		t.Fatal(err)
	}
	if got := recordPresentation(ctx, runner, entity.Name, id.String()); got != "A-1" {
		t.Fatalf("primary presentation = %q, ожидалось A-1", got)
	}
	rows[0]["Ссылка"] = id.String()
	runner.resolveUUIDs(ctx, rows)
	if got := rows[0]["Ссылка"]; got != "A-1" {
		t.Fatalf("resolved UUID primary presentation = %q, ожидалось A-1", got)
	}
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{
		"Наименование": "Старое имя", "Артикул": " ", "Описание": "",
	}, entity); err != nil {
		t.Fatal(err)
	}
	if got, want := recordPresentation(ctx, runner, entity.Name, id.String()), shortID(id.String()); got != want {
		t.Fatalf("empty explicit presentation = %q, ожидался id fallback %q", got, want)
	}
	rows[0]["Ссылка"] = id.String()
	runner.resolveUUIDs(ctx, rows)
	if got, want := rows[0]["Ссылка"], shortID(id.String()); got != want {
		t.Fatalf("resolved UUID empty explicit presentation = %q, ожидался id fallback %q", got, want)
	}
}
