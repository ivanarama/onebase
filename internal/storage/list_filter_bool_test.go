package storage_test

// Отбор списка по булеву реквизиту — точное сравнение, а не подстрока.
//
// Матричный: до исправления значение сравнивалось через LIKE '%…%', и на
// SQLite (булево хранится как 0/1) «Да» не находило ничего, а на PostgreSQL
// текст колонки — «true»/«false», так что поведение диалектов молча
// расходилось.

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestList_BoolFilterMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		done := metadata.Field{Name: "Выполнена", Type: metadata.FieldTypeBool}
		ent := &metadata.Entity{
			Name: "Задачи" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, done},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		for _, row := range []map[string]any{
			{"Наименование": "Выполненная", "Выполнена": true},
			{"Наименование": "Невыполненная", "Выполнена": false},
			{"Наименование": "Незаполненная"},
		} {
			if err := db.Upsert(ctx, ent.Name, uuid.New(), row, ent); err != nil {
				t.Fatalf("запись: %v", err)
			}
		}
		// Незаполненное значение — NULL, как у реквизита, добавленного к
		// таблице уже после записи строк.
		if _, err := db.Exec(ctx, "UPDATE "+metadata.TableName(ent.Name)+" SET "+metadata.ColumnName(done)+" = NULL WHERE "+
			metadata.ColumnName(metadata.Field{Name: "Наименование", Type: metadata.FieldTypeString})+" = 'Незаполненная'"); err != nil {
			t.Fatalf("NULL: %v", err)
		}

		for _, tc := range []struct {
			value string
			want  []string
		}{
			{"true", []string{"Выполненная"}},
			{"false", []string{"Невыполненная", "Незаполненная"}},
			// Старые адреса со словом, набранным в текстовом поле.
			{"Да", []string{"Выполненная"}},
			{"нет", []string{"Невыполненная", "Незаполненная"}},
			// Непонятное значение не открывает весь список.
			{"может быть", nil},
		} {
			params := storage.ListParams{Filters: map[string]storage.FilterValue{"Выполнена": {Value: tc.value}}}
			rows, err := db.List(ctx, ent.Name, ent, params)
			if err != nil {
				t.Fatalf("%q: List: %v", tc.value, err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r["Наименование"].(string))
			}
			sort.Strings(got)
			if len(got) != len(tc.want) {
				t.Fatalf("%q: got %v, want %v", tc.value, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("%q: got %v, want %v", tc.value, got, tc.want)
				}
			}
			n, err := db.CountList(ctx, ent.Name, ent, params)
			if err != nil {
				t.Fatalf("%q: CountList: %v", tc.value, err)
			}
			if n != len(tc.want) {
				t.Fatalf("%q: CountList = %d, want %d", tc.value, n, len(tc.want))
			}
		}
	})
}
