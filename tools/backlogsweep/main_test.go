package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)

func ago(d int) time.Time { return now.AddDate(0, 0, -d) }

// testPlans — каталог из двух планов: 46 и 155 (155 занят «чужим» файлом,
// ровно как в жизни — на этом ловится сверка по номеру вместо имени).
func testPlans(prFiles ...string) plans {
	p := plans{files: map[string]bool{}, nums: map[int]bool{}, slugs: map[string]string{}, ok: true}
	for _, name := range []string{"46-tablepart-commands-and-picker.md", "155-excel-print-template.md"} {
		p.remember(name)
	}
	for _, f := range prFiles {
		p.remember(f)
	}
	return p
}

func testConfig() config {
	return config{
		now:        now,
		holdDays:   56,
		decideDays: 14,
		replyDays:  7,
		team:       map[string]bool{"ivanarama": true, "ivantit66": true},
		plans:      testPlans(),
	}
}

// mk собирает заявку: автор, дата последнего движения, метки, комментарии.
// Тело задаётся отдельно — через withBody, чтобы не плодить параметры.
func mk(number int, author string, updated time.Time, labels []string, comments ...[2]string) issue {
	var is issue
	is.Number = number
	is.Title = "заявка"
	is.Author.Login = author
	is.CreatedAt = updated
	is.UpdatedAt = updated
	for _, l := range labels {
		is.Labels = append(is.Labels, label{Name: l})
	}
	for _, c := range comments {
		is.Comments = append(is.Comments, comment{
			Author:    user{Login: c[0]},
			Body:      c[1],
			CreatedAt: updated,
		})
	}
	return is
}

func withBody(is issue, body string) issue {
	is.Body = body
	return is
}

// commentAt переставляет дату последнего комментария — нужно там, где важна не
// сама переписка, а кто говорил последним.
func commentAt(is issue, i int, at time.Time) issue {
	is.Comments[i].CreatedAt = at
	if at.After(is.UpdatedAt) {
		is.UpdatedAt = at
	}
	return is
}

func bucketByTitle(buckets []bucket, title string) []finding {
	for _, b := range buckets {
		if b.title == title {
			return b.findings
		}
	}
	return nil
}

func numbers(fs []finding) []int {
	out := make([]int, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.issue.Number)
	}
	return out
}

// details — пояснения находок: там, где у одной заявки их несколько, номер
// заявки в сообщении о падении уже ничего не различает.
func details(fs []finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.detail)
	}
	return out
}

func TestHoldWithoutPlanIsFlagged(t *testing.T) {
	issues := []issue{
		mk(1, "ivanarama", ago(3), []string{"hold"}), // пауза без плана
		mk(2, "ivanarama", ago(3), []string{"hold"}, // ссылка в комментарии
			[2]string{"ivanarama", "делаем планом Plans/46-tablepart-commands-and-picker.md"}),
		// Ссылка в ТЕЛЕ заявки — обычное место, и раньше она не читалась вовсе.
		withBody(mk(3, "ivanarama", ago(3), []string{"hold"}),
			"Подробности — в плане `Plans/46-tablepart-commands-and-picker.md`."),
	}

	got := numbers(bucketByTitle(analyze(issues, testConfig()), "hold без ссылки на план"))

	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("ожидалась только #1, получено %v", got)
	}
}

// Ровно случай #1122: ссылка в теле на 155-nav-collapse.md, а под номером 155
// лежит совсем другой план. Сверка по номеру такую поломку пропускала.
func TestPlanReferenceIsCheckedByFileNameNotNumber(t *testing.T) {
	issues := []issue{
		withBody(mk(1, "ivanarama", ago(1), []string{"hold"}),
			"Подробности — в плане `Plans/155-nav-collapse.md`."),
		withBody(mk(2, "ivanarama", ago(1), []string{"hold"}),
			"Подробности — в плане `Plans/155-excel-print-template.md`."),
	}

	got := bucketByTitle(analyze(issues, testConfig()), "ссылка на план, которого нет")

	if len(got) != 1 || got[0].issue.Number != 1 {
		t.Fatalf("ожидалась только #1, получено %v", numbers(got))
	}
	if !strings.Contains(got[0].detail, "155-nav-collapse.md") {
		t.Fatalf("в пояснении нет имени файла: %q", got[0].detail)
	}
}

