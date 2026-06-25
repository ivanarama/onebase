package storage

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestActivityScopeFiltersAndSetActivity(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entity := &metadata.Entity{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Активный", Type: metadata.FieldTypeBool},
		},
		Activity: &metadata.ActivityConfig{
			Field:          "Активный",
			DefaultScope:   metadata.ActivityScopeActive,
			HideFromChoice: true,
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}

	activeID := uuid.New()
	inactiveID := uuid.New()
	unsetID := uuid.New()
	if err := db.Upsert(ctx, entity.Name, activeID, map[string]any{"Наименование": "Летний товар", "Активный": true}, entity); err != nil {
		t.Fatalf("upsert active: %v", err)
	}
	if err := db.Upsert(ctx, entity.Name, inactiveID, map[string]any{"Наименование": "Зимний товар", "Активный": false}, entity); err != nil {
		t.Fatalf("upsert inactive: %v", err)
	}
	if err := db.Upsert(ctx, entity.Name, unsetID, map[string]any{"Наименование": "Старый товар"}, entity); err != nil {
		t.Fatalf("upsert unset: %v", err)
	}

	activeRows, err := db.List(ctx, entity.Name, entity, ListParams{ActivityScope: metadata.ActivityScopeActive})
	if err != nil {
		t.Fatalf("List active: %v", err)
	}
	if got := rowNames(activeRows); !sameStrings(got, []string{"Летний товар", "Старый товар"}) {
		t.Fatalf("active rows = %v, want explicit active plus NULL", got)
	}
	if count, err := db.CountList(ctx, entity.Name, entity, ListParams{ActivityScope: metadata.ActivityScopeActive}); err != nil || count != 2 {
		t.Fatalf("CountList active = %d, %v; want 2", count, err)
	}

	inactiveRows, err := db.List(ctx, entity.Name, entity, ListParams{ActivityScope: metadata.ActivityScopeInactive})
	if err != nil {
		t.Fatalf("List inactive: %v", err)
	}
	if got := rowNames(inactiveRows); !sameStrings(got, []string{"Зимний товар"}) {
		t.Fatalf("inactive rows = %v, want only explicit false", got)
	}

	allRows, err := db.List(ctx, entity.Name, entity, ListParams{ActivityScope: metadata.ActivityScopeAll})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if got := rowNames(allRows); !sameStrings(got, []string{"Зимний товар", "Летний товар", "Старый товар"}) {
		t.Fatalf("all rows = %v, want all records", got)
	}

	if err := db.SetActivity(ctx, entity, activeID, false); err != nil {
		t.Fatalf("SetActivity false: %v", err)
	}
	inactiveRows, err = db.List(ctx, entity.Name, entity, ListParams{ActivityScope: metadata.ActivityScopeInactive})
	if err != nil {
		t.Fatalf("List inactive after SetActivity: %v", err)
	}
	if got := rowNames(inactiveRows); !sameStrings(got, []string{"Зимний товар", "Летний товар"}) {
		t.Fatalf("inactive after SetActivity = %v, want two hidden records", got)
	}
}

func rowNames(rows []map[string]any) []string {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if s, ok := row["Наименование"].(string); ok {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	return names
}

func sameStrings(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
