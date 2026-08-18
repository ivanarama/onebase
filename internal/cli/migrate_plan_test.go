package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// `onebase migrate --dry-run` — предохранитель перед реструктуризацией рабочей
// базы: пользователь смотрит план и решает, готов ли он к потере данных.
// Функция печати плана была покрыта на 0% (#988, А6), хотя именно её вывод и
// принимает это решение.

func migrateCmdFor(t *testing.T, dir, dbPath string, flags map[string]string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("project", ".", "")
	cmd.Flags().String("db", "", "")
	cmd.Flags().String("sqlite", "", "")
	cmd.Flags().String("config-source", "file", "")
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().Bool("allow-destructive", false, "")
	for k, v := range map[string]string{"project": dir, "sqlite": dbPath} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

// migratePlanFixture — конфигурация с одним справочником. Поля объявлены с
// устойчивым `id`: без него PlanMigration возвращает пустой план по построению
// (`anyFieldHasID`) — переименование распознаётся именно по id, а поле без него
// мигрирует аддитивно (план 81).
//
// renamed=true переименовывает Цена в Стоимость, СОХРАНЯЯ id: это и есть
// изменение, ради которого dry-run существует. Смена типа тут не подошла бы —
// на SQLite типизация динамическая (number хранится как TEXT), string в number
// плана не порождает, и тест оказался бы зелёным на пустом плане.
func migratePlanFixture(t *testing.T, dir string, renamed bool) {
	t.Helper()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: migrate-test\nversion: \"1.0\"\n")
	price := "Цена"
	if renamed {
		price = "Стоимость"
	}
	writeProcrunFixture(t, dir, "catalogs/Товар.yaml",
		"name: Товар\nfields:\n"+
			"  - name: Наименование\n    id: f_name\n    type: string\n"+
			"  - name: "+price+"\n    id: f_price\n    type: number\n")
}

// TestMigratePlanOnMatchingSchema — план пуст, и сообщение обязано это
// объяснить: «изменений нет» легко прочитать как «команда не сработала».
func TestMigratePlanOnMatchingSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "plan.db")
	migratePlanFixture(t, dir, false)

	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("runMigrate: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return runMigrate(migrateCmdFor(t, dir, dbPath, map[string]string{"dry-run": "true"}), nil)
	})
	if err != nil {
		t.Fatalf("runMigrate --dry-run: %v", err)
	}
	if !strings.Contains(out, "Реструктуризация не требуется") {
		t.Errorf("нет объяснения пустого плана:\n%s", out)
	}
}

// TestMigratePlanShowsRenameAndKeepsDataIntact — переименование колонки это
// ровно тот класс изменений, ради которого dry-run существует. Тест проверяет и
// то, что пробный прогон ничего не поменял: иначе «пробный» — пустое слово.
func TestMigratePlanShowsRenameAndKeepsDataIntact(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "plan.db")
	migratePlanFixture(t, dir, false)
	if _, err := captureStdout(t, func() error { return runMigrate(migrateCmdFor(t, dir, dbPath, nil), nil) }); err != nil {
		t.Fatalf("первичная миграция: %v", err)
	}

	// Переименовываем поле (id прежний) и просим план.
	migratePlanFixture(t, dir, true)
	out, err := captureStdout(t, func() error {
		return runMigrate(migrateCmdFor(t, dir, dbPath, map[string]string{"dry-run": "true"}), nil)
	})
	if err != nil {
		t.Fatalf("runMigrate --dry-run: %v", err)
	}
	if !strings.Contains(out, "План реструктуризации") {
		t.Fatalf("план не напечатан:\n%s", out)
	}
	if !strings.Contains(out, "Это пробный прогон — база не изменена.") {
		t.Errorf("нет отметки о пробном прогоне:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "стоимость") {
		t.Errorf("в плане не названо изменяемое поле:\n%s", out)
	}

	// Повторный план обязан быть тем же: dry-run не должен ничего применить.
	again, err := captureStdout(t, func() error {
		return runMigrate(migrateCmdFor(t, dir, dbPath, map[string]string{"dry-run": "true"}), nil)
	})
	if err != nil {
		t.Fatalf("повторный runMigrate --dry-run: %v", err)
	}
	if again != out {
		t.Errorf("пробный прогон изменил базу — второй план отличается:\n--- первый ---\n%s\n--- второй ---\n%s", out, again)
	}
}