// План, написанный в открытом PR, существует — просто ещё не влит. Первый
// прогон инструмента объявил такой план (157 из PR #1133) несуществующим.
func TestPlanFromOpenPullRequestCounts(t *testing.T) {
	cfg := testConfig()
	cfg.plans = testPlans("157-nav-collapse.md")
	issues := []issue{
		withBody(mk(1, "ivanarama", ago(1), []string{"hold"}),
			"план лежит в `Plans/157-nav-collapse.md`, PR ещё открыт"),
		mk(2, "ivanarama", ago(1), []string{"hold"},
			[2]string{"ivanarama", "номер плана — 157, а не 155"}),
	}

	if got := bucketByTitle(analyze(issues, cfg), "ссылка на план, которого нет"); len(got) != 0 {
		t.Fatalf("план из открытого PR ложной находкой быть не должен, получено %v", numbers(got))
	}
}

// Битых ссылок у заявки может быть несколько, и показать надо все: версия с
// `break` отдавала одну находку на заявку, поэтому вторая ссылка всплывала
// только после починки первой — неделей позже.
func TestEveryBrokenPlanReferenceIsReported(t *testing.T) {
	is := withBody(mk(1, "ivanarama", ago(1), []string{"hold"}),
		"обещаны `Plans/900-первый.md` и `Plans/901-второй.md`, оба не написаны")

	got := bucketByTitle(analyze([]issue{is}, testConfig()), "ссылка на план, которого нет")

	if len(got) != 2 {
		t.Fatalf("ожидались две находки, получено %d: %v", len(got), details(got))
	}
	if !strings.Contains(got[0].detail, "900-первый.md") || !strings.Contains(got[1].detail, "901-второй.md") {
		t.Fatalf("ожидались обе ссылки по порядку номеров, получено %v", details(got))
	}
}

// Живой случай #1134: имя плана названо неверно, а следующим комментарием
// поправлено. Находка остаётся (скрытая неотличима от «инструмент не заметил»),
// но помечается — иначе отчёт печатает знакомую строку каждую неделю, и глаз
// перестаёт читать не только её.
func TestPlanReferenceCorrectedLaterInThreadIsMarked(t *testing.T) {
	cfg := testConfig()
	cfg.plans = testPlans("158-open-form.md")
	is := withBody(mk(1, "ivanarama", ago(1), []string{"hold"},
		[2]string{"ivanarama", "поправка: план получил номер 158 — `Plans/158-open-form.md`"}),
		"работа оформлена планом `Plans/157-open-form.md`")

	got := bucketByTitle(analyze([]issue{is}, cfg), "ссылка на план, которого нет")

	if len(got) != 1 {
		t.Fatalf("ожидалась одна находка, получено %v", details(got))
	}
	if !strings.Contains(got[0].detail, "157-open-form.md") ||
		!strings.Contains(got[0].detail, "ниже в треде уже поправлено на Plans/158-open-form.md") {
		t.Fatalf("ожидалась пометка о поправке ниже, получено %q", got[0].detail)
	}
}

// Порядок — часть смысла: та же пара ссылок, но верная названа ВЫШЕ, а
// неверная дописана после неё. Это не поправка, а свежая опечатка.
func TestCorrectionAboveTheBrokenReferenceIsNotAMark(t *testing.T) {
	cfg := testConfig()
	cfg.plans = testPlans("158-open-form.md")
	is := withBody(mk(1, "ivanarama", ago(1), []string{"hold"},
		[2]string{"ivanarama", "напоминаю: делаем по `Plans/157-open-form.md`"}),
		"работа оформлена планом `Plans/158-open-form.md`")

	got := bucketByTitle(analyze([]issue{is}, cfg), "ссылка на план, которого нет")

	if len(got) != 1 || strings.Contains(got[0].detail, "поправлено") {
		t.Fatalf("ожидалась непомеченная находка на 157, получено %v", details(got))
	}
}

// Ссылка, повторённая НИЖЕ поправки, снова актуальна: считаем по последнему
// упоминанию, а не по первому.
func TestBrokenReferenceRepeatedAfterCorrectionStaysUnmarked(t *testing.T) {
	cfg := testConfig()
	cfg.plans = testPlans("158-open-form.md")
	is := withBody(mk(1, "ivanarama", ago(1), []string{"hold"},
		[2]string{"ivanarama", "поправка: `Plans/158-open-form.md`"},
		[2]string{"ivanarama", "сводка: делаем по `Plans/157-open-form.md`"}),
		"работа оформлена планом `Plans/157-open-form.md`")

	got := bucketByTitle(analyze([]issue{is}, cfg), "ссылка на план, которого нет")

	if len(got) != 1 || strings.Contains(got[0].detail, "поправлено") {
		t.Fatalf("ожидалась непомеченная находка на 157, получено %v", details(got))
	}
}

