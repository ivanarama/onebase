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

// В исходной базе остаётся таблица метаданных, убранных из конфигурации
// (регистр удалили — таблица с данными осталась). Экспорт кладёт её в архив,
// импорт её пропускает (в схеме восстановленной конфигурации её нет), а сверка
// с манифестом роняла восстановление: «imported 0 rows, manifest requires 1».
// Так падал перенос рабочей базы cc2 в PostgreSQL после двух часов загрузки.
func TestUniversalImportSkipsTableAbsentFromConfig_Matrix(t *testing.T) {
	entity := &metadata.Entity{
		Name:   "Остающийся",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "onebase.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg, "catalogs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "catalogs", "Остающийся.yaml"),
		[]byte("name: Остающийся\nfields:\n  - name: Наименование\n    type: string\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	source := newSQLite(t, "orphan-table")
	if err := source.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := source.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "Элемент"}, entity); err != nil {
		t.Fatal(err)
	}
	// Таблица регистра, которого в конфигурации больше нет.
	if _, err := source.Exec(ctx, "CREATE TABLE инфо_убранныйрегистр (пользователь_id TEXT, номерзаявки TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(ctx, "INSERT INTO инфо_убранныйрегистр VALUES (?, '')", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", cfg, "", "test", &archive); err != nil {
		t.Fatal("export:", err)
	}

	dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
		target.SetFilesDir(t.TempDir())
		report, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()),
			ImportOptions{ExchangeMode: ExchangeRestoreClone})
		if err != nil {
			t.Fatal("import:", err)
		}
		if got, ok := report.SkippedTables["инфо_убранныйрегистр"]; !ok || got != 1 {
			t.Fatalf("пропуск таблицы не отражён в отчёте: SkippedTables=%v", report.SkippedTables)
		}
		row, err := target.GetByID(ctx, entity.Name, id, entity)
		if err != nil || row["Наименование"] != "Элемент" {
			t.Fatalf("данные конфигурации не перенесены: %v %v", row, err)
		}
	})
}
