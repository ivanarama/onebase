package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

// `onebase init` — первая команда, которую запускает новый пользователь, и она
// была покрыта на 0% (#988, А6). Самое неприятное, что здесь может случиться,
// молча: заготовка, которая не проходит собственный `onebase check`.

// withInitFlags выставляет глобальные переменные флагов init и возвращает их
// на место. Значения живут в пакете, а не в объекте команды, поэтому забытый
// сброс утёк бы в соседний тест.
func withInitFlags(t *testing.T, template string, list bool) {
	t.Helper()
	oldTemplate, oldList := initTemplate, initListTemplate
	initTemplate, initListTemplate = template, list
	t.Cleanup(func() { initTemplate, initListTemplate = oldTemplate, oldList })
}

func runInitInto(t *testing.T, dir, template string) string {
	t.Helper()
	withInitFlags(t, template, false)
	out, err := captureStdout(t, func() error { return runInit(&cobra.Command{}, []string{dir}) })
	if err != nil {
		t.Fatalf("runInit(%q, шаблон %q): %v", dir, template, err)
	}
	return out
}

// TestInitScaffoldPassesOwnCheck — главный тест этого файла. Заготовка обязана
// проходить `onebase check`: пользователь запускает эти две команды подряд, и
// «свежесозданный проект не валиден» — отказ ровно на первом шаге знакомства
// с платформой.
func TestInitScaffoldPassesOwnCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "склад")
	runInitInto(t, dir, "")

	out, err := runCheckCmd(t, runCheck, dir, nil)
	if err != nil {
		t.Fatalf("свежесозданный проект не проходит onebase check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK: ошибок не найдено") {
		t.Errorf("check не подтвердил успех:\n%s", out)
	}
}

// TestInitTemplatesPassOwnCheck — то же для всех встроенных шаблонов: они
// рекламируются в QUICKSTART как «рекомендуется», значит должны быть рабочими.
func TestInitTemplatesPassOwnCheck(t *testing.T) {
	for _, tmpl := range project.ListTemplates() {
		t.Run(tmpl.Name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tmpl.Name)
			out := runInitInto(t, dir, tmpl.Name)
			if !strings.Contains(out, tmpl.Name) {
				t.Errorf("вывод не назвал применённый шаблон: %q", out)
			}
			checkOut, err := runCheckCmd(t, runCheck, dir, nil)
			if err != nil {
				t.Fatalf("шаблон %q не проходит onebase check: %v\n%s", tmpl.Name, err, checkOut)
			}
		})
	}
}

// TestInitWritesAIGuide — AGENTS.md и CLAUDE.md кладутся best-effort, то есть
// без ошибки при сбое. Именно поэтому их отсутствие надо проверять тестом:
// иначе пропажа не проявится ничем.
func TestInitWritesAIGuide(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "проект")
	runInitInto(t, dir, "")

	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s не создан: %v", name, err)
		}
		if len(body) == 0 {
			t.Fatalf("%s пуст", name)
		}
	}
	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Руководство генерируется из платформы, а не из шаблона-строки: в нём
	// обязаны быть встроенные функции из реестра langref.
	if !strings.Contains(string(agents), "onebase check") || !strings.Contains(string(agents), "СтрЗаменить") {
		t.Error("AGENTS.md не похож на сгенерированное руководство: нет ни рабочего цикла, ни функций из langref")
	}
}

// TestInitKeepsExistingClaudeMD фиксирует поведение, обещанное комментарием в
// коде: CLAUDE.md пользователя не перезаписывается. Проверить это можно только
// повторным запуском — при первом файла ещё нет.
func TestInitKeepsExistingClaudeMD(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "проект")
	runInitInto(t, dir, "")

	custom := "# мои правила\nне трогать\n"
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	runInitInto(t, dir, "")

	got, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Errorf("CLAUDE.md перезаписан:\n%s", got)
	}
}

