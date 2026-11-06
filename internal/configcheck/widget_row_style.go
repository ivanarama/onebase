package configcheck

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckWidgetRowStyle проверяет row_style list-виджета: только для type: list,
// хотя бы одна колонка, и каждая объявленная колонка встречается в тексте
// запроса отдельным словом (как поле или алиас КАК). Выходные имена колонок
// компилятор заранее не сообщает, а в рантайме отсутствующая колонка просто
// не даёт оформления — опечатка осталась бы незамеченной.
func CheckWidgetRowStyle(proj *project.Project) []Issue {
	var issues []Issue
	for _, w := range proj.Widgets {
		if w == nil || w.RowStyle == nil {
			continue
		}
		add := func(msg string) {
			issues = append(issues, Issue{
				File:    "widgets/" + w.Name + ".yaml",
				Object:  w.Name,
				Kind:    "Виджет (row_style)",
				Code:    "widget.row-style",
				Message: msg,
			})
		}
		if w.Type != metadata.WidgetTypeList {
			add("row_style поддерживается только для виджетов типа list")
			continue
		}
		bg, bold := strings.TrimSpace(w.RowStyle.Background), strings.TrimSpace(w.RowStyle.Bold)
		if bg == "" && bold == "" {
			add("row_style требует колонку background и/или bold")
			continue
		}
		for _, col := range []struct{ key, name string }{{"background", bg}, {"bold", bold}} {
			if col.name != "" && !queryHasWord(w.Query, col.name) {
				add(fmt.Sprintf("row_style.%s: колонки %q нет в запросе виджета", col.key, col.name))
			}
		}
	}
	return issues
}

// queryHasWord — name встречается в тексте запроса отдельным словом (без учёта
// регистра; границы слова — по Unicode, а не ASCII-\b регулярных выражений).
func queryHasWord(text, name string) bool {
	low, needle := strings.ToLower(text), strings.ToLower(name)
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	for i := 0; ; {
		j := strings.Index(low[i:], needle)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(needle)
		before, _ := utf8.DecodeLastRuneInString(low[:start])
		after, _ := utf8.DecodeRuneInString(low[end:])
		if (start == 0 || !isWord(before)) && (end == len(low) || !isWord(after)) {
			return true
		}
		i = start + 1
	}
}
