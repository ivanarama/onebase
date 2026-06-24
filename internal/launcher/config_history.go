package launcher

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
)

const configHistoryLimit = 100

// renderConfigHistory builds the admin overlay fragment for DB-backed
// configuration history. The underlying snapshots/diff/rollback live in
// internal/configdb; this layer only presents them in the configurator.
func renderConfigHistory(baseID string, versions []configdb.Version, fromID, toID string, diff []configdb.DiffEntry, diffErr error) string {
	var b strings.Builder
	b.WriteString(`<div style="padding:16px">`)
	b.WriteString(`<div style="display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:12px">`)
	b.WriteString(`<div><h3 style="margin:0 0 6px;font-size:15px">История конфигурации</h3>`)
	b.WriteString(`<div style="font-size:12px;color:#64748b;line-height:1.45">Версии создаются при сохранении файлов конфигурации и при откате. После отката выполните миграцию/перезапуск базы, если схема изменилась.</div></div>`)
	b.WriteString(`<button onclick="cfgAdmin('config-history')" style="background:#e2e8f0;color:#334155;border:none;padding:5px 12px;border-radius:3px;cursor:pointer;font-size:12px">Обновить</button>`)
	b.WriteString(`</div>`)

	if len(versions) == 0 {
		b.WriteString(`<div style="padding:18px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px;color:#64748b;font-size:12px">История пока пуста. Сохраните любой файл конфигурации, чтобы появился первый снимок.</div>`)
		b.WriteString(`</div>`)
		return b.String()
	}

	b.WriteString(`<div style="display:flex;align-items:end;gap:8px;flex-wrap:wrap;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px;padding:10px;margin-bottom:12px">`)
	b.WriteString(`<div style="font-size:12px;font-weight:600;color:#475569;margin-right:4px">Сравнить версии</div>`)
	b.WriteString(`<label style="font-size:11px;color:#64748b">До<br><select id="cfg-hist-from" style="min-width:210px;padding:4px 7px;border:1px solid #cbd5e1;border-radius:3px;font-size:12px">`)
	for _, v := range versions {
		sel := ""
		if v.ID == fromID {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s · %s</option>`, html.EscapeString(v.ID), sel, html.EscapeString(shortConfigVersionID(v.ID)), html.EscapeString(v.Message))
	}
	b.WriteString(`</select></label>`)
	b.WriteString(`<label style="font-size:11px;color:#64748b">После<br><select id="cfg-hist-to" style="min-width:210px;padding:4px 7px;border:1px solid #cbd5e1;border-radius:3px;font-size:12px">`)
	for i, v := range versions {
		sel := ""
		if v.ID == toID || (toID == "" && i == 0) {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s · %s</option>`, html.EscapeString(v.ID), sel, html.EscapeString(shortConfigVersionID(v.ID)), html.EscapeString(v.Message))
	}
	b.WriteString(`</select></label>`)
	b.WriteString(`<button onclick="cfgHistoryCompare()" style="background:#2563eb;color:#fff;border:none;padding:5px 12px;border-radius:3px;cursor:pointer;font-size:12px">Показать diff</button>`)
	b.WriteString(`</div>`)

	b.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:12px;margin-bottom:14px">`)
	b.WriteString(`<thead><tr style="background:#f1f5f9;color:#475569">`)
	b.WriteString(`<th style="text-align:left;padding:6px 8px;font-weight:600">Дата</th>`)
	b.WriteString(`<th style="text-align:left;padding:6px 8px;font-weight:600">Автор</th>`)
	b.WriteString(`<th style="text-align:left;padding:6px 8px;font-weight:600">Сообщение</th>`)
	b.WriteString(`<th style="text-align:left;padding:6px 8px;font-weight:600">ID</th>`)
	b.WriteString(`<th style="padding:6px 8px"></th></tr></thead><tbody>`)
	for i, v := range versions {
		bg := ""
		if i%2 == 1 {
			bg = `background:#f9fafb;`
		}
		if v.ID == toID {
			bg += `outline:2px solid #bfdbfe;outline-offset:-2px;`
		}
		author := v.AuthorLogin
		if author == "" {
			author = "—"
		}
		message := v.Message
		if message == "" {
			message = "—"
		}
		b.WriteString(`<tr style="border-bottom:1px solid #eef2f7;` + bg + `">`)
		fmt.Fprintf(&b, `<td style="padding:6px 8px;white-space:nowrap;color:#475569">%s</td>`, html.EscapeString(v.CreatedAt.Format("02.01.2006 15:04:05")))
		fmt.Fprintf(&b, `<td style="padding:6px 8px">%s</td>`, html.EscapeString(author))
		fmt.Fprintf(&b, `<td style="padding:6px 8px">%s</td>`, html.EscapeString(message))
		fmt.Fprintf(&b, `<td style="padding:6px 8px;font-family:ui-monospace,SFMono-Regular,Consolas,monospace;color:#64748b">%s</td>`, html.EscapeString(shortConfigVersionID(v.ID)))
		b.WriteString(`<td style="padding:6px 8px;white-space:nowrap;text-align:right">`)
		if i+1 < len(versions) {
			prevID := versions[i+1].ID
			fmt.Fprintf(&b, `<button onclick="cfgAdmin('config-history?from=%s&amp;to=%s')" style="background:#e0f2fe;color:#075985;border:none;padding:4px 9px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:5px">Diff</button>`,
				escAttrJS(prevID), escAttrJS(v.ID))
		}
		fmt.Fprintf(&b, `<a href="/bases/%s/configurator/admin/config-history/%s/export-zip" style="display:inline-block;text-decoration:none;background:#f1f5f9;color:#334155;border:none;padding:4px 9px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:5px">ZIP</a>`,
			escAttrJS(baseID), escAttrJS(v.ID))
		fmt.Fprintf(&b, `<a href="/bases/%s/configurator/admin/config-history/%s/export-obz" style="display:inline-block;text-decoration:none;background:#ecfdf5;color:#166534;border:none;padding:4px 9px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:5px">OBZ</a>`,
			escAttrJS(baseID), escAttrJS(v.ID))
		fmt.Fprintf(&b, `<button onclick="cfgHistoryRollback('%s')" style="background:#fee2e2;color:#991b1b;border:none;padding:4px 9px;border-radius:3px;cursor:pointer;font-size:11px">Откатить</button>`,
			escAttrJS(v.ID))
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)

	if fromID != "" || toID != "" {
		b.WriteString(renderConfigHistoryDiff(fromID, toID, diff, diffErr))
	}

	fmt.Fprintf(&b, `<script>
if(typeof cfgInfo!=='function'){window.cfgInfo=function(text){var ov=document.createElement('div');ov.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.35);z-index:10001;display:flex;align-items:center;justify-content:center';var box=document.createElement('div');box.style.cssText='background:#fff;padding:18px 22px;border-radius:8px;box-shadow:0 6px 28px rgba(0,0,0,.2);min-width:240px;font-size:13px';box.innerHTML='<div style="margin-bottom:12px">'+text+'</div>';var ok=document.createElement('button');ok.textContent='OK';ok.style.cssText='background:#1a4a80;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer;float:right';ok.onclick=function(){document.body.removeChild(ov)};box.appendChild(ok);ov.appendChild(box);document.body.appendChild(ov)}}
if(typeof cfgConfirm!=='function'){window.cfgConfirm=function(text,onOk){var ov=document.createElement('div');ov.style.cssText='position:fixed;inset:0;background:rgba(0,0,0,.35);z-index:10001;display:flex;align-items:center;justify-content:center';var box=document.createElement('div');box.style.cssText='background:#fff;padding:18px 22px;border-radius:8px;box-shadow:0 6px 28px rgba(0,0,0,.2);min-width:300px;font-size:13px';box.innerHTML='<div style="margin-bottom:14px">'+text+'</div>';var row=document.createElement('div');row.style.cssText='display:flex;gap:8px;justify-content:flex-end';var ok=document.createElement('button');ok.textContent='OK';ok.style.cssText='background:#c00;color:#fff;border:none;padding:5px 14px;border-radius:4px;cursor:pointer';var cancel=document.createElement('button');cancel.textContent='Отмена';cancel.style.cssText='background:#e2e8f0;color:#333;border:none;padding:5px 12px;border-radius:4px;cursor:pointer';ok.onclick=function(){document.body.removeChild(ov);onOk()};cancel.onclick=function(){document.body.removeChild(ov)};row.appendChild(ok);row.appendChild(cancel);box.appendChild(row);ov.appendChild(box);document.body.appendChild(ov)}}
function cfgHistoryRollback(id){
  cfgConfirm('Откатить конфигурацию к выбранной версии? Будет создана новая версия-откат.', function(){
    fetch('/bases/%s/configurator/admin/config-history/rollback',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id:id})})
      .then(function(r){return r.json()})
      .then(function(d){
        if(d.error){cfgInfo('Ошибка: '+d.error);return}
        cfgInfo('Откат выполнен. Новая версия: '+(d.id||''));
        cfgAdmin('config-history');
      })
      .catch(function(){cfgInfo('Ошибка сети')});
  });
}
function cfgHistoryCompare(){
  var f=document.getElementById('cfg-hist-from');
  var t=document.getElementById('cfg-hist-to');
  if(!f||!t||!f.value||!t.value){cfgInfo('Выберите две версии');return}
  cfgAdmin('config-history?from='+encodeURIComponent(f.value)+'&to='+encodeURIComponent(t.value));
}
</script>`, escJS(baseID))
	b.WriteString(`</div>`)
	return b.String()
}

