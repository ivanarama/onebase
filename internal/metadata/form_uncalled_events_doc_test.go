package metadata

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Перечень невызываемых событий в `docs/forms.md` выписан руками, а тот же
// перечень платформа вычисляет как «известные минус диспетчеризуемые»
// (issue #1355). Сегодня списки совпадают, но сверки не было ни одной: в день,
// когда событие реализуют, `UncalledFormEvents()` перестанет его отдавать, а
// руководство продолжит утверждать, что событие не вызывается.
//
// Это ровно тот отказ, против которого аргументирует комментарий в
// `form_uncalled_events.go`: перечень намеренно не хранится вторым рукописным
// списком, «который забудут вычеркнуть». В документации такой список как раз
// лежал — теперь он обрамлён маркерами и сверяется отсюда.
//
// Тест живёт рядом с источником правды намеренно: тот, кто добавит диспетчер
// события, запустит тесты этого пакета и сразу увидит, что документацию надо
// поправить, — а не узнает об этом от пользователя через полгода.

const (
	uncalledDocBegin = "<!-- ob:uncalled-form-events:begin -->"
	uncalledDocEnd   = "<!-- ob:uncalled-form-events:end -->"
)

var docEventName = regexp.MustCompile("`([^`]+)`")

// uncalledEventsFromDoc достаёт имена событий из блока между маркерами.
// Границы обязаны быть однозначными: без них соседние абзацы, где в обратных
// кавычках стоят `onebase check` или имя вызываемого события, дали бы ложную
// зелень или ложное падение.
func uncalledEventsFromDoc(t *testing.T) []string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	raw, err := os.ReadFile(filepath.Join(root, "docs", "forms.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	if got := strings.Count(doc, uncalledDocBegin); got != 1 {
		t.Fatalf("docs/forms.md: маркеров %s — %d, ожидался ровно один", uncalledDocBegin, got)
	}
	if got := strings.Count(doc, uncalledDocEnd); got != 1 {
		t.Fatalf("docs/forms.md: маркеров %s — %d, ожидался ровно один", uncalledDocEnd, got)
	}
	start := strings.Index(doc, uncalledDocBegin) + len(uncalledDocBegin)
	end := strings.Index(doc, uncalledDocEnd)
	if end < start {
		t.Fatal("docs/forms.md: закрывающий маркер стоит раньше открывающего")
	}

	var names []string
	for _, m := range docEventName.FindAllStringSubmatch(doc[start:end], -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatal("docs/forms.md: блок невызываемых событий пуст — сверка сравнивала бы пустоту")
	}
	sort.Strings(names)
	return names
}

func TestUncalledFormEventsDocMatchesPlatform(t *testing.T) {
	documented := uncalledEventsFromDoc(t)

	platform := make([]string, 0, len(UncalledFormEvents()))
	for _, event := range UncalledFormEvents() {
		platform = append(platform, string(event))
	}
	sort.Strings(platform)

	stale := missingFrom(documented, platform)
	undocumented := missingFrom(platform, documented)
	if len(stale) > 0 {
		t.Errorf("docs/forms.md всё ещё называет невызываемыми %v: у события появился диспетчер, вычеркните его из блока", stale)
	}
	if len(undocumented) > 0 {
		t.Errorf("docs/forms.md не называет невызываемыми %v: обработчик на такое событие молча не выполнится, а руководство об этом не предупреждает", undocumented)
	}
}

// missingFrom возвращает элементы want, которых нет в have.
func missingFrom(want, have []string) []string {
	present := make(map[string]bool, len(have))
	for _, name := range have {
		present[name] = true
	}
	var out []string
	for _, name := range want {
		if !present[name] {
			out = append(out, name)
		}
	}
	return out
}
