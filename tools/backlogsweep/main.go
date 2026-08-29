// backlogsweep — ревизия припаркованных заявок: показывает те, что застряли
// молча.
//
// Зачем. Конвейер сопровождения умеет ставить заявку на паузу (`hold`) и
// спрашивать человека (`needs-decision`), но снять паузу и ответить может
// только человек — а забывчивость не лечится дисциплиной. В этом репозитории
// это проверено дважды: `docs/features.md` накопил 146 разделов `testing`
// против 52 `stable` при нуле переходов, пока не появился `featureage`;
// заявки, сделанные и влитые, оставались открытыми, пока не появился
// `issuetail`. Оба — отчёты, не гейты, и оба работают.
//
// Правило, ради которого всё: припаркованная заявка обязана попадать ровно в
// одну корзину — «делаем планом» (`hold` + ссылка на план), «не делаем»
// (закрыта с причиной) или «не решено» (`needs-decision`). Сто заявок в первой
// корзине читаются нормально: это дорожная карта. Двадцать без ссылки на план —
// уже свалка, потому что через месяц не отличить решённое от забытого.
//
// Что ищем:
//
//	hold без ссылки на план      — решение не оформлено, повод забыть навсегда
//	hold без движения N дней     — «потом» без срока это «никогда» с лишней строкой
//	needs-decision без движения  — ход человека завис
//	внешняя заявка без ответа    — самое дорогое молчание: автор ждёт
//	ссылка на план, которого нет — план обещан и не написан либо номер переехал
//
// Запуск:
//
//	go run ./tools/backlogsweep                 # отчёт по текущему репозиторию
//	go run ./tools/backlogsweep -hold-days 30   # строже к залежавшимся паузам
//	go run ./tools/backlogsweep -team a,b       # кто считается «своим»
//
// Данные берутся через gh CLI: открытые заявки и открытые PR (по ним видно
// планы, которые уже написаны, но ещё не влиты). Для тестов вход подменяется
// файлами (-issues, -prs) и временем (-now 2026-08-26T00:00:00Z).
//
// Инструмент НЕ гейт: находки не влияют на код возврата, он всегда нулевой.
// «Заявка залежалась» — не дефект кода, а повод посмотреть; в CI такому месту
// не место. Ненулевой код бывает только при сбое самого прогона (gh недоступен,
// битый -now): несделанная проверка не должна выглядеть чистым отчётом.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// issue — открытая заявка в том виде, в каком её отдаёт `gh issue list --json`.
type issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Author    user      `json:"author"`
	Labels    []label   `json:"labels"`
	Comments  []comment `json:"comments"`
}

// pull — открытый PR: нужен только список файлов, чтобы знать про планы,
// которые уже написаны, но ещё не влиты.
type pull struct {
	Number int `json:"number"`
	Files  []struct {
		Path string `json:"path"`
	} `json:"files"`
}

type user struct {
	Login string `json:"login"`
}

type label struct {
	Name string `json:"name"`
}

type comment struct {
	Author    user      `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// finding — заявка и то, чем она подозрительна. Одна заявка может попасть в
// несколько корзин: «внешняя без ответа» и «hold без плана» — разные починки.
type finding struct {
	issue  issue
	detail string
}

// bucket — корзина находок с объяснением, что с ней делать. Совет пишем в
// отчёте, а не в голове: отчёт читают раз в неделю и по диагонали.
type bucket struct {
	title    string
	advice   string
	findings []finding
}

var (
	// planFileRe — ссылка файлом: `Plans/155-nav-collapse.md`. Имя файла важнее
	// номера: номер живёт своей жизнью (планы перенумеровывают при коллизиях), и
	// сверка «есть ли файл с таким номером» пропускает ровно тот вид поломки,
	// ради которого всё затевалось — номер переехал, ссылка осталась. Так в
	// #1122 ссылка ведёт на `Plans/155-nav-collapse.md`, а под номером 155
	// лежит `155-excel-print-template.md`, совсем другой план.
	planFileRe = regexp.MustCompile("(?i)Plans/((\\d{1,3})-[^\\s`)\"',<>]*\\.md)")

	// planNumRe — ссылка словами: «план 157» в любом падеже. Окончания
	// перечислены не для красоты: живой текст почти всегда косвенный
	// («оформлено планом 157», «по плану 46»), и без них проверка молчала бы
	// ровно там, где она нужна.
	planNumRe = regexp.MustCompile(`(?i)план(?:а|у|ом|е|ы|ов|ам|ами)?\s+(\d{1,3})`)
)

