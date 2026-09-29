package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Круг ссылок между документами — не экзотика: заявка помнит обращение, из
// которого родилась, а обращение — заявку-дополнение. До починки такая
// конфигурация не мигрировала на PostgreSQL вовсе: порядок зависимостей круг не
// различал, внешний ключ объявлялся прямо в CREATE TABLE, и первая же таблица
// круга ссылалась на ещё не созданную («relation "заявка" does not exist»).
// На SQLite это проходило молча — там ссылка на будущую таблицу допустима,
// и тем надёжнее дефект прятался до первой установки на PostgreSQL.
func refCycleEntities() (appeal, request, task *metadata.Entity) {
	task = &metadata.Entity{
		Name:   "ЦиклЗадача",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	appeal = &metadata.Entity{
		Name: "ЦиклОбращение",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "ДополнениеКЗаявке", Type: metadata.FieldType("reference:ЦиклЗаявка"), RefEntity: "ЦиклЗаявка"},
		},
	}
	request = &metadata.Entity{
		Name: "ЦиклЗаявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Обращение", Type: metadata.FieldType("reference:ЦиклОбращение"), RefEntity: "ЦиклОбращение"},
			{Name: "Задача", Type: metadata.FieldType("reference:ЦиклЗадача"), RefEntity: "ЦиклЗадача"},
		},
	}
	return appeal, request, task
}

func TestMigrateReferenceCycle_Matrix(t *testing.T) {
	appeal, request, task := refCycleEntities()
	// Порядок сущностей решал исход: обход помечал сущность посещённой до
	// спуска в её ссылки, и круг разворачивался «не той» стороной. Поэтому
	// проверяются обе стороны, а не та, на которой дефект однажды поймали.
	orders := map[string][]*metadata.Entity{
		"заявка первой":    {request, appeal, task},
		"обращение первым": {appeal, request, task},
	}
	for name, entities := range orders {
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				if err := db.Migrate(ctx, entities); err != nil {
					t.Fatalf("круг ссылок не мигрировал: %v", err)
				}
				// Миграция идёт при каждом запуске: отложенный ключ добавляется
				// один раз, повтор не должен спотыкаться о «ключ уже есть».
				if err := db.Migrate(ctx, entities); err != nil {
					t.Fatalf("повторная миграция: %v", err)
				}

				// Отложенный ключ обязан быть НАСТОЯЩИМ ключом: если бы его
				// потеряли по дороге, база молча приняла бы висячую ссылку.
				// Проверяются обе стороны круга — какая из них отложена,
				// зависит от порядка обхода.
				for _, broken := range []struct {
					entity *metadata.Entity
					field  string
				}{
					{appeal, "ДополнениеКЗаявке"},
					{request, "Обращение"},
				} {
					err := db.Upsert(ctx, broken.entity.Name, uuid.New(), map[string]any{
						"Номер":      "Б-1",
						broken.field: uuid.New().String(),
					}, broken.entity)
					if !errors.Is(err, storage.ErrForeignKeyViolation) {
						t.Fatalf("%s.%s: висячая ссылка принята (ошибка = %v)", broken.entity.Name, broken.field, err)
					}
				}

				// А законный круг записывается: сначала сторона без ссылки,
				// потом встречная, потом первая дополняется ссылкой назад.
				appealID, requestID := uuid.New(), uuid.New()
				if err := db.Upsert(ctx, appeal.Name, appealID, map[string]any{"Номер": "О-1"}, appeal); err != nil {
					t.Fatalf("обращение: %v", err)
				}
				if err := db.Upsert(ctx, request.Name, requestID, map[string]any{
					"Номер": "З-1", "Обращение": appealID.String(),
				}, request); err != nil {
					t.Fatalf("заявка на обращение: %v", err)
				}
				if err := db.Upsert(ctx, appeal.Name, appealID, map[string]any{
					"Номер": "О-1", "ДополнениеКЗаявке": requestID.String(),
				}, appeal); err != nil {
					t.Fatalf("обращение на заявку: %v", err)
				}
			})
		})
	}
}

// Самоссылка (иерархия, «родитель») кругом в этом смысле не является: такой
// ключ допустим прямо в CREATE TABLE на обоих диалектах, и откладывать его
// незачем — но миграция от него падать не должна тем более.
func TestMigrateSelfReference_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{
			Name: "ЦиклПодразделение",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Родитель", Type: metadata.FieldType("reference:ЦиклПодразделение"), RefEntity: "ЦиклПодразделение"},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatalf("самоссылка не мигрировала: %v", err)
		}

		head := uuid.New()
		if err := db.Upsert(ctx, entity.Name, head, map[string]any{"Наименование": "Головное"}, entity); err != nil {
			t.Fatalf("корень: %v", err)
		}
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
			"Наименование": "Филиал", "Родитель": head.String(),
		}, entity); err != nil {
			t.Fatalf("подчинённое: %v", err)
		}
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{
			"Наименование": "Ничьё", "Родитель": uuid.New().String(),
		}, entity); !errors.Is(err, storage.ErrForeignKeyViolation) {
			t.Fatalf("висячий родитель принят (ошибка = %v)", err)
		}
	})
}
