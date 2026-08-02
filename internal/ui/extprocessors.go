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
	processorpkg "github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/storage"
)

var extProcTmpl = template.Must(template.New("extprocessors").Parse(tplAdminExtProcessors))

func (s *Server) adminExtProcessors(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	recs, err := s.extprocessors.List(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderTemplate(w, extProcTmpl, "admin-extprocessors", map[string]any{
		"Procs": recs,
		"Msg":   r.URL.Query().Get("msg"),
		"Err":   r.URL.Query().Get("err"),
	})
}

func (s *Server) adminExtProcessorUpload(w http.ResponseWriter, r *http.Request) {
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
		s.extProcRedirect(w, r, "", s.tr(lang, "не удалось прочитать файл")+": "+s.errText(r, err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.extProcRedirect(w, r, "", s.tr(lang, "файл не выбран"))
		return
	}
	defer file.Close()
	data, err := readUploadedBytes(file, maxSize)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			http.Error(w, s.uploadTooLargeText(lang, maxSize), http.StatusRequestEntityTooLarge)
			return
		}
		s.extProcRedirect(w, r, "", s.tr(lang, "ошибка чтения файла")+": "+s.errText(r, err))
		return
	}

	// ParseProcessorUpload выполняет полную валидацию: метаданные, наличие и
	// компиляцию кода, наличие процедуры Выполнить().
	parsed, err := extform.ParseProcessorUpload(data)
	if err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := extform.CheckMinPlatform(parsed.MinPlatform, s.cfg.PlatVersion); err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}

	rec := &extform.ProcessorRecord{
		Name:       parsed.Name,
		Content:    parsed.Content,
		Author:     parsed.Author,
		Version:    parsed.Version,
		UploadedBy: currentLogin(r),
	}
	if err := s.extprocessors.Save(r.Context(), rec); err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditExtProc(r, "extprocessor.upload", rec)
	s.reloadExtProcessors(r.Context())
	s.extProcRedirect(w, r, fmt.Sprintf("обработка %q загружена (по умолчанию недоверенная — запускает только админ)", rec.Name), "")
}

func (s *Server) adminExtProcessorToggle(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extprocessors.Get(r.Context(), id)
	if err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extprocessors.SetEnabled(r.Context(), id, !rec.Enabled); err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	action := "extprocessor.enable"
	if rec.Enabled {
		action = "extprocessor.disable"
	}
	s.auditExtProc(r, action, rec)
	s.reloadExtProcessors(r.Context())
	s.extProcRedirect(w, r, "статус обработки изменён", "")
}

// adminExtProcessorTrust переключает признак «доверенная»: доверенную обработку
// видят и запускают обычные пользователи, недоверенную — только админ.
func (s *Server) adminExtProcessorTrust(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extprocessors.Get(r.Context(), id)
	if err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extprocessors.SetTrusted(r.Context(), id, !rec.Trusted); err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	action := "extprocessor.trust"
	if rec.Trusted {
		action = "extprocessor.untrust"
	}
	s.auditExtProc(r, action, rec)
	s.reloadExtProcessors(r.Context())
	s.extProcRedirect(w, r, "признак доверенности изменён", "")
}