// planRef — упоминание плана в тексте заявки. File пуст, если план назван
// только номером: тогда и проверять можно только номер.
//
// At — место в треде: 0 заголовок, 1 тело, дальше комментарии сверху вниз. По
// нему отличается «ссылка битая» от «ссылка битая, но ниже её уже поправили»;
// без порядка все упоминания сваливались в один мешок, и момент правки терялся.
// Считается по ПОСЛЕДНЕМУ упоминанию: ссылка, повторённая ниже поправки, снова
// актуальна.
type planRef struct {
	File string
	Num  int
	At   int
}

// String — как показать ссылку в отчёте.
func (r planRef) String() string {
	if r.File != "" {
		return "Plans/" + r.File
	}
	return "план " + strconv.Itoa(r.Num)
}

func main() {
	var (
		limit       = flag.Int("limit", 200, "сколько открытых заявок просмотреть")
		holdDays    = flag.Int("hold-days", 56, "через сколько дней без движения пауза считается залежавшейся")
		decideDays  = flag.Int("decision-days", 14, "через сколько дней без движения needs-decision считается зависшим")
		replyDays   = flag.Int("reply-days", 7, "через сколько дней молчания внешней заявке нужен ответ")
		team        = flag.String("team", "ivanarama,ivantit66", "логины, которые считаются своими (через запятую)")
		plansDir    = flag.String("plans", "Plans", "каталог планов — по нему проверяются ссылки")
		issuesPath  = flag.String("issues", "", "файл с JSON заявок вместо вызова gh")
		prsPath     = flag.String("prs", "", "файл с JSON открытых PR вместо вызова gh")
		nowOverride = flag.String("now", "", "текущее время (RFC3339) — для тестов")
	)
	flag.Parse()

	now := time.Now().UTC()
	if *nowOverride != "" {
		parsed, err := time.Parse(time.RFC3339, *nowOverride)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "backlogsweep: -now: %v\n", err)
			os.Exit(2)
		}
		now = parsed.UTC()
	}

	issues, prs, err := load(*issuesPath, *prsPath, *limit)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "backlogsweep: %v\n", err)
		os.Exit(2)
	}

	var prFiles []string
	for _, pr := range prs {
		for _, f := range pr.Files {
			prFiles = append(prFiles, f.Path)
		}
	}

	cfg := config{
		now:        now,
		holdDays:   *holdDays,
		decideDays: *decideDays,
		replyDays:  *replyDays,
		team:       split(*team),
		plans:      knownPlans(*plansDir, prFiles),
	}

	buckets := analyze(issues, cfg)
	report(os.Stdout, buckets, len(issues), len(issues) >= *limit)
}

type config struct {
	now        time.Time
	holdDays   int
	decideDays int
	replyDays  int
	team       map[string]bool
	plans      plans
}

