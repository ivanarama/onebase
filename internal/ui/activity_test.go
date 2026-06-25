package ui

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestReferenceOptions_HidesInactiveOnlyForChoices(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ent := activityCatalogEntity()
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Показывать", "Активный": true}, ent); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Скрыть", "Активный": false}, ent); err != nil {
		t.Fatal(err)
	}

	srv := &Server{store: db}
	choiceRows, err := srv.referenceOptions(ctx, ent, refOptionsChoice)
	if err != nil {
		t.Fatalf("referenceOptions choice: %v", err)
	}
	if labels := optionLabels(choiceRows); !sameStringSet(labels, []string{"Показывать"}) {
		t.Fatalf("choice labels = %v, want only active", labels)
	}

	filterRows, err := srv.referenceOptions(ctx, ent, refOptionsFilter)
	if err != nil {
		t.Fatalf("referenceOptions filter: %v", err)
	}
	if labels := optionLabels(filterRows); !sameStringSet(labels, []string{"Показывать", "Скрыть"}) {
		t.Fatalf("filter labels = %v, want active and inactive", labels)
	}
}

func TestPageList_ActivityControlsAndActions(t *testing.T) {
	ent := activityCatalogEntity()
	html := renderPageList(t, map[string]any{
		"Entity": ent,
		"Rows": []map[string]any{{
			"id":                 "11111111-1111-1111-1111-111111111111",
			"Наименование":       "Скрыть",
			"Активный":           false,
			"_activity_inactive": true,
		}},
		"Params":           storage.ListParams{ActivityScope: metadata.ActivityScopeInactive},
		"RefFilterOptions": map[string]any{},
		"CanWrite":         true,
		"Lang":             "ru",
		"Total":            1,
		"Page":             1,
		"TotalPages":       1,
	})

	for _, want := range []string{
		"Активные",
		"Скрытые",
		"Все",
		`name="activity" value="inactive"`,
		`data-activity-enabled="1"`,
		`data-activity-inactive="1"`,
		`data-activity-hide-url=`,
		`/11111111-1111-1111-1111-111111111111/activity?active=0`,
		"Скрыть из выбора",
		"Вернуть в выбор",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("activity list HTML does not contain %q", want)
		}
	}
}

func activityCatalogEntity() *metadata.Entity {
	return &metadata.Entity{
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
}

func optionLabels(rows []map[string]any) []string {
	labels := make([]string, 0, len(rows))
	for _, row := range rows {
		if s, ok := row["_label"].(string); ok {
			labels = append(labels, s)
		}
	}
	return labels
}

func sameStringSet(a, b []string) bool {
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
