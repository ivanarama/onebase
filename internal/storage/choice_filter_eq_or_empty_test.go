package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// eq_or_empty — «равно источнику ИЛИ ссылка пуста»: так 1С отбирает общие
// записи вместе со своими (запись без филиала видна любому филиалу). Меняет
// SQL, поэтому матричный тест: одно тело на SQLite и PostgreSQL.
func TestChoiceFilterEqualOrEmptyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		fixture := seedChoiceFilterFixture(t, db)
		// Общая запись: направление не задано вовсе.
		if err := db.Upsert(context.Background(), fixture.target.Name, choiceTestUUID("000000000201"), map[string]any{
			"Наименование": "common alpha",
			"Owner":        "alice",
		}, fixture.target); err != nil {
			t.Fatalf("seed common: %v", err)
		}
		notFolder := storage.ChoicePredicate{Field: "is_folder", Op: metadata.FormChoiceOpEqual, Value: false}

		t.Run("own and common rows", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
				{Field: "Направление", Op: metadata.FormChoiceOpEqualOrEmpty, Value: fixture.child.String()},
				notFolder,
			}}, 3)
			got := strings.Join(choiceRowNames(rows), ",")
			for _, want := range []string{"child alpha", "child blocked", "common alpha"} {
				if !strings.Contains(got, want) {
					t.Fatalf("eq_or_empty rows %q miss %q", got, want)
				}
			}
			if strings.Contains(got, "root alpha") || strings.Contains(got, "sibling") {
				t.Fatalf("eq_or_empty leaked foreign rows: %q", got)
			}
		})

		t.Run("empty source leaves only common rows", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
				{Field: "Направление", Op: metadata.FormChoiceOpEqualOrEmpty, Value: nil},
				notFolder,
			}}, 1)
			if got := strings.Join(choiceRowNames(rows), ","); got != "common alpha" {
				t.Fatalf("empty-source rows = %q, want common alpha", got)
			}
		})

		t.Run("plain eq still hides common rows", func(t *testing.T) {
			rows := assertChoiceListAndCount(t, db, fixture, storage.ListParams{ChoicePredicates: []storage.ChoicePredicate{
				{Field: "Направление", Op: metadata.FormChoiceOpEqual, Value: fixture.child.String()},
				notFolder,
			}}, 2)
			if strings.Contains(strings.Join(choiceRowNames(rows), ","), "common") {
				t.Fatalf("eq returned common row: %v", choiceRowNames(rows))
			}
		})
	})
}