// analyze раскладывает заявки по корзинам. Порядок корзин в выводе — по
// убыванию цены молчания: сначала то, где ждёт живой человек.
func analyze(issues []issue, cfg config) []bucket {
	unanswered := bucket{
		title:  "внешняя заявка без ответа",
		advice: "автор ждёт: ответить по существу или закрыть с причиной — молчание дороже отказа",
	}
	holdNoPlan := bucket{
		title:  "hold без ссылки на план",
		advice: "решение не оформлено: дописать ссылку на план или закрыть — иначе через месяц не отличить решённое от забытого",
	}
	planMissing := bucket{
		title:  "ссылка на план, которого нет",
		advice: "план обещан и не написан либо номер переехал: написать план или поправить ссылку",
	}
	holdStale := bucket{
		title:  "hold без движения",
		advice: "«потом» без срока это «никогда»: вернуть в работу (approved) или закрыть",
	}
	decisionStale := bucket{
		title:  "needs-decision без движения",
		advice: "ход человека завис: ответить меткой approved / decision:N либо закрыть",
	}

	for _, is := range issues {
		labels := labelSet(is)
		quiet := days(cfg.now, is.UpdatedAt)

		// Молчание считаем от последнего НАШЕГО ответа, а не от UpdatedAt:
		// комментарий автора обновляет заявку, и версия на UpdatedAt выкидывала
		// из отчёта именно того, кто напоминает о себе, — то есть худший случай.
		if !cfg.team[is.Author.Login] {
			if silence, answered, waiting := waitingSince(is, cfg); waiting && silence >= cfg.replyDays {
				tail := " (ответа не было ни разу)"
				if answered {
					tail = " с последнего вопроса"
				}
				unanswered.findings = append(unanswered.findings, finding{is,
					fmt.Sprintf("автор @%s ждёт %d дн.%s", is.Author.Login, silence, tail)})
			}
		}

		refs := planRefs(is)
		if labels["hold"] {
			if len(refs) == 0 {
				holdNoPlan.findings = append(holdNoPlan.findings, finding{is,
					fmt.Sprintf("заявке %d дн., плана не назначено", days(cfg.now, is.CreatedAt))})
			}
			if quiet >= cfg.holdDays {
				holdStale.findings = append(holdStale.findings, finding{is,
					fmt.Sprintf("без движения %d дн.", quiet)})
			}
		}

		// Ссылку на несуществующий план проверяем у любой заявки, не только у
		// hold: «сделаем планом» пишут и в разборе, и в ответе автору. Каталог
		// планов не прочитан (запуск не из корня) — проверка выключена целиком,
		// иначе отчёт объявил бы несуществующими все планы разом.
		//
		// Печатаем ВСЕ битые ссылки заявки. Прежняя версия обрывалась на первой,
		// поэтому вторая всплывала только после починки первой — то есть
		// неделей позже, и так по одной.
		if cfg.plans.ok {
			for _, ref := range refs {
				if cfg.plans.has(ref) {
					continue
				}
				detail := fmt.Sprintf("ссылка на %s — такого плана нет ни в каталоге, ни в открытых PR", ref)
				if actual, ok := cfg.plans.renamed(ref); ok {
					detail = fmt.Sprintf("ссылка на %s устарела — план лежит как Plans/%s", ref, actual)
				}
				if fixed, ok := correctedBelow(refs, ref, cfg.plans); ok {
					detail = fmt.Sprintf("ссылка на %s устарела — ниже в треде уже поправлено на %s", ref, fixed)
				}
				planMissing.findings = append(planMissing.findings, finding{is, detail})
			}
		}

		// approved старше needs-decision: человек уже сходил, метку он снимать
		// не обязан (см. docs/maintenance-pipeline.md).
		if labels["needs-decision"] && !labels["approved"] && !labels["hold"] && quiet >= cfg.decideDays {
			decisionStale.findings = append(decisionStale.findings, finding{is,
				fmt.Sprintf("без движения %d дн.", quiet)})
		}
	}

	return []bucket{unanswered, holdNoPlan, planMissing, holdStale, decisionStale}
}

