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
//	предшественник закрыт        — то, чего ждала пауза, уже сделано
//	Blocked-by в никуда          — номер, которого нет: опечатка похожа на разблокировку
//
// Зависимость между заявками объявляется строкой в теле (или комментарии):
//
//	Blocked-by: #1204
//
// Понятие нужно ровно затем, чтобы отличить «ждём предшественника» от «просто
// лежит»: без него пауза, которая идёт по плану, и пауза, о которой забыли,
// выглядят одинаково, и снятие первой держится на памяти человека — то самое,
// против чего написаны issuetail и featureage.
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

	// blockedByRe — объявленная зависимость: строка `Blocked-by: #1204`.
	// Требуется и маркер, и начало строки, в отличие от ссылок на планы: живой
	// текст заявки полон `#N` (соседние обсуждения, номера PR из разбора), и
	// сверка по голому номеру объявила бы зависимостью каждую вторую ссылку.
	// Слева допускаются оформление списком и цитирование — `- Blocked-by: #1204`
	// в перечне условий и `> Blocked-by: #1204` в ответе значат ровно то же.
	blockedByRe = regexp.MustCompile(`(?im)^[ \t>*+-]*blocked-by\s*:\s*(.*)$`)

	// blockerNumRe — номера в хвосте такой строки: предшественников бывает
	// несколько, и перечислять их одной строкой естественнее, чем плодить их.
	blockerNumRe = regexp.MustCompile(`#(\d+)`)
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

	issues, prs, issuedThrough, err := load(*issuesPath, *prsPath, *limit)
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
		objects:    knownObjects(issues, prs, issuedThrough),
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
	objects    objects
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
	blockerMissing := bucket{
		title:  "Blocked-by на номер, которого нет",
		advice: "номер выше последнего выданного в репозитории: поправить его — иначе пауза так и не разблокируется",
	}
	holdStale := bucket{
		title:  "hold без движения",
		advice: "«потом» без срока это «никогда»: вернуть в работу (approved) или закрыть",
	}
	decisionStale := bucket{
		title:  "needs-decision без движения",
		advice: "ход человека завис: ответить меткой approved / decision:N либо закрыть",
	}
	unblocked := bucket{
		title:  "предшественник закрыт, а hold остался",
		advice: "пауза кончилась: снять hold и поставить approved — или дописать, чего ещё ждём",
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
		blockers := blockedBy(is)
		waiting, done, unknown := cfg.objects.classify(blockers)

		if labels["hold"] {
			// Объявленный предшественник — тоже оформленное решение: «ждём #N»
			// говорит, почему пауза, ровно как ссылка на план. Без этой оговорки
			// корзина писала бы про такую заявку «решение не оформлено» — то есть
			// неправду, причём ровно о той заявке, которая оформлена лучше всех.
			if len(refs) == 0 && len(blockers) == 0 {
				holdNoPlan.findings = append(holdNoPlan.findings, finding{is,
					fmt.Sprintf("заявке %d дн., плана не назначено", days(cfg.now, is.CreatedAt))})
			}
			// Пауза, у которой предшественник ещё открыт, не залежалась: работа
			// идёт, просто не здесь. Иначе новая корзина принесла бы шум ровно
			// там, где всё по плану, — а шум съедает и соседние строки.
			if quiet >= cfg.holdDays && len(waiting) == 0 {
				holdStale.findings = append(holdStale.findings, finding{is,
					fmt.Sprintf("без движения %d дн.", quiet)})
			}
			// Ждать больше нечего только когда закрыты ВСЕ предшественники:
			// «один из двух готов» — это по-прежнему ожидание.
			if len(done) > 0 && len(waiting) == 0 && len(unknown) == 0 {
				subject, verb := "предшественник", "закрыт"
				if len(done) > 1 {
					subject, verb = "предшественники", "закрыты"
				}
				unblocked.findings = append(unblocked.findings, finding{is,
					fmt.Sprintf("%s %s %s, а пауза держится %d дн.", subject, hashes(done), verb, quiet)})
			}
		}

		// Номер, которого нет, показываем у любой заявки, а не только у hold:
		// врёт он одинаково, а на снятой паузе его вдобавок некому заметить.
		for _, n := range unknown {
			blockerMissing.findings = append(blockerMissing.findings, finding{is,
				fmt.Sprintf("Blocked-by: #%d — номер ещё не выдавался (последний #%d), похоже на опечатку",
					n, cfg.objects.issuedThrough)})
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

	// Порядок — по убыванию цены молчания. «Разблокировано» сразу за живым
	// человеком: там не молчание, а простой готовой к работе заявки.
	return []bucket{unanswered, unblocked, holdNoPlan, planMissing, blockerMissing, holdStale, decisionStale}
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

// waitingSince — с какого момента автор ждёт нашего ответа.
//
// Ждёт он тогда, когда последним говорил он: сама заявка — уже его сообщение,
// а после нашего ответа мяч у него, и молчание в этом случае наше право, а не
// долг. Отсчёт идёт от ПЕРВОГО его сообщения, оставшегося без ответа, а не от
// последнего: автор, который напоминает о себе, иначе выглядел бы свежим
// (`UpdatedAt` его напоминанием и обновляется) — а ждёт он дольше всех.
//
// waiting=false означает «мяч не у нас».
func waitingSince(is issue, cfg config) (silence int, answered, waiting bool) {
	var lastTeam time.Time
	for _, c := range is.Comments {
		if cfg.team[c.Author.Login] && c.CreatedAt.After(lastTeam) {
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

// blockedBy — номера, названные строкой `Blocked-by: #N` в заголовке, теле или
// комментариях. Места те же, что у planRefs: зависимость чаще всего пишут в
// тело при парковке, но обнаруживается она и позже — тогда её дописывают
// комментарием, и не прочитать его значило бы не увидеть свежую зависимость.
//
// Ссылка заявки на саму себя отбрасывается. Это не выдуманный случай, а
// ловушка: своя же заявка всегда открыта, поэтому такая строка навсегда
// объявила бы паузу честным ожиданием и убрала заявку из «hold без движения» —
// то есть опечатка выключала бы ровно ту проверку, ради которой всё писалось.
func blockedBy(is issue) []int {
	parts := make([]string, 0, len(is.Comments)+2)
	parts = append(parts, is.Title, is.Body)
	for _, c := range is.Comments {
		parts = append(parts, c.Body)
	}

	var out []int
	seen := map[int]bool{}
	for _, text := range parts {
		for _, line := range blockedByRe.FindAllStringSubmatch(withoutMarkdownCodeBlocks(text), -1) {
			for _, m := range blockerNumRe.FindAllStringSubmatch(line[1], -1) {
				n, err := strconv.Atoi(m[1])
				if err != nil || n <= 0 || n == is.Number || seen[n] {
					continue
				}
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Ints(out)
	return out
}

// withoutMarkdownCodeBlocks удаляет содержимое fenced- и отступных блоков
// кода. Внутри них `Blocked-by:` показывает синтаксис, а не объявляет
// зависимость. Fenced-блоки могут быть открыты тремя и более ` или ~; закрывающая
// граница использует тот же символ и не короче открывающей. Незакрытая граница
// по правилам Markdown продолжается до конца текста.
func withoutMarkdownCodeBlocks(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var out strings.Builder
	out.Grow(len(text))
	var fence byte
	var fenceLen int
	inIndented := false
	previousBlank := true

	for i, line := range lines {
		omit := false
		if fence != 0 {
			omit = true
			if isMarkdownFenceClose(line, fence, fenceLen) {
				fence = 0
				fenceLen = 0
				previousBlank = true
			}
		} else if marker, width, ok := markdownFenceOpen(line); ok {
			omit = true
			fence = marker
			fenceLen = width
			inIndented = false
			previousBlank = true
		} else {
			blank := strings.TrimSpace(line) == ""
			indented := isMarkdownIndentedCode(line)
			if inIndented {
				if blank || indented {
					omit = true
				} else {
					inIndented = false
				}
			} else if previousBlank && indented {
				omit = true
				inIndented = true
			}
			if !omit {
				previousBlank = blank
			}
		}

		if !omit {
			out.WriteString(line)
		}
		if i+1 < len(lines) {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func markdownFenceOpen(line string) (byte, int, bool) {
	line = markdownBlockQuoteContent(line)
	indent := 0
	for indent < len(line) && indent < 4 && line[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent == len(line) {
		return 0, 0, false
	}
	line = line[indent:]

	// Fenced-блок может начинаться непосредственно в элементе списка.
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && (line[1] == ' ' || line[1] == '\t') {
		line = strings.TrimLeft(line[2:], " \t")
	}
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return 0, 0, false
	}
	marker := line[0]
	width := 0
	for width < len(line) && line[width] == marker {
		width++
	}
	if width < 3 {
		return 0, 0, false
	}
	// В info string backtick-fence обратная кавычка запрещена Markdown.
	if marker == '`' && strings.ContainsRune(line[width:], '`') {
		return 0, 0, false
	}
	return marker, width, true
}

func isMarkdownFenceClose(line string, marker byte, minimum int) bool {
	line = strings.TrimLeft(markdownBlockQuoteContent(line), " \t")
	width := 0
	for width < len(line) && line[width] == marker {
		width++
	}
	return width >= minimum && strings.Trim(line[width:], " \t") == ""
}

func markdownBlockQuoteContent(line string) string {
	for {
		indent := 0
		for indent < len(line) && indent < 4 && line[indent] == ' ' {
			indent++
		}
		if indent > 3 || indent == len(line) || line[indent] != '>' {
			return line
		}
		line = line[indent+1:]
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
	}
}

func isMarkdownIndentedCode(line string) bool {
	line = markdownBlockQuoteContent(line)
	columns := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return columns >= 4
		}
		if columns >= 4 {
			return true
		}
	}
	return false
}

// objects — открытые заявки и PR плюс достоверный потолок общей нумерации
// issue/PR. Потолок требует одного запроса за последним объектом любого
// состояния, а не отдельного запроса к API на каждую зависимость.
//
// Без issuedThrough «нет среди открытых» означало бы сразу и «закрыт», и «не
// существует», а это разные вещи: закрытый объект может быть новее всех ещё
// открытых, а опечатка не должна выглядеть разблокировкой.
//
// Что список открытых полон, инструмент проверить не может: -limit обрезает и
// заявки, и PR. Про обрезку заявок отчёт предупреждает отдельной строкой —
// в такой прогон «предшественник закрыт» стоит перечитать глазами.
type objects struct {
	open          map[int]bool
	issuedThrough int
}

// classify раскладывает предшественников по состоянию: ещё открыт, уже закрыт,
// номера не видели вовсе.
//
// «Не видели» — это строго больше issuedThrough. Ниже него номер точно был
// выдан; если его нет среди открытых, зависимость больше не ждёт живой объект.
// Закрытие от удаления тут неотличимо, поэтому строка разблокировки всегда
// называет номер — человек сверит необычный случай глазами.
func (o objects) classify(nums []int) (waiting, done, unknown []int) {
	for _, n := range nums {
		switch {
		case o.open[n]:
			waiting = append(waiting, n)
		case n > o.issuedThrough:
			unknown = append(unknown, n)
		default:
			done = append(done, n)
		}
	}
	return waiting, done, unknown
}

func knownObjects(issues []issue, prs []pull, issuedThrough int) objects {
	o := objects{open: map[int]bool{}, issuedThrough: issuedThrough}
	mark := func(n int) {
		o.open[n] = true
		if n > o.issuedThrough {
			o.issuedThrough = n
		}
	}
	for _, is := range issues {
		mark(is.Number)
	}
	for _, pr := range prs {
		mark(pr.Number)
	}
	return o
}

// hashes — «#1204, #1218».
func hashes(nums []int) string {
	parts := make([]string, 0, len(nums))
	for _, n := range nums {
		parts = append(parts, "#"+strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
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

func load(issuesPath, prsPath string, limit int) ([]issue, []pull, int, error) {
	// body обязателен: ссылка на план чаще стоит в теле заявки, чем в
	// комментариях, и без него сверка объявляла такие заявки «без плана».
	raw, err := source(issuesPath, "issue", "list", "--state", "open", "--limit", strconv.Itoa(limit),
		"--json", "number,title,body,url,createdAt,updatedAt,author,labels,comments")
	if err != nil {
		return nil, nil, 0, err
	}
	var issues []issue
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, nil, 0, fmt.Errorf("разбор списка заявок: %w", err)
	}

	prsRaw, err := source(prsPath, "pr", "list", "--state", "open", "--limit", strconv.Itoa(limit),
		"--json", "number,files")
	if err != nil {
		return nil, nil, 0, err
	}
	var prs []pull
	if err := json.Unmarshal(prsRaw, &prs); err != nil {
		return nil, nil, 0, fmt.Errorf("разбор списка PR: %w", err)
	}

	issuedThrough := knownObjects(issues, prs, 0).issuedThrough
	// Явные файлы — автономный тестовый вход: в нём потолок равен наибольшему
	// номеру фикстуры и gh больше не вызывается. Живой прогон отдельно читает
	// самый новый issue/PR любого состояния: их нумерация общая и монотонная.
	if issuesPath == "" && prsPath == "" {
		latestRaw, err := source("", "api", "repos/{owner}/{repo}/issues?state=all&sort=created&direction=desc&per_page=1")
		if err != nil {
			return nil, nil, 0, err
		}
		var latest []struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal(latestRaw, &latest); err != nil {
			return nil, nil, 0, fmt.Errorf("разбор последнего номера issue/PR: %w", err)
		}
		if len(latest) != 1 || latest[0].Number < issuedThrough {
			return nil, nil, 0, fmt.Errorf("последний номер issue/PR не согласован со списками открытых объектов")
		}
		issuedThrough = latest[0].Number
	}

	return issues, prs, issuedThrough, nil
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
