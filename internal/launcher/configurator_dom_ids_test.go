package launcher

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Дубликат DOM-id стоил тихой потери данных: id="cc-<отчёт>" был объявлен и у
// таблицы «Колонки (кросс-таблица)», и у таблицы условного оформления, поэтому
// getElementById возвращал первую. Новое правило оформления добавлялось в
// таблицу колонок на соседней вкладке и при сохранении затирало первое
// существующее правило, а перенумерация после удаления не работала вовсе
// (issue #678).
//
// Проверка общая, а не про конкретный id: уникальность идентификаторов —
// свойство всей страницы, и следующий такой дубликат должен валить тест сам.

// Пробел перед id обязателен: без него регулярка ловила бы и data-id.
var domIDRe = regexp.MustCompile(`\sid="([^"]+)"`)

func TestConfigurator_NoDuplicateDOMIDs(t *testing.T) {
	html := renderTabTree(t)
	count := map[string]int{}
	for _, m := range domIDRe.FindAllStringSubmatch(html, -1) {
		count[m[1]]++
	}
	if len(count) == 0 {
		t.Fatal("в отрендеренной странице нет ни одного id — сломан рендер или регулярка")
	}
	var dups []string
	for id, n := range count {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	if len(dups) > 0 {
		t.Fatalf("повторяющиеся DOM-id (%d): %s\n\n"+
			"getElementById вернёт первый элемент, поэтому JS будет править не ту таблицу.",
			len(dups), strings.Join(dups, ", "))
	}
}
