package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDSL_SyntaxErrorHasPosition(t *testing.T) {
	// Синтаксическая ошибка должна нести координаты (Line/Column), чтобы в
	// конфигураторе можно было кликнуть по ней и перейти к месту (issue #103).
	src := `Процедура Тест()
  х = ;
КонецПроцедуры`
	issues := ParseDSL(src, "Тест")
	if len(issues) != 1 {
		t.Fatalf("ожидалась 1 проблема, получено %d: %+v", len(issues), issues)
	}
	is := issues[0]
	if is.Line != 2 {
		t.Errorf("ожидалась строка 2, получено %d (%+v)", is.Line, is)
	}
	if is.Column == 0 {
		t.Errorf("ожидалась ненулевая колонка (%+v)", is)
	}
	// Координаты вычищены из текста — UI покажет их отдельно, без дубля.
	if strings.Contains(is.Message, "Тест:") {
		t.Errorf("координаты не вычищены из сообщения: %q", is.Message)
	}
}

func TestParseDSL_Clean(t *testing.T) {
	src := `Процедура Тест()
  Сообщить("привет");
КонецПроцедуры`
	if issues := ParseDSL(src, "test.os"); len(issues) != 0 {
		t.Fatalf("ожидалось 0 проблем, получено %d: %+v", len(issues), issues)
	}
}

func TestParseDSL_UndefinedFunction(t *testing.T) {
	src := `Процедура Тест()
  НесуществующаяФункция123("x");
КонецПроцедуры`
	issues := ParseDSL(src, "test.os")
	if len(issues) == 0 {
		t.Fatal("ожидалась проблема о неизвестной функции, получено 0")
	}
	found := false
	for _, is := range issues {
		if strings.Contains(is.Message, "неизвестная функция") {
			found = true
			if is.Line != 2 {
				t.Errorf("ожидалась строка 2, получено %d", is.Line)
			}
		}
	}
	if !found {
		t.Fatalf("нет сообщения о неизвестной функции: %+v", issues)
	}
}

func TestParseDSL_UndefinedFunctionInsideArrayLiteral(t *testing.T) {
	src := `Процедура Тест()
  а = [НесуществующаяФункция123("x")];
КонецПроцедуры`
	issues := ParseDSL(src, "test.os")
	for _, is := range issues {
		if strings.Contains(is.Message, "неизвестная функция") && is.Line == 2 {
			return
		}
	}
	t.Fatalf("ожидалась неизвестная функция внутри литерала массива, получено: %+v", issues)
}

func TestCheckDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "ok.os"), `Процедура П() Сообщить("ok"); КонецПроцедуры`)
	mustWrite(t, filepath.Join(src, "bad.os"), "Процедура П()\n  ВызовНесуществующей();\nКонецПроцедуры")

	issues, _ := CheckDir(dir)
	for _, is := range issues {
		if strings.HasPrefix(is.File, "src/ok.os") {
			t.Errorf("ok.os не должен иметь проблем: %+v", is)
		}
	}
	hasBad := false
	for _, is := range issues {
		if strings.HasPrefix(is.File, "src/bad.os") && strings.Contains(is.Message, "неизвестная функция") {
			hasBad = true
			if is.Line == 0 {
				t.Error("ожидался номер строки для bad.os")
			}
		}
	}
	if !hasBad {
		t.Fatalf("не найдена проблема в bad.os: %+v", issues)
	}
}

func TestCheckDir_ProcessorWizardWarning(t *testing.T) {
	dir := t.TempDir()
	procs := filepath.Join(dir, "processors")
	if err := os.MkdirAll(procs, 0o755); err != nil {
		t.Fatal(err)
	}
	// Плоская обработка — без предупреждений.
	mustWrite(t, filepath.Join(procs, "flat.yaml"), "name: Плоская\nparams:\n  - name: Путь\n    type: string\n")
	// Обработка-мастер — должна вызвать предупреждение про wizard и steps.
	mustWrite(t, filepath.Join(procs, "wiz.yaml"), "name: Мастер\nwizard: true\nsteps:\n  - title: Шаг 1\n    params:\n      - name: Файл\n        type: string\n")

	issues, _ := CheckDir(dir)
	for _, is := range issues {
		if strings.HasPrefix(is.File, "processors/flat.yaml") {
			t.Errorf("плоская обработка не должна иметь проблем: %+v", is)
		}
	}
	var wizardKeys []string
	for _, is := range issues {
		if strings.HasPrefix(is.File, "processors/wiz.yaml") && strings.Contains(is.Message, "не поддерживается") {
			if strings.Contains(is.Message, `"wizard"`) {
				wizardKeys = append(wizardKeys, "wizard")
			}
			if strings.Contains(is.Message, `"steps"`) {
				wizardKeys = append(wizardKeys, "steps")
			}
		}
	}
	if len(wizardKeys) != 2 {
		t.Fatalf("ожидались предупреждения про wizard и steps, получено %v (все: %+v)", wizardKeys, issues)
	}
}

func TestCheckDir_RejectsPeriodicInfoRegisterFieldNamedPeriod(t *testing.T) {
	dir := t.TempDir()
	inforegs := filepath.Join(dir, "inforegs")
	if err := os.MkdirAll(inforegs, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(inforegs, "history.yaml"), `name: История
periodic: true
dimensions:
  - name: Период
    type: string
`)

	issues, _ := CheckDir(dir)
	for _, issue := range issues {
		if strings.HasPrefix(issue.File, "inforegs/history.yaml") &&
			strings.Contains(issue.Message, "системного периода") {
			return
		}
	}
	t.Fatalf("configcheck did not report reserved periodic Период: %+v", issues)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
