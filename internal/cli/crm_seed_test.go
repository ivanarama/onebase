package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

// Проверяем поставляемую обработку через тот же migrate → procrun, что и CI.
// Успех команды недостаточен: неверный разделитель раньше оставлял в имени
// всю запись с «|», а числовые поля и цвет — пустыми.
func TestCRMSeedStagesThroughCLI(t *testing.T) {
	projectDir, err := filepath.Abs("../../examples/crm")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "crm.db")
	run := func(name string, args ...string) {
		t.Helper()
		if !t.Run(name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"project", "sqlite", "proc"} {
				if flag := cmd.Flag(name); flag != nil {
					value, changed := flag.Value.String(), flag.Changed
					t.Cleanup(func() {
						if err := flag.Value.Set(value); err != nil {
							t.Error(err)
						}
						flag.Changed = changed
					})
				}
			}
			if out, err := executeRootArgs(t, args...); err != nil {
				t.Fatalf("CLI: %v\n%s", err, out)
			}
		}) {
			t.FailNow()
		}
	}
	run("migrate", "migrate", "--project", projectDir, "--sqlite", dbPath)
	for _, name := range []string{"seed", "seed_again"} {
		run(name, "procrun", "--project", projectDir, "--sqlite", dbPath, "--proc", "ЗаполнитьТестовуюБазу")
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT наименование, CAST(порядок AS INTEGER),
		CAST(вероятность AS INTEGER), цвет FROM стадиясделки ORDER BY CAST(порядок AS INTEGER)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type stage struct {
		name               string
		order, probability int
		color              string
	}
	want := []stage{
		{"Первый контакт (клиент ещё не знает)", 10, 10, "#AAAAAA"},
		{"Квалификация (поняли, что у клиента есть деньги)", 20, 25, "#4ECDC4"},
		{"Коммерческое предложение (цензурная версия)", 30, 50, "#FFE66D"},
		{"Переговоры (клиент торгуется за копейки)", 40, 70, "#FF6B6B"},
		{"Согласование договора (юристы в шоке)", 50, 85, "#C44D58"},
		{"Успешная реализация (чудо произошло!)", 60, 100, "#2ECC71"},
		{"Закрыто и не реализовано (ну и ладно)", 70, 0, "#95A5A6"},
	}
	count := 0
	for rows.Next() {
		var got stage
		if err := rows.Scan(&got.name, &got.order, &got.probability, &got.color); err != nil {
			t.Fatal(err)
		}
		if count >= len(want) {
			t.Fatalf("лишняя стадия: %+v", got)
		}
		if got != want[count] {
			t.Errorf("стадия %d: %+v, ожидалась %+v", count, got, want[count])
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Errorf("стадий %d, ожидалось %d", count, len(want))
	}
}