func renderConfigHistoryDiff(fromID, toID string, diff []configdb.DiffEntry, diffErr error) string {
	var b strings.Builder
	b.WriteString(`<div style="border:1px solid #dbeafe;background:#eff6ff;border-radius:5px;padding:10px 12px;margin-top:10px">`)
	b.WriteString(`<div style="display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:8px">`)
	fmt.Fprintf(&b, `<div style="font-size:13px;font-weight:600;color:#1e3a8a">Diff %s → %s</div>`,
		html.EscapeString(shortConfigVersionID(fromID)), html.EscapeString(shortConfigVersionID(toID)))
	b.WriteString(`<button onclick="cfgAdmin('config-history')" style="background:#dbeafe;color:#1e40af;border:none;padding:4px 9px;border-radius:3px;cursor:pointer;font-size:11px">Скрыть</button>`)
	b.WriteString(`</div>`)
	if diffErr != nil {
		fmt.Fprintf(&b, `<div style="color:#b91c1c;font-size:12px">%s</div>`, html.EscapeString(diffErr.Error()))
		b.WriteString(`</div>`)
		return b.String()
	}
	if len(diff) == 0 {
		b.WriteString(`<div style="font-size:12px;color:#64748b">Изменений между версиями нет.</div></div>`)
		return b.String()
	}
	for _, e := range diff {
		kindColor := "#334155"
		switch e.Kind {
		case configdb.DiffAdded:
			kindColor = "#15803d"
		case configdb.DiffDeleted:
			kindColor = "#b91c1c"
		case configdb.DiffModified:
			kindColor = "#1d4ed8"
		}
		fmt.Fprintf(&b, `<details style="background:#fff;border:1px solid #e2e8f0;border-radius:4px;margin:8px 0" open><summary style="cursor:pointer;padding:7px 9px;font-size:12px"><span style="color:%s;font-weight:700">%s</span> <code>%s</code></summary>`,
			kindColor, html.EscapeString(string(e.Kind)), html.EscapeString(e.Path))
		switch e.Kind {
		case configdb.DiffAdded:
			b.WriteString(configHistoryPre("После", e.After))
		case configdb.DiffDeleted:
			b.WriteString(configHistoryPre("До", e.Before))
		default:
			b.WriteString(`<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;padding:0 8px 8px">`)
			b.WriteString(configHistoryPre("До", e.Before))
			b.WriteString(configHistoryPre("После", e.After))
			b.WriteString(`</div>`)
		}
		b.WriteString(`</details>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func configHistoryPre(title string, content []byte) string {
	text := configHistoryClip(string(content), 8000)
	return fmt.Sprintf(`<div><div style="font-size:11px;color:#64748b;margin:6px 0 3px">%s</div><pre style="white-space:pre-wrap;word-break:break-word;max-height:280px;overflow:auto;background:#0f172a;color:#e2e8f0;border-radius:4px;padding:8px;margin:0;font-size:11px;line-height:1.45">%s</pre></div>`,
		html.EscapeString(title), html.EscapeString(text))
}

func configHistoryClip(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n… [обрезано]"
}

func shortConfigVersionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// cfgAdminConfigHistory renders configuration version history. It is meaningful
// only for database-backed configurations; file-backed projects should use Git.
func (h *handler) cfgAdminConfigHistory(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if b.ConfigSource != "database" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<div style="padding:16px"><h3 style="margin:0 0 10px;font-size:15px">История конфигурации</h3><div style="color:#64748b;font-size:12px">Эта база использует файловый режим конфигурации. Историю и diff ведите через Git; встроенная история доступна для режима «конфигурация в БД».</div></div>`))
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">` + html.EscapeString(err.Error()) + `</div>`))
		return
	}
	versions, err := repo.ListVersions(r.Context(), configHistoryLimit)
	if err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">` + html.EscapeString(err.Error()) + `</div>`))
		return
	}
	fromID := r.URL.Query().Get("from")
	toID := r.URL.Query().Get("to")
	var (
		diff    []configdb.DiffEntry
		diffErr error
	)
	if fromID != "" && toID != "" {
		diff, diffErr = repo.DiffVersions(r.Context(), fromID, toID)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(renderConfigHistory(b.ID, versions, fromID, toID, diff, diffErr)))
}

