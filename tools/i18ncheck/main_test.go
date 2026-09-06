package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// runMainEnv переключает тестовый бинарь в режим «я — i18ncheck»: см.
// TestMain_PrintsCoverageReport.
const runMainEnv = "I18NCHECK_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func TestExtractGoKeys_TemplateLanguageFormsAndTranslationExpressions(t *testing.T) {
	source := []byte("package sample\n\nconst tmpl = `" +
		`{{t .Lang "dot language"}} {{t $.Lang "root language"}}` +
		"`\n\n" +
		`func render(r any, data struct{ Lang string }, s *server, dynamic string) {
	_ = tr(resolveLang(r), "call expression")
	_ = tr(data.Lang, "selector expression")
	_ = s.tr(s.resolveLang(r), "selector call expression")
	_ = tr(data.Lang, dynamic)
}
`)

	keys, err := extractGoKeys(source)
	if err != nil {
		t.Fatalf("extractGoKeys: %v", err)
	}
	want := map[string]bool{
		"call expression":          true,
		"dot language":             true,
		"root language":            true,
		"selector call expression": true,
		"selector expression":      true,
	}
	if len(keys) != len(want) {
		t.Fatalf("keys = %q, want %d keys", keys, len(want))
	}
	for _, key := range keys {
		if !want[key] {
			t.Errorf("unexpected key %q in %q", key, keys)
		}
	}
}

// Язык, у которого пока есть только машинный ярус, обязан попадать в отчёт:
// именно так приезжает каждый новый перевод (человеческий ярус появляется
// позже, при ревью носителем). Отчёт, собранный по одному человеческому ярусу,
// молчал бы о нём вовсе — и «языка нет» было бы не отличить от «язык переведён».
func TestReportCoverage_ListsMachineOnlyLanguage(t *testing.T) {
	keys := []string{"Записать", "Удалить"}
	human := map[string]map[string]string{
		"en": {"Записать": "Save", "Удалить": "Delete"},
		"de": {"Записать": "Speichern"},
	}
	machine := map[string]map[string]string{
		"de": {"Удалить": "Löschen"},
		"be": {"Записать": "Запісаць"},
	}

	var out bytes.Buffer
	reportCoverage(&out, keys, human, machine)

	got := out.String()
	for _, want := range []string{
		"3 locales",
		"  be: 2 не переведено человеком (1 закрыто машинным ярусом, 1 останется по-английски)\n",
		"  de: 1 не переведено человеком (1 закрыто машинным ярусом, 0 останется по-английски)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("отчёт не содержит %q:\n%s", want, got)
		}
	}
}

// Проводка «main() печатает отчёт» проверяется отдельно от самого отчёта:
// тест выше зовёт reportCoverage напрямую и о том, вызывают ли её вообще,
// ничего не говорит, а шаг i18ncheck в CI смотрит только на код возврата.
// Между ними и жила дырка — «main перестал звать reportCoverage» не поймал бы
// никто, проводку проверяли руками (#1163).
//
// Запуск идёт через настоящий main(): os.Exit делает его невызываемым внутри
// теста, поэтому тестовый бинарь перезапускает сам себя, а TestMain по
// переменной окружения отдаёт управление main() вместо прогона тестов. Так
// проверяется та самая функция, а не её копия.
func TestMain_PrintsCoverageReport(t *testing.T) {
	cmd := exec.Command(os.Args[0]) //nolint:gosec // G204: запускается сам тестовый бинарь без аргументов, режим задаётся переменной окружения; ни shell, ни пользовательский ввод не участвуют
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	out, err := cmd.CombinedOutput()
	got := string(out)
	// Код возврата 1 — это сработавший гейт непереведённых ключей, а не
	// поломка проводки: отчёт печатается раньше него. Дублировать гейт здесь
	// незачем, иначе тест начнёт падать вместо него и о своём предмете молчать.
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("запуск main(): %v\n%s", err, got)
		}
	}

	header := regexp.MustCompile(`i18ncheck: (\d+) keys in templates, (\d+) locales`)
	m := header.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("main() не напечатал отчёт о покрытии — reportCoverage не вызвана:\n%s", got)
	}
	// Числа, а не только заголовок: пустые словари дали бы «0 keys, 0 locales»,
	// то есть отчёт по ничему. Точные значения не закрепляем — они меняются с
	// каждым новым ключом и языком.
	keys, _ := strconv.Atoi(m[1])
	locales, _ := strconv.Atoi(m[2])
	if keys == 0 || locales < 2 {
		t.Errorf("отчёт собран не по настоящим словарям: %d ключей, %d локалей\n%s", keys, locales, got)
	}
}
