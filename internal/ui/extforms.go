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
	"github.com/ivantit66/onebase/internal/storage"
)

var extFormTmpl = template.Must(template.New("extforms").Parse(tplAdminExtForms))

// adminExtForms показывает список внешних печатных форм и форму загрузки.
func (s *Server) adminExtForms(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	recs, err := s.extforms.List(r.Context())
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	extFormTmpl.ExecuteTemplate(w, "admin-extforms", map[string]any{
		"Forms": recs,
		"Msg":   r.URL.Query().Get("msg"),
		"Err":   r.URL.Query().Get("err"),
	})
}

// adminExtFormUpload принимает YAML печатной формы или бандл *.obform.
func (s *Server) adminExtFormUpload(w http.ResponseWriter, r *http.Request) {
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
		s.extFormRedirect(w, r, "", s.tr(lang, "не удалось прочитать файл")+": "+s.errText(r, err))
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		s.extFormRedirect(w, r, "", s.tr(lang, "файл не выбран"))
		return
	}
	defer file.Close()
	data, err := readUploadedBytes(file, maxSize)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			http.Error(w, s.uploadTooLargeText(lang, maxSize), http.StatusRequestEntityTooLarge)
			return
		}
		s.extFormRedirect(w, r, "", s.tr(lang, "ошибка чтения файла")+": "+s.errText(r, err))
		return
	}

	parsed, err := extform.ParseUpload(data)
	if err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}
	// Документ обязан существовать в конфигурации — иначе форму некуда
	// привязать и она никогда не отрисуется.
	if s.reg.GetEntity(parsed.Document) == nil {
		s.extFormRedirect(w, r, "", fmt.Sprintf(s.tr(lang, "документ %q не найден в конфигурации"), parsed.Document))
		return
	}
	// min_platform из бандла: не даём загрузить форму, требующую более новой
	// платформы (best-effort, см. CheckMinPlatform).
	if err := extform.CheckMinPlatform(parsed.MinPlatform, s.cfg.PlatVersion); err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}

	rec := &extform.Record{
		Document:   parsed.Document,
		Name:       parsed.Name,
		Content:    parsed.Content,
		Author:     parsed.Author,
		Version:    parsed.Version,
		UploadedBy: currentLogin(r),
	}
	if err := s.extforms.Save(r.Context(), rec); err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditExtForm(r, "extform.upload", rec)
	s.reloadExtForms(r.Context())
	s.extFormRedirect(w, r, fmt.Sprintf("форма %q для %q загружена", rec.Name, rec.Document), "")
}

// adminExtFormToggle включает/выключает форму.
func (s *Server) adminExtFormToggle(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extforms.Get(r.Context(), id)
	if err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extforms.SetEnabled(r.Context(), id, !rec.Enabled); err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}
	action := "extform.enable"
	if rec.Enabled {
		action = "extform.disable"
	}
	s.auditExtForm(r, action, rec)
	s.reloadExtForms(r.Context())
	s.extFormRedirect(w, r, "статус формы изменён", "")
}

// adminExtFormDelete удаляет форму.
func (s *Server) adminExtFormDelete(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extforms.Get(r.Context(), id)
	if err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}
	if err := s.extforms.Delete(r.Context(), id); err != nil {
		s.extFormRedirect(w, r, "", s.errText(r, err))
		return
	}
	s.auditExtForm(r, "extform.delete", rec)
	s.reloadExtForms(r.Context())
	s.extFormRedirect(w, r, fmt.Sprintf("форма %q удалена", rec.Name), "")
}