func (h *handler) cfgAdminConfigHistoryRollback(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if b.ConfigSource != "database" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "история конфигурации доступна только для режима database"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id версии обязателен"})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	login := cfgLogin(r.Context())
	rolled, err := repo.RollbackToVersion(r.Context(), req.ID, configdb.VersionOptions{
		AuthorLogin: login,
		Message:     "rollback to " + req.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": rolled.ID})
}

func (h *handler) cfgAdminConfigHistoryExportZip(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if b.ConfigSource != "database" {
		http.Error(w, "configuration history is available only for database mode", http.StatusBadRequest)
		return
	}
	versionID := chi.URLParam(r, "version")
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	repo := configdb.New(db)
	_, files, err := repo.GetVersion(r.Context(), versionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		if err := configdb.ValidatePath(f.Path); err != nil {
			_ = zw.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		wr, err := zw.Create(f.Path)
		if err != nil {
			_ = zw.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := wr.Write(f.Content); err != nil {
			_ = zw.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := zw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := "config_" + shortConfigVersionID(versionID) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Write(buf.Bytes())
}

func (h *handler) cfgAdminConfigHistoryExportOBZ(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if b.ConfigSource != "database" {
		http.Error(w, "configuration history is available only for database mode", http.StatusBadRequest)
		return
	}
	versionID := chi.URLParam(r, "version")
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	repo := configdb.New(db)
	_, files, err := repo.GetVersion(r.Context(), versionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := writeConfigHistoryFiles(zw, files, "config/"); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	manifestJSON, _ := json.MarshalIndent(map[string]int{}, "", "  ")
	if wr, err := zw.Create("manifest.json"); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if _, err := wr.Write(manifestJSON); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meta := fmt.Sprintf("onebase_full_export\nversion=2\nformat=universal\nsource_db_type=%s\nsource_base=%s\ndate=%s\nhas_attachments=false\nsource_config_version=%s\n",
		db.Dialect().Name(), b.Name, time.Now().UTC().Format(time.RFC3339), versionID)
	if wr, err := zw.Create("META.txt"); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if _, err := wr.Write([]byte(meta)); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := zw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := "config_" + shortConfigVersionID(versionID) + ".obz"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Write(buf.Bytes())
}

func writeConfigHistoryFiles(zw *zip.Writer, files []configdb.ConfigFile, prefix string) error {
	for _, f := range files {
		if err := configdb.ValidatePath(f.Path); err != nil {
			return err
		}
		wr, err := zw.Create(prefix + f.Path)
		if err != nil {
			return err
		}
		if _, err := wr.Write(f.Content); err != nil {
			return err
		}
	}
	return nil
}
