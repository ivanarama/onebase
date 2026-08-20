package storage

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestRequiredBackstop_IsIndependentFromAuditMode(t *testing.T) {
	db, ctx := openTxHooksTestDB(t)
	entity := &metadata.Entity{
		Name: "RequiredAuditIndependent",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Name", Type: metadata.FieldTypeString, Required: true},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}

	id := uuid.New()
	err := db.upsert(ctx, entity.Name, id, map[string]any{}, entity, upsertWriteOptions{
		bumpVersion:      true,
		validateRequired: true,
		auditMode:        upsertAuditSkip,
	})
	if !errors.Is(err, ErrRequiredFieldEmpty) {
		t.Fatalf("audit-skip write without required Name: got %v, want ErrRequiredFieldEmpty", err)
	}
	if _, err := db.GetByID(ctx, entity.Name, id, entity); !IsNotFound(err) {
		t.Fatalf("audit mode disabled the required invariant and persisted a row: %v", err)
	}
}
