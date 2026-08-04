package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// `onebase reindex` — восстановление полнотекстового индекса из данных базы
// (план 82). Проверяем сквозной путь команды: загрузку конфигурации, обход
// объектов и наполнение индекса.

func writeReindexFixture(t *testing.T, root, relativePath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func reindexFixtureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeReindexFixture(t, dir, "config/app.yaml", "name: reindex-test\nversion: \"1.0\"\n")
	writeReindexFixture(t, dir, "catalogs/Контрагент.yaml", `name: Контрагент
fields:
  - {name: Наименование, type: string}
  - {name: Комментарий, type: string}
`)
	return dir
}

func reindexCmdFor(t *testing.T, projectDir, dbPath, only string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("project", ".", "")
	cmd.Flags().String("db", "", "")
	cmd.Flags().String("sqlite", "", "")
	cmd.Flags().String("config-source", "file", "")
	cmd.Flags().String("entity", "", "")
	cmd.Flags().Int("batch", 500, "")
	for flag, value := range map[string]string{"project": projectDir, "sqlite": dbPath, "entity": only} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func seedReindexData(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	e := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{e}); err != nil {
		t.Fatal(err)
	}
	if err := db.Upsert(ctx, e.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, e); err != nil {
		t.Fatal(err)
	}
	// Индекс намеренно опустошаем: команда обязана восстановить его из данных.
	if _, err := db.Exec(ctx, "DELETE FROM _fts"); err != nil {
		t.Fatal(err)
	}
}

func assertFoundAfterReindex(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hits, err := db.SearchFullText(ctx, storage.FTSQuery{Text: "ромашка", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "Контрагент" {
		t.Fatalf("после reindex объект должен находиться: %+v", hits)
	}
}

func TestRunReindex_RebuildsIndexFromData(t *testing.T) {
	projectDir := reindexFixtureProject(t)
	dbPath := filepath.Join(t.TempDir(), "reindex.db")
	seedReindexData(t, dbPath)

	if err := runReindex(reindexCmdFor(t, projectDir, dbPath, ""), nil); err != nil {
		t.Fatalf("runReindex: %v", err)
	}
	assertFoundAfterReindex(t, dbPath)
}

func TestRunReindex_SingleEntity(t *testing.T) {
	projectDir := reindexFixtureProject(t)
	dbPath := filepath.Join(t.TempDir(), "reindex.db")
	seedReindexData(t, dbPath)

	if err := runReindex(reindexCmdFor(t, projectDir, dbPath, "Контрагент"), nil); err != nil {
		t.Fatalf("runReindex --entity: %v", err)
	}
	assertFoundAfterReindex(t, dbPath)
}

func TestRunReindex_UnknownEntityFails(t *testing.T) {
	projectDir := reindexFixtureProject(t)
	dbPath := filepath.Join(t.TempDir(), "reindex.db")
	seedReindexData(t, dbPath)

	if err := runReindex(reindexCmdFor(t, projectDir, dbPath, "Опечатка"), nil); err == nil {
		t.Fatal("ожидалась ошибка для несуществующего объекта")
	}
}
