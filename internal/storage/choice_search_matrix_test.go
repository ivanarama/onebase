package storage_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestChoiceSearchIDsWithEightConditionsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ent := &metadata.Entity{Name: "SearchCandidates", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}}
		predicates := make([]storage.ChoicePredicate, 0, 8)
		for i := range 8 {
			name := fmt.Sprintf("Flag%d", i)
			ent.Fields = append(ent.Fields, metadata.Field{Name: name, Type: metadata.FieldTypeBool})
			predicates = append(predicates, storage.ChoicePredicate{Field: name, Op: metadata.FormChoiceOpEqual, Value: true})
		}
		if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		for i, id := range ids {
			fields := map[string]any{"Name": fmt.Sprintf("hit%d", i)}
			for j := range 8 {
				fields[fmt.Sprintf("Flag%d", j)] = i != 1 || j != 7
			}
			if err := db.Upsert(t.Context(), ent.Name, id, fields, ent); err != nil {
				t.Fatal(err)
			}
		}
		for _, tc := range []struct {
			name       string
			candidates []uuid.UUID
			rls        *storage.Predicate
			total      int
		}{
			{"nil keeps eight form conditions", nil, nil, 2},
			{"candidate excludes allowed rows", ids[:1], nil, 1},
			{"empty candidates deny all", []uuid.UUID{}, nil, 0},
			{"candidates cannot bypass eighth condition", ids[1:2], nil, 0},
			{"row access remains ANDed", ids, &storage.Predicate{Field: "Name", Op: "eq", Value: "hit0"}, 1},
		} {
			t.Run(tc.name, func(t *testing.T) {
				params := storage.ListParams{ChoicePredicates: predicates, ChoiceSearchIDs: tc.candidates, Search: "hit", Filters: map[string]storage.FilterValue{"Name": {Value: "hit"}}, RowFilter: tc.rls}
				rows, err := db.List(t.Context(), ent.Name, ent, params)
				if err != nil {
					t.Fatal(err)
				}
				total, err := db.CountList(t.Context(), ent.Name, ent, params)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != tc.total || total != tc.total {
					t.Fatalf("rows=%v total=%d want=%d", rows, total, tc.total)
				}
			})
		}
		// The service restriction grants no ninth public form condition.
		invalid := append(append([]storage.ChoicePredicate{}, predicates...), storage.ChoicePredicate{Field: "Name", Op: metadata.FormChoiceOpEqual, Value: "hit0"})
		if _, err := db.List(t.Context(), ent.Name, ent, storage.ListParams{ChoicePredicates: invalid, ChoiceSearchIDs: ids}); err == nil {
			t.Fatal("ninth form condition was accepted")
		}
	})
}
