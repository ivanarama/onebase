package ui

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/extform"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
)

var extReportTmpl = template.Must(template.New("extreports").Parse(tplAdminExtReports))

func (s *Server) adminExtReports(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	recs, err := s.extreports.List(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	extReportTmpl.ExecuteTemplate(w, "admin-extreports", map[string]any{
		"Reports": recs,
		"Msg":     r.URL.Query().Get("msg"),
		"Err":     r.URL.Query().Get("err"),
	})
}

func (s *Server) adminExtReportUpload(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	lang := s.resolveLang(r)
	maxSize := s.limitMultipartRequest(w, r)
	if err := parseBoundedForm(r, 32<<20); err != nil {
		if uploadErrorStatus(err) == http.StatusRequestEntityTooLarge {
			http.Error(w, s.uploadTooLargeText(lang, maxSize), http.StatusRequestEntityTooLarge)
			return
		}
		s.extReportRedirect(w, r, "", s.tr(lang, "не удалось прочитать файл")+": "+s.errText(r, err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.extReportRedirect(w, r, "", s.tr(lang, "файл не выбран"))
		return
	}
	defer file.Close()
	data, err := readUploadedBytes(file, maxSize)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			http.Error(w, s.uploadTooLargeText(lang, maxSize), http.StatusRequestEntityTooLarge)
			return
		}
		s.extReportRedirect(w, r, "", s.tr(lang, "ошибка чтения файла")+": "+s.errText(r, err))
		return
	}

	parsed, err := extform.ParseReportUpload(data)
	if err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := extform.CheckMinPlatform(parsed.MinPlatform, s.cfg.PlatVersion); err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	// Валидация запроса по схеме конфигурации: компиляция в SQL + (на SQLite)
	// проверка исполнимости. Не даём загрузить заведомо нерабочий отчёт.
	rep, _ := report.ParseBytes(parsed.Content)
	if err := s.validateReportQuery(r.Context(), rep); err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}

	rec := &extform.ReportRecord{
		Name:       parsed.Name,
		Content:    parsed.Content,
		Author:     parsed.Author,
		Version:    parsed.Version,
		UploadedBy: currentLogin(r),
	}
	if err := s.extreports.Save(r.Context(), rec); err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditExtReport(r, "extreport.upload", rec)
	s.reloadExtReports(r.Context())
	s.extReportRedirect(w, r, fmt.Sprintf("отчёт %q загружен", rec.Name), "")
}

func (s *Server) adminExtReportToggle(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extreports.Get(r.Context(), id)
	if err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extreports.SetEnabled(r.Context(), id, !rec.Enabled); err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	action := "extreport.enable"
	if rec.Enabled {
		action = "extreport.disable"
	}
	s.auditExtReport(r, action, rec)
	s.reloadExtReports(r.Context())
	s.extReportRedirect(w, r, "статус отчёта изменён", "")
}

