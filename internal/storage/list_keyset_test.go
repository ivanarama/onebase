package storage_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestListKeysetBoundsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{
			Name:         "KeysetRows",
			Kind:         metadata.KindCatalog,
			Hierarchical: true,
			Fields: []metadata.Field{
				{Name: "Код", Type: metadata.FieldTypeString},
				{Name: "Наименование", Type: metadata.FieldTypeString},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		ids := []uuid.UUID{
			uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			uuid.MustParse("00000000-0000-0000-0001-000000000000"),
			uuid.MustParse("00000000-0000-0001-0000-000000000000"),
			uuid.MustParse("7fffffff-ffff-ffff-ffff-ffffffffffff"),
			uuid.MustParse("80000000-0000-0000-0000-000000000000"),
			uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
		}
		for i, id := range ids[:5] {
			// Значения намеренно идут против id: у иерархического справочника
			// обычный List сортировал бы по первому строковому полю.
			fields := map[string]any{
				"Код":          fmt.Sprintf("%02d", len(ids)-i),
				"Наименование": fmt.Sprintf("Строка %d", i),
			}
			if err := db.Upsert(ctx, ent.Name, id, fields, ent); err != nil {
				t.Fatalf("Upsert(%s): %v", id, err)
			}
		}

		through := ids[4]
		page1 := listKeysetIDs(t, db, ent, storage.ListParams{
			ThroughID: &through, Limit: 2,
		})
		assertUUIDs(t, page1, ids[:2])

		// Удаление позади курсора сдвигает OFFSET, но не id-keyset.
		if err := db.Delete(ctx, ent.Name, ids[0]); err != nil {
			t.Fatalf("Delete(%s): %v", ids[0], err)
		}
		// Новая запись выше зафиксированной high-water границы не должна попасть.
		if err := db.Upsert(ctx, ent.Name, ids[5], map[string]any{
			"Код": "00", "Наименование": "Новая",
		}, ent); err != nil {
			t.Fatalf("Upsert(new): %v", err)
		}

		after := ids[1]
		page2 := listKeysetIDs(t, db, ent, storage.ListParams{
			AfterID: &after, ThroughID: &through, Limit: 2,
		})
		assertUUIDs(t, page2, ids[2:4])
		after = ids[3]
		page3 := listKeysetIDs(t, db, ent, storage.ListParams{
			AfterID: &after, ThroughID: &through, Limit: 2,
		})
		assertUUIDs(t, page3, ids[4:5])

		got := append(append(page1, page2...), page3...)
		assertUUIDs(t, got, ids[:5])

		// AfterID исключителен, ThroughID включителен.
		after = ids[1]
		inclusive := ids[3]
		window := listKeysetIDs(t, db, ent, storage.ListParams{
			AfterID: &after, ThroughID: &inclusive, Limit: 10,
		})
		assertUUIDs(t, window, ids[2:4])

		// Удаление самой high-water строки не мешает завершить последний запрос.
		if err := db.Delete(ctx, ent.Name, through); err != nil {
			t.Fatalf("Delete(through): %v", err)
		}
		after = ids[3]
		if tail := listKeysetIDs(t, db, ent, storage.ListParams{
			AfterID: &after, ThroughID: &through, Limit: 2,
		}); len(tail) != 0 {
			t.Fatalf("после удаления through получен хвост %v", tail)
		}

		for name, params := range map[string]storage.ListParams{
			"offset": {AfterID: &ids[1], Offset: 1},
			"sort":   {AfterID: &ids[1], Sort: "Наименование"},
			"desc":   {AfterID: &ids[1], Dir: "desc"},
		} {
			t.Run("reject_"+name, func(t *testing.T) {
				if _, err := db.List(ctx, ent.Name, ent, params); err == nil {
					t.Fatal("ожидалась ошибка несовместимых параметров keyset")
				}
			})
		}
	})
}

func listKeysetIDs(t *testing.T, db *storage.DB, ent *metadata.Entity, params storage.ListParams) []uuid.UUID {
	t.Helper()
	rows, err := db.List(context.Background(), ent.Name, ent, params)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		id, err := uuid.Parse(fmt.Sprintf("%v", row["id"]))
		if err != nil {
			t.Fatalf("row %d id %v: %v", i, row["id"], err)
		}
		ids[i] = id
	}
	return ids
}

func assertUUIDs(t *testing.T, got, want []uuid.UUID) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("id = %v, ожидались %v", got, want)
	}
}
