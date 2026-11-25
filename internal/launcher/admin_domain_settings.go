package launcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

var cfgDomainSettingsTmpl = template.Must(template.New("cfg-domain-settings").Funcs(template.FuncMap{"t": tr}).Parse(`
<form action="/bases/{{.BaseID}}/configurator/admin/settings/domain" method="post" onsubmit="cfgSaveDomainSettings(event,this)"
  data-saved="{{t .Lang "✓ Сохранено — перезапустите базу"}}" data-expired="{{t .Lang "Сессия конфигуратора завершена — войдите заново"}}" data-unexpected="{{t .Lang "Неожиданный ответ сервера"}}">
  <fieldset style="display:grid;gap:7px;margin:16px 0 0;border:0;padding:0;font-size:12px">
    <legend style="margin-bottom:8px;font-size:13px;font-weight:600">{{t .Lang "Доменный вход"}}</legend>
    <label>{{t .Lang "Публичный HTTPS-адрес базы"}}
      <input type="url" name="public_url" value="{{.Settings.PublicURL}}" placeholder="https://onebase.example.org" style="display:block;width:100%;padding:5px">
    </label>
    <label>{{t .Lang "Имя службы Kerberos (SPN)"}}
      <input type="text" name="spn" value="{{.Settings.SPN}}" placeholder="HTTP/onebase.example.org" style="display:block;width:100%;padding:5px">
    </label>
    <label>{{t .Lang "Домен Kerberos (realm, для Linux)"}}
      <input type="text" name="realm" value="{{.Settings.Realm}}" placeholder="EXAMPLE.ORG" style="display:block;width:100%;padding:5px">
    </label>
    <label>{{t .Lang "Путь к keytab на сервере Linux"}}
      <input type="text" name="keytab" value="{{.Settings.Keytab}}" placeholder="/etc/onebase/http.keytab" style="display:block;width:100%;padding:5px">
    </label>
    <small>{{t .Lang "Для сервера Ubuntu заполните все четыре поля. Для сервера Windows достаточно HTTPS-адреса и SPN. Сохранённые значения заменяют параметры доменного входа из окружения службы. Чтобы вернуться к переменным окружения, очистите все поля и сохраните. После изменения перезапустите базу."}}</small>
    <div><button type="submit" class="btn-save">{{t .Lang "Сохранить настройки"}}</button> <span role="status"></span></div>
  </fieldset>
</form>
<script>
async function cfgSaveDomainSettings(event,form){
  event.preventDefault();
  var body=Object.fromEntries(new FormData(form));
  var fields=form.querySelector('fieldset');
  var status=form.querySelector('[role="status"]');
  fields.disabled=true;
  status.textContent='';
  try {
    var response=await fetch(form.action,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
    if(response.redirected){throw new Error(form.dataset.expired)}
    if(!(response.headers.get('Content-Type')||'').includes('application/json')){throw new Error(form.dataset.unexpected)}
    var result=await response.json();
    if(!response.ok||!result.ok){throw new Error(result.error||form.dataset.unexpected)}
    status.style.color='#15803d';
    status.textContent=form.dataset.saved;
  } catch(e) {
    status.style.color='#b91c1c';
    status.textContent=e.message;
  } finally {
    fields.disabled=false;
  }
}
</script>`))

func cfgDomainSettingsSection(r *http.Request, baseID string, repo *auth.Repo) (string, error) {
	if user := cfgUserFromContext(r.Context()); user == nil || !user.IsAdmin {
		return "", nil
	}
	settings, err := repo.StoredDomainSettings(r.Context())
	if err != nil {
		return "", err
	}
	var section bytes.Buffer
	err = cfgDomainSettingsTmpl.Execute(&section, map[string]any{
		"BaseID": baseID, "Lang": resolveLang(r), "Settings": settings,
	})
	return section.String(), err
}

// The configurator allows initial setup of an empty base. A domain setting
// or login binding can only be changed by an authenticated administrator.
func (h *handler) cfgDomainAdminDB(w http.ResponseWriter, r *http.Request) *storage.DB {
	if actor := cfgUserFromContext(r.Context()); actor == nil || !actor.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": tr(resolveLang(r), "Доступ запрещён. Только для администраторов.")})
		return nil
	}
	base, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return nil
	}
	db, err := getAuthDB(r.Context(), base)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": errText(r, err)})
		return nil
	}
	return db
}

func decodeCfgDomainJSON(w http.ResponseWriter, r *http.Request, request any) bool {
	// The longest field in these requests is a server-side file path.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err == nil {
		err = json.Unmarshal(body, request)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errText(r, err)})
		return false
	}
	return true
}

func (h *handler) cfgAdminDomainSettingsSave(w http.ResponseWriter, r *http.Request) {
	db := h.cfgDomainAdminDB(w, r)
	if db == nil {
		return
	}
	var req struct {
		PublicURL *string `json:"public_url"`
		SPN       *string `json:"spn"`
		Realm     *string `json:"realm"`
		Keytab    *string `json:"keytab"`
	}
	if !decodeCfgDomainJSON(w, r, &req) {
		return
	}
	if req.PublicURL == nil || req.SPN == nil || req.Realm == nil || req.Keytab == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": tr(resolveLang(r), "Передайте все четыре поля доменного входа")})
		return
	}
	settings := auth.DomainSettings{PublicURL: *req.PublicURL, SPN: *req.SPN, Realm: *req.Realm, Keytab: *req.Keytab}
	if err := auth.NewRepo(db).SaveDomainSettings(r.Context(), settings); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, auth.ErrInvalidDomainSettings) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": errText(r, err)})
		return
	}
	logCfgSessionAudit(r, db, "domain_settings_changed", "", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
