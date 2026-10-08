package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// Exercise the shipped configuration through CLI migration and DSL posting,
// rather than recreating its schema or calling a movement writer directly.
func TestTasksExampleHistorySeparatesSameTitleAndDate(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "tasks")
	if err := os.CopyFS(projectDir, os.DirFS(filepath.Join("..", "..", "examples", "tasks"))); err != nil {
		t.Fatal(err)
	}
	writeProcrunFixture(t, projectDir, "processors/ПроверкаИстории.yaml", "name: ПроверкаИстории\n")
	writeProcrunFixture(t, projectDir, "src/ПроверкаИстории.proc.os", `Процедура Выполнить()
    Для Счётчик = 1 По 2 Цикл
        Задача = Документы.Задача.Создать();
        Задача.Заголовок = "Позвонить клиенту";
        Задача.Дата = Дата(2026, 9, 29);
        Задача.Статус = "Новая";
        Задача.Провести();
    КонецЦикла;
КонецПроцедуры
`)

	// Cobra commands keep flag values between executions; restore them so this
	// public-command test does not change the environment of other CLI tests.
	for _, cmd := range []*cobra.Command{migrateCmd, procrunCmd} {
		for _, name := range []string{"project", "sqlite", "proc"} {
			flag := cmd.Flags().Lookup(name)
			if flag == nil {
				continue
			}
			previous, changed := flag.Value.String(), flag.Changed
			t.Cleanup(func() {
				_ = flag.Value.Set(previous)
				flag.Changed = changed
			})
		}
	}
	previousContext := rootCmd.Context()
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetContext(previousContext)
	})
	rootCmd.SetContext(context.Background())
	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	for _, args := range [][]string{
		{"migrate", "--project", projectDir, "--sqlite", dbPath},
		{"procrun", "--project", projectDir, "--sqlite", dbPath, "--proc", "ПроверкаИстории"},
	} {
		rootCmd.SetArgs(args)
		if out, err := captureStdout(t, rootCmd.Execute); err != nil {
			t.Fatalf("onebase %s: %v\n%s", args[0], err, out)
		}
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var documents, posted int
	if err := db.QueryRow(ctx, `SELECT COUNT(*), SUM(CASE WHEN posted THEN 1 ELSE 0 END)
        FROM задача WHERE заголовок = 'Позвонить клиенту'`).Scan(&documents, &posted); err != nil {
		t.Fatal(err)
	}
	if documents != 2 || posted != 2 {
		t.Fatalf("tasks = %d, posted = %d; want two posted tasks", documents, posted)
	}
	var history, distinctTasks, distinctPeriods, matchingDocuments int
	if err := db.QueryRow(ctx, `SELECT COUNT(*), COUNT(DISTINCT h.задача_id), COUNT(DISTINCT h.период),
        SUM(CASE WHEN h.задача_id = d.id AND h.recorder = d.id THEN 1 ELSE 0 END)
        FROM инфо_историястатусовзадач h
        LEFT JOIN задача d ON d.id = h.задача_id`).Scan(&history, &distinctTasks, &distinctPeriods, &matchingDocuments); err != nil {
		t.Fatal(err)
	}
	if history != 2 || distinctTasks != 2 || distinctPeriods != 1 || matchingDocuments != 2 {
		t.Fatalf("history = %d, distinct tasks = %d, periods = %d, matching documents = %d; want 2, 2, 1, 2",
			history, distinctTasks, distinctPeriods, matchingDocuments)
	}
}
