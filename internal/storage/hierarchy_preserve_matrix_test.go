package storage_test

// Иерархия справочника при повторной записи (#1040).
//
// Признак группы и родитель хранятся служебными колонками, которых нет в
// метаданных: ПолучитьОбъект() их не читает, а запись писала всегда — отсутствие
// ключа означало «не группа, без родителя», а не «не трогать». Любая правка
// группы из DSL (переименование, простановка слага, повторный импорт) молча
// разрушала дерево: данные на месте, иерархии нет, а заметно это только когда
// кто-то открывает список.
//
// Матричный: правило живёт в общем SQL записи, и расхождение диалектов здесь
// увидеть иначе нечем.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// hierRef — ссылка в том виде, в каком её кладёт DSL: значение, умеющее отдать
// свой UUID (интерфейс refUUIDer в storage реализует *interpreter.Ref).
type hierRef struct{ id string }

func (r hierRef) GetRefUUID() string { return r.id }

func hierCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Товары", Kind: metadata.KindCatalog, Hierarchical: true,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Слаг", Type: metadata.FieldTypeString},
		},
	}
}

// asBoolValue и valueString читают служебные колонки: SQLite отдаёт булево
// числом, PostgreSQL — bool, а идентификатор приходит строкой или []byte.
func asBoolValue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	case string:
		return t == "true" || t == "1"
	}
	return false
}

func valueString(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func hierRow(t *testing.T, db *storage.DB, ctx context.Context, ent *metadata.Entity, id uuid.UUID) map[string]any {
	t.Helper()
	row, err := db.GetByID(ctx, ent.Name, id, ent)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		t.Fatalf("запись %s не найдена", id)
	}
	return row
}

func TestHierarchyPreservedOnRewrite_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := hierCatalog()
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}

		t.Run("русские имена служебных полей работают", func(t *testing.T) {
			// «Объект.ЭтоГруппа = Истина» раньше не делало ничего: ключ ложился
			// в набор под своим именем и до колонки не доезжал.
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "Насосы", "ЭтоГруппа": true}, ent); err != nil {
				t.Fatal(err)
			}
			if !asBoolValue(hierRow(t, db, ctx, ent, id)["is_folder"]) {
				t.Fatal("ЭтоГруппа = Истина не создало группу")
			}
		})

		t.Run("родитель принимает ссылку, а не только строку", func(t *testing.T) {
			// DSL кладёт в поле-родитель именно ссылку; требовать
			// Строка(Х.УникальныйИдентификатор()) — значит требовать знания о
			// внутреннем устройстве.
			group := uuid.New()
			if err := db.Upsert(ctx, ent.Name, group,
				map[string]any{"наименование": "Насосы", "ЭтоГруппа": true}, ent); err != nil {
				t.Fatal(err)
			}
			item := uuid.New()
			if err := db.Upsert(ctx, ent.Name, item,
				map[string]any{"наименование": "НЦ-100", "Родитель": hierRef{id: group.String()}}, ent); err != nil {
				t.Fatal(err)
			}
			if got := valueString(hierRow(t, db, ctx, ent, item)["parent_id"]); got != group.String() {
				t.Fatalf("родитель = %q, ожидался %s — ссылка не распознана", got, group)
			}
		})

		t.Run("повторная запись без служебных полей их не сбрасывает", func(t *testing.T) {
			// Главный случай заявки: правка группы из DSL шла через
			// ПолучитьОбъект(), который служебные поля не читает.
			parent := uuid.New()
			if err := db.Upsert(ctx, ent.Name, parent,
				map[string]any{"наименование": "Оборудование", "ЭтоГруппа": true}, ent); err != nil {
				t.Fatal(err)
			}
			group := uuid.New()
			if err := db.Upsert(ctx, ent.Name, group, map[string]any{
				"наименование": "Насосы", "ЭтоГруппа": true, "Родитель": parent.String(),
			}, ent); err != nil {
				t.Fatal(err)
			}

			// Переименование: в наборе только реквизиты из метаданных.
			if err := db.Upsert(ctx, ent.Name, group,
				map[string]any{"наименование": "Насосное оборудование", "слаг": "nasosy"}, ent); err != nil {
				t.Fatal(err)
			}

			row := hierRow(t, db, ctx, ent, group)
			if !asBoolValue(row["is_folder"]) {
				t.Error("повторная запись превратила группу в обычный элемент")
			}
			if got := valueString(row["parent_id"]); got != parent.String() {
				t.Errorf("родитель после повторной записи = %q, ожидался %s", got, parent)
			}
			if got := valueString(row["Наименование"]); got != "Насосное оборудование" {
				t.Errorf("реквизит не обновился: %q", got)
			}
		})

		t.Run("явное значение по-прежнему применяется", func(t *testing.T) {
			// Сохранение прежнего значения не должно превратиться в
			// невозможность его изменить: переданный ключ обязан сработать.
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "Раздел", "ЭтоГруппа": true}, ent); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "Раздел", "ЭтоГруппа": false}, ent); err != nil {
				t.Fatal(err)
			}
			if asBoolValue(hierRow(t, db, ctx, ent, id)["is_folder"]) {
				t.Error("явное ЭтоГруппа = Ложь не сработало")
			}
		})

		t.Run("пустой родитель означает «убрать родителя»", func(t *testing.T) {
			parent := uuid.New()
			if err := db.Upsert(ctx, ent.Name, parent,
				map[string]any{"наименование": "Родитель", "ЭтоГруппа": true}, ent); err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "Позиция", "Родитель": parent.String()}, ent); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "Позиция", "Родитель": ""}, ent); err != nil {
				t.Fatal(err)
			}
			if got := valueString(hierRow(t, db, ctx, ent, id)["parent_id"]); got != "" {
				t.Errorf("родитель после явной очистки = %q, ожидалась пустота", got)
			}
		})
	})
}

