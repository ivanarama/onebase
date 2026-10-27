package exchange_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestInfoRegPackageUsesLocalEditRevisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ir := infoRegCodes()
		plan := infoRegPlan()
		resolver := fakeResolverIR{inforegs: map[string]*metadata.InfoRegister{ir.Name: ir}}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "center"); err != nil {
			t.Fatal(err)
		}
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"Ключ": "A1"}}
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Значение": "A"}, nil); err != nil {
			t.Fatal(err)
		}
		before, err := db.InfoRegReadForEdit(ctx, ir, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := exchange.RegisterInfoRegOnSave(ctx, db, []*metadata.ExchangePlan{plan}, ir, key.Dimensions, false); err != nil {
			t.Fatal(err)
		}
		data, err := exchange.BuildPackage(ctx, db, resolver, plan, "fil01")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(storage.InfoRegEditRevisionColumn)) || bytes.Contains(data, []byte(before.Revision)) {
			t.Fatal("local revision in exchange payload")
		}
		// Apply a real outgoing package after switching this isolated base to the
		// receiving node. An existing local row must acquire a new local revision.
		if err := db.SaveExchangeThisNode(ctx, plan.Name, "fil01"); err != nil {
			t.Fatal(err)
		}
		result, err := exchange.ApplyPackage(ctx, db, resolver, plan, data, exchange.ApplyOptions{})
		if err != nil || result.Applied != 1 {
			t.Fatalf("apply %v %v", result, err)
		}
		after, err := db.InfoRegReadForEdit(ctx, ir, key, nil)
		if err != nil || after == nil {
			t.Fatalf("read %v %v", after, err)
		}
		if after.Revision == before.Revision {
			t.Fatal("package retained old local revision")
		}
		result, err = exchange.ApplyPackage(ctx, db, resolver, plan, data, exchange.ApplyOptions{})
		if err != nil || result.Skipped != 1 {
			t.Fatalf("replay %v %v", result, err)
		}
		replay, err := db.InfoRegReadForEdit(ctx, ir, key, nil)
		if err != nil || replay.Revision != after.Revision {
			t.Fatal("skipped package mutated revision")
		}
	})
}
