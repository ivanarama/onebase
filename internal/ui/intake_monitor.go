package ui

// UI-монитор входной приёмки (план 90, заход 1/90B): /ui/admin/intake. По каждому
// шлюзу — счётчики (обработано/карантин) и список ОТКРЫТЫХ записей карантина с
// кнопкой «Повторить» (replay через движковый intake.Replay). Только администратор.
// Числа обработано/карантин — «своя сторона» сверки (CC-INT-007): оператор
// сопоставляет их с источником.

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/ivantit66/onebase/internal/intake"
	"github.com/ivantit66/onebase/internal/metadata"
)

type intakeDLQView struct {
	ID        string
	Key       string
	Reason    string
	Error     string
	Attempts  int
	When      string
	Corr      string
	CanReplay bool
}

type intakeView struct {
	Name        string
	Endpoint    string
	Transport   string
	Auth        string
	Processed   int
	Quarantined int
	OpenDLQ     int
	DLQ         []intakeDLQView
	WS          *wsStatusView // только transport: ws (план 120C)
}

// wsStatusView — состояние исходящего WS-соединения шлюза для монитора.
type wsStatusView struct {
	URL           string
	Connected     bool
	Blocked       string // непустая — причина блокировки предохранителем
	Since         string
	LastMessage   string
	LastError     string
	Reconnects    int64
	Received      int64
	HandlerErrors int64
	Sent          int64
	SendErrors    int64
}

var intakeMonitorTmpl = template.Must(template.New("intake-monitor").Parse(tplIntakeMonitor))