func report(w io.Writer, buckets []bucket, total int, truncated bool) {
	// Ошибку записи глотаем осознанно: это отчёт в stdout, и падать из-за
	// закрытого пайпа (`| head`) ему незачем.
	say := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

	found := 0
	for _, b := range buckets {
		found += len(b.findings)
	}
	if found == 0 {
		say("backlogsweep: хвост чист — среди %d открытых заявок застрявших нет\n", total)
	} else {
		say("backlogsweep: застрявших заявок — %d (из %d открытых)\n", found, total)
		for _, b := range buckets {
			if len(b.findings) == 0 {
				continue
			}
			say("\n%s — %d\n%s\n", b.title, len(b.findings), b.advice)
			// Стабильная: у одной заявки может быть несколько находок в корзине
			// (две битые ссылки), и порядок между ними задан порядком ссылок.
			// Обычная Slice переставляла бы их от прогона к прогону, и отчёт
			// шевелился бы там, где ничего не изменилось.
			sort.SliceStable(b.findings, func(i, j int) bool {
				return b.findings[i].issue.Number < b.findings[j].issue.Number
			})
			for _, f := range b.findings {
				say("  #%-5d %-52.52s  %s\n", f.issue.Number, f.issue.Title, f.detail)
			}
		}
	}
	if truncated {
		// Молчаливая обрезка превращает «из N открытых» в неправду.
		say("\nвнимание: заявок ровно столько, сколько разрешает -limit (%d) — часть могла не попасть в просмотр\n", total)
	}
}

// Маркеры конвейера в теле комментария. Список держим в одном месте: он растёт
// вместе с этапами, а забытый маркер тихо возвращает ту самую слепоту, ради
// которой список заведён.
const (
	// pipelineMarker — любой машинный маркер: `<!-- pp:triage -->`,
	// `<!-- pp:review pp:tail=2 -->`, `<!-- pp:tail-done … -->`. Ищем по
	// ПРЕФИКСУ: маркеры несут параметры прямо внутри, и сверка с точной строкой
	// пропустила бы как раз новые.
	pipelineMarker = "<!-- pp:"
	// replyMarker — единственный маркер, которым конвейер помечает разговор с
	// автором, а не запись для себя (`/triage-issues`, автоответ при
	// `ready-fix`).
	replyMarker = "<!-- pp:reply"
)

// answersAuthor — можно ли считать комментарий «своего» логина ответом автору.
//
// Разбор триажа и заключение ревью пишутся от логина из `-team`, но адресованы
// они конвейеру: `файл:строка`, критерии автохода, план фикса. Засчитывать их
// за ответ — значит гасить находку «внешняя заявка без ответа» первым же
// проходом `/triage-issues` (#1166): корзина ловила только те заявки, до
// которых триаж ещё НЕ дошёл, то есть ровно не тот случай, ради которого
// заведена. Молчание после разбора дороже молчания до него — по разобранной
// заявке уже всё понятно, и не сказать об этом автору не мешает ничего.
//
// Неизвестный маркер считаем машинным: маркер в тексте и ставят затем, чтобы
// комментарий читала машина, а живой ответ человека маркеров не носит вовсе.
func answersAuthor(body string) bool {
	if !strings.Contains(body, pipelineMarker) {
		return true
	}
	return strings.Contains(body, replyMarker)
}

// waitingSince — с какого момента автор ждёт нашего ответа.
//
// Ждёт он тогда, когда последним говорил он: сама заявка — уже его сообщение,
// а после нашего ответа мяч у него, и молчание в этом случае наше право, а не
// долг. Отсчёт идёт от ПЕРВОГО его сообщения, оставшегося без ответа, а не от
// последнего: автор, который напоминает о себе, иначе выглядел бы свежим
// (`UpdatedAt` его напоминанием и обновляется) — а ждёт он дольше всех.
//
// Ответом считается только то, что адресовано автору (`answersAuthor`), —
// иначе граница `lastTeam` уезжает на разбор триажа и вопрос автора, заданный
// РАНЬШЕ разбора, перестаёт быть неотвеченным.
//
// waiting=false означает «мяч не у нас».
func waitingSince(is issue, cfg config) (silence int, answered, waiting bool) {
	var lastTeam time.Time
	for _, c := range is.Comments {
		if cfg.team[c.Author.Login] && answersAuthor(c.Body) && c.CreatedAt.After(lastTeam) {
			lastTeam = c.CreatedAt
			answered = true
		}
	}

	pending := is.CreatedAt // заведение заявки — первое сообщение автора
	if answered {
		pending = time.Time{}
		for _, c := range is.Comments {
			if cfg.team[c.Author.Login] || !c.CreatedAt.After(lastTeam) {
				continue
			}
			if pending.IsZero() || c.CreatedAt.Before(pending) {
				pending = c.CreatedAt
			}
		}
		if pending.IsZero() {
			return 0, true, false // последним говорили мы
		}
	}
	return days(cfg.now, pending), answered, true
}

