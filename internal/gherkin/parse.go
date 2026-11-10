// Package gherkin компилирует тестовые сценарии Gherkin в тест-обработки
// конфигурации (kind: test) — срез C плана 218.
//
// Разбор_line-based: ключевые слова Gherkin (английские и русские), теги,
// предыстория, сценарии, структура сценария с примерами, табличные строки и
// docstring шага. Компиляция связывает шаги с реестром шагов проекта и
// печатает исходник модуля обработки.
package gherkin

import (
	"fmt"
	"strings"
)

// Шаг — одна строка Gherkin с опциональной таблицей и docstring.
type Step struct {
	Keyword  string // нормализованное ключевое слово: Дано, Когда, То, И, Но
	Text     string
	Line     int
	Doc      []string // docstring (""")
	Table    [][]string
}

// Сценарий — последовательность шагов с именем и тегами.
type Scenario struct {
	Name  string
	Tags  []string
	Steps []Step
	// Row — значения столбцов примеров строки Структуры сценария; для обычного
	// сценария пусто. Имя теста строит суффикс из строки, чтобы строки
	// примеров не получили один и тот же тест с суффиксом _2.
	Row []string
}

// Outline — сценарий с параметрами и строками примеров.
type Outline struct {
	Name    string
	Tags    []string
	Steps   []Step
	Columns []string
	Rows    [][]string
}

// Feature — файл сценариев (Gherkin Feature).
type Feature struct {
	Name       string
	Desc       []string
	Tags       []string
	Background []Step
	Scenarios  []Scenario
	Outlines   []Outline
}

// Ключевые слова шагов: английские и русские варианты → нормализованное слово.
var stepKeywords = map[string]string{
	"given": "Дано", "дано": "Дано", "учитывая": "Дано", "если": "Дано",
	"when": "Когда", "когда": "Когда",
	"then": "То", "то": "То", "тогда": "То",
	"and": "И", "и": "И", "также": "И",
	"but": "Но", "но": "Но",
	"*": "Шаг",
}

var (
	featureKeywords    = []string{"feature", "функция"}
	backgroundKeywords = []string{"background", "предыстория", "предваряющие условия"}
	scenarioKeywords   = []string{"scenario", "сценарий"}
	outlineKeywords    = []string{"scenario outline", "структура сценария", "сценарий-контекст"}
	examplesKeywords   = []string{"examples", "примеры"}
	ruleKeywords       = []string{"rule", "правило"}
)

func matchKeyword(line string, words []string) (tail string, ok bool) {
	low := strings.ToLower(line)
	for _, w := range words {
		if !strings.HasPrefix(low, w) {
			continue
		}
		rest := line[len(w):]
		if strings.HasPrefix(rest, ":") {
			return strings.TrimPrefix(rest, ":"), true
		}
		if strings.HasPrefix(rest, " ") {
			return rest, true
		}
	}
	return "", false
}