func (s *Server) intakeMonitor(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	ctx := r.Context()
	var views []intakeView
	for _, in := range s.reg.Intakes() {
		stats, _ := s.store.IntakeLogStats(ctx, in.Name)
		dlq, _ := s.store.ListIntakeDLQ(ctx, in.Name, true, 200)
		v := intakeView{
			Name: in.Name, Endpoint: in.Endpoint, Transport: in.Transport, Auth: in.Auth,
			Processed: stats.Processed, Quarantined: stats.Quarantined, OpenDLQ: stats.OpenDLQ,
		}
		if in.Transport == metadata.IntakeTransportWS {
			ws := &wsStatusView{URL: in.URL}
			if client := s.wsIntakeClient(in.Name); client != nil {
				st := client.Status()
				ws.Connected = st.Connected
				ws.Blocked = st.BlockedReason
				if !st.ConnectedSince.IsZero() {
					ws.Since = st.ConnectedSince.Format("2006-01-02 15:04")
				}
				if !st.LastMessageAt.IsZero() {
					ws.LastMessage = st.LastMessageAt.Format("2006-01-02 15:04:05")
				}
				ws.LastError = st.LastError
				ws.Reconnects = st.Reconnects
				ws.Received = st.Received
				ws.HandlerErrors = st.HandlerErrors
				ws.Sent = st.Sent
				ws.SendErrors = st.SendErrors
			}
			v.WS = ws
		}
		for _, e := range dlq {
			v.DLQ = append(v.DLQ, intakeDLQView{
				ID: e.ID, Key: e.Key, Reason: e.Reason, Error: e.Error, Attempts: e.Attempts,
				When: formatEpoch(e.QuarantinedAt), Corr: e.CorrelationID,
				CanReplay: e.Reason == metadata.DLQHandlerError,
			})
		}
		views = append(views, v)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderTemplate(w, intakeMonitorTmpl, "intake-monitor", map[string]any{
		"Intakes": views,
		"Msg":     r.URL.Query().Get("msg"),
		"Err":     r.URL.Query().Get("err"),
	})
}

func (s *Server) intakeMonitorReplay(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	ctx := r.Context()
	in := s.reg.GetIntake(r.FormValue("intake"))
	if in == nil {
		s.intakeMonitorRedirect(w, r, "", "шлюз не найден")
		return
	}
	dlqID := r.FormValue("dlq_id")
	if dlqID == "" {
		s.intakeMonitorRedirect(w, r, "", "не указана запись карантина")
		return
	}
	entry, found, err := s.store.GetIntakeDLQ(ctx, in.Name, dlqID)
	if err != nil {
		s.intakeMonitorRedirect(w, r, "", s.errText(r, err))
		return
	}
	if !found {
		s.intakeMonitorRedirect(w, r, "", "запись карантина не найдена")
		return
	}
	handler, err := s.newIntakeHandler(in)
	if err != nil {
		s.intakeMonitorRedirect(w, r, "", err.Error())
		return
	}
	res, err := intake.New(s.store).Replay(ctx, in, handler, dlqID)
	if err != nil {
		s.intakeMonitorRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditIntake(ctx, r, "intake.replay", in.Name, entry.Key, string(res.Status), entry.CorrelationID,
		map[string]any{"ref": res.ResultRef, "dlq_id": entry.ID})
	if res.Status == intake.StatusAccepted || res.Status == intake.StatusDuplicate {
		s.intakeMonitorRedirect(w, r, fmt.Sprintf("Повтор %q: %s (%s)", entry.Key, res.Status, res.ResultRef), "")
	} else {
		s.intakeMonitorRedirect(w, r, "", fmt.Sprintf("Повтор %q: %s (%s) — событие снова в карантине",
			entry.Key, res.Status, res.Reason))
	}
}

func (s *Server) intakeMonitorRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	u := "/ui/admin/intake"
	q := url.Values{}
	if msg != "" {
		q.Set("msg", msg)
	}
	if errMsg != "" {
		q.Set("err", errMsg)
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	http.Redirect(w, r, u, http.StatusFound)
}

// formatEpoch форматирует epoch-секунды в читаемую дату; 0 → "—".
func formatEpoch(sec int64) string {
	if sec == 0 {
		return "—"
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04")
}

const tplIntakeMonitor = `{{define "intake-monitor"}}` + adminHead + `
<main>
<h2>Входная приёмка</h2>
{{if .Msg}}<div style="background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:960px">✓ {{.Msg}}</div>{{end}}
{{if .Err}}<div class="error" style="max-width:960px;margin-bottom:16px">{{.Err}}</div>{{end}}
{{if not .Intakes}}
<div class="card" style="max-width:960px"><p class="empty">Входные шлюзы не настроены. Добавьте <code>intake/&lt;имя&gt;.yaml</code> в конфигурацию.</p></div>
{{end}}
{{range .Intakes}}
{{$intake := .Name}}
<div class="card" style="margin-bottom:20px;max-width:960px">
  <div class="row-top">
    <h3 style="font-size:18px;color:#1e293b">{{.Name}}</h3>
    <span style="color:#64748b;font-size:13px"><code>{{.Transport}}</code> {{if .WS}}{{.WS.URL}}{{else}}{{.Endpoint}}{{end}} · auth: {{.Auth}}</span>
  </div>
  <p style="font-size:13px;color:#475569;margin:6px 0 14px">
    Обработано: <b>{{.Processed}}</b> · В карантине: <b style="color:{{if .OpenDLQ}}#b45309{{else}}#15803d{{end}}">{{.OpenDLQ}}</b>
  </p>
  {{with .WS}}
  <p style="font-size:13px;color:#475569;margin:-8px 0 14px">
    Соединение:
    {{if .Connected}}<b style="color:#15803d">подключено</b>{{if .Since}} с {{.Since}}{{end}}
    {{else if .Blocked}}<b style="color:#b45309">заблокировано</b> <span style="color:#64748b">({{.Blocked}})</span>
    {{else}}<b style="color:#b91c1c">разорвано</b>{{if .LastError}} <span style="color:#64748b" title="{{.LastError}}">— {{.LastError}}</span>{{end}}{{end}}
    · Последнее сообщение: {{if .LastMessage}}{{.LastMessage}}{{else}}—{{end}}
    · Реконнектов: {{.Reconnects}} · Принято: {{.Received}}{{if .HandlerErrors}} · <span style="color:#b45309">Не принято: {{.HandlerErrors}}</span>{{end}}
    · Отправлено: {{.Sent}}{{if .SendErrors}} · <span style="color:#b45309">Ошибок отправки: {{.SendErrors}}</span>{{end}}
  </p>
  {{end}}
  {{if .DLQ}}
  <table>
  <thead><tr>
    <th>Ключ</th><th>Причина</th><th>Ошибка</th><th style="text-align:center">Попыток</th><th>Когда</th><th></th>
  </tr></thead>
  <tbody>
  {{range .DLQ}}<tr>
    <td><b>{{.Key}}</b></td>
    <td style="color:#b45309">{{.Reason}}</td>
    <td style="color:#64748b;font-size:12px;max-width:280px;overflow:hidden;text-overflow:ellipsis" title="{{.Error}}">{{.Error}}</td>
    <td style="text-align:center">{{.Attempts}}</td>
    <td style="color:#94a3b8;font-size:12px">{{.When}}</td>
    <td style="text-align:right">
      {{if .CanReplay}}
      <form method="POST" action="/ui/admin/intake/replay" style="margin:0" onsubmit="return confirm('Повторить обработку события {{.Key}}?')">
        <input type="hidden" name="intake" value="{{$intake}}">
        <input type="hidden" name="dlq_id" value="{{.ID}}">
        <button class="btn btn-sm btn-primary" type="submit">Повторить</button>
      </form>
      {{else}}<span style="color:#94a3b8;font-size:12px">Требует разбора</span>{{end}}
    </td>
  </tr>{{end}}
  </tbody>
  </table>
  {{else}}<p class="empty" style="margin:0">Карантин пуст.</p>{{end}}
</div>
{{end}}
</main></body></html>
{{end}}`
