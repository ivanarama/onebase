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

func TestProvisionalSnapshot_FinalEffectsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		e := &metadata.Entity{Name: "SnapshotDoc", Kind: metadata.KindDocument,
			Fields:      []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString, Required: true}, {Name: "Stage", Type: metadata.FieldTypeString}},
			Stages:      &metadata.Stages{Field: "Stage", Order: []string{"Draft", "Ready"}, Transitions: []metadata.StageTransition{{From: "Draft", To: []string{"Ready"}}}, Enforce: metadata.StageEnforceStrict},
			FullTextSet: true, FullText: []string{"Title"},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.EnsureFullTextSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveAuditSettings(ctx, storage.AuditSettings{Enabled: true, Create: true, Update: true}); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := db.UpdateProvisional(ctx, e.Name, id, nil, e); err == nil {
			t.Fatal("intermediate writer accepted no transaction")
		}
		err := db.WithTxScope(ctx, func(tx context.Context) error {
			if err := db.UpdateProvisional(tx, e.Name, id, nil, e); err == nil {
				t.Fatal("intermediate writer accepted no lifecycle")
			}
			// Both required and initial stage are finalized after the hooks. A transient
			// Ready stage must neither reject this operation nor create stage history.
			if err := db.UpsertProvisional(tx, e.Name, id, map[string]any{"Stage": "Ready"}, e); err != nil {
				return err
			}
			if err := db.UpdateProvisional(tx, e.Name, id, map[string]any{"Title": "intermediate"}, e); err != nil {
				return err
			}
			row, err := db.GetByID(tx, e.Name, id, e)
			if err != nil || row["Title"] != "intermediate" {
				t.Fatalf("read-your-writes=%v err=%v", row, err)
			}
			v, err := db.EntityVersion(tx, e.Name, id)
			if err != nil || v != 1 {
				t.Fatalf("intermediate version=%d err=%v", v, err)
			}
			history, err := db.StageHistory(tx, e.Name, id)
			if err != nil || len(history) != 0 {
				t.Fatalf("intermediate stage effects=%v err=%v", history, err)
			}
			audit, err := db.AuditByRecord(tx, e.Name, id)
			if err != nil || len(audit) != 0 {
				t.Fatalf("intermediate audit=%v err=%v", audit, err)
			}
			var fts int
			if err := db.QueryRow(tx, "SELECT COUNT(*) FROM _fts WHERE owner_id="+db.Dialect().Placeholder(1), id.String()).Scan(&fts); err != nil || fts != 0 {
				t.Fatalf("intermediate FTS=%d err=%v", fts, err)
			}
			return db.UpsertPreserveVersion(tx, e.Name, id, map[string]any{"Title": "final", "Stage": "Draft"}, e)
		})
		if err != nil {
			t.Fatal(err)
		}
		history, err := db.StageHistory(ctx, e.Name, id)
		if err != nil || len(history) != 1 || history[0].FromStage != "" || history[0].ToStage != "Draft" {
			t.Fatalf("final stage effects=%v err=%v", history, err)
		}
		audit, err := db.AuditByRecord(ctx, e.Name, id)
		if err != nil || len(audit) != 1 || audit[0].Action != "create" {
			t.Fatalf("final audit=%v err=%v", audit, err)
		}
		var ftsBody string
		if err := db.QueryRow(ctx, "SELECT title FROM _fts WHERE owner_id="+db.Dialect().Placeholder(1), id.String()).Scan(&ftsBody); err != nil || ftsBody != "final" {
			t.Fatalf("final FTS=%q err=%v", ftsBody, err)
		}

		incomplete := uuid.New()
		err = db.WithTxScope(ctx, func(tx context.Context) error {
			if err := db.UpsertProvisional(tx, e.Name, incomplete, map[string]any{"Stage": "Draft"}, e); err != nil {
				return err
			}
			return db.UpdateProvisional(tx, e.Name, incomplete, map[string]any{"Title": "looks valid"}, e)
		})
		if !errors.Is(err, storage.ErrIncompleteWriteLifecycle) {
			t.Fatalf("unfinalized lifecycle committed: %v", err)
		}
		if _, err := db.GetByID(ctx, e.Name, incomplete, e); !storage.IsNotFound(err) {
			t.Fatalf("incomplete row survived: %v", err)
		}
	})
}

func TestPostingPrelude_SeparateConnectionVisibility(t *testing.T) {
	stagePair(t, func(t *testing.T, a, b *storage.DB) {
		ctx := context.Background()
		if err := a.EnsureServiceSchema(ctx); err != nil {
			t.Fatal(err)
		}
		e := &metadata.Entity{Name: "PreludeIsolation", Kind: metadata.KindDocument, Fields: []metadata.Field{{Name: "Title", Type: metadata.FieldTypeString, Required: true}}}
		if err := a.Migrate(ctx, []*metadata.Entity{e}); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := a.Upsert(ctx, e.Name, id, map[string]any{"Title": "old"}, e); err != nil {
			t.Fatal(err)
		}
		v := int64(1)
		err := a.WithTxScope(ctx, func(tx context.Context) error {
			if err := a.UpsertPostingPreludeVersioned(tx, e.Name, id, map[string]any{"Title": "intermediate"}, e, &v); err != nil {
				return err
			}
			row, err := b.GetByID(ctx, e.Name, id, e)
			if err != nil || row["Title"] != "old" {
				t.Fatalf("separate connection saw prelude: %v %v", row, err)
			}
			return a.UpsertAfterVersionBump(tx, e.Name, id, map[string]any{"Title": "final"}, e)
		})
		if err != nil {
			t.Fatal(err)
		}
		row, err := b.GetByID(ctx, e.Name, id, e)
		if err != nil || row["Title"] != "final" {
			t.Fatalf("separate connection final=%v err=%v", row, err)
		}
		err = a.WithTxScope(ctx, func(tx context.Context) error {
			return a.UpsertPostingPreludeVersioned(tx, e.Name, id, map[string]any{"Title": "stale"}, e, &v)
		})
		if !errors.Is(err, storage.ErrVersionConflict) {
			t.Fatalf("CAS stale token accepted: %v", err)
		}
	})
}