// Parse разбирает текст .feature-файла. Имя файла нужно для сообщений об
// ошибках: шаг без имени файла неотличим от соседнего шага.
func Parse(data []byte, filename string) (*Feature, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	f := &Feature{}
	var pendingTags []string
	var section string // feature | background | scenario | outline | examples
	curScenario := -1
	curOutline := -1

	setErr := func(format string, args ...any) error {
		return fmt.Errorf("%s: "+format, append([]any{filename}, args...)...)
	}

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			for _, t := range strings.Fields(line) {
				pendingTags = append(pendingTags, strings.TrimLeft(t, "@"))
			}
			continue
		}

		// Docstring шага: """ … """
		if line == `"""` {
			if section != "scenario" && section != "outline" {
				return nil, setErr("docstring на строке %d вне сценария", i+1)
			}
			var steps *[]Step
			if section == "scenario" {
				steps = &f.Scenarios[curScenario].Steps
			} else {
				steps = &f.Outlines[curOutline].Steps
			}
			if len(*steps) == 0 {
				return nil, setErr("docstring на строке %d без шага", i+1)
			}
			last := &(*steps)[len(*steps)-1]
			if len(last.Doc) > 0 {
				return nil, setErr("docstring на строке %d закрыт раньше строки %d", i+1, last.Line)
			}
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) != `"""` {
				last.Doc = append(last.Doc, strings.TrimSpace(lines[j]))
				j++
			}
			if j == len(lines) {
				return nil, setErr("незакрытый docstring на строке %d", i+1)
			}
			i = j
			continue
		}

		// Табличная строка: | … |
		if strings.HasPrefix(line, "|") {
			cells := splitRow(line)
			switch {
			case section == "examples":
				if len(f.Outlines[curOutline].Columns) == 0 {
					f.Outlines[curOutline].Columns = cells
				} else {
					f.Outlines[curOutline].Rows = append(f.Outlines[curOutline].Rows, cells)
				}
			case section == "scenario" && len(f.Scenarios[curScenario].Steps) > 0:
				last := &f.Scenarios[curScenario].Steps[len(f.Scenarios[curScenario].Steps)-1]
				last.Table = append(last.Table, cells)
			case section == "outline" && len(f.Outlines[curOutline].Steps) > 0:
				last := &f.Outlines[curOutline].Steps[len(f.Outlines[curOutline].Steps)-1]
				last.Table = append(last.Table, cells)
			default:
				return nil, setErr("табличная строка %d без шага или Примеров", i+1)
			}
			continue
		}

		// Ключевое слово раздела
		if tail, ok := matchKeyword(line, featureKeywords); ok {
			if f.Name != "" {
				return nil, setErr("вторая Функция на строке %d", i+1)
			}
			f.Name = strings.TrimSpace(tail)
			f.Tags = pendingTags
			pendingTags = nil
			section = "feature"
			continue
		}
		if tail, ok := matchKeyword(line, ruleKeywords); ok {
			// Правило — группировка сценариев; имя правило не исполняет.
			_ = strings.TrimSpace(tail)
			section = "rule"
			continue
		}
		if tail, ok := matchKeyword(line, backgroundKeywords); ok {
			_ = strings.TrimSpace(tail)
			section = "background"
			pendingTags = nil
			continue
		}
		if tail, ok := matchKeyword(line, scenarioKeywords); ok {
			f.Scenarios = append(f.Scenarios, Scenario{Name: strings.TrimSpace(tail), Tags: pendingTags})
			pendingTags = nil
			curScenario = len(f.Scenarios) - 1
			section = "scenario"
			continue
		}
		if tail, ok := matchKeyword(line, outlineKeywords); ok {
			f.Outlines = append(f.Outlines, Outline{Name: strings.TrimSpace(tail), Tags: pendingTags})
			pendingTags = nil
			curOutline = len(f.Outlines) - 1
			section = "outline"
			continue
		}
		if tail, ok := matchKeyword(line, examplesKeywords); ok {
			_ = strings.TrimSpace(tail)
			if section != "outline" {
				return nil, setErr("Примеры на строке %d без Структуры сценария", i+1)
			}
			section = "examples"
			continue
		}

		// Шаг
		kw, ok := stepKeywords[strings.ToLower(firstWord(line))]
		if !ok {
			if section == "feature" {
				f.Desc = append(f.Desc, line)
				continue
			}
			return nil, setErr("строка %d не шаг и не раздел: %q", i+1, line)
		}
		text := strings.TrimSpace(line[len(firstWord(line)):])
		if text == "" {
			return nil, setErr("пустой шаг на строке %d", i+1)
		}
		step := Step{Keyword: kw, Text: text, Line: i + 1}
		switch section {
		case "background":
			f.Background = append(f.Background, step)
		case "scenario":
			f.Scenarios[curScenario].Steps = append(f.Scenarios[curScenario].Steps, step)
		case "outline":
			f.Outlines[curOutline].Steps = append(f.Outlines[curOutline].Steps, step)
		default:
			return nil, setErr("шаг «%s» на строке %d вне сценария", text, i+1)
		}
	}

	if f.Name == "" {
		return nil, setErr("нет Функции (Feature:) — файл не разобран")
	}
	if len(f.Scenarios) == 0 && len(f.Outlines) == 0 {
		return nil, setErr("нет сценариев в «%s»", f.Name)
	}
	for _, o := range f.Outlines {
		if len(o.Columns) == 0 {
			return nil, setErr("структура сценария «%s» без Примеров", o.Name)
		}
		for _, r := range o.Rows {
			if len(r) != len(o.Columns) {
				return nil, setErr("структура «%s»: строка примера из %d значений против %d столбцов", o.Name, len(r), len(o.Columns))
			}
		}
		for _, s := range o.Steps {
			for _, p := range placeholders(s.Text) {
				if !contains(o.Columns, p) {
					return nil, setErr("структура «%s»: шаг «%s» ссылается на <«%s»», которого нет среди примеров", o.Name, s.Text, p)
				}
			}
		}
	}
	return f, nil
}

func firstWord(s string) string {
	i := 0
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}
	return s[:i]
}

func splitRow(line string) []string {
	cells := strings.Split(strings.Trim(line, "|"), "|")
	out := make([]string, 0, len(cells))
	for _, c := range cells {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

// placeholders — имена <параметр> в тексте шага структуры сценария.
func placeholders(text string) []string {
	var out []string
	for i := 0; i < len(text); {
		if text[i] != '<' {
			i++
			continue
		}
		j := strings.Index(text[i:], ">")
		if j < 0 {
			break
		}
		name := text[i+1 : i+j]
		if name != "" && !contains(out, name) {
			out = append(out, name)
		}
		i += j + 1
	}
	return out
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
