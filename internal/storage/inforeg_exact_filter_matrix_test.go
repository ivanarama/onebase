package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegExactMatchesRowFilterUsesFullPrimaryKeyMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		instant := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)
		ir := &metadata.InfoRegister{
			Name: "ExactRowPolicyKey",
			Dimensions: []metadata.Field{
				{Name: "Slice", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{{Name: "EventAt", Type: metadata.FieldTypeDate}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Slice": "S", "Key": ""},
			map[string]any{"EventAt": instant}, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.InfoRegSet(ctx, ir,
			map[string]any{"Slice": "S", "Key": "A"},
			map[string]any{"EventAt": instant.Add(time.Hour)}, nil); err != nil {
			t.Fatal(err)
		}
		policy := &storage.Predicate{Field: "EventAt", Op: "ne", Value: instant}

		matches, err := db.InfoRegExactMatchesRowFilter(ctx, ir,
			map[string]any{"Slice": "S", "Key": ""}, nil, policy)
		if err != nil {
			t.Fatal(err)
		}
		if matches {
			t.Fatal("empty-key row matched policy through an allowed sibling")
		}

		matches, err = db.InfoRegExactMatchesRowFilter(ctx, ir,
			map[string]any{"Slice": "S", "Key": "A"}, nil, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !matches {
			t.Fatal("exact allowed row did not match its SQL policy")
		}
	})
}