// Ссылка одним номером опознанию не поддаётся: «план 46» ниже «плана 900» не
// значит, что это тот же план под новым номером.
func TestNumberOnlyReferenceIsNeverConsideredCorrected(t *testing.T) {
	is := withBody(mk(1, "ivanarama", ago(1), []string{"hold"},
		[2]string{"ivanarama", "точнее, план 46"}), "оформлено планом 900")

	got := bucketByTitle(analyze([]issue{is}, testConfig()), "ссылка на план, которого нет")

	if len(got) != 1 || strings.Contains(got[0].detail, "поправлено") {
		t.Fatalf("ожидалась непомеченная находка на план 900, получено %v", details(got))
	}
}

// Отчёт — публичный вывод инструмента: обе находки одной заявки обязаны дойти
// до строк, а не схлопнуться по номеру заявки.
func TestReportPrintsBothBrokenReferencesOfOneIssue(t *testing.T) {
	var buf bytes.Buffer
	issues := []issue{withBody(mk(9, "ivanarama", ago(1), []string{"hold"}),
		"обещаны `Plans/900-первый.md` и `Plans/901-второй.md`")}

	report(&buf, analyze(issues, testConfig()), 1, false)

	out := buf.String()
	if strings.Count(out, "#9") != 2 {
		t.Fatalf("ожидались две строки про #9:\n%s", out)
	}
	if !strings.Contains(out, "ссылка на план, которого нет — 2") {
		t.Fatalf("счётчик корзины не сошёлся с числом строк:\n%s", out)
	}
}

// Порядок комментариев в выдаче gh нигде не обещан, а «поправлено ниже в треде»
// держится ровно на нём: те же два комментария, поданные в обратном порядке,
// обязаны читаться как переписка, а не как есть.
func TestCommentsAreOrderedByCreationNotByDelivery(t *testing.T) {
	cfg := testConfig()
	cfg.plans = testPlans("158-open-form.md")
	// В слайсе — новый комментарий первым, старый вторым.
	is := mk(1, "ivanarama", ago(1), []string{"hold"},
		[2]string{"ivanarama", "поправка: план получил номер 158 — `Plans/158-open-form.md`"},
		[2]string{"ivanarama", "работа оформлена планом `Plans/157-open-form.md`"})
	is = commentAt(is, 0, ago(1))
	is = commentAt(is, 1, ago(5))

	got := bucketByTitle(analyze([]issue{is}, cfg), "ссылка на план, которого нет")

	if len(got) != 1 {
		t.Fatalf("ожидалась одна находка, получено %v", details(got))
	}
	if !strings.Contains(got[0].detail, "ниже в треде уже поправлено на Plans/158-open-form.md") {
		t.Fatalf("порядок комментариев взят из выдачи, а не по createdAt: %q", got[0].detail)
	}
}

// Потолок строк на заявку: десяток планов в одной заявке иначе занимает экран
// отчёта, который чинили ради читаемости. Обрезка обязана называться вслух —
// счётчик корзины при этом считает все находки, а не показанные.
func TestReportCapsLinesPerIssueAndSaysHowManyAreHidden(t *testing.T) {
	var buf bytes.Buffer
	issues := []issue{withBody(mk(9, "ivanarama", ago(1), []string{"hold"}),
		"обещаны `Plans/900-первый.md`, `Plans/901-второй.md`, `Plans/902-третий.md`, "+
			"`Plans/903-четвёртый.md` и `Plans/904-пятый.md`")}

	report(&buf, analyze(issues, testConfig()), 1, false)

	out := buf.String()
	if !strings.Contains(out, "ссылка на план, которого нет — 5") {
		t.Fatalf("счётчик корзины обязан считать все находки, а не показанные:\n%s", out)
	}
	for _, want := range []string{"900-первый.md", "901-второй.md", "902-третий.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("первые %d находок обязаны печататься, нет %q:\n%s", maxPerIssue, want, out)
		}
	}
	for _, hidden := range []string{"903-четвёртый.md", "904-пятый.md"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("сверх потолка ожидалась сводка, а не строка %q:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, "и ещё 2 — покажутся после починки первых 3") {
		t.Fatalf("обрезка не названа вслух:\n%s", out)
	}
}