func (s *Server) adminExtProcessorDelete(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extprocessors.Get(r.Context(), id)
	if err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extprocessors.Delete(r.Context(), id); err != nil {
		s.extProcRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditExtProc(r, "extprocessor.delete", rec)
	s.reloadExtProcessors(r.Context())
	s.extProcRedirect(w, r, fmt.Sprintf("обработка %q удалена", rec.Name), "")
}

func (s *Server) adminExtProcessorExport(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extprocessors.Get(r.Context(), id)
	if err != nil {
		http.Error(w, s.errText(r, err), 404)
		return
	}
	bundle, err := extform.BuildProcessorBundle(rec, s.cfg.PlatVersion)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	fname := rec.Name + ".obform"
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", contentDisposition(fname))
	w.Write(bundle)
}

func (s *Server) reloadExtProcessors(ctx context.Context) {
	procs, programs, err := s.extprocessors.LoadEnabled(ctx)
	if err != nil {
		fmt.Println("extprocessor reload:", err)
		return
	}
	s.reg.SetExternalProcessors(procs, programs)
}

// canRunExternalProc решает, может ли текущий пользователь видеть/запускать
// обработку. Обработки конфигурации — без ограничений (как раньше). Внешняя
// обработка: доверенная — всем, недоверенная — только администратору.
func (s *Server) canRunExternalProc(r *http.Request, proc *processorpkg.Processor) bool {
	if !proc.External || proc.Trusted {
		return true
	}
	return s.isAdmin(r)
}

// auditExtProcRun логирует факт запуска внешней обработки (исполнение DSL).
func (s *Server) auditExtProcRun(r *http.Request, name string) {
	e := &storage.AuditEntry{
		Action:     "extprocessor.run",
		EntityKind: "extprocessor",
		EntityName: name,
		Field:      name,
		IP:         r.RemoteAddr,
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		e.UserID = u.ID
		e.UserLogin = u.Login
	}
	_ = s.store.Log(r.Context(), e)
}

func (s *Server) auditExtProc(r *http.Request, action string, rec *extform.ProcessorRecord) {
	e := &storage.AuditEntry{
		Action:     action,
		EntityKind: "extprocessor",
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

func (s *Server) extProcRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	v := url.Values{}
	if msg != "" {
		v.Set("msg", msg)
	}
	if errMsg != "" {
		v.Set("err", errMsg)
	}
	dest := "/ui/admin/extprocessors"
	if enc := v.Encode(); enc != "" {
		dest += "?" + enc
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

const tplAdminExtProcessors = `{{define "admin-extprocessors"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1100px">
  <h2>Внешние обработки</h2>
</div>
<div style="background:#fef3c7;border:1px solid #fcd34d;color:#92400e;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:13px;max-width:1100px">
  ⚠️ Внешняя обработка исполняет произвольный DSL-код с полными правами платформы (запросы, движения,
  HTTP, файлы). Загружайте только код из доверенного источника. По умолчанию обработку запускает
  только администратор; чтобы её могли запускать обычные пользователи, пометьте её «доверенной».
</div>
{{if .Msg}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:1100px">✓ {{.Msg}}</div>{{end}}
{{if .Err}}<div class="error" style="max-width:1100px">{{.Err}}</div>{{end}}

<div class="card" style="max-width:1100px;margin-bottom:20px">
<h3 style="margin-bottom:8px;font-size:16px">Загрузить обработку</h3>
<p style="color:#64748b;font-size:12px;margin-bottom:12px">YAML с метаданными (name, params) и полем <code>code</code> (исходник .proc.os с процедурой Выполнить()), либо бандл *.obform.</p>
<form method="POST" action="/ui/admin/extprocessors" enctype="multipart/form-data" style="display:flex;gap:12px;align-items:center;flex-wrap:wrap">
  <input type="file" name="file" accept=".yaml,.yml,.obform" required>
  <button class="btn btn-primary" type="submit">Загрузить</button>
</form>
</div>

<div class="card" style="max-width:1100px">
{{if .Procs}}
<table style="font-size:13px">
<thead><tr>
  <th>Обработка</th><th>Статус</th><th>Доверенная</th><th>Автор</th><th>Версия</th><th>Загрузил</th><th>Когда</th><th></th>
</tr></thead>
<tbody>
{{range .Procs}}<tr>
  <td><strong>{{.Name}}</strong></td>
  <td>{{if .Enabled}}<span style="color:#16a34a;font-weight:600">включена</span>{{else}}<span style="color:#94a3b8">выключена</span>{{end}}</td>
  <td>{{if .Trusted}}<span style="color:#2563eb;font-weight:600">да</span>{{else}}<span style="color:#94a3b8">нет (только админ)</span>{{end}}</td>
  <td style="color:#475569">{{.Author}}</td>
  <td style="color:#475569">{{.Version}}</td>
  <td style="color:#475569">{{.UploadedBy}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.UploadedAt.Format "02.01.2006 15:04"}}</td>
  <td>
    <div style="display:flex;gap:4px;flex-wrap:wrap">
      <form method="POST" action="/ui/admin/extprocessors/{{.ID}}/toggle" style="margin:0">
        <button class="btn btn-sm btn-secondary" type="submit">{{if .Enabled}}Выключить{{else}}Включить{{end}}</button>
      </form>
      <form method="POST" action="/ui/admin/extprocessors/{{.ID}}/trust" style="margin:0">
        <button class="btn btn-sm btn-secondary" type="submit">{{if .Trusted}}Снять доверие{{else}}Доверять{{end}}</button>
      </form>
      <a class="btn btn-sm btn-secondary" href="/ui/admin/extprocessors/{{.ID}}/export">Экспорт</a>
      <form method="POST" action="/ui/admin/extprocessors/{{.ID}}/delete" data-ob-confirm="Удалить обработку {{.Name}}?" style="margin:0">
        <button class="btn btn-sm btn-danger" type="submit">Удалить</button>
      </form>
    </div>
  </td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">Внешних обработок пока нет. Загрузите YAML обработки или бандл *.obform.</p>
{{end}}
</div>
</main></body></html>
{{end}}`