// adminExtFormExport отдаёт форму как переносимый бандл *.obform.
func (s *Server) adminExtFormExport(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		s.renderForbidden(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	rec, err := s.extforms.Get(r.Context(), id)
	if err != nil {
		http.Error(w, s.errText(r, err), 404)
		return
	}
	bundle, err := extform.BuildBundle(rec, s.cfg.PlatVersion)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	fname := rec.Document + "." + rec.Name + ".obform"
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", contentDisposition(fname))
	w.Write(bundle)
}

// reloadExtForms перечитывает включённые внешние формы из БД и обновляет
// реестр, чтобы изменения сразу попадали в меню печати.
func (s *Server) reloadExtForms(ctx context.Context) {
	forms, layouts, err := s.extforms.LoadEnabledPrintForms(ctx)
	if err != nil {
		fmt.Println("extform reload:", err)
		return
	}
	s.reg.SetExternalPrintForms(forms)
	s.reg.SetExternalLayoutForms(layouts)
}

func (s *Server) auditExtForm(r *http.Request, action string, rec *extform.Record) {
	e := &storage.AuditEntry{
		Action:     action,
		EntityKind: "extform",
		EntityName: rec.Document,
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

func (s *Server) extFormRedirect(w http.ResponseWriter, r *http.Request, msg, errMsg string) {
	v := url.Values{}
	if msg != "" {
		v.Set("msg", msg)
	}
	if errMsg != "" {
		v.Set("err", errMsg)
	}
	dest := "/ui/admin/extforms"
	if enc := v.Encode(); enc != "" {
		dest += "?" + enc
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func currentLogin(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.Login
	}
	return ""
}

const tplAdminExtForms = `{{define "admin-extforms"}}` + adminHead + `
<main>
<div class="row-top" style="max-width:1000px">
  <h2>Внешние печатные формы</h2>
</div>
<p style="color:#64748b;font-size:13px;margin-bottom:16px;max-width:1000px">
  Формы из внешнего контура хранятся в базе и не входят в версионируемую конфигурацию проekta.
  Это позволяет добавлять печатные формы без правки и передеплоя конфигурации проекта. Поддерживается
  «голый» YAML печатной формы или бандл <code>*.obform</code>. Форма с именем, совпадающим с формой
  конфигурации, не перекрывает её — основной остаётся форма конфигурации.
</p>
{{if .Msg}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:1000px">✓ {{.Msg}}</div>{{end}}
{{if .Err}}<div class="error" style="max-width:1000px">{{.Err}}</div>{{end}}

<div class="card" style="max-width:1000px;margin-bottom:20px">
<h3 style="margin-bottom:12px;font-size:16px">Загрузить форму</h3>
<form method="POST" action="/ui/admin/extforms" enctype="multipart/form-data" style="display:flex;gap:12px;align-items:center;flex-wrap:wrap">
  <input type="file" name="file" accept=".yaml,.yml,.obform" required>
  <button class="btn btn-primary" type="submit">Загрузить</button>
</form>
</div>

<div class="card" style="max-width:1000px">
{{if .Forms}}
<table style="font-size:13px">
<thead><tr>
  <th>Документ</th><th>Форма</th><th>Статус</th><th>Автор</th><th>Версия</th><th>Загрузил</th><th>Когда</th><th></th>
</tr></thead>
<tbody>
{{range .Forms}}<tr>
  <td><strong>{{.Document}}</strong></td>
  <td>{{.Name}}</td>
  <td>{{if .Enabled}}<span style="color:#16a34a;font-weight:600">включена</span>{{else}}<span style="color:#94a3b8">выключена</span>{{end}}</td>
  <td style="color:#475569">{{.Author}}</td>
  <td style="color:#475569">{{.Version}}</td>
  <td style="color:#475569">{{.UploadedBy}}</td>
  <td style="font-size:12px;color:#94a3b8">{{.UploadedAt.Format "02.01.2006 15:04"}}</td>
  <td>
    <div style="display:flex;gap:4px">
      <form method="POST" action="/ui/admin/extforms/{{.ID}}/toggle" style="margin:0">
        <button class="btn btn-sm btn-secondary" type="submit">{{if .Enabled}}Выключить{{else}}Включить{{end}}</button>
      </form>
      <a class="btn btn-sm btn-secondary" href="/ui/admin/extforms/{{.ID}}/export">Экспорт</a>
      <form method="POST" action="/ui/admin/extforms/{{.ID}}/delete" data-ob-confirm="Удалить форму {{.Name}}?" style="margin:0">
        <button class="btn btn-sm btn-danger" type="submit">Удалить</button>
      </form>
    </div>
  </td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">Внешних форм пока нет. Загрузите YAML печатной формы или бандл *.obform.</p>
{{end}}
</div>
</main></body></html>
{{end}}`
