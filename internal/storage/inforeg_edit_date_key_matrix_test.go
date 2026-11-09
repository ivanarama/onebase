package storage_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegEditDateDimensionPrecisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		for _, periodic := range []bool{false, true} {
			for _, numeric := range []bool{false, true} {
				t.Run(fmt.Sprintf("periodic=%t/numeric=%t", periodic, numeric), func(t *testing.T) {
					ir := revisionRegister(fmt.Sprintf("EditDate%t%t", periodic, numeric))
					ir.Periodic = periodic
					ir.Dimensions = []metadata.Field{{Name: "WhenKey", Type: metadata.FieldTypeDate}}
					if numeric {
						ir.Dimensions = append(ir.Dimensions, metadata.Field{Name: "Amount", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2})
					}
					if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
						t.Fatal(err)
					}
					second := time.Date(2026, 10, 19, 13, 22, 33, 0, time.UTC)
					fraction := second.Add(123456 * time.Microsecond)
					key := storage.InfoRegEditKey{Dimensions: map[string]any{"WhenKey": second}}
					if periodic {
						key.Period = &second
					}
					if numeric {
						key.Dimensions["Amount"] = "1.00"
					}
					if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "second-sibling"}, key.Period); err != nil {
						t.Fatal(err)
					}
					sibling := readRevision(t, db, ir, key)
					// String writes and universal restore preserve the full timestamp.
					key.Dimensions["WhenKey"] = fraction.Format(time.RFC3339Nano)
					if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "fractional-target"}, key.Period); err != nil {
						t.Fatal(err)
					}
					target := readRevision(t, db, ir, key)
					for _, number := range []string{"1.00", "1"} {
						if numeric {
							key.Dimensions["Amount"] = number
						}
						local := fraction.In(time.FixedZone("offset", 3*60*60))
						for _, date := range []any{fraction, local, &local} {
							key.Dimensions["WhenKey"] = date
							got := readRevision(t, db, ir, key)
							if got.Row["Value"] != "fractional-target" || got.Revision != target.Revision || got.Revision == sibling.Revision {
								t.Fatalf("DATE lookup selected sibling: %#v", got)
							}
							if gotDate, ok := got.Row["WhenKey"].(time.Time); !ok || !gotDate.Equal(fraction) {
								t.Fatalf("DATE lost precision: %#v", got.Row)
							}
							if again := readRevision(t, db, ir, got.Key); again.Revision != target.Revision {
								t.Fatal("physical DATE key did not round trip")
							}
						}
					}
					key.Dimensions["WhenKey"] = fraction.Add(time.Microsecond)
					if got, err := db.InfoRegReadForEdit(ctx, ir, key, nil); err != nil || got != nil {
						t.Fatalf("missing fractional key selected sibling: %#v, %v", got, err)
					}
					key.Dimensions["WhenKey"] = fraction
					denied, err := db.InfoRegReadForEdit(ctx, ir, key, &storage.Predicate{Field: "Value", Op: "eq", Value: "second-sibling"})
					if err != nil || denied != nil {
						t.Fatalf("DATE lookup bypassed read RLS: %#v, %v", denied, err)
					}
					// The native SQLite spelling may also contain fractional seconds
					// when supplied as text rather than a time.Time writer argument.
					nativeFraction := fraction.Add(time.Second)
					key.Dimensions["WhenKey"] = nativeFraction.Format("2006-01-02 15:04:05.999999999-07:00")
					if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "native-fraction"}, key.Period); err != nil {
						t.Fatal(err)
					}
					key.Dimensions["WhenKey"] = nativeFraction
					if got := readRevision(t, db, ir, key); got.Row["Value"] != "native-fraction" {
						t.Fatalf("typed DATE missed native fractional spelling: %#v", got)
					}
				})
			}
		}
	})
}

func TestInfoRegEditDateDimensionSpellingMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := revisionRegister("EditDateSpelling")
		ir.Dimensions = []metadata.Field{{Name: "WhenKey", Type: metadata.FieldTypeDate}, {Name: "Amount", Type: metadata.FieldTypeNumber, Length: 10, Scale: 2}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		second := time.Date(2026, 10, 19, 13, 22, 33, 0, time.UTC)
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"WhenKey": second, "Amount": "1.00"}}
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "native"}, nil); err != nil {
			t.Fatal(err)
		}
		native := readRevision(t, db, ir, key)
		if !db.IsSQLite() {
			return // PostgreSQL has one physical instant rather than TEXT aliases.
		}
		// Exercise exact-first lookup of a legacy numeric key, too.
		if _, err := db.Exec(ctx, "UPDATE "+metadata.InfoRegTableName(ir.Name)+" SET amount = '01.00'"); err != nil {
			t.Fatal(err)
		}
		key.Dimensions["Amount"] = "01.00"
		if got := readRevision(t, db, ir, key); got.Revision != native.Revision {
			t.Fatal("typed DATE missed legacy numeric key")
		}
		key.Dimensions["WhenKey"] = second.Format(time.RFC3339Nano)
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Value": "alias"}, nil); err != nil {
			t.Fatal(err)
		}
		// New numeric writes canonicalize the alias to 1.00; restore the same
		// legacy numeric spelling so only the DATE spelling distinguishes rows.
		if _, err := db.Exec(ctx, "UPDATE "+metadata.InfoRegTableName(ir.Name)+" SET amount = '01.00'"); err != nil {
			t.Fatal(err)
		}
		alias := readRevision(t, db, ir, key)
		key.Dimensions["WhenKey"] = second
		if got, err := db.InfoRegReadForEdit(ctx, ir, key, nil); err == nil || got != nil {
			t.Fatalf("ambiguous DATE selected a physical sibling: %#v, %v", got, err)
		}
		for _, want := range []*storage.InfoRegEditRecord{native, alias} {
			want.Key.Dimensions["Amount"] = "01.00"
			if got := readRevision(t, db, ir, want.Key); got.Revision != want.Revision || got.Row["Value"] != want.Row["Value"] {
				t.Fatalf("physical DATE selected sibling: %#v", got)
			}
		}
	})
}
