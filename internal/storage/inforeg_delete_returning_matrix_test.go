package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegDeleteByFilterReturningMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name: "DeleteReturning",
			Dimensions: []metadata.Field{
				{Name: "Segment", Type: metadata.FieldTypeString},
				{Name: "Key", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		put := func(group, key, value string) {
			t.Helper()
			if err := db.InfoRegSet(ctx, ir,
				map[string]any{"Segment": group, "Key": key},
				map[string]any{"Value": value}, nil); err != nil {
				t.Fatal(err)
			}
		}
		put("A", "1", "one")
		put("A", "2", "two")
		put("B", "3", "three")

		rollback := errors.New("rollback returning delete")
		var deleted []map[string]any
		err := db.WithTxScope(ctx, func(txCtx context.Context) error {
			var err error
			deleted, err = db.InfoRegDeleteByFilterReturning(txCtx, ir,
				storage.RegFilter{Dims: map[string]string{"Segment": "A"}})
			if err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("rollback error = %v", err)
		}
		if len(deleted) != 2 {
			t.Fatalf("DELETE RETURNING returned %d rows: %#v", len(deleted), deleted)
		}
		got := map[string]string{}
		for _, row := range deleted {
			got[row["Key"].(string)] = row["Value"].(string)
		}
		if got["1"] != "one" || got["2"] != "two" {
			t.Fatalf("returned rows = %#v", deleted)
		}

		// Returning rows are provisional until the surrounding scope commits.
		// A policy denial after DELETE must restore the complete slice.
		rows, err := db.InfoRegList(ctx, ir,
			storage.RegFilter{Dims: map[string]string{"Segment": "A"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Fatalf("rollback preserved %d rows, want 2: %#v", len(rows), rows)
		}
	})
}

func TestInfoRegDeleteByFilterReturningKeepsTypedPolicyFieldsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := &metadata.InfoRegister{
			Name:       "DeleteReturningPolicyFields",
			Periodic:   true,
			Recorder:   true,
			Dimensions: []metadata.Field{{Name: "Key", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Value", Type: metadata.FieldTypeString}},
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		period := time.Date(2026, 8, 15, 12, 34, 56, 0, time.UTC)
		recorder := uuid.New()
		if err := db.WriteInfoMovements(ctx, ir.Name, "Document", recorder,
			[]map[string]any{{"Key": "A", "Value": "secret"}}, ir, &period); err != nil {
			t.Fatal(err)
		}

		rollback := errors.New("inspect raw returning row")
		var deleted []map[string]any
		err := db.WithTxScope(ctx, func(txCtx context.Context) error {
			var err error
			deleted, err = db.InfoRegDeleteByFilterReturning(txCtx, ir,
				storage.RegFilter{DimValues: map[string]any{"Key": "A"}})
			if err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("rollback error = %v", err)
		}
		if len(deleted) != 1 {
			t.Fatalf("returned rows = %#v", deleted)
		}
		gotPeriod, ok := deleted[0]["period"].(time.Time)
		if !ok || !gotPeriod.Equal(period) {
			t.Fatalf("raw period = %T(%v), want time.Time(%v)", deleted[0]["period"], deleted[0]["period"], period)
		}
		if deleted[0]["recorder"] != recorder.String() || deleted[0]["recorder_type"] != "Document" {
			t.Fatalf("system policy fields missing from RETURNING: %#v", deleted[0])
		}
	})
}
