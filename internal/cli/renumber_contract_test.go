package cli

// Контракт `onebase renumber --json` ↔ лаунчер (#1067).
//
// Лаунчер лечит отказ запуска кнопкой: запускает эту команду дочерним
// процессом и разбирает её JSON. Через границу процесса не проходит ни один
// компилятор, поэтому переименованное поле отчёта не сломает сборку — кнопка
// просто перестанет считать объём и молча исчезнет. Тест держит обе стороны:
// вывод настоящей команды разбирается настоящим разборщиком лаунчера.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/launcher"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// renumberContractFixture — проект с нумератором и база, где у двух записей
// код пуст: ровно то состояние, из-за которого не стартует база с
// numerator.unique.
func renumberContractFixture(t *testing.T) (dir, dbPath string) {
	t.Helper()
	dir = t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: renumber-contract\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Контрагенты.yaml",
		"name: Контрагенты\nnumerator:\n  prefix: \"К-\"\n  length: 6\nfields:\n  - name: Наименование\n    type: string\n")

	ent := renumberCatalog()
	dbPath = filepath.Join(t.TempDir(), "contract.db")
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		t.Fatalf("схема нумератора: %v", err)
	}
	for _, name := range []string{"Альфа", "Бета"} {
		if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": name}, ent); err != nil {
			t.Fatalf("вставка %s: %v", name, err)
		}
	}
	return dir, dbPath
}

// runRenumberJSON выполняет команду полным путём пользователя: argv →
// диспетчер cobra → RunE, и снимает её stdout.
func runRenumberJSON(t *testing.T, dir, dbPath string, write bool) string {
	t.Helper()
	args := []string{"renumber", "--project", dir, "--sqlite", dbPath, "--json"}
	if write {
		args = append(args, "--write")
	}
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		for _, name := range []string{"project", "sqlite", "object"} {
			if f := renumberCmd.Flags().Lookup(name); f != nil {
				if err := f.Value.Set(""); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, name := range []string{"write", "json"} {
			if f := renumberCmd.Flags().Lookup(name); f != nil {
				if err := f.Value.Set("false"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	out, err := captureStdout(t, rootCmd.Execute)
	if err != nil {
		t.Fatalf("`onebase renumber --json`: %v\n%s", err, out)
	}
	return out
}

func TestRenumberJSONMatchesLauncherContract(t *testing.T) {
	dir, dbPath := renumberContractFixture(t)

	// Разведка: столько записей лаунчер покажет в окне ошибки.
	rep, err := launcher.ParseRenumberReport([]byte(runRenumberJSON(t, dir, dbPath, false)))
	if err != nil {
		t.Fatalf("отчёт разведки не разобран лаунчером: %v", err)
	}
	if rep.Write {
		t.Error("отчёт без --write помечен как запись")
	}
	if rep.EmptyCount() != 2 {
		t.Errorf("EmptyCount = %d, ожидалось 2 — лаунчер покажет неверный объём", rep.EmptyCount())
	}
	pending := rep.Pending()
	if len(pending) != 1 {
		t.Fatalf("Pending = %+v, ожидался один объект с пустыми значениями", pending)
	}
	// Имя объекта и поля попадают в текст диалога — пустые строки там означают
	// «— 2 записей без значения «»».
	if pending[0].Object != "Контрагенты" || pending[0].Field != metadata.StandardCodeField {
		t.Errorf("объект/поле в отчёте = %q/%q, ожидались Контрагенты/%s",
			pending[0].Object, pending[0].Field, metadata.StandardCodeField)
	}

	// Само лечение: столько записей лаунчер покажет как дозаполненные.
	done, err := launcher.ParseRenumberReport([]byte(runRenumberJSON(t, dir, dbPath, true)))
	if err != nil {
		t.Fatalf("отчёт записи не разобран лаунчером: %v", err)
	}
	if !done.Write {
		t.Error("отчёт с --write не помечен как запись")
	}
	if done.FilledCount() != 2 {
		t.Errorf("FilledCount = %d, ожидалось 2", done.FilledCount())
	}

	// Проверяется состояние базы, а не отчёт о ней: в отчёте о записи empty —
	// это «сколько было пусто», и оно остаётся равным двум по определению.
	// Пустых записей после лечения быть не должно, иначе повторный запуск базы
	// упрётся в ту же миграцию.
	after, err := launcher.ParseRenumberReport([]byte(runRenumberJSON(t, dir, dbPath, false)))
	if err != nil {
		t.Fatalf("повторная разведка не разобрана: %v", err)
	}
	if after.EmptyCount() != 0 {
		t.Errorf("после лечения осталось пустых: %d (%+v)", after.EmptyCount(), after.Pending())
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("база пропала: %v", err)
	}
}
