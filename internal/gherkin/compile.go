package gherkin

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// CompiledStep — шаг сценария вместе с DSL, который он даёт.
type CompiledStep struct {
	Keyword string
	Text    string
	Lines   []string
	IsCheck bool
}

// CompiledTest — тест-обработка kind: test, собранная из одного сценария.
type CompiledTest struct {
	Name         string // имя обработки (и имя пары YAML/модуля)
	Title        string // заголовок обработки
	FeatureName  string
	ScenarioName string
	Steps        []CompiledStep
	Source       string // исходник модуля .proc.os
	YAML         string // содержимое processors/<имя>.yaml
}

// Compile переводит разобранный файл Gherkin и реестр шагов в набор
// тест-обработок. Возвращает ошибку со списком несопоставленных шагов:
// компиляция не пишет половину теста, молча пропуская шаги.
func Compile(f *Feature, reg *Registry, featureFile string) ([]CompiledTest, error) {
	scenarios := []Scenario{}
	for _, s := range f.Scenarios {
		scenarios = append(scenarios, s)
	}
	for _, o := range f.Outlines {
		for _, row := range o.Rows {
			steps := make([]Step, 0, len(o.Steps))
			for _, st := range o.Steps {
				text, err := expandPlaceholders(st.Text, o.Columns, row)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", featureFile, err)
				}
				steps = append(steps, Step{Keyword: st.Keyword, Text: text, Line: st.Line})
			}
			scenarios = append(scenarios, Scenario{
				Name:  o.Name + ": " + strings.Join(row, " / "),
				Tags:  o.Tags,
				Steps: steps,
				Row:   row,
			})
		}
	}

	var tests []CompiledTest
	var problems []string
	seen := map[string]int{}
	for _, sc := range scenarios {
		compiled := []CompiledStep{}
		for _, st := range f.Background {
			c, err := compileStep(st, reg)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			compiled = append(compiled, c)
		}
		for _, st := range sc.Steps {
			c, err := compileStep(st, reg)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			compiled = append(compiled, c)
		}
		checks := 0
		for _, c := range compiled {
			if c.IsCheck {
				checks++
			}
		}
		if len(problems) > 0 {
			continue
		}
		if checks == 0 {
			problems = append(problems, fmt.Sprintf("%s: сценарий «%s» не содержит шагов-проверок — тест без проверок раннер считает провалом", featureFile, sc.Name))
			continue
		}
		// имя теста: Тест + функция + сценарий + строка примера, по рунам,
		// иначе сценарий с длинным названием даёт нечитаемое имя файла
		name := "Тест" + slug(f.Name, 18) + "_" + slug(sc.Name, 16)
		if len(sc.Row) > 0 {
			name += "_" + slug(strings.Join(sc.Row, " "), 12)
		}
		if n := seen[name]; n > 0 {
			name = fmt.Sprintf("%s_%d", name, n+1)
		}
		seen[name]++
		t := CompiledTest{
			Name:         name,
			Title:        f.Name + " — " + sc.Name,
			FeatureName:  f.Name,
			ScenarioName: sc.Name,
			Steps:        compiled,
		}
		t.Source = renderSource(f, sc, featureFile, compiled)
		t.YAML = renderYAML(t)
		tests = append(tests, t)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("gherkin: %d шагов не сопоставлены с реестром\n%s", len(problems), strings.Join(problems, "\n"))
	}
	return tests, nil
}

// compileStep связывает шаг с реестром и строит DSL-строки.
func compileStep(st Step, reg *Registry) (CompiledStep, error) {
	def, values := reg.Match(st.Text)
	if def == nil {
		return CompiledStep{}, fmt.Errorf("строка %d: шаг «%s %s» не сопоставлен с реестром шагов", st.Line, st.Keyword, st.Text)
	}
	out := CompiledStep{Keyword: st.Keyword, Text: st.Text}
	if strings.EqualFold(def.Kind, "действие") {
		lines, err := substituteBlock(def.Code, values)
		if err != nil {
			return out, fmt.Errorf("строка %d: шаг «%s»: %w", st.Line, st.Text, err)
		}
		out.Lines = lines
		return out, nil
	}
	if strings.TrimSpace(def.Compute) != "" {
		lines, err := substituteBlock(def.Compute, values)
		if err != nil {
			return out, fmt.Errorf("строка %d: шаг «%s»: %w", st.Line, st.Text, err)
		}
		out.Lines = append(out.Lines, lines...)
	}
	expr, err := substitute(def.Expr, values)
	if err != nil {
		return out, fmt.Errorf("строка %d: шаг «%s»: выражение: %w", st.Line, st.Text, err)
	}
	label := st.Text
	if strings.TrimSpace(def.Label) != "" {
		label, err = substitute(def.Label, values)
		if err != nil {
			return out, fmt.Errorf("строка %d: шаг «%s»: имя: %w", st.Line, st.Text, err)
		}
	}
	a := strings.TrimSpace(def.Assert)
	var call string
	switch a {
	case "Равно", "НеРавно":
		expect, err := substitute(def.Expect, values)
		if err != nil {
			return out, fmt.Errorf("строка %d: шаг «%s»: ожидание: %w", st.Line, st.Text, err)
		}
		call = fmt.Sprintf("Утверждать.%s(%s, %s, \"%s\")", a, expr, expect, escapeDSL(label))
	case "Истина", "Ложь", "Заполнено":
		call = fmt.Sprintf("Утверждать.%s(%s, \"%s\")", a, expr, escapeDSL(label))
	case "Провалить":
		call = fmt.Sprintf("Утверждать.Провалить(\"%s\")", escapeDSL(label))
	default:
		return out, fmt.Errorf("строка %d: шаг «%s»: неизвестная проверка %q", st.Line, st.Text, def.Assert)
	}
	out.Lines = append(out.Lines, call)
	out.IsCheck = true
	return out, nil
}

