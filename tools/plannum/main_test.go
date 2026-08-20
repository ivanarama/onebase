package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plans пишет во временный каталог набор планов: имя файла → первая строка.
// Каталог временный намеренно — тест про правило, а не про текущее содержимое
// Plans/ (иначе он краснел бы от каждого нового плана и его чинили бы правкой
// ожиданий, а не кода).
func plans(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, head := range files {
		body := head + "\n\nТело плана.\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func requireNoProblems(t *testing.T, problems []string) {
	t.Helper()
	if len(problems) > 0 {
		t.Fatalf("ожидалось «чисто», получено: %s", strings.Join(problems, " | "))
	}
}

func requireProblem(t *testing.T, problems []string, want string) {
	t.Helper()
	for _, p := range problems {
		if strings.Contains(p, want) {
			return
		}
	}
	t.Fatalf("нет нарушения про %q, получено: %s", want, strings.Join(problems, " | "))
}

// Ровно тот дефект, ради которого инструмент написан: разные планы под одним
// номером (#1035 — так жили 06, 30, 31, 32, 38, 39, 45, 46, 52, 71, 72, 74).
func TestCheck_CollisionIsReported(t *testing.T) {
	dir := plans(t, map[string]string{
		"74-realtime-sse-push.md": "# План 74 — Realtime-push",
		"74-database-rollup.md":   "# План 74: свёртка информационной базы",
	})
	problems, _, err := check(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "номер 74 делят разные планы")
}

// Части одного плана под одним номером — норма, если номер объявлен в multiPart.
func TestCheck_MultiPartAllowed(t *testing.T) {
	dir := plans(t, map[string]string{
		"86-data-exchange.md":      "# План 86 - Обмен данными",
		"86-data-exchange-demo.md": "# Демо: обмен данными между базами (план 86, фаза 1)",
	})
	problems, total, err := check(dir, map[int]multiPartPlan{86: {
		topic: "обмен данными: план + демо",
		files: []string{"86-data-exchange.md", "86-data-exchange-demo.md"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
	if total != 1 {
		t.Errorf("занятых номеров = %d, ожидался 1", total)
	}
}

// Историческое multiPart-исключение не должно отключать гейт для всего
// номера: третий несвязанный план обязан уронить CI.
func TestCheck_MultiPartRejectsUnrelatedThirdPlan(t *testing.T) {
	dir := plans(t, map[string]string{
		"86-data-exchange.md":      "# План 86 - Обмен данными",
		"86-data-exchange-demo.md": "# Демо: обмен данными между базами (план 86, фаза 1)",
		"86-unrelated-cashflow.md": "# План 86 - Прогноз денежного потока",
	})
	problems, _, err := check(dir, map[int]multiPartPlan{86: {
		topic: "обмен данными: план + демо",
		files: []string{"86-data-exchange.md", "86-data-exchange-demo.md"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "третий несвязанный план")
}

// Исключение, переставшее быть правдой, опаснее отсутствующего: оно молча
// разрешает новую коллизию под тем же номером.
func TestCheck_StaleMultiPartEntryIsReported(t *testing.T) {
	dir := plans(t, map[string]string{"86-data-exchange.md": "# План 86 — Обмен данными"})
	problems, _, err := check(dir, map[int]multiPartPlan{86: {
		topic: "обмен данными: план + демо",
		files: []string{"86-data-exchange.md", "86-data-exchange-demo.md"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "протух")
}

// Номер, живущий только в имени файла, — механизм появления коллизий: следующий
// автор открывает план, номера в тексте не видит и занимает его снова.
func TestCheck_HeadingMustClaimNumber(t *testing.T) {
	dir := plans(t, map[string]string{"31-home-page-widgets.md": "# План: Стартовая страница с виджетами"})
	problems, _, err := check(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireProblem(t, problems, "заголовок не называет номер 31")
}

// Формы записи номера, принятые в репозитории, обязаны считаться объявлением —
// иначе гейт заставит переписывать заголовки под свой вкус.
func TestCheck_AcceptedHeadingForms(t *testing.T) {
	dir := plans(t, map[string]string{
		"06-hierarchical-catalogs.md":     "# Этап 6 — Иерархические справочники",
		"45-mobile-pwa.md":                "# Этап 45 — Мобильный доступ",
		"72-subsystem-icons.md":           "# 72 — Иконки подсистем",
		"108-atomic-hook-side-effects.md": "# 108. Атомарность побочных записей",
		"65-richtext.md":                  "# Тип поля richtext — план 65",
		"51-followups-impl.md":            "# Реализация follow-up'ов плана 51",
		"136-config-testing-tooling.md":   "# Stage 136 — config testing tooling",
	})
	problems, _, err := check(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
}

// Файл без ведущего номера планом не считается: README.md и заметки лежат рядом.
func TestCheck_UnnumberedFilesIgnored(t *testing.T) {
	dir := plans(t, map[string]string{
		"README.md":                    "# Планы развития",
		"dev-workflow-improvements.md": "# Улучшения dev-цикла",
	})
	problems, total, err := check(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
	if total != 0 {
		t.Errorf("занятых номеров = %d, ожидался 0", total)
	}
}

// Настоящий каталог планов обязан проходить гейт: инструмент, красный на своём
// же репозитории, через неделю станет «известным красным» и перестанет что-либо
// значить.
func TestCheck_RepositoryPlansAreClean(t *testing.T) {
	problems, total, err := check(filepath.Join("..", "..", "Plans"), multiPart)
	if err != nil {
		t.Fatal(err)
	}
	requireNoProblems(t, problems)
	if total < 100 {
		t.Errorf("найдено планов: %d — похоже, каталог прочитан не тот", total)
	}
}
