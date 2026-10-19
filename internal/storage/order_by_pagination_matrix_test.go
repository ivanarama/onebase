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

// Fixed UUIDs and reverse insertion make ties observable even when a database
// happens to return the same rows on repeated queries without a total order.
func TestOrderByStablePaginationMatrix(t *testing.T) {
	type row struct {
		number any
		text   any
		folder bool
	}
	equal := []row{{number: "20"}, {number: "20"}, {number: "20"}, {number: "20"}, {number: "20"}, {number: "20"}, {number: "20"}}
	composite := []row{
		{number: "20", text: "A"}, {number: "3", text: "A"},
		{number: "3", text: "B"}, {number: "3", text: "B"},
		{number: "100", text: "Z"}, {number: "20", text: "C"}, {number: "20", text: "C"},
	}
	emptyTextEqual := []row{
		{number: "20"}, {number: "20", text: ""}, {number: "20", text: "B"},
		{number: "20", text: "A"}, {number: "20"}, {number: "20", text: ""}, {number: "20", text: "C"},
	}
	emptyTextDifferent := []row{
		{number: "100"}, {number: "3", text: ""}, {number: "20"},
		{number: "20", text: ""}, {number: "20", text: "A"}, {number: "3", text: "B"}, {number: "3", text: "A"},
	}
	emptyNumber := []row{{}, {number: "100"}, {number: "3"}, {number: "20"}, {}, {number: "3"}, {number: "20"}}
	hierarchy := []row{
		{number: "3", text: "B"}, {number: "20", text: "A", folder: true},
		{number: "3", text: "C", folder: true}, {number: "3", text: "A"},
		{number: "3", text: "C", folder: true}, {number: "3", text: "A"}, {number: "20", text: "A", folder: true},
	}
	for _, tc := range []struct {
		name         string
		orderBy      []string
		rows         []row
		hierarchical bool
		want         []int
	}{
		{"equal_numbers", []string{"Ранг"}, equal, false, []int{1, 2, 3, 4, 5, 6, 7}},
		{"composite_asc_desc", []string{"Ранг", "Текст desc"}, composite, false, []int{3, 4, 2, 6, 7, 1, 5}},
		{"composite_desc_asc", []string{"Ранг desc", "Текст"}, composite, false, []int{5, 1, 6, 7, 2, 3, 4}},
		{"empty_text_equal_asc", []string{"Текст", "Ранг"}, emptyTextEqual, false, []int{4, 3, 7, 1, 2, 5, 6}},
		{"empty_text_equal_desc", []string{"Текст desc", "Ранг"}, emptyTextEqual, false, []int{7, 3, 4, 1, 2, 5, 6}},
		{"empty_text_secondary_asc", []string{"Текст", "Ранг"}, emptyTextDifferent, false, []int{7, 5, 6, 2, 3, 4, 1}},
		{"empty_text_secondary_desc", []string{"Текст desc", "Ранг desc"}, emptyTextDifferent, false, []int{6, 5, 7, 1, 3, 4, 2}},
		{"empty_number_asc", []string{"Ранг"}, emptyNumber, false, []int{3, 6, 4, 7, 2, 1, 5}},
		{"empty_number_desc", []string{"Ранг desc"}, emptyNumber, false, []int{2, 4, 7, 3, 6, 1, 5}},
		{"hierarchy", []string{"Ранг", "Текст desc"}, hierarchy, true, []int{3, 5, 2, 7, 1, 4, 6}},
		{"unknown_keys", []string{"УдалённыйКлюч", "ДругойКлюч desc"}, composite, false, []int{1, 2, 3, 4, 5, 6, 7}},
		{"hierarchy_unknown_keys", []string{"УдалённыйКлюч"}, hierarchy, true, []int{2, 3, 5, 7, 1, 4, 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := context.Background()
				entity := &metadata.Entity{
					Name: "СтраницыПорядка" + uuid.NewString()[:8], Kind: metadata.KindCatalog,
					Hierarchical: tc.hierarchical,
					Fields: []metadata.Field{
						{Name: "Ранг", Type: metadata.FieldTypeNumber},
						{Name: "Текст", Type: metadata.FieldTypeString},
						{Name: "Выборка", Type: metadata.FieldTypeString},
					},
				}
				if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
					t.Fatalf("Migrate: %v", err)
				}
				// Apply after Migrate to exercise stale metadata as well as valid
				// keys through List, without asking migration to accept bad YAML.
				entity.OrderBy = tc.orderBy
				for i := len(tc.rows) - 1; i >= 0; i-- {
					r := tc.rows[i]
					fields := map[string]any{"Выборка": "keep"}
					if r.number != nil {
						fields["Ранг"] = r.number
					}
					if r.text != nil {
						fields["Текст"] = r.text
					}
					if tc.hierarchical {
						fields["is_folder"] = r.folder
					}
					if err := db.Upsert(ctx, entity.Name, orderByPageID(i+1), fields, entity); err != nil {
						t.Fatalf("Upsert %d: %v", i+1, err)
					}
				}
				// An extra row sorts first but must remain outside every page.
				if err := db.Upsert(ctx, entity.Name, orderByPageID(0), map[string]any{
					"Ранг": "0", "Текст": "0", "Выборка": "excluded",
				}, entity); err != nil {
					t.Fatalf("Upsert excluded: %v", err)
				}
				params := storage.ListParams{
					Filters:            map[string]storage.FilterValue{"Выборка": {Value: "keep"}},
					RowFilterEvaluated: true,
				}
				if tc.hierarchical {
					params.ParentStr = "root"
				}
				want := make([]string, len(tc.want))
				for i, id := range tc.want {
					want[i] = orderByPageID(id).String()
				}
				listIDs := func(p storage.ListParams) []string {
					t.Helper()
					rows, err := db.List(ctx, entity.Name, entity, p)
					if err != nil {
						t.Fatalf("List offset=%d limit=%d: %v", p.Offset, p.Limit, err)
					}
					ids := make([]string, len(rows))
					for i, r := range rows {
						ids[i] = fmt.Sprint(r["id"])
					}
					return ids
				}
				full := listIDs(params)
				if !reflect.DeepEqual(full, want) {
					t.Fatalf("full UUID order = %v, want %v", full, want)
				}
				for _, size := range []int{1, 2} {
					var first []string
					for pass := 0; pass < 2; pass++ {
						var all []string
						for offset := 0; offset < len(want); offset += size {
							pageParams := params
							pageParams.Limit, pageParams.Offset = size, offset
							page := listIDs(pageParams)
							end := offset + size
							if end > len(want) {
								end = len(want)
							}
							if !reflect.DeepEqual(page, want[offset:end]) {
								t.Fatalf("size=%d pass=%d offset=%d UUIDs = %v, want %v", size, pass, offset, page, want[offset:end])
							}
							all = append(all, page...)
						}
						tailParams := params
						tailParams.Limit, tailParams.Offset = size, len(want)
						if tail := listIDs(tailParams); len(tail) != 0 {
							t.Fatalf("rows beyond final page: %v", tail)
						}
						if !reflect.DeepEqual(all, want) || !reflect.DeepEqual(all, full) {
							t.Fatalf("combined pages = %v, want %v", all, want)
						}
						if pass == 0 {
							first = all
						} else if !reflect.DeepEqual(all, first) {
							t.Fatalf("repeat UUID order = %v, first %v", all, first)
						}
					}
				}
			})
		})
	}
}

func orderByPageID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
}
