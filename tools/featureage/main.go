// featureage — взросление разделов docs/features.md: `testing`, пролежавший
// дольше срока, переводится в `stable` (или в `needs-description`, если тела у
// раздела нет).
//
// Зачем. Раздел «Тестируем» на сайте читает docs/features.md и показывает всё,
// что помечено `status: testing`. Вход в этот статус есть, выхода не было:
// на 19.08.2026 в файле 146 разделов `testing` против 52 `stable`, из них 57
// старше шести недель, а переходов `testing → stable` не случилось ни одного
// (#962, рекомендация Р3). То есть «Тестируем» перестал быть списком свежего
// и стал полным каталогом продукта — пользователь не может понять, что новое.
//
// Запуск:
//
//	go run ./tools/featureage                # отчёт: что созрело
//	go run ./tools/featureage -apply         # переписать статусы в файле
//	go run ./tools/featureage -weeks 8       # другой срок выдержки
//
// Инструмент НЕ гейт: без -apply он только печатает список и всегда завершается
// нулём. Ставить его блокирующим в CI нельзя — «раздел созрел» не дефект, а
// повод для решения человека.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const defaultPath = "docs/features.md"

var (
	sectionRe = regexp.MustCompile(`(?m)^## .+$`)
	statusRe  = regexp.MustCompile(`<!--\s*status:\s*([A-Za-z-]+)\s*-->`)
	dateRe    = regexp.MustCompile(`<!--\s*date:\s*(\d{4}-\d{2}-\d{2})\s*-->`)
	issueRe   = regexp.MustCompile(`<!--\s*issue:\s*\d+\s*-->`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// section — один раздел файла: заголовок и тело до следующего «## ».
type section struct {
	title string
	body  string
	start int // смещение тела в исходном тексте
}

type verdict struct {
	title  string
	date   time.Time
	status string // новый статус
	reason string
}

func main() {
	var (
		path  = flag.String("file", defaultPath, "путь к features.md")
		weeks = flag.Int("weeks", 6, "срок выдержки в неделях")
		apply = flag.Bool("apply", false, "переписать статусы в файле")
		nowS  = flag.String("now", "", "дата отсчёта YYYY-MM-DD (для тестов)")
	)
	flag.Parse()

	now := time.Now()
	if *nowS != "" {
		parsed, err := time.Parse("2006-01-02", *nowS)
		if err != nil {
			fmt.Fprintf(os.Stderr, "featureage: неверная дата -now: %v\n", err)
			os.Exit(2)
		}
		now = parsed
	}

	raw, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "featureage: %v\n", err)
		os.Exit(2)
	}

	updated, ripe, skipped := process(string(raw), now, *weeks)
	if len(ripe) == 0 {
		fmt.Printf("featureage: созревших разделов нет (выдержка %d недель)\n", *weeks)
		return
	}

	fmt.Printf("featureage: созрело разделов — %d (выдержка %d недель)\n", len(ripe), *weeks)
	for _, v := range ripe {
		fmt.Printf("  %s  %-16s %s\n", v.date.Format("2006-01-02"), "→ "+v.status, v.title)
	}
	if skipped > 0 {
		fmt.Printf("пропущено из-за ссылки на заявку: %d (обсуждение открыто — статус меняет человек)\n", skipped)
	}
	if !*apply {
		fmt.Println("\nничего не изменено; чтобы применить: go run ./tools/featureage -apply")
		return
	}
	if err := os.WriteFile(*path, []byte(updated), 0o644); err != nil { //nolint:gosec // G306: документ репозитория, не секрет
		fmt.Fprintf(os.Stderr, "featureage: запись %s: %v\n", *path, err)
		os.Exit(2)
	}
	fmt.Printf("\n%s обновлён: %d раздел(ов)\n", *path, len(ripe))
}

// process возвращает новый текст файла, список созревших разделов и число
// пропущенных из-за открытого обсуждения.
func process(text string, now time.Time, weeks int) (string, []verdict, int) {
	cutoff := now.AddDate(0, 0, -7*weeks)
	sections := splitSections(text)

	var ripe []verdict
	skipped := 0
	out := text
	// Правки идут с конца, чтобы смещения предыдущих разделов не сдвигались.
	for i := len(sections) - 1; i >= 0; i-- {
		s := sections[i]
		st := statusRe.FindStringSubmatch(s.body)
		if st == nil || st[1] != "testing" {
			continue
		}
		dm := dateRe.FindStringSubmatch(s.body)
		if dm == nil {
			// Без даты возраст неизвестен. Молча «состарить» такой раздел
			// нельзя: он может быть свежим, просто без пометки.
			continue
		}
		d, err := time.Parse("2006-01-02", dm[1])
		if err != nil || d.After(cutoff) {
			continue
		}
		if issueRe.MatchString(s.body) {
			// Раздел с ссылкой на заявку означает открытое обсуждение — такие
			// оставляем человеку. Точная проверка «нет открытых заявок»
			// потребовала бы обращения к GitHub из репозиторного инструмента:
			// токен и сеть в CI ради подсказки — плохой размен.
			skipped++
			continue
		}

		next := "stable"
		reason := "выдержка вышла"
		if bodyIsEmpty(s.body) {
			next = "needs-description"
			reason = "тела у раздела нет"
		}
		ripe = append(ripe, verdict{title: s.title, date: d, status: next, reason: reason})

		loc := statusRe.FindStringIndex(s.body)
		abs := s.start + loc[0]
		out = out[:abs] + "<!-- status: " + next + " -->" + out[s.start+loc[1]:]
	}

	// Разделы просматривались с конца — вернём хронологический порядок вывода.
	for i, j := 0, len(ripe)-1; i < j; i, j = i+1, j-1 {
		ripe[i], ripe[j] = ripe[j], ripe[i]
	}
	return out, ripe, skipped
}

func splitSections(text string) []section {
	idx := sectionRe.FindAllStringIndex(text, -1)
	sections := make([]section, 0, len(idx))
	for i, loc := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		sections = append(sections, section{
			title: strings.TrimSpace(strings.TrimPrefix(text[loc[0]:loc[1]], "## ")),
			body:  text[loc[1]:end],
			start: loc[1],
		})
	}
	return sections
}

// bodyIsEmpty — у раздела нет ничего, кроме служебных комментариев.
func bodyIsEmpty(body string) bool {
	return strings.TrimSpace(commentRe.ReplaceAllString(body, "")) == ""
}