func TestInitListTemplates(t *testing.T) {
	withInitFlags(t, "", true)
	out, err := captureStdout(t, func() error { return runInit(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runInit --list-templates: %v", err)
	}
	for _, tmpl := range project.ListTemplates() {
		if !strings.Contains(out, tmpl.Name) {
			t.Errorf("шаблон %q не показан:\n%s", tmpl.Name, out)
		}
	}
	// --list-templates обязан завершаться, ничего не создавая: иначе он
	// насорил бы заготовкой в текущем каталоге.
	if _, err := os.Stat("config"); err == nil {
		t.Error("--list-templates создал config/ в рабочем каталоге")
	}
}

func TestInitUnknownTemplateFails(t *testing.T) {
	withInitFlags(t, "такого-нет", false)
	dir := filepath.Join(t.TempDir(), "проект")
	_, err := captureStdout(t, func() error { return runInit(&cobra.Command{}, []string{dir}) })
	if err == nil {
		t.Fatal("несуществующий шаблон принят без ошибки")
	}
	if errors.Is(err, errSilentExit) {
		t.Error("ошибка выдана как «тихий выход» — пользователь не увидит причину")
	}
}

// TestInitNamesAppByDirectory — имя приложения берётся из имени каталога;
// в app.yaml должно оказаться оно, а не «myapp» или путь целиком.
func TestInitNamesAppByDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "СкладОбуви")
	runInitInto(t, dir, "")

	body, err := os.ReadFile(filepath.Join(dir, "config", "app.yaml"))
	if err != nil {
		t.Fatalf("config/app.yaml не создан: %v", err)
	}
	if !strings.Contains(string(body), "СкладОбуви") {
		t.Errorf("имя приложения не взято из каталога:\n%s", body)
	}
}

// TestWarehouseTemplatePostingWritesMovements — зелёный `onebase check` не
// доказывает, что проведение работает: check исполняет ЗАПРОСЫ модулей, а не
// сам обработчик проведения. Поэтому один шаблон проверяется сквозняком —
// init → migrate → procrun → чтение регистра.
//
// Именно этот класс дефекта и разбирался в #988, А6: три шаблона из четырёх
// звали `Движения("Регистр")` как функцию, которой в платформе нет. Движения
// добавляются через `Движения.Регистр.Добавить()`, и записывает их платформа
// сама после обработчика.
func TestWarehouseTemplatePostingWritesMovements(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "склад")
	runInitInto(t, dir, "warehouse")

	// Обработка, которая заполняет и проводит поступление.
	writeProcrunFixture(t, dir, "processors/Загрузка.yaml", "name: Загрузка\n")
	writeProcrunFixture(t, dir, "src/Загрузка.proc.os", `Процедура Выполнить()
  Товар = Справочники.Номенклатура.Создать();
  Товар.Наименование = "Гвозди";
  Товар.Записать();

  Склад = Справочники.Склад.Создать();
  Склад.Наименование = "Основной";
  Склад.Записать();

  Док = Документы.Поступление.Создать();
  Док.Дата = ТекущаяДата();
  Док.Склад = Склад.Ссылка;
  Стр = Док.Товары.Добавить();
  Стр.Номенклатура = Товар.Ссылка;
  Стр.Количество = 7;
  Стр.Сумма = 700;
  Док.Записать();
  Док.Провести();
КонецПроцедуры
`)

	dbPath := filepath.Join(t.TempDir(), "склад.db")

	migrate := &cobra.Command{}
	addBaseFlags(migrate)
	for k, v := range map[string]string{"project": dir, "sqlite": dbPath} {
		if err := migrate.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureStdout(t, func() error { return runMigrate(migrate, nil) }); err != nil {
		t.Fatalf("схема по шаблону warehouse не создаётся: %v", err)
	}

	cmd := &cobra.Command{}
	addBaseFlags(cmd)
	cmd.Flags().String("proc", "", "")
	cmd.Flags().StringArray("set", nil, "")
	cmd.Flags().StringArray("file", nil, "")
	for k, v := range map[string]string{"project": dir, "sqlite": dbPath, "proc": "Загрузка"} {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureStdout(t, func() error { return runProcrun(cmd, nil) }); err != nil {
		t.Fatalf("проведение по шаблону warehouse не отработало: %v", err)
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	var qty float64
	if err := db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CAST(количество AS NUMERIC)), 0) FROM рег_остаткитоваров`,
	).Scan(&count, &qty); err != nil {
		t.Fatalf("чтение регистра: %v", err)
	}
	if count == 0 {
		t.Fatal("проведение не записало ни одного движения — обработчик отработал вхолостую")
	}
	if qty != 7 {
		t.Errorf("в регистре количество %v, ожидалось 7", qty)
	}
}
