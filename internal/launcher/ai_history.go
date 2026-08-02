package launcher

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/llm"
	"github.com/ivantit66/onebase/internal/storage"
)

// logCfgAI пишет обращение к ИИ из конфигуратора в журнал _ai_audit, если в
// настройках включён log_history. Best-effort: на ответ пользователю не влияет.
func logCfgAI(ctx context.Context, db *storage.DB, cfg llm.Config, login, task, query, response string, resp llm.ChatResponse) {
	if !cfg.LogHistory || db == nil {
		return
	}
	db.LogAIQuery(ctx, storage.AIAuditEntry{
		UserLogin:    login,
		Task:         task,
		Model:        resp.Model,
		Query:        query,
		Response:     response,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	})
}

// cfgLogin возвращает логин текущего пользователя конфигуратора (или "").
func cfgLogin(ctx context.Context) string {
	if u := cfgUserFromContext(ctx); u != nil {
		return u.Login
	}
	return ""
}

// genResponseSummary формирует текст ответа генератора для журнала: пояснение
// модели + список предложенных объектов.
func genResponseSummary(text string, changes []GenChange, trace []GenToolTrace) string {
	var b strings.Builder
	b.WriteString(text)
	if len(trace) > 0 {
		b.WriteString("\n\nTool trace:")
		for _, tr := range trace {
			status := "ok"
			if tr.IsError {
				status = "error"
			}
			b.WriteString("\n- " + tr.Name + " [" + status + "]: " + truncateText(tr.Result, 180))
		}
	}
	if len(changes) > 0 {
		b.WriteString("\n\nОбъекты:")
		for _, c := range changes {
			b.WriteString("\n- " + c.Kind + ": " + c.Path)
		}
	}
	return b.String()
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type aiHistoryResponseParts struct {
	Text    string
	Trace   []aiHistoryTraceLine
	Objects []string
}

type aiHistoryTraceLine struct {
	Name   string
	Status string
	Result string
}

func parseAIHistoryResponse(s string) aiHistoryResponseParts {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var parts aiHistoryResponseParts
	var textLines []string
	section := "text"
	for _, line := range strings.Split(s, "\n") {
		switch strings.TrimSpace(line) {
		case "Tool trace:":
			section = "trace"
			continue
		case "Объекты:":
			section = "objects"
			continue
		}
		switch section {
		case "trace":
			if tr, ok := parseAIHistoryTraceLine(line); ok {
				parts.Trace = append(parts.Trace, tr)
			}
		case "objects":
			item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if item != "" {
				parts.Objects = append(parts.Objects, item)
			}
		default:
			textLines = append(textLines, line)
		}
	}
	parts.Text = strings.TrimSpace(strings.Join(textLines, "\n"))
	return parts
}

func parseAIHistoryTraceLine(line string) (aiHistoryTraceLine, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	if line == "" {
		return aiHistoryTraceLine{}, false
	}
	open := strings.LastIndex(line, " [")
	close := strings.Index(line, "]:")
	if open <= 0 || close <= open+2 {
		return aiHistoryTraceLine{Name: line, Status: "ok"}, true
	}
	status := strings.TrimSpace(line[open+2 : close])
	result := strings.TrimSpace(line[close+2:])
	if status == "" {
		status = "ok"
	}
	return aiHistoryTraceLine{
		Name:   strings.TrimSpace(line[:open]),
		Status: status,
		Result: result,
	}, true
}

func renderAIHistoryResponse(response string) string {
	parts := parseAIHistoryResponse(response)
	if len(parts.Trace) == 0 && len(parts.Objects) == 0 {
		return `<pre style="white-space:pre-wrap;word-break:break-word;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px;padding:6px;margin:4px 0">` + html.EscapeString(response) + `</pre>`
	}
	var b strings.Builder
	b.WriteString(`<div style="display:grid;gap:8px;margin:6px 0">`)
	if parts.Text != "" {
		b.WriteString(`<pre style="white-space:pre-wrap;word-break:break-word;background:#f8fafc;border:1px solid #e2e8f0;border-radius:6px;padding:8px;margin:0">`)
		b.WriteString(html.EscapeString(parts.Text))
		b.WriteString(`</pre>`)
	}
	if len(parts.Trace) > 0 {
		b.WriteString(`<div style="border:1px solid #e2e8f0;border-radius:6px;overflow:hidden">`)
		b.WriteString(`<div style="background:#f8fafc;color:#475569;font-weight:600;padding:6px 8px">Tool trace</div>`)
		for _, tr := range parts.Trace {
			color := "#16a34a"
			label := "ok"
			if strings.EqualFold(tr.Status, "error") {
				color = "#dc2626"
				label = "error"
			}
			b.WriteString(`<div style="display:grid;grid-template-columns:minmax(90px,160px) 58px minmax(0,1fr);gap:8px;border-top:1px solid #e2e8f0;padding:6px 8px;align-items:start">`)
			b.WriteString(`<code style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">` + html.EscapeString(tr.Name) + `</code>`)
			b.WriteString(`<span style="color:` + color + `;font-weight:600">` + label + `</span>`)
			b.WriteString(`<span style="white-space:pre-wrap;word-break:break-word;color:#334155">` + html.EscapeString(tr.Result) + `</span>`)
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
	}
	if len(parts.Objects) > 0 {
		b.WriteString(`<div style="display:flex;gap:6px;flex-wrap:wrap">`)
		for _, obj := range parts.Objects {
			b.WriteString(`<span style="border:1px solid #cbd5e1;background:#f8fafc;border-radius:999px;padding:3px 8px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:11px">`)
			b.WriteString(html.EscapeString(obj))
			b.WriteString(`</span>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// renderAIHistory строит HTML-фрагмент таблицы журнала ИИ (для admin-оверлея).
func renderAIHistory(entries []storage.AIAuditEntry) string {
	var b strings.Builder
	b.WriteString(`<div style="padding:16px"><h3 style="margin:0 0 10px;font-size:15px">История ИИ-запросов</h3>`)
	if len(entries) == 0 {
		b.WriteString(`<div style="color:#888;font-size:12px">Журнал пуст. Включите запись в настройках ИИ: <code>"log_history": true</code>.</div></div>`)
		return b.String()
	}
	b.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:12px"><thead><tr style="text-align:left;border-bottom:1px solid #e2e8f0;color:#666">` +
		`<th style="padding:4px">Дата</th><th style="padding:4px">Инструмент</th><th style="padding:4px">Модель</th><th style="padding:4px">Токены</th><th style="padding:4px">Запрос / ответ</th></tr></thead><tbody>`)
	for _, e := range entries {
		fmt.Fprintf(&b, `<tr style="border-bottom:1px solid #f1f5f9;vertical-align:top">`+
			`<td style="padding:4px;white-space:nowrap">%s</td><td style="padding:4px">%s</td><td style="padding:4px">%s</td><td style="padding:4px;white-space:nowrap">%d+%d</td>`+
			`<td style="padding:4px"><details><summary style="cursor:pointer">%s</summary>`+
			`%s</details></td></tr>`,
			e.At.Format("02.01.2006 15:04"), html.EscapeString(e.Task), html.EscapeString(e.Model),
			e.InputTokens, e.OutputTokens, html.EscapeString(truncateText(e.Query, 80)), renderAIHistoryResponse(e.Response))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// cfgAdminAIHistory — страница «История ИИ» в админ-меню конфигуратора.
func (h *handler) cfgAdminAIHistory(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	entries, err := db.ListAIAudit(r.Context(), 200)
	if err != nil {
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">`+html.EscapeString(err.Error())+`</div>`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeBody(w, []byte(renderAIHistory(entries)))
}