// planRefs — планы, упомянутые в заявке: заголовок, ТЕЛО и комментарии. Тело
// читать обязательно: в #1122 ссылка на план стоит именно там, и первая версия
// сверки, смотревшая только заголовок с комментариями, объявляла такую заявку
// «без плана».
//
// Текст разбирается по кускам, а не склеенным в один: кусок — это место в
// треде, и оно попадает в planRef.At.
func planRefs(is issue) []planRef {
	parts := make([]string, 0, len(is.Comments)+2)
	parts = append(parts, is.Title, is.Body)
	for _, c := range is.Comments {
		parts = append(parts, c.Body)
	}

	var out []planRef
	seen := map[string]int{} // ключ ссылки → её место в out
	add := func(ref planRef) {
		key := ref.File + "#" + strconv.Itoa(ref.Num)
		if i, ok := seen[key]; ok {
			if ref.At > out[i].At {
				out[i].At = ref.At
			}
			return
		}
		seen[key] = len(out)
		out = append(out, ref)
	}

	// Два прохода, а не один: ссылка файлом отменяет ссылку тем же номером, где
	// бы в треде она ни стояла, поэтому сначала собираем все файлы.
	byNum := map[int]bool{} // номера, у которых уже есть ссылка файлом
	for at, text := range parts {
		for _, m := range planFileRe.FindAllStringSubmatch(text, -1) {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				continue
			}
			byNum[n] = true
			add(planRef{File: m[1], Num: n, At: at})
		}
	}
	for at, text := range parts {
		for _, m := range planNumRe.FindAllStringSubmatch(text, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || byNum[n] {
				continue // тот же план уже назван файлом — точная ссылка старше
			}
			add(planRef{Num: n, At: at})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Num != out[j].Num {
			return out[i].Num < out[j].Num
		}
		return out[i].File < out[j].File
	})
	return out
}

// correctedBelow — живая ссылка на тот же план ниже по треду. Так выглядит
// обычная человеческая поправка: план перенумеровали, верное имя дописали
// комментарием, а старый текст не тронули — ровно это в #1134 («план получил
// номер 158, а не 157»).
//
// Находку это не отменяет: скрытая неотличима от «инструмент не заметил», а
// отчёт читают ради полноты. Но помеченную строку глаз отличает от новой, и
// знакомая строка не уносит с собой соседние.
//
// Сравниваем по slug: переехал как раз номер. Ссылку, названную одним номером,
// поправленной не считаем никогда — «план 158» ниже «плана 157» не значит, что
// это тот же план.
func correctedBelow(refs []planRef, broken planRef, p plans) (planRef, bool) {
	if broken.File == "" {
		return planRef{}, false
	}
	for _, ref := range refs {
		if ref.File == "" || ref.At <= broken.At || slug(ref.File) != slug(broken.File) {
			continue
		}
		if p.has(ref) {
			return ref, true
		}
	}
	return planRef{}, false
}

// plans — что считается существующим планом: имена файлов и занятые номера.
// ok=false означает «каталог не прочитан» — тогда проверка ссылок выключается
// целиком, иначе отчёт объявил бы несуществующими все планы разом.
type plans struct {
	files map[string]bool
	nums  map[int]bool
	slugs map[string]string // «nav-collapse» → «157-nav-collapse.md»
	ok    bool
}

