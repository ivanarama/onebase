// issuetail — хвост заявок: находит открытые заявки, которые уже сделаны и
// влиты, но остались открытыми.
//
// Зачем. Заявку закрывает PR, а не человек потом: в теле PR нужно английское
// ключевое слово (`Fixes #123`). Русское «Закрывает #123» GitHub не понимает —
// он ищет только английские слова, поэтому исправленная и влитая заявка висит
// открытой, пока её не закроют руками. Так копится хвост: на 15.08.2026 из 13
// открытых заявок четыре были давно в main (#962, рекомендация Р4). Шаблон PR
// помогает только тем, кто его читает; сверка ловит остальных.
//
// Запуск:
//
//	go run ./tools/issuetail            # заявки, чьё закрытие заявлено во влитом PR
//	go run ./tools/issuetail -all       # плюс просто упоминания (шумно)
//	go run ./tools/issuetail -limit 400 # глубже по истории PR
//
// Данные берутся через gh CLI из текущего репозитория; авторизация — та же, что
// у `gh`. Для отладки и тестов вход можно подменить файлами:
//
//	go run ./tools/issuetail -issues issues.json -prs prs.json
//
// Инструмент НЕ гейт: он всегда завершается нулём. «Заявка забыта открытой» —
// не дефект кода, а повод посмотреть и закрыть; в CI такому месту не место.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// issue — открытая заявка в том виде, в каком её отдаёт `gh issue list --json`.
type issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// pull — влитый PR. Body нужен целиком: ключевое слово чаще в теле, чем в
// заголовке.
type pull struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	MergedAt string `json:"mergedAt"`
}

// finding — одна заявка и то, чем она упомянута.
type finding struct {
	issue   issue
	pr      pull
	phrase  string // сработавшая фраза, как она написана в PR
	english bool   // фраза английская — GitHub обязан был закрыть сам
}

var (
	// Английские слова, которые GitHub понимает как закрытие. Список ровно тот,
	// что описан в документации GitHub, — расширять его нельзя: иначе отчёт
	// будет обещать автозакрытие там, где его не произойдёт.
	englishRe = regexp.MustCompile(`(?i)\b(close[sd]?|fix(e[sd])?|resolve[sd]?)\b[\s:]*#(\d+)`)

	// Русские формулировки: то, что человек пишет, думая, что закрывает заявку.
	// GitHub их не понимает — ради них сверка и заведена.
	russianRe = regexp.MustCompile(`(?i)(закрывает|закрывают|закрыто|закрыта|закрывае[тм]|исправляет|исправлено|решает|решено|чинит|фиксит)[\s:]*#(\d+)`)

	// Любое упоминание номера. `#123` в ссылке вида /pull/123 не ловится —
	// перед номером обязана стоять решётка.
	mentionRe = regexp.MustCompile(`#(\d+)`)
)

// stripCode убирает из Markdown код, чтобы ключевое слово закрытия внутри
// примера не считалось заявленным закрытием (#1540): влитой PR #1374 писал
// «намеренно нет `Fixes #1274`», а сверка уверенно рапортовала хвост.
//
// Убирается ровно то, что Markdown считает кодом:
//   - fenced-блоки: строки-разделители из трёх и более backtick'ов или тильд,
//     закрывающий не короче открывающего, содержимое между ними целиком;
//   - строчные code spans: backtick-разделители равной длины (двойной backtick
//     закрывается двойным, одиночный внутри его не закрывает).
//
// Границы намеренные: строчный span не переходит через перевод строки, а fenced
// внутри элемента списка (отступ 4+) не распознаётся — такие места ведут себя
// как прежде, то есть поиск работает по сырому тексту. Блокquot-семантику не
// трогаем: доказанный дефект — только кодовые примеры.
func stripCode(text string) string {
	var b strings.Builder
	inFence := false
	var fenceChar byte
	fenceLen := 0
	for _, line := range strings.Split(text, "\n") {
		if inFence {
			if run, ok := fenceRun(line); ok && run.ch == fenceChar && run.n >= fenceLen {
				inFence = false
			}
			continue // строка кода в отчёт не попадает
		}
		if run, ok := fenceRun(line); ok && run.n >= 3 {
			inFence = true
			fenceChar, fenceLen = run.ch, run.n
			continue
		}
		stripCodeSpans(&b, line)
		b.WriteByte('\n')
	}
	return b.String()
}