// Потолок считается по заявке, а не по корзине: соседняя заявка своё получает
// целиком, иначе одна многословная вытесняла бы из отчёта остальные.
func TestPerIssueCapDoesNotEatNeighbouringIssues(t *testing.T) {
	var buf bytes.Buffer
	issues := []issue{
		withBody(mk(9, "ivanarama", ago(1), []string{"hold"}),
			"обещаны `Plans/900-первый.md`, `Plans/901-второй.md`, `Plans/902-третий.md`, `Plans/903-четвёртый.md`"),
		withBody(mk(10, "ivanarama", ago(1), []string{"hold"}), "обещан `Plans/905-шестой.md`"),
	}

	report(&buf, analyze(issues, testConfig()), 2, false)

	out := buf.String()
	if !strings.Contains(out, "905-шестой.md") {
		t.Fatalf("находка соседней заявки съедена потолком:\n%s", out)
	}
	if !strings.Contains(out, "и ещё 1 — покажутся после починки первых 3") {
		t.Fatalf("обрезка первой заявки не названа вслух:\n%s", out)
	}
}

func TestPlanCheckIsOffWithoutPlansDirectory(t *testing.T) {
	cfg := testConfig()
	cfg.plans = plans{} // каталог не прочитан: запуск не из корня репозитория
	issues := []issue{withBody(mk(1, "ivanarama", ago(1), []string{"hold"}),
		"`Plans/900-выдуманный.md`")}

	if got := bucketByTitle(analyze(issues, cfg), "ссылка на план, которого нет"); len(got) != 0 {
		t.Fatalf("без каталога планов проверка обязана молчать, получено %v", numbers(got))
	}
}

func TestExternalIssueWithoutAnswer(t *testing.T) {
	// Напоминание автора: заявка заведена 60 дней назад, ответа не было ни
	// одного, а вчера автор написал сам. UpdatedAt при этом свежий — прежняя
	// версия теряла ровно того, кто виднее всего ждёт.
	reminder := commentAt(mk(6, "boffik", ago(60), nil, [2]string{"boffik", "есть новости?"}), 0, ago(1))
	// Ответили последними — мяч у автора, молчание не наш долг.
	replied := commentAt(mk(2, "boffik", ago(10), nil, [2]string{"ivanarama", "ответ есть"}), 0, ago(9))

	issues := []issue{
		mk(1, "boffik", ago(10), nil), // внешний, тишина
		replied,                       // ответили
		mk(3, "boffik", ago(2), nil),  // молчим, но недолго
		mk(4, "boffik", ago(10), nil, [2]string{"boffik", "дописал сам"}), // сам себе не ответ
		mk(5, "ivanarama", ago(30), nil),                                  // свой — не считаем
		reminder,
	}

	got := bucketByTitle(analyze(issues, testConfig()), "внешняя заявка без ответа")

	if want := []int{1, 4, 6}; len(got) != 3 || numbers(got)[0] != want[0] ||
		numbers(got)[1] != want[1] || numbers(got)[2] != want[2] {
		t.Fatalf("ожидались %v, получено %v", want, numbers(got))
	}
	for _, f := range got {
		if !strings.Contains(f.detail, "ответа не было ни разу") {
			t.Fatalf("#%d: ожидалось «ответа не было ни разу», получено %q", f.issue.Number, f.detail)
		}
	}
	if !strings.Contains(got[2].detail, "ждёт 60 дн.") {
		t.Fatalf("напоминание автора обнулило счётчик ожидания: %q", got[2].detail)
	}
}

// Отвечали, автор спросил снова — ждёт он с момента своего вопроса, а не с
// заведения заявки и не с нашего ответа.
func TestSilenceCountsFromTheUnansweredQuestion(t *testing.T) {
	is := mk(1, "boffik", ago(90), nil,
		[2]string{"ivanarama", "разбираемся"}, [2]string{"boffik", "а теперь как?"})
	is = commentAt(is, 0, ago(60))
	is = commentAt(is, 1, ago(30))

	got := bucketByTitle(analyze([]issue{is}, testConfig()), "внешняя заявка без ответа")

	if len(got) != 1 {
		t.Fatalf("ожидалась одна находка, получено %v", numbers(got))
	}
	if !strings.Contains(got[0].detail, "ждёт 30 дн.") || !strings.Contains(got[0].detail, "с последнего вопроса") {
		t.Fatalf("ожидание посчитано не от вопроса автора: %q", got[0].detail)
	}
}