func TestHierarchyVersionedPreservesAndUpdatesServiceFields_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := hierCatalog()
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}

		parent := uuid.New()
		if err := db.Upsert(ctx, ent.Name, parent,
			map[string]any{"наименование": "Оборудование", "ЭтоГруппа": true}, ent); err != nil {
			t.Fatal(err)
		}
		group := uuid.New()
		if err := db.Upsert(ctx, ent.Name, group, map[string]any{
			"наименование": "Насосы", "ЭтоГруппа": true, "Родитель": parent.String(),
		}, ent); err != nil {
			t.Fatal(err)
		}

		version, err := db.EntityVersion(ctx, ent.Name, group)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertVersioned(ctx, ent.Name, group,
			map[string]any{"наименование": "Насосное оборудование", "слаг": "nasosy"}, ent, &version); err != nil {
			t.Fatal(err)
		}
		row := hierRow(t, db, ctx, ent, group)
		if !asBoolValue(row["is_folder"]) {
			t.Error("версионная запись без служебных полей превратила группу в обычный элемент")
		}
		if got := valueString(row["parent_id"]); got != parent.String() {
			t.Errorf("родитель после версионной записи = %q, ожидался %s", got, parent)
		}

		newParent := uuid.New()
		if err := db.Upsert(ctx, ent.Name, newParent,
			map[string]any{"наименование": "Архив", "ЭтоГруппа": true}, ent); err != nil {
			t.Fatal(err)
		}
		version, err = db.EntityVersion(ctx, ent.Name, group)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertVersioned(ctx, ent.Name, group, map[string]any{
			"наименование": "Насосное оборудование", "Родитель": hierRef{id: newParent.String()}, "ЭтоГруппа": false,
		}, ent, &version); err != nil {
			t.Fatal(err)
		}
		row = hierRow(t, db, ctx, ent, group)
		if asBoolValue(row["is_folder"]) {
			t.Error("версионная запись не применила ЭтоГруппа = Ложь")
		}
		if got := valueString(row["parent_id"]); got != newParent.String() {
			t.Errorf("родитель из DSL-ссылки = %q, ожидался %s", got, newParent)
		}

		version, err = db.EntityVersion(ctx, ent.Name, group)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertVersioned(ctx, ent.Name, group,
			map[string]any{"наименование": "Насосное оборудование", "Родитель": ""}, ent, &version); err != nil {
			t.Fatal(err)
		}
		row = hierRow(t, db, ctx, ent, group)
		if got := valueString(row["parent_id"]); got != "" {
			t.Errorf("родитель после явной версионной очистки = %q, ожидалась пустота", got)
		}
	})
}
