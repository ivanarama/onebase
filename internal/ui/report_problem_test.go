package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/bugreport"
	"github.com/ivantit66/onebase/internal/incident"
)

func renderReportProblem(t *testing.T, data map[string]any) string {
	t.Helper()
	base := map[string]any{
		"Lang": "ru", "IsAdmin": false,
		"Did": "", "Expected": "", "Got": "", "IncidentID": "", "AttachLog": false,
		"Preview": "", "Incidents": []incidentView(nil),
		"CanAttachLog": false, "Contacts": bugreport.Contacts{},
		"Cfg": Config{},
	}
	for k, v := range data {
		base[k] = v
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-report-problem", base); err != nil {
		t.Fatalf("render page-report-problem: %v", err)
	}
	return buf.String()
}

func TestReportProblem_RegularUserHasNoLogCheckbox(t *testing.T) {
	html := renderReportProblem(t, map[string]any{"CanAttachLog": false})
	if strings.Contains(html, "attach_log") {
		t.Error("обычному пользователю галочка журнала показываться не должна")
	}
	// Форма при этом полноценная — жалуется как раз тот, у кого прав нет.
	for _, want := range []string{"Что вы делали", "Что вы ожидали", "Что получилось", "Сформировать отчёт"} {
		if !strings.Contains(html, want) {
			t.Errorf("в форме нет %q", want)
		}
	}
}

func TestReportProblem_AdminHasLogCheckbox(t *testing.T) {
	html := renderReportProblem(t, map[string]any{"CanAttachLog": true})
	if !strings.Contains(html, "attach_log") {
		t.Error("администратору галочка журнала нужна")
	}
}

func TestReportProblem_IncidentListAndFallback(t *testing.T) {
	withList := renderReportProblem(t, map[string]any{
		"Incidents": []incidentView{{ID: "E-3F7A2C", When: "14:32:11", Short: "no such column: цена"}},
	})
	for _, want := range []string{"E-3F7A2C", "14:32:11", "no such column: цена", "<select"} {
		if !strings.Contains(withList, want) {
			t.Errorf("в списке инцидентов нет %q", want)
		}
	}

	// Пустой список — поле для кода с экрана, а не тупик.
	empty := renderReportProblem(t, nil)
	if strings.Contains(empty, "<select") {
		t.Error("пустой список не должен рисовать выпадашку")
	}
	if !strings.Contains(empty, `name="incident"`) {
		t.Error("без списка нужно поле для кода инцидента с экрана")
	}
}

func TestReportProblem_PreviewIsEditableAndWarns(t *testing.T) {
	html := renderReportProblem(t, map[string]any{"Preview": "# отчёт\nno such column"})
	if !strings.Contains(html, `<textarea id="ob-report-text" name="report"`) {
		t.Error("предпросмотр должен быть редактируемым — это единственная защита от утечки")
	}
	if !strings.Contains(html, "Проверьте текст") {
		t.Error("нет предупреждения о проверке текста перед отправкой")
	}
	// Никаких обещаний отправки.
	for _, forbidden := range []string{">Отправить<", "Отправить отчёт"} {
		if strings.Contains(html, forbidden) {
			t.Errorf("кнопка %q обманывает: платформа ничего не отправляет", forbidden)
		}
	}
	if !strings.Contains(html, "Скачать файл") || !strings.Contains(html, "Скопировать текст") {
		t.Error("в предпросмотре должны быть «Скачать» и «Скопировать»")
	}
}

func TestReportProblem_ContactsBlock(t *testing.T) {
	html := renderReportProblem(t, map[string]any{
		"Preview":  "# отчёт",
		"Contacts": bugreport.Contacts{App: "help@trade.ru", IssuesURL: "https://github.com/x/y/issues/new"},
	})
	if !strings.Contains(html, "help@trade.ru") {
		t.Error("контакт поддержки конфигурации не показан")
	}
	if !strings.Contains(html, "нужен аккаунт GitHub") {
		t.Error("про аккаунт для трекера надо предупреждать честно")
	}

	// Контактов нет — блока тоже нет.
	bare := renderReportProblem(t, map[string]any{"Preview": "# отчёт"})
	if strings.Contains(bare, "Куда отправить") {
		t.Error("без контактов блок «Куда отправить» не нужен")
	}
}

func TestAbout_HasReportProblemButton(t *testing.T) {
	html := renderAbout(t, Config{}, false)
	if !strings.Contains(html, "/ui/report-problem") {
		t.Error("с «О программе» должен быть переход в «Сообщить об ошибке»")
	}
}

