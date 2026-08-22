package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func schemaGapCatalog(name string) *metadata.Entity {
	return &metadata.Entity{
		Name: name,
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Numerator: &metadata.Numerator{Prefix: "К-", Length: 6},
	}
}

func schemaGapDocument(name string) *metadata.Entity {
	return &metadata.Entity{
		Name: name,
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: metadata.StandardNumberField, Type: metadata.FieldTypeString},
			{Name: "Дата", Type: metadata.FieldTypeDate},
		},
		Numerator: &metadata.Numerator{Prefix: "Д-", Length: 6},
	}
}

func assertUnreadableSchemaGap(t *testing.T, db *storage.DB, ent *metadata.Entity, missing ...string) {
	t.Helper()
	ctx := context.Background()
	gap, err := renumberSchemaGap(ctx, db, ent)
	if err != nil {
		t.Fatalf("renumberSchemaGap: %v", err)
	}
	if gap == "" {
		t.Fatal("отставшая схема признана читаемой")
	}
	for _, col := range missing {
		if !strings.Contains(gap, col) {
			t.Errorf("причина %q не называет колонку %s", gap, col)
		}
	}
	if _, err := db.List(ctx, ent.Name, ent, storage.ListParams{Limit: 1}); err == nil {
		t.Fatal("fixture должна подтверждать, что DB.List без этих колонок не работает")
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatalf("догоняющая миграция: %v", err)
	}
	gap, err = renumberSchemaGap(ctx, db, ent)
	if err != nil {
		t.Fatalf("renumberSchemaGap после миграции: %v", err)
	}
	if gap != "" {
		t.Fatalf("догнавшая схема всё ещё пропущена: %s", gap)
	}
	if _, err := db.List(ctx, ent.Name, ent, storage.ListParams{Limit: 1}); err != nil {
		t.Fatalf("DB.List после миграции: %v", err)
	}
}

// Миграция идёт по объектам и может остановиться на гейте уникальности до
// следующего объекта. У него от конфигурации тогда отстают не только обычные
// реквизиты, но и условные служебные колонки List. Матрица держит одинаковую
// диагностику на SQLite и PostgreSQL (PG запускается при TEST_DATABASE_URL).
func TestRenumberSchemaGapListColumnsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		t.Run("hierarchy enabled", func(t *testing.T) {
			stale := schemaGapCatalog("GapHierarchy" + uuid.NewString()[:8])
			if err := db.Migrate(ctx, []*metadata.Entity{stale}); err != nil {
				t.Fatal(err)
			}
			current := *stale
			current.Hierarchical = true
			assertUnreadableSchemaGap(t, db, &current, "is_folder", "parent_id")
		})

		t.Run("predefined enabled", func(t *testing.T) {
			stale := schemaGapCatalog("GapPredefined" + uuid.NewString()[:8])
			if err := db.Migrate(ctx, []*metadata.Entity{stale}); err != nil {
				t.Fatal(err)
			}
			current := *stale
			current.Predefined = []*metadata.PredefinedItem{{Name: "Основной"}}
			assertUnreadableSchemaGap(t, db, &current, "_is_predefined")
		})

		t.Run("document service columns", func(t *testing.T) {
			current := schemaGapDocument("GapDocument" + uuid.NewString()[:8])
			if err := db.Migrate(ctx, []*metadata.Entity{current}); err != nil {
				t.Fatal(err)
			}
			table := metadata.TableName(current.Name)
			for _, col := range []string{"posted", "deletion_mark"} {
				if _, err := db.Exec(ctx, "ALTER TABLE "+table+" DROP COLUMN "+col); err != nil {
					t.Fatalf("удаление %s: %v", col, err)
				}
			}
			assertUnreadableSchemaGap(t, db, current, "posted", "deletion_mark")
		})

		t.Run("introspection error remains fatal", func(t *testing.T) {
			ent := schemaGapCatalog("GapCanceled" + uuid.NewString()[:8])
			if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if gap, err := renumberSchemaGap(canceled, db, ent); err == nil {
				t.Fatalf("ошибка контекста выдана за schema lag %q", gap)
			}
			if _, err := renumberEntity(canceled, db, ent, false); err == nil {
				t.Fatal("ошибка List на отменённом контексте скрыта как успех")
			}
		})

		t.Run("missing primary key remains fatal", func(t *testing.T) {
			ent := schemaGapCatalog("GapNoID" + uuid.NewString()[:8])
			table := metadata.TableName(ent.Name)
			d := db.Dialect()
			q := "CREATE TABLE " + table + " (код " + d.TypeText() +
				", наименование " + d.TypeText() +
				", deletion_mark " + d.TypeBool() + ")"
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
			gap, err := renumberSchemaGap(ctx, db, ent)
			if err == nil {
				t.Fatalf("таблица без id выдана за schema lag %q", gap)
			}
			if gap != "" {
				t.Fatalf("повреждение таблицы вернуло skip: %q", gap)
			}
		})
	})
}