func TestStaleDecisionIgnoresApprovedAndHold(t *testing.T) {
	issues := []issue{
		mk(1, "ivanarama", ago(30), []string{"needs-decision"}),
		// approved старше needs-decision: человек уже сходил.
		mk(2, "ivanarama", ago(30), []string{"needs-decision", "approved"}),
		// hold — решение принято, у паузы своя корзина.
		withBody(mk(3, "ivanarama", ago(30), []string{"needs-decision", "hold"}),
			"`Plans/46-tablepart-commands-and-picker.md`"),
		mk(4, "ivanarama", ago(3), []string{"needs-decision"}), // свежая
	}

	got := numbers(bucketByTitle(analyze(issues, testConfig()), "needs-decision без движения"))

	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("ожидалась только #1, получено %v", got)
	}
}

func TestStaleHoldNeedsSilence(t *testing.T) {
	body := "`Plans/46-tablepart-commands-and-picker.md`"
	issues := []issue{
		withBody(mk(1, "ivanarama", ago(60), []string{"hold"}), body),
		withBody(mk(2, "ivanarama", ago(10), []string{"hold"}), body),
	}

	got := numbers(bucketByTitle(analyze(issues, testConfig()), "hold без движения"))

	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("ожидалась только #1, получено %v", got)
	}
}

func TestReportSaysNothingWhenClean(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, analyze([]issue{mk(1, "ivanarama", ago(1), nil)}, testConfig()), 1, false)

	if !strings.Contains(buf.String(), "хвост чист") {
		t.Fatalf("ожидался чистый отчёт, получено: %s", buf.String())
	}
}

func TestReportPrintsAdviceWithFindings(t *testing.T) {
	var buf bytes.Buffer
	issues := []issue{mk(7, "boffik", ago(30), []string{"hold"})}
	report(&buf, analyze(issues, testConfig()), 1, false)

	out := buf.String()
	for _, want := range []string{"#7", "внешняя заявка без ответа", "hold без ссылки на план", "автор ждёт"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в отчёте нет %q:\n%s", want, out)
		}
	}
}

// Обрезка по -limit молча превращает «из N открытых» в неправду.
func TestReportWarnsAboutTruncation(t *testing.T) {
	var buf bytes.Buffer
	report(&buf, analyze([]issue{mk(1, "ivanarama", ago(1), nil)}, testConfig()), 1, true)

	if !strings.Contains(buf.String(), "-limit") {
		t.Fatalf("предупреждения об обрезке нет:\n%s", buf.String())
	}
}

func TestPlanRefsReadTitleBodyAndComments(t *testing.T) {
	is := withBody(mk(1, "ivanarama", ago(1), nil,
		[2]string{"ivanarama", "а ещё см. план 46"}), "тело со ссылкой `Plans/155-nav-collapse.md`")
	is.Title = "заголовок про план 900"

	got := planRefs(is)

	if len(got) != 3 {
		t.Fatalf("ожидались три ссылки (46, 155-файл, 900), получено %+v", got)
	}
	if got[0].Num != 46 || got[0].File != "" {
		t.Fatalf("46 должен быть ссылкой номером: %+v", got[0])
	}
	if got[1].File != "155-nav-collapse.md" {
		t.Fatalf("155 должен быть ссылкой файлом: %+v", got[1])
	}
	if got[2].Num != 900 {
		t.Fatalf("900 из заголовка потерян: %+v", got)
	}
}

func TestDaysNeverGoesNegative(t *testing.T) {
	if got := days(now, now.AddDate(0, 0, 3)); got != 0 {
		t.Fatalf("время в будущем должно давать 0, получено %d", got)
	}
}

// Перенумерация плана — обычное дело при коллизии номеров: ссылка осталась
// старая, файл лежит под новым номером. Это не «плана нет», и починка другая.
func TestRenamedPlanIsReportedAsMovedNotMissing(t *testing.T) {
	cfg := testConfig()
	cfg.plans = testPlans("157-nav-collapse.md")
	is := withBody(mk(1, "ivanarama", ago(1), []string{"hold"}),
		"Подробности — в плане `Plans/155-nav-collapse.md`.")

	got := bucketByTitle(analyze([]issue{is}, cfg), "ссылка на план, которого нет")

	if len(got) != 1 {
		t.Fatalf("ожидалась одна находка, получено %v", numbers(got))
	}
	if !strings.Contains(got[0].detail, "устарела") || !strings.Contains(got[0].detail, "157-nav-collapse.md") {
		t.Fatalf("ожидалось «номер переехал», получено %q", got[0].detail)
	}
}