func TestShortenIncidentText(t *testing.T) {
	if got := shortenIncidentText(""); got != "—" {
		t.Errorf("пустой текст = %q, ожидался прочерк", got)
	}
	if got := shortenIncidentText("первая\nвторая"); got != "первая вторая" {
		t.Errorf("перевод строки должен схлопываться: %q", got)
	}
	long := strings.Repeat("я", 100)
	got := shortenIncidentText(long)
	if r := []rune(got); len(r) != 71 { // 70 символов + многоточие
		t.Errorf("длинный текст обрезан до %d символов", len(r))
	}
}

func TestReportProblemDownload_ReturnsExactlyWhatUserEdited(t *testing.T) {
	s := &Server{cfg: Config{Lang: "ru"}}
	edited := "# мой отчёт\nбез лишнего\n"
	form := url.Values{"report": []string{edited}}
	req := httptest.NewRequest("POST", "/ui/report-problem/download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.reportProblemDownload(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("код ответа %d", rr.Code)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") ||
		!strings.Contains(cd, "onebase-report-") || !strings.HasSuffix(cd, `.md"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if rr.Body.String() != edited {
		t.Errorf("сервер подменил отредактированный текст:\n%q", rr.Body.String())
	}
}

func TestReportProblemDownload_RejectsEmpty(t *testing.T) {
	s := &Server{cfg: Config{Lang: "ru"}}
	form := url.Values{"report": []string{"   \n "}}
	req := httptest.NewRequest("POST", "/ui/report-problem/download", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	s.reportProblemDownload(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("пустой отчёт должен давать 400, получено %d", rr.Code)
	}
}

// Связка «зарегистрированный инцидент → строка выпадающего списка»: между
// хранилищем и шаблоном есть слой представления, и ошибка в нём оставила бы
// пользователя без единственного способа сослаться на сбой.
func TestRecentIncidentViews_MapsStoreToForm(t *testing.T) {
	s := &Server{cfg: Config{}, incidents: incident.NewStore(10), authRepo: &auth.Repo{}}
	rec := s.incidents.Record(incident.Record{
		Kind:  incident.KindError,
		Where: "POST /ui/doc/заказ/new",
		Text:  "no such column: цена",
		User:  "ivanov",
	})
	s.incidents.Record(incident.Record{Text: "чужая ошибка", User: "petrov"})

	req := httptest.NewRequest("GET", "/ui/report-problem", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "u1", Login: "ivanov"}))

	views := s.recentIncidentViews(req)
	if len(views) != 1 {
		t.Fatalf("пользователю показано %d инцидентов, ожидался один свой", len(views))
	}
	if views[0].ID != rec.ID {
		t.Errorf("код инцидента = %q, ожидался %q", views[0].ID, rec.ID)
	}
	if views[0].Short != "no such column: цена" {
		t.Errorf("текст в списке = %q", views[0].Short)
	}
	if views[0].When == "" {
		t.Error("в списке нет времени инцидента")
	}

	// Администратор видит все.
	admin := httptest.NewRequest("GET", "/ui/report-problem", nil)
	admin = admin.WithContext(auth.ContextWithUser(admin.Context(), &auth.User{ID: "a", Login: "root", IsAdmin: true}))
	if n := len(s.recentIncidentViews(admin)); n != 2 {
		t.Errorf("администратору показано %d инцидентов, ожидались оба", n)
	}

	// Хранилища нет (Server собран литералом в тестах) — не падаем.
	if v := (&Server{}).recentIncidentViews(req); v != nil {
		t.Error("без хранилища список должен быть пустым")
	}
}

func TestMaySeeIncident_ForeignIncidentHidden(t *testing.T) {
	// Код инцидента короткий, и подобрать чужой можно перебором: текст чужой
	// ошибки в отчёт попадать не должен.
	//
	// authRepo обязателен: без него isAdmin отвечает «да» всем (однопользовательский
	// режим), и проверка прав в тесте была бы фиктивной.
	s := &Server{cfg: Config{}, incidents: incident.NewStore(10), authRepo: &auth.Repo{}}
	req := httptest.NewRequest("GET", "/ui/report-problem", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), &auth.User{ID: "u1", Login: "ivanov"}))

	if s.maySeeIncident(req, incident.Record{ID: "E-000001", User: "petrov", Time: time.Now()}) {
		t.Error("пользователь не должен видеть чужой инцидент")
	}
	if !s.maySeeIncident(req, incident.Record{ID: "E-000002", User: "ivanov", Time: time.Now()}) {
		t.Error("свой инцидент должен быть доступен")
	}

	admin := httptest.NewRequest("GET", "/ui/report-problem", nil)
	admin = admin.WithContext(auth.ContextWithUser(admin.Context(), &auth.User{ID: "a", Login: "root", IsAdmin: true}))
	if !s.maySeeIncident(admin, incident.Record{ID: "E-000001", User: "petrov"}) {
		t.Error("администратор должен видеть любой инцидент")
	}
}
