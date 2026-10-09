package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// SQLite принимает пустую строку в колонке любого типа, и в базе, которую
// пополняли загрузчики мимо платформы, «не заполнено» в поле даты, числа или
// булева лежит именно так. PostgreSQL такое значение отвергает, и перенос
// базы SQLite → PostgreSQL падал на первой же строке:
// «invalid input syntax for type timestamp with time zone: ""».
func TestUniversalImportEmptyStringInTypedColumns_Matrix(t *testing.T) {
	entity := &metadata.Entity{
		Name: "ПустыеЗначения",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "ДатаЗагрузки", Type: metadata.FieldTypeDate},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Архивный", Type: metadata.FieldTypeBool},
		},
	}
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "onebase.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: ПустыеЗначения\nfields:\n  - name: Наименование\n    type: string\n  - name: ДатаЗагрузки\n    type: date\n" +
		"  - name: Количество\n    type: number\n  - name: Архивный\n    type: boolean\n"
	if err := os.WriteFile(filepath.Join(cfg, "catalogs", "ПустыеЗначения.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	source := newSQLite(t, "empty-typed")
	if err := source.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	// Ровно так пишут внешние загрузчики: пустая строка вместо NULL.
	if _, err := source.Exec(ctx, "INSERT INTO пустыезначения (id, наименование, датазагрузки, количество, архивный) VALUES (?, 'Элемент', '', '', '')", id.String()); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", cfg, "", "test", &archive); err != nil {
		t.Fatal("export:", err)
	}

	dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
		target.SetFilesDir(t.TempDir())
		// Схема у приёмника есть заранее, как в обычном восстановлении.
		if err := target.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()),
			ImportOptions{ExchangeMode: ExchangeRestoreClone}); err != nil {
			t.Fatal("import:", err)
		}
		row, err := target.GetByID(ctx, entity.Name, id, entity)
		if err != nil {
			t.Fatalf("строка не перенесена: %v", err)
		}
		if row["Наименование"] != "Элемент" {
			t.Fatalf("строка перенесена не та: %v", row)
		}
	})
}
