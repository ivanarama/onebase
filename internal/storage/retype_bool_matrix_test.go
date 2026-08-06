package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// truthy приводит значение булева реквизита к bool независимо от диалекта:
// PostgreSQL отдаёт bool, SQLite хранит булево в INTEGER и отдаёт int64.
func truthy(t *testing.T, v any) bool {
	t.Helper()
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case string:
		return x == "true" || x == "1" || x == "t"
	default:
		t.Fatalf("неожиданный тип булева значения %T (%v)", v, v)
		return false
	}
}

func boolRetypeCatalog(t *testing.T, typ metadata.FieldType) *metadata.Entity {
	t.Helper()
	return &metadata.Entity{
		Name: "РетайпБул",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			// ID стабилен — значит смена Type это ретайп, а не «поле исчезло,
			// другое появилось» (план 81).
			{ID: "f_flag", Name: "Активен", Type: typ},
		},
	}
}

// Регрессия к issue #607: ретайп string→boolean обязан сохранять истинные
// значения на ОБОИХ диалектах.
//
// До фикса на SQLite перенос значений шёл через CAST(колонка AS INTEGER), а
// SQLite приводит к целому только числовые литералы: CAST('true' AS INTEGER)
// = 0. При этом checkConvertible считает 'true'/'t'/'yes'/'on' годными
// значениями, поэтому миграция проходила успешно и молча превращала «истину»
// в «ложь». На PostgreSQL тот же сценарий отрабатывал верно (ALTER … USING),
// из-за чего раздельные тесты расхождения не показывали.
func TestRetypeStringToBoolKeepsTruthyValues(t *testing.T) {
	forEachDialect(t, func(t *testing.T, db *DB) {
		ctx := context.Background()

		before := boolRetypeCatalog(t, metadata.FieldTypeString)
		if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
			t.Fatalf("миграция (строка): %v", err)
		}

		// Все формы, которые valueChecker считает конвертируемыми в булево.
		cases := []struct {
			stored string
			want   bool
		}{
			{"true", true},
			{"t", true},
			{"yes", true},
			{"on", true},
			{"1", true},
			{"false", false},
			{"no", false},
			{"0", false},
		}
		ids := make([]uuid.UUID, len(cases))
		for i, c := range cases {
			ids[i] = uuid.New()
			row := map[string]any{"Наименование": fmt.Sprintf("Строка %d", i), "Активен": c.stored}
			if err := db.Upsert(ctx, before.Name, ids[i], row, before); err != nil {
				t.Fatalf("вставка %q: %v", c.stored, err)
			}
		}

		after := boolRetypeCatalog(t, metadata.FieldTypeBool)
		if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
			t.Fatalf("миграция (булево): %v", err)
		}

		for i, c := range cases {
			row, err := db.GetByID(ctx, after.Name, ids[i], after)
			if err != nil {
				t.Fatalf("чтение %q: %v", c.stored, err)
			}
			if row == nil {
				t.Fatalf("строка для %q исчезла после ретайпа", c.stored)
			}
			got := truthy(t, row["Активен"])
			if got != c.want {
				t.Errorf("значение %q после ретайпа в булево = %v, ожидалось %v (сырое: %#v)",
					c.stored, got, c.want, row["Активен"])
			}
		}
	})
}
