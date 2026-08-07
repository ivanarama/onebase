package ui

// Обработчики страницы «Сообщить об ошибке» (план 116).
//
// Ничего не отправляется по сети: отчёт складывается в текст, пользователь его
// смотрит, правит и скачивает файлом. Такой путь работает в закрытом контуре и
// не требует аккаунта на GitHub — ровно то, из-за чего задача и появилась.

import (
	"net/http"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/bugreport"
	"github.com/ivantit66/onebase/internal/incident"
)

const (
	// reportIncidentsShown — сколько последних ошибок предлагаем в списке.
	reportIncidentsShown = 15
	// Своей константы на строки журнала здесь нет намеренно: Предприятие кладёт
	// в отчёт только след запусков платформы (bugreport.StartupLogLines), а
	// полный журнал базы ведёт лаунчер — он же его и прикладывает.
	//
	// reportMaxTextBytes — предел на присланный текст отчёта. Отчёт правит
	// человек, и мегабайты здесь взяться могут только из чужой шалости.
	reportMaxTextBytes = 1 << 20
)

// incidentView — строка списка инцидентов в форме.
type incidentView struct {
	ID    string
	When  string
	Short string
}

func (s *Server) reportProblemForm(w http.ResponseWriter, r *http.Request) {
	s.renderReportProblem(w, r, reportFormState{})
}

// reportFormState — то, что пользователь уже ввёл. Переживает переход
// «форма → предпросмотр» и обратно.
type reportFormState struct {
	Did        string
	Expected   string
	Got        string
	IncidentID string
	AttachLog  bool
	Preview    string
}

func (s *Server) renderReportProblem(w http.ResponseWriter, r *http.Request, st reportFormState) {
	s.render(w, r, "page-report-problem", map[string]any{
		"Did":        st.Did,
		"Expected":   st.Expected,
		"Got":        st.Got,
		"IncidentID": st.IncidentID,
		"AttachLog":  st.AttachLog,
		"Preview":    st.Preview,
		"Incidents":  s.recentIncidentViews(r),
		// Журнал сервера содержит пути, имена баз и SQL. Обычный пользователь
		// отдаёт отчёт с кодом инцидента — журнал по этому коду достанет
		// администратор. Тот же принцип, что на «О программе».
		"CanAttachLog": s.isAdmin(r),
		"Contacts":     bugreport.PlatformContacts(s.cfg.AppSupport),
	})
}

// recentIncidentViews отдаёт последние ошибки для выпадающего списка.
// Администратор видит все, обычный пользователь — только свои: чужая ошибка
// ему ни о чём не говорит, а её текст может содержать чужие данные.
func (s *Server) recentIncidentViews(r *http.Request) []incidentView {
	if s.incidents == nil {
		return nil
	}
	user := ""
	if !s.isAdmin(r) {
		user = currentUserLogin(r)
	}
	recs := s.incidents.Recent(user, reportIncidentsShown)
	out := make([]incidentView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, incidentView{
			ID:    rec.ID,
			When:  rec.Time.Format("15:04:05"),
			Short: shortenIncidentText(rec.Text),
		})
	}
	return out
}

// shortenIncidentText укорачивает текст ошибки до строки выпадающего списка.
func shortenIncidentText(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	const limit = 70
	if r := []rune(text); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	if text == "" {
		return "—"
	}
	return text
}

func (s *Server) reportProblemPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	st := reportFormState{
		Did:        r.FormValue("did"),
		Expected:   r.FormValue("expected"),
		Got:        r.FormValue("got"),
		IncidentID: strings.TrimSpace(r.FormValue("incident")),
		AttachLog:  r.FormValue("attach_log") == "1",
	}

	in := bugreport.Input{
		Did:          st.Did,
		Expected:     st.Expected,
		Got:          st.Got,
		AppName:      s.cfg.AppName,
		AppVersion:   s.cfg.AppVersion,
		ConfigSource: s.cfg.ConfigSource,
		DBKind:       s.cfg.DatabaseType,
		Contacts:     bugreport.PlatformContacts(s.cfg.AppSupport),
		Now:          time.Now(),
	}
	if st.IncidentID != "" && s.incidents != nil {
		if rec, ok := s.incidents.Get(st.IncidentID); ok && s.maySeeIncident(r, rec) {
			in.Incident = &rec
		}
	}
	// Галочку журнала рисуем только администратору, но проверяем право и здесь:
	// форму можно отправить и мимо страницы.
	if st.AttachLog && s.isAdmin(r) {
		in.LogTail = s.serverLogTail()
	}

	st.Preview = bugreport.Markdown(in)
	s.renderReportProblem(w, r, st)
}

// maySeeIncident не даёт подтянуть в отчёт чужую ошибку по угаданному коду:
// её текст может содержать данные другого пользователя.
func (s *Server) maySeeIncident(r *http.Request, rec incident.Record) bool {
	if s.isAdmin(r) {
		return true
	}
	return rec.User == currentUserLogin(r)
}

// serverLogTail возвращает хвост журнала процесса базы.
//
// Свой журнал процесс базы в файл не пишет — stdout/stderr перенаправляет
// лаунчер (см. launcher.baseLogPath). Поэтому здесь доступен только стартовый
// след: полный журнал базы прикладывается со страницы лаунчера, где он и лежит.
func (s *Server) serverLogTail() string {
	return bugreport.Redact(bugreport.TailFile(bugreport.StartupLogPath(), bugreport.StartupLogLines, 8<<10))
}

func (s *Server) reportProblemDownload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, reportMaxTextBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, s.errText(r, err), http.StatusBadRequest)
		return
	}
	// Отдаём ровно то, что пользователь видел и правил. Никаких подстановок на
	// этом шаге: иначе предпросмотр перестал бы быть гарантией.
	body := r.FormValue("report")
	if strings.TrimSpace(body) == "" {
		http.Error(w, s.tr(s.resolveLang(r), "Пустой отчёт"), http.StatusBadRequest)
		return
	}
	name := bugreport.FileName(time.Now(), "md")
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	writeBody(w, []byte(body))
}
