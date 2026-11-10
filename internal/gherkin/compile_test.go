package gherkin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const demoRegistry = `шаги:
  - шаг: "заведён проект «<Проект>»"
    тип: действие
    код: |
      Пр = Справочники.Проект.Создать()
      Пр.Наименование = <Проект>
      ПроектСсылка = Пр.Записать()

  - шаг: "заведена функция с кодом «<Код>»"
    тип: действие
    код: |
      Ф = Справочники.Функция.Создать()
      Ф.Код = <Код>
      ФункцияСсылка = Ф.Записать()

  - шаг: "функция с кодом «<Код>» заведена"
    тип: проверка
    вычисление: |
      Пр = Новый Запрос
      Пр.Текст = "ВЫБРАТЬ ПЕРВЫЕ 1 Ф.Ссылка ИЗ Справочник.Функция КАК Ф ГДЕ Ф.Код = <Код>"
      ФункцияСсылка = Пр.Выполнить().ПолучитьЗначение(0, "Ссылка")
    выражение: ФункцияСсылка
    проверка: Заполнено

  - шаг: "функция объекта «<Имя>» имеет код «<Код>»"
    тип: проверка
    вычисление: |
      Пр = Новый Запрос
      Пр.Текст = "ВЫБРАТЬ ПЕРВЫЕ 1 О.Функция.Код КАК Код ИЗ Справочник.ОбъектМетаданных КАК О ГДЕ О.Имя = <Имя>"
      КодОбъекта = Пр.Выполнить().ПолучитьЗначение(0, "Код")
    выражение: КодОбъекта
    проверка: Равно
    ожидание: <Код>
`

func writeRegistry(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "steps.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRegistry_Match(t *testing.T) {
	reg, err := LoadRegistry(writeRegistry(t, demoRegistry))
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	def, vals := reg.Match("заведён проект «Целевой проект»")
	if def == nil {
		t.Fatalf("шаг не сопоставлен")
	}
	if vals["Проект"] != "Целевой проект" {
		t.Fatalf("захват параметра: %v", vals)
	}
	def, vals = reg.Match("функция объекта «internal/registry» имеет код «2.1»")
	if def == nil {
		t.Fatalf("шаг с двумя параметрами не сопоставлен")
	}
	if vals["Имя"] != "internal/registry" || vals["Код"] != "2.1" {
		t.Fatalf("захват двух параметров: %v", vals)
	}
	if def, _ := reg.Match("чужой шаг"); def != nil {
		t.Fatalf("несопоставленный шаг дал определение %+v", def)
	}
}

func TestLoadRegistry_Errors(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"шаги: []", "пуст"},
		{"шаги:\n  - тип: действие\n    код: 'x'\n", "без «шаг»"},
		{"шаги:\n  - шаг: \"a\"\n    тип: прочее\n", "тип"},
		{"шаги:\n  - шаг: \"a\"\n    тип: действие\n", "без кода"},
		{"шаги:\n  - шаг: \"a\"\n    тип: проверка\n    проверка: Равно\n", "без «выражение»"},
		{"шаги:\n  - шаг: \"a\"\n    тип: проверка\n    выражение: x\n    проверка: Сравнение\n", "неизвестная проверка"},
		{"шаги:\n  - шаг: \"a <B>\"\n    тип: проверка\n    выражение: x\n    проверка: Истина\n    ожидание: y\n", "не принимает «ожидание»"},
		{"шаги:\n  - шаг: \"a\"\n    тип: проверка\n    выражение: x\n    проверка: Равно\n", "без «ожидание»"},
	}
	for _, c := range cases {
		_, err := LoadRegistry(writeRegistry(t, c.body))
		if err == nil {
			t.Fatalf("ожидалась ошибка %q, файл принят: %s", c.want, c.body)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("ожидалось %q, получено %v", c.want, err)
		}
	}
}