// fenceRun проверяет, начинается ли строка (до трёх пробелов отступа) с
// последовательности из трёх и более одинаковых backtick'ов или тильд.
// Открывающая последовательность backtick'ов, после которой в строке есть ещё
// backtick'и, по CommonMark — строчный code span, а не разделитель, поэтому
// fenceRun её разделителем не считает.
type fenceRunInfo struct {
	ch byte
	n  int
}

func fenceRun(line string) (fenceRunInfo, bool) {
	indented := strings.TrimLeft(line, " ")
	if len(line)-len(indented) > 3 {
		return fenceRunInfo{}, false // отступ 4+ — уже не разделитель верхнего уровня
	}
	if indented == "" {
		return fenceRunInfo{}, false
	}
	ch := indented[0]
	if ch != '`' && ch != '~' {
		return fenceRunInfo{}, false
	}
	n := 1
	for n < len(indented) && indented[n] == ch {
		n++
	}
	if n < 3 {
		return fenceRunInfo{}, false
	}
	rest := indented[n:]
	if ch == '`' && strings.ContainsRune(rest, '`') {
		return fenceRunInfo{}, false // `` ` `` — code span, не разделитель
	}
	// Закрывающий разделитель допускает после себя только пробелы; открывающий —
	// информационную строку. Для поиска закрытия этой разницы достаточно:
	// обе ситуации означают «строка кода» и обе выкидываются.
	return fenceRunInfo{ch: ch, n: n}, true
}

// stripCodeSpans пишет в b строку без строчных code spans: span из n backtick'ов
// закрывается следующей последовательностью ровно из n backtick'ов и заменяется
// пробелом (не пустой строкой — чтобы соседние слова не склеивались в новую
// фразу). Непарный разделитель остаётся как есть.
func stripCodeSpans(b *strings.Builder, line string) {
	i := 0
	for i < len(line) {
		if line[i] != '`' {
			j := strings.IndexByte(line[i:], '`')
			if j < 0 {
				b.WriteString(line[i:])
				return
			}
			b.WriteString(line[i : i+j])
			i += j
			continue
		}
		n := 1
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		if end := closingSpan(line, i+n, n); end >= 0 {
			b.WriteByte(' ')
			i = end + n
			continue
		}
		b.WriteString(line[i : i+n])
		i += n
	}
}

// closingSpan ищет первую последовательность ровно n backtick'ов начиная с from
// (более длинные последовательности пропускаются целиком — по CommonMark они
// строчный span не закрывают). Возвращает индекс начала закрывающей
// последовательности или -1.
func closingSpan(line string, from, n int) int {
	for i := from; i < len(line); {
		if line[i] != '`' {
			j := strings.IndexByte(line[i:], '`')
			if j < 0 {
				return -1
			}
			i += j
			continue
		}
		m := 1
		for i+m < len(line) && line[i+m] == '`' {
			m++
		}
		if m == n {
			return i
		}
		i += m
	}
	return -1
}

func main() {
	var (
		limit      = flag.Int("limit", 200, "сколько влитых PR просмотреть")
		all        = flag.Bool("all", false, "показать и просто упоминания")
		issuesPath = flag.String("issues", "", "файл с JSON заявок вместо вызова gh")
		prsPath    = flag.String("prs", "", "файл с JSON влитых PR вместо вызова gh")
	)
	flag.Parse()

	issues, prs, err := load(*issuesPath, *prsPath, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "issuetail: %v\n", err)
		os.Exit(2)
	}

	declared, mentioned := analyze(issues, prs)

	if len(declared) == 0 {
		fmt.Printf("issuetail: хвоста нет — среди %d открытых заявок ни одна не заявлена закрытой во влитом PR\n", len(issues))
	} else {
		fmt.Printf("issuetail: заявлено закрытие, но заявка открыта — %d\n\n", len(declared))
		for _, f := range declared {
			fmt.Printf("  #%-5d %s\n", f.issue.Number, f.issue.Title)
			fmt.Printf("         PR #%d: «%s»%s\n", f.pr.Number, f.phrase, reason(f))
			fmt.Printf("         %s\n\n", f.pr.URL)
		}
	}

	if len(mentioned) > 0 && !*all {
		fmt.Printf("\nупомянуты во влитых PR без заявки о закрытии: %d (показать: -all)\n", len(mentioned))
	}
	if *all {
		fmt.Printf("\nупоминания без заявки о закрытии — %d (это норма для сводных заявок):\n", len(mentioned))
		for _, f := range mentioned {
			fmt.Printf("  #%-5d %-60.60s ← PR #%d\n", f.issue.Number, f.issue.Title, f.pr.Number)
		}
	}
}

