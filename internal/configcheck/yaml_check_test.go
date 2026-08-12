package configcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDSL_Broken(t *testing.T) {
	src := `Процедура Привет(
    Сообщить("Hi")
КонецПроцедуры`
	issues := ParseDSL(src, "test.os")
	if len(issues) == 0 {
		t.Fatal("expected at least one issue for missing )")
	}
}

func TestCheckWidgetYAML_OK(t *testing.T) {
	yaml := `name: ВыручкаМесяца
type: kpi
title: Выручка
format: money
query: ВЫБРАТЬ СУММА(Сумма) КАК Значение ИЗ Документ.X`
	if issues := CheckWidgetYAML(yaml, "ВыручкаМесяца"); len(issues) != 0 {
		t.Fatalf("expected clean widget, got %+v", issues)
	}
}

func TestCheckWidgetYAML_UnknownType(t *testing.T) {
	yaml := `name: X
type: gauge
title: ok`
	issues := CheckWidgetYAML(yaml, "X")
	if len(issues) == 0 {
		t.Fatal("expected error on unknown widget type")
	}
	if !strings.Contains(issues[0].Message, "type") && !strings.Contains(issues[0].Message, "тип") {
		t.Errorf("expected type-related message, got %q", issues[0].Message)
	}
}

func TestCheckHomePageYAML_Empty(t *testing.T) {
	if issues := CheckHomePageYAML(""); len(issues) != 0 {
		t.Fatalf("empty body should be considered valid, got %+v", issues)
	}
}

func TestCheckHomePageYAML_Bad(t *testing.T) {
	bad := "title: Главная\nlayout: ::not-yaml::\n  - broken"
	if issues := CheckHomePageYAML(bad); len(issues) == 0 {
		t.Fatal("expected YAML parse error")
	}
}

func TestCheckDir_WithWidget(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "widgets", "ok.yaml"), `name: A
type: kpi
title: A
query: SELECT 1`)
	mkFile(t, filepath.Join(dir, "src", "broken.os"), `Процедура X(
КонецПроцедуры`)
	mkFile(t, filepath.Join(dir, "src", "good.os"), `Процедура Y()
КонецПроцедуры`)

	issues, _ := CheckDir(dir)
	var hasBroken bool
	for _, i := range issues {
		if strings.Contains(i.File, "broken.os") {
			hasBroken = true
		}
	}
	if !hasBroken {
		t.Fatalf("expected broken.os issue, got: %+v", issues)
	}
}

// TestCheckDir_LegacyFormWarning проверяет, что непустая legacy-форма даёт
// OK=true и ровно 1 предупреждение (план 64, этап 4): exit code 0 не ломается.
func TestCheckDir_LegacyFormWarning(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "printforms", "накладная.yaml"), `name: Накладная
document: Реализация
title: "Накладная № {{Номер}}"
header: "**Поставщик**: {{Поставщик}}"`)

	issues, warnings := CheckDir(dir)
	if len(issues) != 0 {
		t.Errorf("ожидалось 0 ошибок, получено %d: %+v", len(issues), issues)
	}
	if len(warnings) != 1 {
		t.Errorf("ожидалось 1 предупреждение, получено %d: %+v", len(warnings), warnings)
	}
	if len(warnings) > 0 && !strings.Contains(warnings[0].Message, "устаревший формат") {
		t.Errorf("неожиданное сообщение в предупреждении: %q", warnings[0].Message)
	}
	res := NewResult(issues, warnings)
	if !res.OK {
		t.Error("ожидался OK=true: legacy-форма — только предупреждение")
	}
	if len(res.Warnings) != 1 {
		t.Errorf("ожидался 1 warning в Result, получено %d", len(res.Warnings))
	}
	if len(res.Warnings) > 0 && res.Warnings[0].Code != "printform.legacy" {
		t.Errorf("ожидался code=printform.legacy, получено %+v", res.Warnings[0])
	}
}

