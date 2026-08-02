package ui

// UI-монитор обмена данными (план 86, фаза 2): /ui/admin/exchange. Показывает
// планы обмена, текущий узел базы, по каждому узлу — глубину очереди и счётчики
// сообщений; для онлайн-узлов (задан url + токен) — кнопку «Синхронизировать».

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/ivantit66/onebase/internal/exchange"
)

type exchangeNodeView struct {
	Code   string
	Name   string
	IsThis bool
	Online bool // есть url и токен, и это не текущий узел
	Queue  int64
	Sent   int64
	Ack    int64
	Recv   int64
}

type exchangePlanView struct {
	Name     string
	Conflict string
	ThisNode string
	HasToken bool
	Nodes    []exchangeNodeView
}

var exchangeMonitorTmpl = template.Must(template.New("exchange-monitor").Parse(tplExchangeMonitor))

func (s *Server) exchangeMonitor(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	ctx := r.Context()
	var plans []exchangePlanView
	for _, plan := range s.reg.ExchangePlans() {
		thisNode, _ := s.store.GetExchangeThisNode(ctx, plan.Name)
		token, _ := s.store.GetExchangeToken(ctx, plan.Name)
		counts, _ := s.store.ExchangePendingCounts(ctx, plan.Name)
		pv := exchangePlanView{Name: plan.Name, Conflict: plan.Conflict, ThisNode: thisNode, HasToken: token != ""}
		for _, n := range plan.Nodes {
			peer, _ := s.store.GetExchangePeer(ctx, plan.Name, n.Code)
			isThis := thisNode != "" && strings.EqualFold(n.Code, thisNode)
			pv.Nodes = append(pv.Nodes, exchangeNodeView{
				Code: n.Code, Name: n.Name, IsThis: isThis,
				Online: !isThis && strings.TrimSpace(n.URL) != "" && token != "",
				Queue:  counts[n.Code], Sent: peer.SentNo, Ack: peer.AckNo, Recv: peer.RecvNo,
			})
		}
		plans = append(plans, pv)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderTemplate(w, exchangeMonitorTmpl, "exchange-monitor", map[string]any{
		"Plans": plans,
		"Msg":   r.URL.Query().Get("msg"),
		"Err":   r.URL.Query().Get("err"),
	})
}

func (s *Server) exchangeMonitorSync(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	ctx := r.Context()
	plan := s.reg.GetExchangePlan(r.FormValue("plan"))
	if plan == nil {
		s.exchangeMonitorRedirect(w, r, "", "план обмена не найден")
		return
	}
	nodeCode := r.FormValue("node")
	peer := plan.Node(nodeCode)
	if peer == nil || strings.TrimSpace(peer.URL) == "" {
		s.exchangeMonitorRedirect(w, r, "", "у узла нет адреса (url)")
		return
	}
	token, _ := s.store.GetExchangeToken(ctx, plan.Name)
	if token == "" {
		s.exchangeMonitorRedirect(w, r, "", "токен плана не задан")
		return
	}
	thisNode, _ := s.store.GetExchangeThisNode(ctx, plan.Name)
	if thisNode == "" {
		s.exchangeMonitorRedirect(w, r, "", "узел этой базы не задан")
		return
	}
	push, load, err := exchange.SyncWithNode(ctx, s.store, s.reg, plan, thisNode, nodeCode, peer.URL, token,
		s.exchangeApplyOptions())
	if err != nil {
		s.exchangeMonitorRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.exchangeMonitorRedirect(w, r, fmt.Sprintf("Синхронизация с %q: отправлено %d, получено %d",
		nodeCode, push.Applied+push.Deleted, load.Applied+load.Deleted), "")
}

func (s *Server) exchangeMonitorRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	u := "/ui/admin/exchange"
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

const tplExchangeMonitor = `{{define "exchange-monitor"}}` + adminHead + `
<main>
<h2>Обмен данными</h2>
{{if .Msg}}<div style="background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:900px">✓ {{.Msg}}</div>{{end}}
{{if .Err}}<div class="error" style="max-width:900px;margin-bottom:16px">{{.Err}}</div>{{end}}
{{if not .Plans}}
<div class="card" style="max-width:900px"><p class="empty">Планы обмена не настроены. Добавьте <code>exchange/&lt;имя&gt;.yaml</code> в конфигурацию.</p></div>
{{end}}
{{range .Plans}}
{{$plan := .Name}}
<div class="card" style="margin-bottom:20px;max-width:900px">
  <div class="row-top">
    <h3 style="font-size:18px;color:#1e293b">{{.Name}}</h3>
    <span style="color:#64748b;font-size:13px">правило конфликта: {{.Conflict}}</span>
  </div>
  <p style="font-size:13px;color:#475569;margin:6px 0 14px">
    Текущий узел: {{if .ThisNode}}<b>{{.ThisNode}}</b>{{else}}<span style="color:#dc2626">не задан</span> — <code>onebase exchange init</code>{{end}}
    {{if not .HasToken}} · <span style="color:#b45309">токен онлайн-обмена не задан</span>{{end}}
  </p>
  <table>
  <thead><tr>
    <th>Узел</th><th>Название</th>
    <th style="text-align:center" title="строки, ждущие выгрузки">Очередь</th>
    <th style="text-align:center">Отпр.</th><th style="text-align:center">Подтв.</th><th style="text-align:center">Принято</th><th></th>
  </tr></thead>
  <tbody>
  {{range .Nodes}}<tr{{if .IsThis}} style="background:#f8fafc"{{end}}>
    <td><b>{{.Code}}</b>{{if .IsThis}} <span style="color:#2563eb;font-size:12px">← этот</span>{{end}}</td>
    <td style="color:#475569">{{.Name}}</td>
    <td style="text-align:center;font-weight:600">{{.Queue}}</td>
    <td style="text-align:center;color:#94a3b8">{{.Sent}}</td>
    <td style="text-align:center;color:#94a3b8">{{.Ack}}</td>
    <td style="text-align:center;color:#94a3b8">{{.Recv}}</td>
    <td style="text-align:right">
      {{if .Online}}<form method="POST" action="/ui/admin/exchange/sync" style="margin:0">
        <input type="hidden" name="plan" value="{{$plan}}">
        <input type="hidden" name="node" value="{{.Code}}">
        <button class="btn btn-sm btn-primary" type="submit">Синхронизировать</button>
      </form>{{else if .IsThis}}<span style="color:#cbd5e1">—</span>{{else}}<span style="color:#cbd5e1" title="нет url или токена">оффлайн</span>{{end}}</td>
  </tr>{{end}}
  </tbody>
  </table>
</div>
{{end}}
</main></body></html>
{{end}}`