// reason объясняет, почему заявка осталась открытой: русская фраза GitHub'у
// ничего не говорит, а английская — говорит, и тогда странно уже то, что она
// открыта.
func reason(f finding) string {
	if f.english {
		return " — слово английское, GitHub должен был закрыть сам: проверь, в ту ли ветку влит PR"
	}
	return " — GitHub понимает только английские ключевые слова, поэтому заявка и не закрылась"
}

func load(issuesPath, prsPath string, limit int) ([]issue, []pull, error) {
	var (
		issues []issue
		prs    []pull
	)

	issuesRaw, err := source(issuesPath, "issue", "list", "--state", "open", "--limit", strconv.Itoa(limit), "--json", "number,title,url")
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(issuesRaw, &issues); err != nil {
		return nil, nil, fmt.Errorf("разбор списка заявок: %w", err)
	}

	prsRaw, err := source(prsPath, "pr", "list", "--state", "merged", "--limit", strconv.Itoa(limit), "--json", "number,title,body,url,mergedAt")
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(prsRaw, &prs); err != nil {
		return nil, nil, fmt.Errorf("разбор списка PR: %w", err)
	}

	return issues, prs, nil
}

// source отдаёт содержимое файла, если путь задан, иначе спрашивает gh.
func source(path string, args ...string) ([]byte, error) {
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	// G204: имя программы фиксировано, а args собираются здесь же из литералов и
	// одного числового флага — снаружи в них ничего не попадает; shell не запускается.
	out, err := exec.Command("gh", args...).Output() //nolint:gosec
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// analyze делит открытые заявки на две корзины: те, чьё закрытие во влитом PR
// заявлено словами, и те, что просто упомянуты. Вторая корзина шумная и сама по
// себе ни о чём не говорит: сводную заявку вроде ревью недели упоминает каждый
// второй PR, и это нормально.
func analyze(issues []issue, prs []pull) (declared, mentioned []finding) {
	open := make(map[int]issue, len(issues))
	for _, is := range issues {
		open[is.Number] = is
	}

	// Заявку показываем один раз — по первому PR, который её задел. PR идут от
	// свежих к старым, поэтому сначала разворачиваем: интересен тот PR, который
	// закрытие заявил раньше всех.
	seen := make(map[int]bool, len(issues))
	for i := len(prs) - 1; i >= 0; i-- {
		pr := prs[i]
		// Закрытие ищем по тексту без кода: ключевое слово внутри примера —
		// не заявление (#1540). Упоминания ниже считаем по сырому тексту.
		prose := stripCode(pr.Title + "\n" + pr.Body)

		for _, m := range englishRe.FindAllStringSubmatch(prose, -1) {
			add(&declared, seen, open, pr, m[0], m[3], true)
		}
		for _, m := range russianRe.FindAllStringSubmatch(prose, -1) {
			add(&declared, seen, open, pr, m[0], m[2], false)
		}
	}

	// Упоминания считаем после: заявка, попавшая в первую корзину, во вторую
	// уже не идёт.
	for i := len(prs) - 1; i >= 0; i-- {
		pr := prs[i]
		text := pr.Title + "\n" + pr.Body
		for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || seen[n] {
				continue
			}
			is, ok := open[n]
			if !ok || n == pr.Number {
				continue
			}
			seen[n] = true
			mentioned = append(mentioned, finding{issue: is, pr: pr})
		}
	}

	sort.Slice(declared, func(i, j int) bool { return declared[i].issue.Number < declared[j].issue.Number })
	sort.Slice(mentioned, func(i, j int) bool { return mentioned[i].issue.Number < mentioned[j].issue.Number })
	return declared, mentioned
}

func add(dst *[]finding, seen map[int]bool, open map[int]issue, pr pull, phrase, num string, english bool) {
	n, err := strconv.Atoi(num)
	if err != nil || seen[n] || n == pr.Number {
		return
	}
	is, ok := open[n]
	if !ok {
		return // заявка уже закрыта — ровно то, чего мы и хотим
	}
	seen[n] = true
	*dst = append(*dst, finding{
		issue:   is,
		pr:      pr,
		phrase:  strings.TrimSpace(phrase),
		english: english,
	})
}
