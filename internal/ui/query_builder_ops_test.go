package ui

import (
	"os"
	"strings"
	"testing"
)

// Конструктор отбора согласован сам с собой: каждый оператор из выпадающего
// списка обязан узнаваться обратным разбором текста запроса, иначе разобранное
// условие потерялось бы при повторной генерации (#1542). Разметка конструктора
// живёт JS-шаблоном, исполняемого теста у неё нет, поэтому сверяем исходник:
// список операторов qbAddCond против регулярки разбора.
func TestQueryBuilderEveryOpIsParseable(t *testing.T) {
	raw, err := os.ReadFile("tpl_dev_tools.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	var opsLine, parseLine string
	for _, ln := range strings.Split(text, "\n") {
		if strings.Contains(ln, "].forEach(function(op){") {
			opsLine = ln
		}
		if strings.Contains(ln, "var opM = cp.match(") {
			parseLine = ln
		}
	}
	if opsLine == "" || parseLine == "" {
		t.Fatal("не нашёл в шаблоне список операторов или регулярку разбора")
	}

	ops := regexpExtractQuoted(opsLine)
	if len(ops) == 0 {
		t.Fatalf("не разобрал список операторов: %s", opsLine)
	}
	for _, op := range ops {
		if !hasLetter(op) {
			continue // символьные операторы в регулярке экранированы по-своему
		}
		pattern := strings.ReplaceAll(op, " ", `\s+`)
		if !strings.Contains(parseLine, pattern) {
			t.Errorf("оператор %q есть в списке конструктора, но не узнётся при разборе ГДЕ", op)
		}
	}

	// Синоним порядка слов тоже должен приводиться к варианту из списка:
	// иначе разбор его распознает, а повторная генерация молча потеряет.
	if !strings.Contains(text, "'ЕСТЬ НЕ ПУСТО'") || !strings.Contains(parseLine, `ЕСТЬ\s+НЕ\s+ПУСТО`) {
		t.Error("разбор не согласован с генерацией для синонима «ЕСТЬ НЕ ПУСТО»")
	}
}

func hasLetter(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 'а' && r <= 'я' || r >= 'А' && r <= 'Я' || r == 'ё' || r == 'Ё' {
			return true
		}
	}
	return false
}

func regexpExtractQuoted(line string) []string {
	var out []string
	for {
		i := strings.IndexByte(line, '\'')
		if i < 0 {
			return out
		}
		line = line[i+1:]
		j := strings.IndexByte(line, '\'')
		if j < 0 {
			return out
		}
		out = append(out, line[:j])
		line = line[j+1:]
	}
}
