package onec_forms

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
)

// updateGolden — если установлен флаг `-update-golden` (или env GOLDEN_UPDATE=1),
// тесты не сравнивают вывод с эталонными файлами, а перезаписывают их.
// Используется при изменении формата YAML.
var updateGolden = flag.Bool("update-golden", false, "перезаписать эталонные файлы фикстур")

func isUpdateGolden() bool {
	return (updateGolden != nil && *updateGolden) || os.Getenv("GOLDEN_UPDATE") == "1"
}

const (
	fixtureXMLPath  = "fixtures/minimal-Form.xml"
	fixtureBSLPath  = "fixtures/minimal-Module.bsl"
	fixtureYAMLGold = "fixtures/minimal.form.yaml"
	fixtureOSGold   = "fixtures/minimal.form.os"
)

// TestGoldenImport — сквозной импорт минимальной фикстуры и сравнение
// результата с эталонными .form.yaml и .form.os.
//
// При несовпадении тест падает с подробным diff'ом. Если нужно
// перегенерировать эталоны (например после изменения YAML-схемы),
// запустите тест с -update-golden или GOLDEN_UPDATE=1.
func TestGoldenImport(t *testing.T) {
	tmp := t.TempDir()
	dstYAML := filepath.Join(tmp, "minimal.form.yaml")
	dstOS := filepath.Join(tmp, "minimal.form.os")

	report, err := ImportFromOneC(ImportOptions{
		XMLPath:     fixtureXMLPath,
		BSLPath:     fixtureBSLPath,
		EntityName:  "РеализацияТоваров",
		FormName:    "ФормаОбъекта",
		FormKind:    "object",
		DstYAMLPath: dstYAML,
		DstOSPath:   dstOS,
	})
	if err != nil {
		t.Fatalf("ImportFromOneC: %v", err)
	}
	if report.YAMLPath == "" || report.ModulePath == "" {
		t.Fatalf("ImportReport не содержит пути: %+v", report)
	}

	// Не ожидаем error-warnings на минимальной фикстуре.
	for _, w := range report.Warnings {
		if w.Severity == SeverityError {
			t.Errorf("неожиданный error: %s", w)
		}
	}

	// Сравниваем YAML
	got, err := os.ReadFile(dstYAML)
	if err != nil {
		t.Fatal(err)
	}
	if isUpdateGolden() {
		if err := os.WriteFile(fixtureYAMLGold, got, 0o644); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
		t.Logf("перезаписан эталон: %s", fixtureYAMLGold)
	} else {
		want, err := os.ReadFile(fixtureYAMLGold)
		if err != nil {
			t.Fatalf("эталон не найден (%s) — запустите с -update-golden: %v", fixtureYAMLGold, err)
		}
		if string(got) != string(want) {
			showDiff(t, fixtureYAMLGold, string(want), string(got))
		}
	}

	// Сравниваем .form.os
	gotOS, err := os.ReadFile(dstOS)
	if err != nil {
		t.Fatal(err)
	}
	if isUpdateGolden() {
		if err := os.WriteFile(fixtureOSGold, gotOS, 0o644); err != nil { //nolint:gosec // G703: путь построен под контролем (SafeJoin либо каталог, заданный администратором)
			t.Fatal(err)
		}
		t.Logf("перезаписан эталон: %s", fixtureOSGold)
	} else {
		wantOS, err := os.ReadFile(fixtureOSGold)
		if err != nil {
			t.Fatalf("эталон не найден (%s) — запустите с -update-golden: %v", fixtureOSGold, err)
		}
		if string(gotOS) != string(wantOS) {
			showDiff(t, fixtureOSGold, string(wantOS), string(gotOS))
		}
	}
}

// TestGoldenImport_RoundTrip — после импорта мы должны уметь повторно
// прочитать YAML через managed_form_loader и получить эквивалентную
// FormModule (тот же набор реквизитов, команд и дерева элементов).
func TestGoldenImport_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	dstYAML := filepath.Join(tmp, "minimal.form.yaml")

	if _, err := ImportFromOneC(ImportOptions{
		XMLPath:     fixtureXMLPath,
		EntityName:  "РеализацияТоваров",
		FormName:    "ФормаОбъекта",
		FormKind:    "object",
		DstYAMLPath: dstYAML,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	mfl := loader.NewManagedFormLoader()
	fm, err := mfl.LoadFormFile(dstYAML, "РеализацияТоваров")
	if err != nil {
		t.Fatalf("reload yaml: %v", err)
	}
	if !fm.IsManaged() {
		t.Error("reloaded form должна быть managed")
	}
	if fm.EntityName != "РеализацияТоваров" || fm.Name != "ФормаОбъекта" || fm.Kind != "object" {
		t.Errorf("reloaded metadata: entity=%q name=%q kind=%q",
			fm.EntityName, fm.Name, fm.Kind)
	}
	// 2 реквизита, второй — таблица с 2 колонками
	if len(fm.Attributes) != 2 {
		t.Fatalf("attributes count = %d", len(fm.Attributes))
	}
	if fm.Attributes[1].TypeRef != "ValueTable" || len(fm.Attributes[1].Columns) != 2 {
		t.Errorf("reloaded Товары = %+v", fm.Attributes[1])
	}
	// 1 команда + 1 командная панель с 1 кнопкой
	if len(fm.Commands) != 1 || fm.Commands[0].Name != "Провести" {
		t.Errorf("reloaded commands = %+v", fm.Commands)
	}
	if fm.AutoCommandBar == nil || len(fm.AutoCommandBar.Buttons) != 1 {
		t.Errorf("reloaded command_bar = %+v", fm.AutoCommandBar)
	}
	// Дерево: 1 верхняя ГруппаФормы → 2 поля
	if len(fm.Elements) != 1 {
		t.Fatalf("reloaded root elements = %d", len(fm.Elements))
	}
	root := fm.Elements[0]
	if len(root.Children) != 2 {
		t.Fatalf("group children = %d", len(root.Children))
	}
	// Событие OnChange на ПолеНомер → ПриИзменении
	first := root.Children[0]
	if first.Handlers["ПриИзменении"] != "НомерПриИзменении" {
		t.Errorf("ПолеНомер.Handlers = %+v", first.Handlers)
	}
	// Form-level: ПриОткрытии
	if fm.Handlers["ПриОткрытии"] != "ПриОткрытии" {
		t.Errorf("form Handlers = %+v", fm.Handlers)
	}
}

// showDiff выводит небольшой diff построчно, чтобы при поломке тест мог
// быстро подсветить место расхождения без тяжелых зависимостей.
func showDiff(t *testing.T, label, want, got string) {
	t.Helper()
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	t.Errorf("эталон %s не совпадает (want=%d строк, got=%d строк):", label, len(wantLines), len(gotLines))
	max := len(wantLines)
	if len(gotLines) > max {
		max = len(gotLines)
	}
	shown := 0
	for i := 0; i < max && shown < 20; i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			t.Errorf("  line %d:\n    want: %q\n    got:  %q", i+1, w, g)
			shown++
		}
	}
	if shown == 0 && len(wantLines) != len(gotLines) {
		t.Errorf("  построчно совпадает но длина разная: want=%d got=%d", len(wantLines), len(gotLines))
	}
	t.Errorf("  при изменении формата перегенерируйте: GOLDEN_UPDATE=1 go test ./internal/onec_forms/")
}
