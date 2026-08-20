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

type requiredValueRef struct{ id string }

func (r *requiredValueRef) GetRefUUID() string { return r.id }

func requiredPersistenceEntities() (*metadata.Entity, *metadata.Entity) {
	owner := &metadata.Entity{
		Name: "RequiredOwner",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Label", Type: metadata.FieldTypeString},
		},
	}
	item := &metadata.Entity{
		Name: "RequiredPersistenceItem",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString, Required: true},
			{
				Name:      "Owner",
				Type:      metadata.FieldType("reference:RequiredOwner"),
				RefEntity: owner.Name,
				Required:  true,
			},
			{Name: "Note", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{
			{
				Name: "Lines",
				Fields: []metadata.Field{
					{Name: "Product", Type: metadata.FieldTypeString, Required: true},
					{Name: "Quantity", Type: metadata.FieldTypeNumber},
					{
						Name:      "Owner",
						Type:      metadata.FieldType("reference:RequiredOwner"),
						RefEntity: owner.Name,
						Required:  true,
					},
				},
			},
		},
	}
	return owner, item
}

func TestRequiredPersistence_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ownerEntity, item := requiredPersistenceEntities()
		if err := db.EnsureAuditSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, []*metadata.Entity{ownerEntity, item}); err != nil {
			t.Fatal(err)
		}

		ownerID := uuid.New()
		if err := db.Upsert(ctx, ownerEntity.Name, ownerID,
			map[string]any{"Label": "primary"}, ownerEntity); err != nil {
			t.Fatalf("seed reference target: %v", err)
		}

		t.Run("direct create requires the complete object", func(t *testing.T) {
			id := uuid.New()
			err := db.Upsert(ctx, item.Name, id,
				map[string]any{"Owner": ownerID.String()}, item)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("direct create without Name: got %v, want ErrRequiredFieldEmpty", err)
			}
			if _, err := db.GetByID(ctx, item.Name, id, item); !storage.IsNotFound(err) {
				t.Fatalf("rejected direct create persisted a row: %v", err)
			}
		})

		t.Run("partial upsert preserves omitted required values", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "kept", "before")

			if err := db.Upsert(ctx, item.Name, id,
				map[string]any{"Note": "after"}, item); err != nil {
				t.Fatalf("partial Upsert: %v", err)
			}

			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("partial Upsert version = %#v, want int64(2)", got["_version"])
			}
			entries, err := db.AuditByRecord(ctx, item.Name, id)
			if err != nil {
				t.Fatalf("reload audit: %v", err)
			}
			var updateFields []string
			for _, entry := range entries {
				if entry.Action == "update" {
					updateFields = append(updateFields, entry.Field)
				}
			}
			if len(updateFields) != 1 || updateFields[0] != "Note" {
				t.Fatalf("partial update audit fields = %#v, want only Note", updateFields)
			}
		})

		t.Run("versioned partial update preserves values and stale CAS wins", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "cas-kept", "before")

			expected := int64(1)
			if err := db.UpsertVersioned(ctx, item.Name, id,
				map[string]any{"Note": "after"}, item, &expected); err != nil {
				t.Fatalf("partial UpsertVersioned: %v", err)
			}
			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "cas-kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("partial UpsertVersioned version = %#v, want int64(2)", got["_version"])
			}

			err := db.UpsertVersioned(ctx, item.Name, id,
				map[string]any{"Name": "   "}, item, &expected)
			if !errors.Is(err, storage.ErrVersionConflict) {
				t.Fatalf("stale CAS with invalid required value: got %v, want ErrVersionConflict", err)
			}
			got = loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "cas-kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("rejected stale write changed version to %#v", got["_version"])
			}

			current := int64(2)
			err = db.UpsertVersioned(ctx, item.Name, id,
				map[string]any{"Name": "   "}, item, &current)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("current CAS with invalid required value: got %v, want ErrRequiredFieldEmpty", err)
			}
			got = loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "cas-kept", ownerID, "after")
			if got["_version"] != int64(2) {
				t.Fatalf("required rejection changed version to %#v", got["_version"])
			}
			entries, err := db.AuditByRecord(ctx, item.Name, id)
			if err != nil {
				t.Fatalf("reload audit: %v", err)
			}
			var updates int
			for _, entry := range entries {
				if entry.Action == "update" {
					updates++
				}
			}
			if updates != 1 {
				t.Fatalf("rejected CAS writes changed audit: update entries = %d, want 1", updates)
			}
		})

		t.Run("malformed required reference is rejected before null coercion", func(t *testing.T) {
			id := uuid.New()
			err := db.Upsert(ctx, item.Name, id,
				map[string]any{"Name": "bad-ref", "Owner": "not-a-uuid"}, item)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("malformed required reference: got %v, want ErrRequiredFieldEmpty", err)
			}
			if _, err := db.GetByID(ctx, item.Name, id, item); !storage.IsNotFound(err) {
				t.Fatalf("malformed reference was coerced to NULL and persisted: %v", err)
			}

			// Syntactically valid references remain a database-integrity concern;
			// required validation must not mask the existing FK error contract.
			missingID := uuid.New()
			err = db.Upsert(ctx, item.Name, uuid.New(), map[string]any{
				"Name": "missing-target", "Owner": missingID.String(),
			}, item)
			if !errors.Is(err, storage.ErrForeignKeyViolation) {
				t.Fatalf("valid missing reference: got %v, want ErrForeignKeyViolation", err)
			}

			// DSL reference structs are sometimes copied by value although their
			// GetRefUUID method has a pointer receiver. Required validation must
			// accept exactly the representations persistence accepts.
			valueRefID := uuid.New()
			if err := db.Upsert(ctx, item.Name, valueRefID, map[string]any{
				"Name": "value-ref", "Owner": requiredValueRef{id: ownerID.String()}, "Note": "",
			}, item); err != nil {
				t.Fatalf("valid reference value rejected: %v", err)
			}
			got := loadRequiredItem(t, ctx, db, item, valueRefID)
			assertRequiredItem(t, got, "value-ref", ownerID, "")
		})

		t.Run("table part validation precedes replacement delete", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "with-lines", "")
			tp := item.TableParts[0]
			original := []map[string]any{{"Product": "original", "Owner": ownerID.String()}}
			if err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id, original, tp); err != nil {
				t.Fatalf("seed table part: %v", err)
			}

			err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "   ", "Owner": ownerID.String()}}, tp)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("blank required table-part field: got %v, want ErrRequiredFieldEmpty", err)
			}
			rows, err := db.GetTablePartRows(ctx, item.Name, tp.Name, id, tp)
			if err != nil {
				t.Fatalf("reload table part after rejection: %v", err)
			}
			assertRequiredLine(t, rows, "original", ownerID)

			if err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "replacement", "Owner": ownerID.String()}}, tp); err != nil {
				t.Fatalf("valid table-part replacement: %v", err)
			}
			rows, err = db.GetTablePartRows(ctx, item.Name, tp.Name, id, tp)
			if err != nil {
				t.Fatalf("reload valid table part: %v", err)
			}
			assertRequiredLine(t, rows, "replacement", ownerID)
		})

		t.Run("exchange bypasses entity and table part required checks", func(t *testing.T) {
			const source = `["exchange","required-matrix","remote-node",1]`
			id := uuid.New()
			if err := db.ApplyReplicatedEntity(ctx, item.Name, id,
				map[string]any{"Name": "", "Owner": "remote-non-uuid"}, item, source); err != nil {
				t.Fatalf("replicated entity rejected by required backstop: %v", err)
			}
			tp := item.TableParts[0]
			if err := db.ApplyReplicatedTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "", "Owner": "remote-non-uuid"}}, tp, source); err != nil {
				t.Fatalf("replicated table part rejected by required backstop: %v", err)
			}
		})

		t.Run("predefined direct SQL validates effective persisted state", func(t *testing.T) {
			predefined := &metadata.Entity{
				Name: "RequiredPredefinedItem",
				Kind: metadata.KindCatalog,
				Fields: []metadata.Field{
					{Name: "Name", Type: metadata.FieldTypeString, Required: true},
					{Name: "Note", Type: metadata.FieldTypeString},
				},
			}
			if err := db.Migrate(ctx, []*metadata.Entity{predefined}); err != nil {
				t.Fatalf("migrate predefined entity: %v", err)
			}
			predefined.Predefined = []*metadata.PredefinedItem{{
				Name: "Core",
				Fields: map[string]any{
					"Note": "invalid create",
				},
			}}
			if err := db.EnsurePredefinedColumns(ctx, []*metadata.Entity{predefined}); err != nil {
				t.Fatalf("ensure predefined columns: %v", err)
			}

			err := db.SyncPredefined(ctx, predefined)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("predefined create without Name: got %v, want ErrRequiredFieldEmpty", err)
			}
			if _, getErr := db.GetPredefinedID(ctx, predefined.Name, "Core"); getErr == nil {
				t.Fatal("rejected predefined create persisted a row")
			}

			predefined.Predefined[0].Fields = map[string]any{
				"Name": "kept",
				"Note": "before",
			}
			if err := db.SyncPredefined(ctx, predefined); err != nil {
				t.Fatalf("valid predefined create: %v", err)
			}
			id, err := db.GetPredefinedID(ctx, predefined.Name, "Core")
			if err != nil {
				t.Fatalf("get valid predefined: %v", err)
			}

			// Omitting a required field on conflict means preserve, not clear.
			// A backstop that validates only item.Fields would reject this update;
			// direct SQL without a backstop would accept the invalid create above.
			predefined.Predefined[0].Fields = map[string]any{"Note": "after"}
			if err := db.SyncPredefined(ctx, predefined); err != nil {
				t.Fatalf("partial predefined update: %v", err)
			}
			got, err := db.GetByID(ctx, predefined.Name, id, predefined)
			if err != nil {
				t.Fatalf("reload predefined: %v", err)
			}
			if got["Name"] != "kept" || got["Note"] != "after" {
				t.Fatalf("partial predefined update = %#v, want preserved Name and changed Note", got)
			}
		})

		t.Run("preserve-version writer requires its matching provisional lifecycle", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "preserve-before", "before")
			beforeAudit, err := db.AuditByRecord(ctx, item.Name, id)
			if err != nil {
				t.Fatalf("audit before rejected preserve write: %v", err)
			}

			err = db.UpsertPreserveVersion(ctx, item.Name, id, map[string]any{
				"Name": "preserve-after", "Owner": ownerID.String(), "Note": "after",
			}, item)
			if err == nil {
				t.Fatal("UpsertPreserveVersion accepted a standalone write")
			}
			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "preserve-before", ownerID, "before")
			if got["_version"] != int64(1) {
				t.Fatalf("rejected preserve write changed version to %#v", got["_version"])
			}
			afterAudit, auditErr := db.AuditByRecord(ctx, item.Name, id)
			if auditErr != nil {
				t.Fatalf("audit after rejected preserve write: %v", auditErr)
			}
			if len(afterAudit) != len(beforeAudit) {
				t.Fatalf("rejected preserve write changed audit: before=%d after=%d", len(beforeAudit), len(afterAudit))
			}
		})

		t.Run("incomplete provisional create cannot commit", func(t *testing.T) {
			id := uuid.New()
			err := db.WithTxScope(ctx, func(txCtx context.Context) error {
				return db.UpsertProvisional(txCtx, item.Name, id,
					map[string]any{"Name": "provisional-only"}, item)
			})
			if !errors.Is(err, storage.ErrIncompleteWriteLifecycle) {
				t.Fatalf("incomplete provisional commit: got %v, want ErrIncompleteWriteLifecycle", err)
			}
			if _, err := db.GetByID(ctx, item.Name, id, item); !storage.IsNotFound(err) {
				t.Fatalf("incomplete provisional row survived rejected commit: %v", err)
			}
		})

		t.Run("incomplete posting preludes cannot commit header or table part", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "guard-before", "before")
			tp := item.TableParts[0]
			if err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id,
				[]map[string]any{{"Product": "original", "Owner": ownerID.String()}}, tp); err != nil {
				t.Fatalf("seed guarded table part: %v", err)
			}

			expected := int64(1)
			err := db.WithTxScope(ctx, func(txCtx context.Context) error {
				return db.UpsertPostingPreludeVersioned(txCtx, item.Name, id,
					map[string]any{"Name": ""}, item, &expected)
			})
			if !errors.Is(err, storage.ErrIncompleteWriteLifecycle) {
				t.Fatalf("incomplete header prelude commit: got %v, want ErrIncompleteWriteLifecycle", err)
			}
			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "guard-before", ownerID, "before")
			if got["_version"] != int64(1) {
				t.Fatalf("rejected header prelude changed version to %#v", got["_version"])
			}

			err = db.WithTxScope(ctx, func(txCtx context.Context) error {
				return db.UpsertPostingPreludeTablePartRows(txCtx, item.Name, tp.Name, id,
					[]map[string]any{{"Product": "", "Owner": ownerID.String()}}, tp)
			})
			if !errors.Is(err, storage.ErrIncompleteWriteLifecycle) {
				t.Fatalf("incomplete table-part prelude commit: got %v, want ErrIncompleteWriteLifecycle", err)
			}
			rows, loadErr := db.GetTablePartRows(ctx, item.Name, tp.Name, id, tp)
			if loadErr != nil {
				t.Fatalf("reload guarded table part: %v", loadErr)
			}
			assertRequiredLine(t, rows, "original", ownerID)
		})

		t.Run("failed preludes cannot be finalized or committed", func(t *testing.T) {
			t.Run("stale header CAS", func(t *testing.T) {
				id := uuid.New()
				seedRequiredItem(t, ctx, db, item, id, ownerID, "stale-before", "before")
				stale := int64(0)
				var preludeErr, finalErr error
				commitErr := db.WithTxScope(ctx, func(txCtx context.Context) error {
					preludeErr = db.UpsertPostingPreludeVersioned(txCtx, item.Name, id,
						map[string]any{"Name": "transient"}, item, &stale)
					finalErr = db.UpsertAfterVersionBump(txCtx, item.Name, id,
						map[string]any{"Name": "must-not-commit"}, item)
					return nil
				})
				if !errors.Is(preludeErr, storage.ErrVersionConflict) {
					t.Fatalf("stale posting prelude: got %v, want ErrVersionConflict", preludeErr)
				}
				if !errors.Is(finalErr, storage.ErrIncompleteWriteLifecycle) {
					t.Fatalf("final after stale prelude: got %v, want ErrIncompleteWriteLifecycle", finalErr)
				}
				if !errors.Is(commitErr, storage.ErrIncompleteWriteLifecycle) {
					t.Fatalf("commit after ignored stale prelude: got %v, want ErrIncompleteWriteLifecycle", commitErr)
				}
				got := loadRequiredItem(t, ctx, db, item, id)
				assertRequiredItem(t, got, "stale-before", ownerID, "before")
				if got["_version"] != int64(1) {
					t.Fatalf("ignored stale prelude changed version to %#v", got["_version"])
				}
			})

			t.Run("failed provisional create", func(t *testing.T) {
				id := uuid.New()
				missingOwner := uuid.New()
				var preludeErr, finalErr error
				commitErr := db.WithTxScope(ctx, func(txCtx context.Context) error {
					preludeErr = db.UpsertProvisional(txCtx, item.Name, id, map[string]any{
						"Name": "failed-provisional", "Owner": missingOwner.String(),
					}, item)
					finalErr = db.UpsertPreserveVersion(txCtx, item.Name, id, map[string]any{
						"Name": "must-not-commit", "Owner": ownerID.String(),
					}, item)
					return nil
				})
				if !errors.Is(preludeErr, storage.ErrForeignKeyViolation) {
					t.Fatalf("failed provisional create: got %v, want ErrForeignKeyViolation", preludeErr)
				}
				if !errors.Is(finalErr, storage.ErrIncompleteWriteLifecycle) {
					t.Fatalf("final after failed provisional: got %v, want ErrIncompleteWriteLifecycle", finalErr)
				}
				if !errors.Is(commitErr, storage.ErrIncompleteWriteLifecycle) {
					t.Fatalf("commit after ignored provisional failure: got %v, want ErrIncompleteWriteLifecycle", commitErr)
				}
				if _, err := db.GetByID(ctx, item.Name, id, item); !storage.IsNotFound(err) {
					t.Fatalf("failed provisional lifecycle persisted a row: %v", err)
				}
			})

			t.Run("failed table-part replacement", func(t *testing.T) {
				id := uuid.New()
				seedRequiredItem(t, ctx, db, item, id, ownerID, "tp-failure", "before")
				tp := item.TableParts[0]
				original := []map[string]any{{"Product": "original", "Owner": ownerID.String()}}
				if err := db.UpsertTablePartRows(ctx, item.Name, tp.Name, id, original, tp); err != nil {
					t.Fatalf("seed table part before failed prelude: %v", err)
				}
				var preludeErr, finalErr error
				commitErr := db.WithTxScope(ctx, func(txCtx context.Context) error {
					preludeErr = db.UpsertPostingPreludeTablePartRows(txCtx, item.Name, tp.Name, id,
						[]map[string]any{{
							"Product": "broken", "Quantity": "not-a-number", "Owner": ownerID.String(),
						}}, tp)
					finalErr = db.FinalizePostingPreludeTablePartRows(txCtx, item.Name, tp.Name, id,
						[]map[string]any{{"Product": "must-not-commit", "Owner": ownerID.String()}}, true, tp)
					return nil
				})
				if preludeErr == nil {
					t.Fatal("table-part prelude with an invalid number unexpectedly succeeded")
				}
				if !errors.Is(finalErr, storage.ErrIncompleteWriteLifecycle) {
					t.Fatalf("final after failed table-part prelude: got %v, want ErrIncompleteWriteLifecycle", finalErr)
				}
				if !errors.Is(commitErr, storage.ErrIncompleteWriteLifecycle) {
					t.Fatalf("commit after ignored table-part failure: got %v, want ErrIncompleteWriteLifecycle", commitErr)
				}
				rows, err := db.GetTablePartRows(ctx, item.Name, tp.Name, id, tp)
				if err != nil {
					t.Fatal(err)
				}
				assertRequiredLine(t, rows, "original", ownerID)
			})
		})

		t.Run("rolled-back final write reactivates outer prelude guard", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "savepoint-before", "before")
			expected := int64(1)
			innerRollback := errors.New("rollback final savepoint")

			err := db.WithTxScope(ctx, func(txCtx context.Context) error {
				if err := db.UpsertPostingPreludeVersioned(txCtx, item.Name, id,
					map[string]any{"Name": ""}, item, &expected); err != nil {
					return err
				}
				innerErr := db.WithTxScope(txCtx, func(innerCtx context.Context) error {
					if err := db.UpsertAfterVersionBump(innerCtx, item.Name, id,
						map[string]any{"Name": "savepoint-final"}, item); err != nil {
						return err
					}
					return innerRollback
				})
				if !errors.Is(innerErr, innerRollback) {
					t.Fatalf("inner final scope: got %v, want rollback sentinel", innerErr)
				}
				return nil
			})
			if !errors.Is(err, storage.ErrIncompleteWriteLifecycle) {
				t.Fatalf("outer commit after rolled-back final: got %v, want ErrIncompleteWriteLifecycle", err)
			}
			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "savepoint-before", ownerID, "before")
			if got["_version"] != int64(1) {
				t.Fatalf("rolled-back final/prelude changed version to %#v", got["_version"])
			}
		})

		t.Run("posting prelude audit is one original-to-final update", func(t *testing.T) {
			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "audit-A", "same")
			expected := int64(1)
			if err := db.WithTxScope(ctx, func(txCtx context.Context) error {
				if err := db.UpsertPostingPreludeVersioned(txCtx, item.Name, id,
					map[string]any{"Name": ""}, item, &expected); err != nil {
					return err
				}
				return db.UpsertAfterVersionBump(txCtx, item.Name, id,
					map[string]any{"Name": "audit-B"}, item)
			}); err != nil {
				t.Fatalf("complete posting lifecycle: %v", err)
			}

			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "audit-B", ownerID, "same")
			if got["_version"] != int64(2) {
				t.Fatalf("complete posting lifecycle version = %#v, want 2", got["_version"])
			}
			entries, err := db.AuditByRecord(ctx, item.Name, id)
			if err != nil {
				t.Fatalf("reload posting audit: %v", err)
			}
			var nameUpdates []*storage.AuditEntry
			for _, entry := range entries {
				if entry.Action == "update" && entry.Field == "Name" {
					nameUpdates = append(nameUpdates, entry)
				}
			}
			if len(nameUpdates) != 1 {
				t.Fatalf("Name update entries = %d, want 1: %#v", len(nameUpdates), nameUpdates)
			}
			if nameUpdates[0].OldValue != "audit-A" || nameUpdates[0].NewValue != "audit-B" {
				t.Fatalf("posting audit = %#v -> %#v, want audit-A -> audit-B",
					nameUpdates[0].OldValue, nameUpdates[0].NewValue)
			}
		})

		t.Run("posting preludes are transaction-local and final backstops remain active", func(t *testing.T) {
			outsideID := uuid.New()
			if err := db.UpsertProvisional(ctx, item.Name, outsideID,
				map[string]any{"Name": "outside"}, item); err == nil {
				t.Fatal("UpsertProvisional accepted an incomplete row outside a transaction")
			}
			if _, err := db.GetByID(ctx, item.Name, outsideID, item); !storage.IsNotFound(err) {
				t.Fatalf("outside-tx provisional persisted a row: %v", err)
			}

			id := uuid.New()
			seedRequiredItem(t, ctx, db, item, id, ownerID, "before-prelude", "before")
			expected := int64(1)
			if err := db.UpsertPostingPreludeVersioned(ctx, item.Name, id,
				map[string]any{"Name": ""}, item, &expected); err == nil {
				t.Fatal("versioned posting prelude ran outside a transaction")
			}
			if err := db.UpsertPostingPreludeTablePartRows(ctx, item.Name, item.TableParts[0].Name, id,
				[]map[string]any{{"Product": ""}}, item.TableParts[0]); err == nil {
				t.Fatal("table-part posting prelude ran outside a transaction")
			}

			rollback := errors.New("rollback posting prelude test")
			err := db.WithTxScope(ctx, func(txCtx context.Context) error {
				if err := db.UpsertPostingPreludeVersioned(txCtx, item.Name, id,
					map[string]any{"Name": ""}, item, &expected); err != nil {
					return err
				}
				if err := db.UpsertPostingPreludeTablePartRows(txCtx, item.Name, item.TableParts[0].Name, id,
					[]map[string]any{{"Product": ""}}, item.TableParts[0]); err != nil {
					return err
				}

				// The exemption belongs only to the two explicit prelude calls.
				// Ordinary writes in the same context (including nested hook writes)
				// must still hit the required backstop.
				if err := db.Upsert(txCtx, item.Name, uuid.New(), map[string]any{"Name": "nested"}, item); !errors.Is(err, storage.ErrRequiredFieldEmpty) {
					t.Fatalf("ordinary nested write inherited prelude bypass: %v", err)
				}
				if err := db.UpsertAfterVersionBump(txCtx, item.Name, id,
					map[string]any{"Name": ""}, item); !errors.Is(err, storage.ErrRequiredFieldEmpty) {
					t.Fatalf("final header write did not restore required backstop: %v", err)
				}
				if err := db.FinalizePostingPreludeTablePartRows(txCtx, item.Name, item.TableParts[0].Name, id,
					[]map[string]any{{"Product": ""}}, true, item.TableParts[0]); !errors.Is(err, storage.ErrRequiredFieldEmpty) {
					t.Fatalf("final table-part write did not restore required backstop: %v", err)
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("posting prelude transaction: got %v, want rollback sentinel", err)
			}
			got := loadRequiredItem(t, ctx, db, item, id)
			assertRequiredItem(t, got, "before-prelude", ownerID, "before")
			if got["_version"] != int64(1) {
				t.Fatalf("rolled-back prelude version = %#v, want 1", got["_version"])
			}
		})
	})
}

