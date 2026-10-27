package dbcheck

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestDoctorRefsInvalidatesInfoRegRevisionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := &metadata.Entity{Name: "RevisionTarget", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: "string"}}}
		ir := &metadata.InfoRegister{Name: "RevisionDoctor", Dimensions: []metadata.Field{{Name: "Key", Type: "string"}}, Resources: []metadata.Field{{Name: "Ref", Type: "reference", RefEntity: cat.Name}}}
		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
			t.Fatal(err)
		}
		key := storage.InfoRegEditKey{Dimensions: map[string]any{"Key": "X"}}
		if err := db.InfoRegSet(ctx, ir, key.Dimensions, map[string]any{"Ref": uuid.New()}, nil); err != nil {
			t.Fatal(err)
		}
		before, err := db.InfoRegReadForEdit(ctx, ir, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		env := &Env{DB: db, Entities: []*metadata.Entity{cat}, InfoRegisters: []*metadata.InfoRegister{ir}}
		report := Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true})
		if len(report.Results) != 1 || report.Results[0].Error != "" || report.Results[0].Fixed != 1 {
			t.Fatalf("doctor: %+v", report)
		}
		after, err := db.InfoRegReadForEdit(ctx, ir, key, nil)
		if err != nil || after == nil {
			t.Fatalf("read %v %v", after, err)
		}
		if after.Revision == before.Revision {
			t.Fatal("doctor retained stale edit revision")
		}
	})
}