// substitute подставляет значения параметров шага в шаблон DSL. литерал
// зависит от места: внутри строкового литерала DSL (текст запроса) значение
// идёт SQL-литералом в одинарных кавычках, вне литерала — строкой DSL в
// двойных. Двойные кавычки внутри DSL-строки закрывают строку, поэтому
// текст запроса подставляет одинарные; вне строки число остаётся числом.
func substitute(tmpl string, values map[string]string) (string, error) {
	var sb strings.Builder
	inLit := false
	i := 0
	for i < len(tmpl) {
		c := tmpl[i]
		if c == '"' {
			if inLit && i+1 < len(tmpl) && tmpl[i+1] == '"' {
				sb.WriteString(`""`)
				i += 2
				continue
			}
			inLit = !inLit
			sb.WriteByte(c)
			i++
			continue
		}
		if c == '<' {
			if name, ok := placeholder(tmpl[i:]); ok {
				v, found := values[name]
				if !found {
					return "", fmt.Errorf("шаблон ссылается на <%s>, которого нет в шаге", name)
				}
				sb.WriteString(literalFor(v, inLit))
				i += len(name) + 2
				continue
			}
		}
		sb.WriteByte(c)
		i++
	}
	return sb.String(), nil
}

// placeholder читает <Имя> в начале строки: параметр — кириллица, латиница,
// цифры и подчёркивание; иначе это не параметр, а сравнение «меньше».
func placeholder(s string) (string, bool) {
	r := []rune(s)
	if len(r) < 2 || r[0] != '<' {
		return "", false
	}
	for j := 1; j < len(r); j++ {
		if r[j] == '>' {
			if j > 1 {
				return string(r[1:j]), true
			}
			return "", false
		}
		if !isNameRune(r[j]) {
			return "", false
		}
	}
	return "", false
}

func isNameRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' ||
		(r >= '\u0410' && r <= '\u042f') || (r >= '\u0430' && r <= '\u044f') || r == '\u0401'
}

func literalFor(v string, inSQLLiteral bool) string {
	if inSQLLiteral {
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	if isNumber(v) {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
}

func substituteBlock(block string, values map[string]string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		s, err := substitute(line, values)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.TrimRight(s, " \t"))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("пустой блок DSL")
	}
	return out, nil
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func escapeDSL(s string) string { return strings.ReplaceAll(s, `"`, `""`) }

// renderSource печатает модуль обработки: шаг-таблица комментарием, затем
// DSL шагов внутри процедуры Выполнить().
func renderSource(f *Feature, sc Scenario, featureFile string, steps []CompiledStep) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "// Тест скомпилирован onebase gherkin compile — правка руками рвёт связь с %s.\n", featureFile)
	fmt.Fprintf(&sb, "// Функция: %s\n// Сценарий: %s\n// Шаг-таблица:\n", f.Name, sc.Name)
	for _, st := range steps {
		fmt.Fprintf(&sb, "//   %s %s\n", st.Keyword, st.Text)
	}
	sb.WriteString("\nПроцедура Выполнить()\n")
	for _, st := range steps {
		fmt.Fprintf(&sb, "    // %s %s\n", st.Keyword, st.Text)
		for _, line := range st.Lines {
			fmt.Fprintf(&sb, "    %s\n", line)
		}
	}
	sb.WriteString("КонецПроцедуры\n")
	return sb.String()
}

func renderYAML(t CompiledTest) string {
	// title котируется: название сценария содержит «: » (строки примеров),
	// и без кавычек YAML читает его как вложенную пару ключ-значение.
	return fmt.Sprintf("kind: test\n# Скомпилировано из сценария Gherkin «%s» (onebase gherkin compile).\nname: %s\ntitle: %s\n",
		t.ScenarioName, strconv.Quote(t.Name), strconv.Quote(t.Title))
}

// slug строит имя обработки из текста: слова с заглавной буквы, слитно,
// не длиннее лимита. Ограничение нужно потому, что имя обработки становится
// именем файла, и сценарий с длинным названием дал бы нечитаемый дифф.
func slug(text string, limit int) string {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var sb strings.Builder
	n := 0 // счёт в рунах: кириллица в байтах вдвое длиннее, имя режут по буквам
	for _, w := range words {
		r := []rune(w)
		if len(r) == 0 {
			continue
		}
		if n+len(r) > limit {
			break
		}
		n += len(r)
		sb.WriteString(strings.ToUpper(string(r[0])))
		sb.WriteString(string(r[1:]))
	}
	if sb.Len() == 0 {
		return "Тест"
	}
	return sb.String()
}

func expandPlaceholders(text string, columns []string, row []string) (string, error) {
	out := text
	for i, col := range columns {
		out = strings.ReplaceAll(out, "<"+col+">", row[i])
	}
	if strings.ContainsRune(out, '<') {
		return "", fmt.Errorf("шаг «%s» содержит <параметр>, которого нет среди столбцов Примеров", text)
	}
	return out, nil
}