// CheckDir должен валидировать журналы/роли/печатные формы пофайлово, с
// указанием конкретного файла (раньше эти типы проверял только project.Load,
// который падал на первой ошибке без локации).
func TestCheckDir_NewObjectTypes(t *testing.T) {
	dir := t.TempDir()
	// журнал: columns строками вместо структур {field: ...} — роняет парсинг
	mkFile(t, filepath.Join(dir, "journals", "j.yaml"), `name: J
documents: [Док]
columns:
  - Дата
  - Сумма`)
	// роль: documents списком вместо map — невалидный формат прав
	mkFile(t, filepath.Join(dir, "roles", "r.yaml"), `name: R
permissions:
  documents: [a, b, c]`)
	// печатная форма: выдуманный «layout:» — парсится, но форма пустая
	mkFile(t, filepath.Join(dir, "printforms", "p.yaml"), `name: P
document: Док
layout: |
  Область Шапка
    Поле Дата`)
	// корректная legacy печатная форма — валидна, но устарела: ожидаем
	// предупреждение о миграции (план 64, этап 4.6), не ошибку «пустая».
	mkFile(t, filepath.Join(dir, "printforms", "ok.yaml"), `name: OK
document: Док
title: OK
header: "**X**: {{X}}"`)

	issues, warnings := CheckDir(dir)

	// Корректная legacy-форма не должна давать ошибок — только предупреждение.
	for _, i := range issues {
		if i.File == "printforms/ok.yaml" {
			t.Errorf("корректная legacy-форма не должна быть в issues: %+v", i)
		}
	}

	// Предупреждение о миграции должно быть в warnings.
	var okMigrate bool
	for _, w := range warnings {
		if w.File == "printforms/ok.yaml" && strings.Contains(w.Message, "устаревший формат") {
			okMigrate = true
		}
	}
	if !okMigrate {
		t.Errorf("ожидалось предупреждение о миграции для printforms/ok.yaml. warnings=%+v", warnings)
	}

	res := NewResult(issues, warnings)
	if len(res.Warnings) == 0 {
		t.Error("ожидался хотя бы 1 warning в Result")
	}

	want := map[string]bool{"journals/j.yaml": false, "roles/r.yaml": false, "printforms/p.yaml": false}
	for _, i := range issues {
		if _, ok := want[i.File]; ok {
			want[i.File] = true
		}
	}
	for f, found := range want {
		if !found {
			t.Errorf("ожидалась ошибка для %s, не найдена. issues=%+v", f, issues)
		}
	}
}

func mkFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunFull_DSLPrintFormBinding: проверка конфигурации обязана ловить
// .os-печатную форму без привязки к сущности-источнику. Раньше DSL-формы не
// валидировались вовсе: форма с нераспознанным комментарием попадала в реестр
// под пустым ключом и не показывалась ни на одной карточке, а `onebase check`
// печатал «ошибок не найдено» — пользователь оставался без единого следа
// причины (discussion #757). Идём через RunFull — ровно ту функцию, которую
// зовут и CLI, и «проверить всё» в конфигураторе.
func TestRunFull_DSLPrintFormBinding(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Печать\n")
	mkFile(t, filepath.Join(dir, "catalogs", "Клиент.yaml"), `name: Клиент
fields:
  - name: Наименование
    type: string`)
	const body = "Функция Сформировать()\n    Возврат Новый ТабличныйДокумент\nКонецФункции\n"
	// Привязка к справочнику — корректна, ошибок быть не должно.
	mkFile(t, filepath.Join(dir, "printforms", "КарточкаКлиента.os"), "// Справочник: Клиент\n"+body)
	// Привязки нет вовсе — форма нигде не отрисуется.
	mkFile(t, filepath.Join(dir, "printforms", "БезПривязки.os"), body)
	// Привязка есть, но сущности с таким именем в конфигурации нет.
	mkFile(t, filepath.Join(dir, "printforms", "КривойИсточник.os"), "// Справочник: НетТакого\n"+body)

	res := RunFull(dir)

	byFile := map[string]Issue{}
	for _, i := range res.Issues {
		if strings.HasPrefix(i.File, "printforms/") {
			byFile[i.File] = i
		}
	}
	if i, ok := byFile["printforms/КарточкаКлиента.os"]; ok {
		t.Errorf("форма со справочником-источником не должна давать ошибок: %+v", i)
	}
	for _, f := range []string{"printforms/БезПривязки.os", "printforms/КривойИсточник.os"} {
		i, ok := byFile[f]
		if !ok {
			t.Errorf("ожидалась ошибка привязки для %s. issues=%+v", f, res.Issues)
			continue
		}
		if !strings.Contains(i.Message, "источник") {
			t.Errorf("%s: сообщение не про источник: %q", f, i.Message)
		}
		if i.Code != "printform.source-invalid" {
			t.Errorf("%s: ожидался code=printform.source-invalid, получено %q (подсказка про синтаксис .os тут не к месту)", f, i.Code)
		}
	}
}

// TestRunFull_DSLPrintFormBindingSubfolder: форма, разложенная по папке-
// сущности, привязывается именем папки — ошибки быть не должно, а в диагностике
// путь обязан включать папку.
func TestRunFull_DSLPrintFormBindingSubfolder(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Печать\n")
	mkFile(t, filepath.Join(dir, "catalogs", "Клиент.yaml"), `name: Клиент
fields:
  - name: Наименование
    type: string`)
	const body = "Функция Сформировать()\n    Возврат Новый ТабличныйДокумент\nКонецФункции\n"
	mkFile(t, filepath.Join(dir, "printforms", "Клиент", "Карточка.os"), body)
	mkFile(t, filepath.Join(dir, "printforms", "НетТакого", "Карточка.os"), body)

	res := RunFull(dir)

	var sawBad bool
	for _, i := range res.Issues {
		switch i.File {
		case "printforms/Клиент/Карточка.os":
			t.Errorf("форма в папке справочника не должна давать ошибок: %+v", i)
		case "printforms/НетТакого/Карточка.os":
			sawBad = true
		}
	}
	if !sawBad {
		t.Errorf("ожидалась ошибка для printforms/НетТакого/Карточка.os с путём вместе с папкой. issues=%+v", res.Issues)
	}
}
