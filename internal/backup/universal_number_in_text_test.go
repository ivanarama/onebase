package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// SQLite держит в TEXT-колонке и число, и в архив оно уходит числом. Так
// лежит служебная колонка от прежней конфигурации (_is_predefined = 0):
// в новой схеме её нет, импорт добавляет её как TEXT, и PostgreSQL отвергал
// число — «unable to encode 0 into text format for text». Перенос базы cc2 в
// PostgreSQL падал так через полтора часа загрузки.
func TestUniversalImportNumberInTextColumn_Matrix(t *testing.T) {
	entity := &metadata.Entity{
		Name: "ЧислаВТексте",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "onebase.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: ЧислаВТексте\nfields:\n  - name: Наименование\n    type: string\n"
	if err := os.WriteFile(filepath.Join(cfg, "catalogs", "ЧислаВТексте.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	source := newSQLite(t, "number-in-text")
	if err := source.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	// Колонка, которой в конфигурации больше нет, и число в текстовом реквизите.
	if _, err := source.Exec(ctx, "ALTER TABLE числавтексте ADD COLUMN старая_служебная INTEGER"); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := source.Exec(ctx, "INSERT INTO числавтексте (id, наименование, старая_служебная) VALUES (?, 42, 0)", id.String()); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", cfg, "", "test", &archive); err != nil {
		t.Fatal("export:", err)
	}

	dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
		target.SetFilesDir(t.TempDir())
		if err := target.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()),
			ImportOptions{ExchangeMode: ExchangeRestoreClone}); err != nil {
			t.Fatal("import:", err)
		}
		var name, old any
		q := "SELECT наименование, старая_служебная FROM числавтексте WHERE id = " + target.Dialect().Placeholder(1)
		arg := any(id.String())
		if !target.IsSQLite() {
			arg = id
		}
		if err := target.QueryRow(ctx, q, arg).Scan(&name, &old); err != nil {
			t.Fatalf("строка не перенесена: %v", err)
		}
		if fmt.Sprint(name) != "42" || fmt.Sprint(old) != "0" {
			t.Fatalf("значения перенесены не те: наименование=%v старая_служебная=%v", name, old)
		}
	})
}
