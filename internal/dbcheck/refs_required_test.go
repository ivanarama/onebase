package dbcheck

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// doctor --fix refs is itself a direct SQL writer. A required reference may
// be broken, but replacing it with NULL would turn referential damage into a
// second persisted invariant violation. Such fields must remain manual while
// an ordinary optional reference in the same run is still auto-fixable.
func TestRefsFixKeepsRequiredEntityAndTablePartReferences(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "required-refs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	target := &metadata.Entity{
		Name:   "RequiredRefTarget",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}},
	}
	requiredHeader := metadata.Field{
		Name: "RequiredOwner", Type: metadata.FieldType("reference:" + target.Name),
		RefEntity: target.Name, Required: true,
	}
	optionalHeader := metadata.Field{
		Name: "OptionalOwner", Type: metadata.FieldType("reference:" + target.Name),
		RefEntity: target.Name,
	}
	requiredLine := metadata.Field{
		Name: "RequiredProduct", Type: metadata.FieldType("reference:" + target.Name),
		RefEntity: target.Name, Required: true,
	}
	doc := &metadata.Entity{
		Name:   "RequiredRefDocument",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{requiredHeader, optionalHeader},
		TableParts: []metadata.TablePart{{
			Name: "Lines", Fields: []metadata.Field{requiredLine},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{target, doc}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	docID := uuid.New()
	brokenRequiredHeader := uuid.New()
	brokenOptionalHeader := uuid.New()
	brokenRequiredLine := uuid.New()
	if _, err := db.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	_, insertHeaderErr := db.Exec(ctx, fmt.Sprintf(
		"INSERT INTO %s (id, %s, %s) VALUES (?, ?, ?)",
		metadata.TableName(doc.Name), metadata.ColumnName(requiredHeader), metadata.ColumnName(optionalHeader)),
		docID.String(), brokenRequiredHeader.String(), brokenOptionalHeader.String())
	if insertHeaderErr == nil {
		_, insertLineErr := db.Exec(ctx, fmt.Sprintf(
			"INSERT INTO %s (id, parent_id, строка, %s) VALUES (?, ?, ?, ?)",
			metadata.TablePartTableName(doc.Name, doc.TableParts[0].Name), metadata.ColumnName(requiredLine)),
			uuid.NewString(), docID.String(), 1, brokenRequiredLine.String())
		if insertLineErr != nil {
			insertHeaderErr = insertLineErr
		}
	}
	if _, err := db.Exec(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if insertHeaderErr != nil {
		t.Fatalf("seed broken references: %v", insertHeaderErr)
	}

	env := &Env{DB: db, Entities: []*metadata.Entity{target, doc}}
	before := findResult(t, Run(ctx, env, []Check{refsCheck{}}, nil), "refs")
	if len(before.Findings) != 3 {
		t.Fatalf("broken reference findings = %+v, want required header + optional header + required TP", before.Findings)
	}

	fixed := findResult(t, Run(ctx, env, []Check{refsCheck{}}, map[string]bool{"refs": true}), "refs")
	if fixed.Fixed != 1 {
		t.Fatalf("fixed references = %d, want only the optional header: %s", fixed.Fixed, fixed.Error)
	}
	if !strings.Contains(fixed.Error, doc.Name+"."+requiredHeader.Name) ||
		!strings.Contains(fixed.Error, doc.Name+"."+doc.TableParts[0].Name+"."+requiredLine.Name) ||
		!strings.Contains(fixed.Error, "обязательный") {
		t.Fatalf("manual required-reference report = %q", fixed.Error)
	}

	var storedRequired, storedOptional *string
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT %s, %s FROM %s WHERE id = ?",
		metadata.ColumnName(requiredHeader), metadata.ColumnName(optionalHeader), metadata.TableName(doc.Name)),
		docID.String()).Scan(&storedRequired, &storedOptional); err != nil {
		t.Fatalf("reload header references: %v", err)
	}
	if storedRequired == nil || *storedRequired != brokenRequiredHeader.String() {
		t.Fatalf("required header reference was changed: %v", storedRequired)
	}
	if storedOptional != nil {
		t.Fatalf("optional broken reference was not cleared: %q", *storedOptional)
	}

	var storedLine *string
	if err := db.QueryRow(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE parent_id = ?",
		metadata.ColumnName(requiredLine), metadata.TablePartTableName(doc.Name, doc.TableParts[0].Name)),
		docID.String()).Scan(&storedLine); err != nil {
		t.Fatalf("reload table-part reference: %v", err)
	}
	if storedLine == nil || *storedLine != brokenRequiredLine.String() {
		t.Fatalf("required table-part reference was changed: %v", storedLine)
	}

	after := findResult(t, Run(ctx, env, []Check{refsCheck{}}, nil), "refs")
	if len(after.Findings) != 2 {
		t.Fatalf("post-fix findings = %+v, want the two manual required references", after.Findings)
	}
}
