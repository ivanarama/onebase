package gherkin

import (
	"strings"
	"testing"
)

const demoFeature = `@каркас
Функция: Расстановка функций целевого проекта
  Импорт описания объекты не закрепляет.

  Предыстория:
    Дано заведён проект «Целевой проект»

  Сценарий: объект закрепляется за узлом декомпозиции
    Дано заведена функция с кодом «2.1»
    Когда объект «internal/registry» закреплён за функцией с кодом «2.1»
    Тогда функция объекта «internal/registry» имеет код «2.1»
    И трассировка содержит запись для объекта «internal/registry»

  Структура сценария: узлы дерева функций
    Дано заведена функция с кодом «<Код>»
    Тогда функция с кодом «<Код>» заведена
    Примеры:
      | Код |
      | 2.1 |
      | 2.2 |
`

func TestParse_RussianKeywordsAndOutlines(t *testing.T) {
	f, err := Parse([]byte(demoFeature), "demo.feature")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Name != "Расстановка функций целевого проекта" {
		t.Fatalf("имя функции: %q", f.Name)
	}
	if len(f.Tags) != 1 || f.Tags[0] != "каркас" {
		t.Fatalf("теги: %v", f.Tags)
	}
	if len(f.Desc) != 1 {
		t.Fatalf("описание функции: %v", f.Desc)
	}
	if len(f.Background) != 1 || f.Background[0].Keyword != "Дано" {
		t.Fatalf("предыстория: %+v", f.Background)
	}
	if len(f.Scenarios) != 1 {
		t.Fatalf("сценариев: %d", len(f.Scenarios))
	}
	sc := f.Scenarios[0]
	if len(sc.Steps) != 4 {
		t.Fatalf("шагов сценария: %d", len(sc.Steps))
	}
	if sc.Steps[3].Keyword != "И" {
		t.Fatalf("нормализация «И также»: %q", sc.Steps[3].Keyword)
	}
	if len(f.Outlines) != 1 {
		t.Fatalf("структур сценария: %d", len(f.Outlines))
	}
	o := f.Outlines[0]
	if len(o.Columns) != 1 || o.Columns[0] != "Код" {
		t.Fatalf("столбцы примеров: %v", o.Columns)
	}
	if len(o.Rows) != 2 {
		t.Fatalf("строк примеров: %d", len(o.Rows))
	}
}

func TestParse_AnGLKeywords(t *testing.T) {
	src := "Feature: Mapping\n  Scenario: one\n    Given project\n    When x\n    Then y\n    And z\n"
	f, err := Parse([]byte(src), "en.feature")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Scenarios) != 1 || len(f.Scenarios[0].Steps) != 4 {
		t.Fatalf("английские ключевые слова: %+v", f.Scenarios)
	}
	if f.Scenarios[0].Steps[0].Keyword != "Дано" {
		t.Fatalf("Given → Дано, получено %q", f.Scenarios[0].Steps[0].Keyword)
	}
}

func TestParse_TableAndDocstring(t *testing.T) {
	src := "Функция: Ф\nСценарий: С\n  Дано список\n    | Имя |\n    | a |\n    | b |\n  То текст\n    \"\"\"\n    строка\n    \"\"\"\n"
	f, err := Parse([]byte(src), "t.feature")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	st := f.Scenarios[0].Steps
	if len(st[0].Table) != 3 {
		t.Fatalf("таблица шага: %v", st[0].Table)
	}
	if len(st[1].Doc) != 1 || st[1].Doc[0] != "строка" {
		t.Fatalf("docstring: %v", st[1].Doc)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"Сценарий: С\n  Дано a\n", "нет Функции"},
		{"Функция: Ф\n", "нет сценариев"},
		{"Функция: Ф\nСценарий: С\n  Простой текст\n", "не шаг и не раздел"},
		{"Функция: Ф\nСтруктура сценария: О\n  Дано <X>\nПримеры:\n  | Y |\n  | 1 |\n", "которого нет среди примеров"},
		{"Функция: Ф\nСценарий: С\n  Дано a\nПримеры:\n  | X |\n", "без Структуры сценария"},
		{"Функция: Ф\nСценарий: С\n  Дано a\n    \"\"\"\n  незакрытый\n", "незакрытый docstring"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.src), "x.feature")
		if err == nil {
			t.Fatalf("ожидалась ошибка %q,Parse молча приняла %q", c.want, c.src)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("ожидалось %q, получено %v", c.want, err)
		}
	}
}