func seedRequiredItem(t *testing.T, ctx context.Context, db *storage.DB, entity *metadata.Entity,
	id, ownerID uuid.UUID, name, note string) {
	t.Helper()
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{
		"Name":  name,
		"Owner": ownerID.String(),
		"Note":  note,
	}, entity); err != nil {
		t.Fatalf("seed required item: %v", err)
	}
}

func loadRequiredItem(t *testing.T, ctx context.Context, db *storage.DB,
	entity *metadata.Entity, id uuid.UUID) map[string]any {
	t.Helper()
	got, err := db.GetByID(ctx, entity.Name, id, entity)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return got
}

func assertRequiredItem(t *testing.T, got map[string]any, name string, ownerID uuid.UUID, note string) {
	t.Helper()
	if got["Name"] != name {
		t.Errorf("Name = %#v, want %q", got["Name"], name)
	}
	if got["Owner"] != ownerID.String() {
		t.Errorf("Owner = %#v, want %q", got["Owner"], ownerID.String())
	}
	if got["Note"] != note {
		t.Errorf("Note = %#v, want %q", got["Note"], note)
	}
}

func assertRequiredLine(t *testing.T, rows []map[string]any, product string, ownerID uuid.UUID) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("table-part row count = %d, want 1: %#v", len(rows), rows)
	}
	if rows[0]["Product"] != product {
		t.Errorf("Product = %#v, want %q", rows[0]["Product"], product)
	}
	if rows[0]["Owner"] != ownerID.String() {
		t.Errorf("Owner = %#v, want %q", rows[0]["Owner"], ownerID.String())
	}
}
