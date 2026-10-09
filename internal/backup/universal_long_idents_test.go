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

// Архив, снятый с базы до #1946, хранит таблицы и колонки с длинными
// именами под полными именами, а текущая платформа создаёт их короткими.
// Импорт пропускал такую таблицу («таблица другой конфигурации») и отказывал
// на сверке с манифестом (`imported 0 rows, manifest requires 1`), а длинную
// колонку молча заводил заново текстовой — на PostgreSQL ещё и обрезанной, и
// значение уходило мимо реквизита.
func TestUniversalImportMapsLegacyLongIdents_Matrix(t *testing.T) {
	const entityName = "СправочникСОченьДлиннымИменемИзКонфигурации1С"
	const fieldName = "РеквизитСОченьДлиннымИменемКоторыйПришёлИзОдинЭс"
	entity := &metadata.Entity{
		Name: entityName,
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: fieldName, Type: metadata.FieldTypeString},
		},
	}
	oldTable := metadata.TableLogical(entityName)
	oldCol := metadata.LogicalColumnName(entity.Fields[1])
	if len(oldTable) <= metadata.MaxSQLIdentBytes || len(oldCol) <= metadata.MaxSQLIdentBytes {
		t.Fatal("имена в тесте должны быть длиннее предела")
	}

	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "onebase.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: " + entityName + "\nfields:\n  - name: Наименование\n    type: string\n  - name: " + fieldName + "\n    type: string\n"
	if err := os.WriteFile(filepath.Join(cfg, "catalogs", entityName+".yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	// Источник — база SQLite прежней платформы: длинные имена целиком.
	ctx := context.Background()
	source := newSQLite(t, "legacy-long")
	if err := source.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	for _, q := range []string{
		"CREATE TABLE " + oldTable + " (id TEXT PRIMARY KEY, наименование TEXT, " + oldCol + " TEXT, deletion_mark INTEGER NOT NULL DEFAULT 0, _version BIGINT NOT NULL DEFAULT 1)",
		"INSERT INTO " + oldTable + " (id, наименование, " + oldCol + ") VALUES ('" + id.String() + "', 'Элемент', 'значение из старой базы')",
	} {
		if _, err := source.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", cfg, "", "test", &archive); err != nil {
		t.Fatal("export:", err)
	}

	dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
		target.SetFilesDir(t.TempDir())
		if _, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()),
			ImportOptions{ExchangeMode: ExchangeRestoreClone}); err != nil {
			t.Fatal("import:", err)
		}
		row, err := target.GetByID(ctx, entityName, id, entity)
		if err != nil {
			t.Fatalf("строка длинной таблицы не перенесена: %v", err)
		}
		if row[fieldName] != "значение из старой базы" {
			t.Fatalf("значение длинной колонки потеряно: %v", row)
		}
		// Колонка не завелась второй раз под длинным (на PostgreSQL —
		// обрезанным) именем рядом с настоящей.
		var extra int
		q := "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2"
		if target.IsSQLite() {
			q = "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?"
		}
		for _, name := range metadata.LegacySQLIdents(oldCol) {
			if err := target.QueryRow(ctx, q, metadata.TableName(entityName), name).Scan(&extra); err != nil {
				t.Fatal(err)
			}
			if extra != 0 {
				t.Fatalf("колонка под прежним именем %q заведена заново", name)
			}
		}
	})
}