func (p plans) has(ref planRef) bool {
	if ref.File != "" {
		return p.files[ref.File]
	}
	return p.nums[ref.Num]
}

// renamed — тот же план под другим номером. Ссылка на `155-nav-collapse.md`
// при живом `157-nav-collapse.md` означает не «плана нет», а «номер переехал»,
// и починка у этих случаев разная: написать план или поправить ссылку.
// Перенумерация здесь обычное дело — номера сталкиваются в открытых PR.
func (p plans) renamed(ref planRef) (string, bool) {
	if ref.File == "" {
		return "", false
	}
	actual, ok := p.slugs[slug(ref.File)]
	return actual, ok && actual != ref.File
}

// slug — имя плана без номера и расширения: «155-nav-collapse.md» → «nav-collapse».
func slug(name string) string {
	name = strings.TrimSuffix(strings.ToLower(name), ".md")
	if i := strings.IndexByte(name, '-'); i > 0 {
		if _, err := strconv.Atoi(name[:i]); err == nil {
			return name[i+1:]
		}
	}
	return name
}

// knownPlans — планы каталога плюс те, что заводятся открытыми PR. Без второй
// половины сверка врёт на каждом плане, который написан, но ещё не влит: так
// первый прогон объявил несуществующим план 157 из открытого PR #1133.
func knownPlans(dir string, prFiles []string) plans {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return plans{}
	}
	p := plans{files: map[string]bool{}, nums: map[int]bool{}, slugs: map[string]string{}, ok: true}
	for _, e := range entries {
		p.remember(filepath.Base(e.Name()))
	}
	for _, path := range prFiles {
		if dir, name := filepath.Split(path); strings.EqualFold(filepath.Clean(dir), "Plans") {
			p.remember(name)
		}
	}
	return p
}

func (p plans) remember(name string) {
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		return
	}
	p.files[name] = true
	p.slugs[slug(name)] = name
	if i := strings.IndexByte(name, '-'); i > 0 {
		if n, err := strconv.Atoi(name[:i]); err == nil {
			p.nums[n] = true
		}
	}
}

func labelSet(is issue) map[string]bool {
	out := make(map[string]bool, len(is.Labels))
	for _, l := range is.Labels {
		out[l.Name] = true
	}
	return out
}

// days — сколько полных суток прошло. Отрицательное (время в будущем — часы
// сервера, подменённый -now) считаем нулём: «залежалось -3 дня» в отчёте хуже,
// чем отсутствие строки.
func days(now, then time.Time) int {
	d := int(now.Sub(then).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func split(s string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out[p] = true
		}
	}
	return out
}

func load(issuesPath, prsPath string, limit int) ([]issue, []pull, error) {
	// body обязателен: ссылка на план чаще стоит в теле заявки, чем в
	// комментариях, и без него сверка объявляла такие заявки «без плана».
	raw, err := source(issuesPath, "issue", "list", "--state", "open", "--limit", strconv.Itoa(limit),
		"--json", "number,title,body,url,createdAt,updatedAt,author,labels,comments")
	if err != nil {
		return nil, nil, err
	}
	var issues []issue
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, nil, fmt.Errorf("разбор списка заявок: %w", err)
	}

	prsRaw, err := source(prsPath, "pr", "list", "--state", "open", "--limit", strconv.Itoa(limit),
		"--json", "number,files")
	if err != nil {
		return nil, nil, err
	}
	var prs []pull
	if err := json.Unmarshal(prsRaw, &prs); err != nil {
		return nil, nil, fmt.Errorf("разбор списка PR: %w", err)
	}

	return issues, prs, nil
}

// source отдаёт содержимое файла, если путь задан, иначе спрашивает gh.
func source(path string, args ...string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	// G204: имя программы фиксировано, args собираются здесь же из литералов и
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