func (s *Server) adminExtReportDelete(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extreports.Get(r.Context(), id)
	if err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extreports.Delete(r.Context(), id); err != nil {
		s.extReportRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditExtReport(r, "extreport.delete", rec)
	s.reloadExtReports(r.Context())
	s.extReportRedirect(w, r, fmt.Sprintf("отчёт %q удалён", rec.Name), "")
}

func (s *Server) adminExtReportExport(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extreports.Get(r.Context(), id)
	if err != nil {
		http.Error(w, s.errText(r, err), 404)
		return
	}
	bundle, err := extform.BuildReportBundle(rec, s.cfg.PlatVersion)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	fname := rec.Name + ".obform"
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", contentDisposition(fname))
	w.Write(bundle)
}

// validateReportQuery компилирует запрос отчёта по схеме конфигурации и (на
// SQLite) проверяет его исполнимость. Параметры заполняются заглушками — на
// этапе компиляции/PREPARE важно лишь, что все таблицы/колонки/параметры
// существуют, а не их значения.
func (s *Server) validateReportQuery(ctx context.Context, rep *report.Report) error {
	if rep == nil {
		return fmt.Errorf("пустой отчёт")
	}
	params := make(map[string]any, len(rep.Params))
	for _, p := range rep.Params {
		params[p.Name] = ""
	}
	compiled, err := query.Compile(rep.Query, query.CompileOpts{
		Entities:    s.reg.Entities(),
		Registers:   s.reg.Registers(),
		InfoRegs:    s.reg.InfoRegisters(),
		AccountRegs: s.reg.AccountRegisters(),
		Params:      params,
		Dialect:     s.store.Dialect(),
	})
	if err != nil {
		return fmt.Errorf("запрос не компилируется: %w", err)
	}
	if err := s.store.ValidateQuery(ctx, compiled.SQL); err != nil {
		return fmt.Errorf("запрос не исполняется: %w", err)
	}
	return nil
}

func (s *Server) reloadExtReports(ctx context.Context) {
	reps, err := s.extreports.LoadEnabledReports(ctx)
	if err != nil {
		fmt.Println("extreport reload:", err)
		return
	}
	s.reg.SetExternalReports(reps)
}

func (s *Server) auditExtReport(r *http.Request, action string, rec *extform.ReportRecord) {
	e := &storage.AuditEntry{
		Action:     action,
		EntityKind: "extreport",
		EntityName: rec.Name,
		RecordID:   rec.ID,
		Field:      rec.Name,
		NewValue:   rec.Name,
		IP:         r.RemoteAddr,
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		e.UserID = u.ID
		e.UserLogin = u.Login
	}
	_ = s.store.Log(r.Context(), e)
}

func (s *Server) extReportRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	v := url.Values{}
	if msg != "" {
		v.Set("msg", msg)
	}
	if errMsg != "" {
		v.Set("err", errMsg)
	}
	dest := "/ui/admin/extreports"
	if enc := v.Encode(); enc != "" {
		dest += "?" + enc
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

const tplAdminExtReports = `{{define "admin-extreports"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1000px">
  <h2>Внешние отчёты</h2>
</div>
<p style="color:#64748b;font-size:13px;margin-bottom:16px;max-width:1000px">
  Отчёты из внешнего контура хранятся в базе и не входят в версионируемую конфигурацию проекта.
  Запрос проверяется при загрузке (компиляция в SQL и, на SQLite, исполнимость). Поддерживается
  «голый» YAML отчёта или бандл <code>*.obform</code> (kind: report). Включённые отчёты появляются
  в общем списке отчётов.
</p>
{{if .Msg}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:1000px">✓ {{.Msg}}</div>{{end}}
{{if .Err}}<div class="error" style="max-width:1000px">{{.Err}}</div>{{end}}

<div class="card" style="max-width:1000px;margin-bottom:20px">
<h3 style="margin-bottom:12px;font-size:16px">Загрузить отчёт</h3>
<form method="POST" action="/ui/admin/extreports" enctype="multipart/form-data" style="display:flex;gap:12px;align-items:center;flex-wrap:wrap">
  <input type="file" name="file" accept=".yaml,.yml,.obform" required>
  <button class="btn btn-primary" type="submit">Загрузить</button>
</form>
</div>

<div class="card" style="max-width:1000px">
{{if .Reports}}
<table style="font-size:13px">
<thead><tr>
  <th>Отчёт</th><th>Статус</th><th>Автор</th><th>Версия</th><th>Загрузил</th><th>Когда</th><th></th>
</tr></thead>
<tbody>
{{range .Reports}}<tr>
  <td><strong>{{.Name}}</strong></td>
  <td>{{if .Enabled}}<span style="color:#16a34a;font-weight:600">включён</span>{{else}}<span style="color:#94a3b8">выключен</span>{{end}}</td>
  <td style="color:#475569">{{.Author}}</td>
  <td style="color:#475569">{{.Version}}</td>
  <td style="color:#475569">{{.UploadedBy}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.UploadedAt.Format "02.01.2006 15:04"}}</td>
  <td>
    <div style="display:flex;gap:4px">
      <form method="POST" action="/ui/admin/extreports/{{.ID}}/toggle" style="margin:0">
        <button class="btn btn-sm btn-secondary" type="submit">{{if .Enabled}}Выключить{{else}}Включить{{end}}</button>
      </form>
      <a class="btn btn-sm btn-secondary" href="/ui/admin/extreports/{{.ID}}/export">Экспорт</a>
      <form method="POST" action="/ui/admin/extreports/{{.ID}}/delete" data-ob-confirm="Удалить отчёт {{.Name}}?" style="margin:0">
        <button class="btn btn-sm btn-danger" type="submit">Удалить</button>
      </form>
    </div>
  </td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">Внешних отчётов пока нет. Загрузите YAML отчёта или бандл *.obform.</p>
{{end}}
</div>
</main></body></html>
{{end}}`