func TestCompile_GeneratesTestPair(t *testing.T) {
	reg, err := LoadRegistry(writeRegistry(t, demoRegistry))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse([]byte(`Функция: Расстановка функций
Сценарий: объект закрепляется за узлом
  Дано заведена функция с кодом «2.1»
  Тогда функция с кодом «2.1» заведена
`), "mapping.feature")
	if err != nil {
		t.Fatal(err)
	}
	tests, err := Compile(f, reg, "mapping.feature")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(tests) != 1 {
		t.Fatalf("тестов: %d", len(tests))
	}
	tr := tests[0]
	if tr.Name != "ТестРасстановкаФункций_Объект" {
		t.Fatalf("имя теста: %q", tr.Name)
	}
	if !strings.Contains(tr.YAML, "kind: test") || !strings.Contains(tr.YAML, `name: "`+tr.Name+`"`) {
		t.Fatalf("YAML: %s", tr.YAML)
	}
	if !strings.Contains(tr.Source, "Ф.Код = \"2.1\"") {
		t.Fatalf("DSL действия потерян: %s", tr.Source)
	}
	if !strings.Contains(tr.Source, `Утверждать.Заполнено(ФункцияСсылка, "функция с кодом «2.1» заведена")`) {
		t.Fatalf("проверка не собрана: %s", tr.Source)
	}
	if !strings.Contains(tr.Source, "// Шаг-таблица:") {
		t.Fatalf("шаг-таблица не напечатана: %s", tr.Source)
	}
	if !strings.Contains(tr.Source, "//   Дано заведена функция с кодом «2.1»") {
		t.Fatalf("шаг не пошёл в шаг-таблицу: %s", tr.Source)
	}
}

func TestCompile_OutlineExpandsRows(t *testing.T) {
	reg, err := LoadRegistry(writeRegistry(t, demoRegistry))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse([]byte(`Функция: Расстановка функций
Структура сценария: узлы дерева
  Дано заведена функция с кодом «<Код>»
  Тогда функция с кодом «<Код>» заведена
Примеры:
  | Код |
  | 2.1 |
  | 2.2 |
`), "o.feature")
	if err != nil {
		t.Fatal(err)
	}
	tests, err := Compile(f, reg, "o.feature")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("тестов из строк примеров: %d", len(tests))
	}
	if !strings.Contains(tests[1].Source, `Ф.Код = "2.2"`) {
		t.Fatalf("строка примера не подставлена: %s", tests[1].Source)
	}
	if tests[0].Name == tests[1].Name {
		t.Fatalf("имена тестов совпали: %s", tests[0].Name)
	}
}

func TestCompile_UnmatchedStepFails(t *testing.T) {
	reg, err := LoadRegistry(writeRegistry(t, demoRegistry))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse([]byte(`Функция: Ф
Сценарий: С
  Дано заведён проект «П»
  Тогда шаг без реестра
`), "x.feature")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(f, reg, "x.feature")
	if err == nil {
		t.Fatalf("несопоставленный шаг компиляция приняла")
	}
	if !strings.Contains(err.Error(), "шаг «То шаг без реестра» не сопоставлен") {
		t.Fatalf("сообщение: %v", err)
	}
}

func TestCompile_NoChecksFails(t *testing.T) {
	reg, err := LoadRegistry(writeRegistry(t, demoRegistry))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse([]byte(`Функция: Ф
Сценарий: С
  Дано заведён проект «П»
  Когда заведена функция с кодом «2.1»
`), "x.feature")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Compile(f, reg, "x.feature")
	if err == nil || !strings.Contains(err.Error(), "не содержит шагов-проверок") {
		t.Fatalf("тест без проверок принят: %v", err)
	}
}

func TestCompile_BackgroundGoesIntoEveryTest(t *testing.T) {
	reg, err := LoadRegistry(writeRegistry(t, demoRegistry))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse([]byte(`Функция: Ф
Предыстория:
  Дано заведён проект «П»
Сценарий: С1
  Тогда функция с кодом «2.1» заведена
Сценарий: С2
  Тогда функция с кодом «2.2» заведена
`), "b.feature")
	if err != nil {
		t.Fatal(err)
	}
	tests, err := Compile(f, reg, "b.feature")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, tr := range tests {
		if !strings.Contains(tr.Source, `Пр.Наименование = "П"`) {
			t.Fatalf("предыстория не вошла в %s: %s", tr.Name, tr.Source)
		}
	}
}
