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
	"strings"
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
	// Конфигурация уже знает следующий объект, но его таблицы ещё нет: ровно
	// такое состояние оставляет миграция, остановленная гейтом уникальности на
	// Контрагентах (#1080). Публичная команда и вызывающий её лаунчер должны
	// считать это «дозаполнять нечего», а не падать с no such table.
	writeProcrunFixture(t, dir, "catalogs/НовыйОбъект.yaml",
		"name: НовыйОбъект\nnumerator:\n  prefix: \"Н-\"\n  length: 6\nfields:\n  - name: Наименование\n    type: string\n")

	// Второй разрыв того же происхождения: таблица объекта есть, а колонки
	// нового реквизита в ней нет — остановленная миграция до него не дошла.
	// Прочитать такой объект нельзя, и раньше его ошибка уносила весь отчёт
	// вместе с Контрагентами, из-за которых база и не стартовала (#1067).
	writeProcrunFixture(t, dir, "catalogs/Поступления.yaml",
		"name: Поступления\nnumerator:\n  prefix: \"П-\"\n  length: 6\nfields:\n"+
			"  - name: Наименование\n    type: string\n  - name: Организация\n    type: string\n")

	ent := renumberCatalog()
	// Тот же объект, каким его знает база: без колонки «Организация».
	stale := &metadata.Entity{
		Name: "Поступления",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	dbPath = filepath.Join(t.TempDir(), "contract.db")
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, []*metadata.Entity{ent, stale}); err != nil {
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

// Ради этого отчёт и переживает сбой на отдельном объекте: база не стартует
// из-за Контрагентов, а падает чтение соседа, чья таблица отстала от
// конфигурации. Раньше команда возвращала ошибку целиком, лаунчер не получал
// отчёта — и кнопка «Дозаполнить коды» не появлялась, оставляя пользователя
// один на один с текстом «выполните onebase renumber» (#1067).
func TestRenumberReportsUnreadableObjectAndKeepsRest(t *testing.T) {
	dir, dbPath := renumberContractFixture(t)

	rep, err := launcher.ParseRenumberReport([]byte(runRenumberJSON(t, dir, dbPath, false)))
	if err != nil {
		t.Fatalf("отчёт не разобран лаунчером: %v", err)
	}
	pending := rep.Pending()
	if len(pending) != 1 || pending[0].Object != "Контрагенты" {
		t.Fatalf("Pending = %+v, ожидались Контрагенты — иначе кнопки в лаунчере нет", pending)
	}
	// Оба разрыва схемы одного класса: у НовогоОбъекта нет таблицы, у
	// Поступлений — колонки под новый реквизит.
	skipped := map[string]string{}
	for _, obj := range rep.Skipped() {
		skipped[obj.Object] = obj.Error
		if obj.Empty != 0 {
			t.Errorf("у пропущенного %s empty = %d, ожидался 0", obj.Object, obj.Empty)
		}
	}
	if len(skipped) != 2 {
		t.Fatalf("Skipped = %+v, ожидались НовыйОбъект и Поступления", rep.Skipped())
	}
	// Причина пропуска обязана доехать до журнала: без неё «объект пропущен»
	// неотличимо от «объект в порядке».
	if !strings.Contains(skipped["Поступления"], "организация") {
		t.Errorf("причина пропуска = %q, в ней нет недостающей колонки", skipped["Поступления"])
	}
	if !strings.Contains(skipped["НовыйОбъект"], "новыйобъект") {
		t.Errorf("причина пропуска = %q, в ней нет имени таблицы", skipped["НовыйОбъект"])
	}

	// Лечение доходит до конца: непрочитанный сосед не мешает дозаполнить то,
	// обо что споткнулась миграция.
	done, err := launcher.ParseRenumberReport([]byte(runRenumberJSON(t, dir, dbPath, true)))
	if err != nil {
		t.Fatalf("отчёт записи не разобран: %v", err)
	}
	if done.FilledCount() != 2 {
		t.Errorf("FilledCount = %d, ожидалось 2", done.FilledCount())
	}
}

// Граница пропуска со стороны схемы: если отстали ВСЕ объекты, лечить нечем.
// Команда отрабатывает без ошибки (нечего делать — не сбой), но отчёт обязан
// остаться пустым по объёму: иначе лаунчер предложит кнопку, которая ничего не
// изменит, и пользователь пойдёт по кругу.
func TestRenumberWithoutReadableObjectsOffersNothing(t *testing.T) {
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: renumber-behind\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Поступления.yaml",
		"name: Поступления\nnumerator:\n  prefix: \"П-\"\n  length: 6\nfields:\n"+
			"  - name: Наименование\n    type: string\n  - name: Организация\n    type: string\n")

	stale := &metadata.Entity{
		Name: "Поступления",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	dbPath := filepath.Join(t.TempDir(), "behind.db")
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if err := db.Migrate(ctx, []*metadata.Entity{stale}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	db.Close()

	rep, err := launcher.ParseRenumberReport([]byte(runRenumberJSON(t, dir, dbPath, false)))
	if err != nil {
		t.Fatalf("отчёт не разобран: %v", err)
	}
	if len(rep.Pending()) != 0 || rep.EmptyCount() != 0 {
		t.Errorf("предложено лечение по отставшей схеме: %+v", rep.Pending())
	}
	if len(rep.Skipped()) != 1 {
		t.Fatalf("Skipped = %+v, ожидался один объект", rep.Skipped())
	}
}

// Обратная сторона пропуска: пропускается ТОЛЬКО отставшая схема. Любая другая
// ошибка обязана уронить команду, даже если предыдущий объект уже обработан и
// часть записи прошла. Иначе лаунчер ответит на сорвавшееся лечение ok:true,
// пользователь нажмёт «Дозаполнить и запустить» и получит ту же ошибку старта
// без единого следа причины.
func TestRenumberFailsOnRealErrorAfterSuccessfulObject(t *testing.T) {
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: renumber-partial\nversion: \"1.0\"\n")
	writeProcrunFixture(t, dir, "catalogs/Контрагенты.yaml",
		"name: Контрагенты\nnumerator:\n  prefix: \"К-\"\n  length: 6\nfields:\n"+
			"  - name: Наименование\n    type: string\n")
	writeProcrunFixture(t, dir, "catalogs/Поступления.yaml",
		"name: Поступления\nnumerator:\n  prefix: \"П-\"\n  length: 6\nfields:\n"+
			"  - name: Наименование\n    type: string\n")

	ent := renumberCatalog()
	// Схема Поступлений в порядке — сорвётся сама работа: у записи
	// идентификатор не UUID, и разбор строки в renumberRowID откажет. Годится
	// любая невозможная запись; важно, что это не отставание схемы.
	broken := &metadata.Entity{
		Name: "Поступления",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	dbPath := filepath.Join(t.TempDir(), "partial.db")
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent, broken}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		t.Fatalf("схема нумератора: %v", err)
	}
	// Контрагенты идут раньше по алфавиту: к моменту сбоя один объект уже
	// дозаполнен, то есть запись частичная.
	if err := db.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Альфа"}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO поступления (id, наименование) VALUES ('не-uuid', 'Тест')"); err != nil {
		t.Fatalf("вставка испорченной записи: %v", err)
	}
	db.Close()

	out, err := runRenumberExpectingFailure(t, dir, dbPath)
	if err == nil {
		t.Fatalf("частичная запись со сбоем отчиталась об успехе: %s", out)
	}
	if !strings.Contains(err.Error(), "Поступления") {
		t.Errorf("в ошибке нет имени объекта: %v", err)
	}
	// И этот сбой не должен выглядеть как отставание схемы: пропуск объясняют
	// пользователю, а здесь объяснять нечего — работа не выполнена.
	if strings.Contains(err.Error(), "отстала от конфигурации") {
		t.Errorf("настоящий сбой выдан за отставание схемы: %v", err)
	}
}

// runRenumberExpectingFailure гоняет команду тем же путём, что и runRenumberJSON,
// но отдаёт ошибку вызывающему: тесты отказа проверяют именно её.
func runRenumberExpectingFailure(t *testing.T, dir, dbPath string) (string, error) {
	t.Helper()
	rootCmd.SetArgs([]string{"renumber", "--project", dir, "--sqlite", dbPath, "--json", "--write"})
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
	return captureStdout(t, rootCmd.Execute)
}
