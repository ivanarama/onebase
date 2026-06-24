package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"gopkg.in/yaml.v3"
)

// ── Roles & permissions management for the configurator ───────────────────────

type roleOp struct{ Op, Label string }

type rolePermSection struct {
	Kind  string // singular: catalog/document/register/inforeg/report
	Title string
	Ops   []roleOp
}

// rolePermSections defines which operations are editable per object kind.
var rolePermSections = []rolePermSection{
	{"catalog", "Справочники", []roleOp{{"read", "Чтение"}, {"write", "Запись"}, {"delete", "Удаление"}}},
	{"document", "Документы", []roleOp{{"read", "Чтение"}, {"write", "Запись"}, {"delete", "Удаление"}, {"post", "Проведение"}, {"unpost", "Отмена"}}},
	{"register", "Регистры (накопления и бухгалтерии)", []roleOp{{"read", "Чтение"}, {"write", "Запись"}}},
	{"inforeg", "Регистры сведений", []roleOp{{"read", "Чтение"}, {"write", "Запись"}, {"delete", "Удаление"}}},
	{"report", "Отчёты", []roleOp{{"run", "Запуск"}}},
}

// rolePermYAML mirrors auth.Permission for clean YAML output (no id field).
type rolePermYAML struct {
	Catalogs  map[string][]string `yaml:"catalogs,omitempty"`
	Documents map[string][]string `yaml:"documents,omitempty"`
	Registers map[string][]string `yaml:"registers,omitempty"`
	InfoRegs  map[string][]string `yaml:"inforegs,omitempty"`
	Reports   map[string][]string `yaml:"reports,omitempty"`
}

type roleYAMLOut struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description,omitempty"`
	Permissions rolePermYAML `yaml:"permissions"`
}

// permTriplets flattens an auth.Permission into "kind|entity|op" strings used by
// the matrix checkboxes (matching the value attribute on the client).
func permTriplets(p auth.Permission) []string {
	var out []string
	add := func(kind string, m map[string][]string) {
		for ent, ops := range m {
			for _, op := range ops {
				out = append(out, kind+"|"+ent+"|"+op)
			}
		}
	}
	add("catalog", p.Catalogs)
	add("document", p.Documents)
	add("register", p.Registers)
	add("inforeg", p.InfoRegs)
	add("report", p.Reports)
	return out
}

// permSummary renders a short "Справочники: 2, Документы: 1" description.
func permSummary(p auth.Permission) string {
	var parts []string
	if n := len(p.Catalogs); n > 0 {
		parts = append(parts, fmt.Sprintf("Справочники: %d", n))
	}
	if n := len(p.Documents); n > 0 {
		parts = append(parts, fmt.Sprintf("Документы: %d", n))
	}
	if n := len(p.Registers); n > 0 {
		parts = append(parts, fmt.Sprintf("Рег. накопления: %d", n))
	}
	if n := len(p.InfoRegs); n > 0 {
		parts = append(parts, fmt.Sprintf("Рег. сведений: %d", n))
	}
	if n := len(p.Reports); n > 0 {
		parts = append(parts, fmt.Sprintf("Отчёты: %d", n))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

// cfgAdminRoles renders the role list and the matrix editor.
func (h *handler) cfgAdminRoles(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := auth.NewRepo(db)
	repo.EnsureSchema(r.Context())
	roles, _ := repo.ListRoles(r.Context())

	data := h.loadCfgData(r.Context(), b, "tree")

	// JS lookup tables for the editor.
	perms := make(map[string][]string, len(roles))
	descs := make(map[string]string, len(roles))
	for _, role := range roles {
		perms[role.Name] = permTriplets(role.Permissions)
		descs[role.Name] = role.Description
	}
	permJSON, _ := json.Marshal(perms)
	descJSON, _ := json.Marshal(descs)

	bid := b.ID

	var sb strings.Builder
	sb.WriteString(`<div style="padding:16px">
	<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:14px">
	  <h3 style="margin:0;font-size:15px">Роли и права доступа</h3>
	  <button onclick="cfgRoleNew()" style="background:#1a5fa8;color:#fff;border:none;padding:5px 14px;border-radius:3px;cursor:pointer;font-size:12px">+ Добавить</button>
	</div>`)

	// ── Role list ──
	sb.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:12px">
	<tr style="background:#f1f5f9"><th style="text-align:left;padding:6px 8px;font-weight:600">Роль</th><th style="text-align:left;padding:6px 8px;font-weight:600">Описание</th><th style="text-align:left;padding:6px 8px;font-weight:600">Права</th><th style="padding:6px 8px"></th></tr>`)
	for i, role := range roles {
		bg := ""
		if i%2 == 1 {
			bg = ` style="background:#f9fafb"`
		}
		sb.WriteString(fmt.Sprintf(`<tr%s><td style="padding:5px 8px;font-weight:600">%s</td><td style="padding:5px 8px;color:#555">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px;white-space:nowrap"><button onclick="cfgRoleEdit('%s')" style="background:#2563eb;color:#fff;border:none;padding:3px 10px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">Изменить</button><button onclick="cfgRoleDel('%s')" style="color:#c00;background:none;border:none;cursor:pointer;font-size:11px" title="Удалить">✕</button></td></tr>`,
			bg, escHTML(role.Name), escHTML(role.Description), escHTML(permSummary(role.Permissions)), escAttrJS(role.Name), escAttrJS(role.Name)))
	}
	if len(roles) == 0 {
		sb.WriteString(`<tr><td colspan="4" style="padding:20px;text-align:center;color:#999">Ролей пока нет</td></tr>`)
	}
	sb.WriteString(`</table>`)

	// ── Editor (hidden until add/edit) ──
	sb.WriteString(`<div id="cfg-role-editor" style="display:none;margin-top:16px;padding:14px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:4px">
	  <h4 id="cfg-role-title" style="margin:0 0 12px;font-size:14px">Новая роль</h4>
	  <form id="cfg-role-form" onsubmit="return false">
	    <input type="hidden" name="orig_name" id="cfg-role-orig">
	    <div style="display:flex;gap:10px;flex-wrap:wrap;margin-bottom:12px">
	      <div style="flex:1;min-width:160px"><label style="font-size:11px;color:#666">Имя роли</label><input name="name" id="cfg-role-name" style="width:100%;padding:5px 7px;border:1px solid #ccc;border-radius:3px;font-size:12px"></div>
	      <div style="flex:2;min-width:200px"><label style="font-size:11px;color:#666">Описание</label><input name="description" id="cfg-role-desc" style="width:100%;padding:5px 7px;border:1px solid #ccc;border-radius:3px;font-size:12px"></div>
	    </div>
	    <div style="font-size:11px;color:#666;margin-bottom:6px">Права на объекты:</div>`)
	sb.WriteString(roleMatrixHTML(data))
	sb.WriteString(`
	    <div style="margin-top:12px;display:flex;gap:8px;align-items:center">
	      <button type="button" onclick="cfgRoleSave()" style="background:#16a34a;color:#fff;border:none;padding:6px 16px;border-radius:3px;cursor:pointer;font-size:12px">Сохранить</button>
	      <button type="button" onclick="document.getElementById('cfg-role-editor').style.display='none'" style="background:#e2e8f0;color:#333;border:none;padding:6px 12px;border-radius:3px;cursor:pointer;font-size:12px">Отмена</button>
	      <span id="cfg-role-err" style="color:#c00;font-size:11px"></span>
	    </div>
	  </form>
	</div>`)

	sb.WriteString(`</div>
<script>
var cfgRolesPerm = ` + string(permJSON) + `;
var cfgRolesDesc = ` + string(descJSON) + `;
var cfgRoleBase = '` + bid + `';
function cfgRoleClearChecks(){
  document.querySelectorAll('#cfg-role-form input[name=perm]').forEach(function(i){i.checked=false});
  document.querySelectorAll('#cfg-role-form input[data-colsel]').forEach(function(i){i.checked=false});
}
function cfgRoleNew(){
  document.getElementById('cfg-role-title').textContent='Новая роль';
  document.getElementById('cfg-role-orig').value='';
  document.getElementById('cfg-role-name').value='';
  document.getElementById('cfg-role-desc').value='';
  cfgRoleClearChecks();
  document.getElementById('cfg-role-err').textContent='';
  document.getElementById('cfg-role-editor').style.display='block';
  document.getElementById('cfg-role-name').focus();
}
function cfgRoleEdit(name){
  document.getElementById('cfg-role-title').textContent='Изменить роль: '+name;
  document.getElementById('cfg-role-orig').value=name;
  document.getElementById('cfg-role-name').value=name;
  document.getElementById('cfg-role-desc').value=cfgRolesDesc[name]||'';
  cfgRoleClearChecks();
  var triplets=cfgRolesPerm[name]||[];
  var set={};
  triplets.forEach(function(t){set[t]=true});
  document.querySelectorAll('#cfg-role-form input[name=perm]').forEach(function(i){
    if(set[i.value])i.checked=true;
  });
  document.getElementById('cfg-role-err').textContent='';
  document.getElementById('cfg-role-editor').style.display='block';
  document.getElementById('cfg-role-name').focus();
}
function cfgRoleCol(cb){
  var sec=cb.getAttribute('data-sec'), op=cb.getAttribute('data-op');
  document.querySelectorAll('#cfg-role-form input[name=perm]').forEach(function(i){
    var p=i.value.split('|');
    if(p[0]===sec && p[2]===op)i.checked=cb.checked;
  });
}
function cfgRoleSave(){
  var form=document.getElementById('cfg-role-form');
  var name=document.getElementById('cfg-role-name').value.trim();
  if(!name){document.getElementById('cfg-role-err').textContent='Укажите имя роли';return}
  var body=new URLSearchParams(new FormData(form)).toString();
  fetch('/bases/'+cfgRoleBase+'/configurator/admin/roles/save',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:body})
    .then(function(r){return r.json()}).then(function(r){
      if(r.error){document.getElementById('cfg-role-err').textContent=r.error;return}
      cfgAdmin('roles');
    });
}
function cfgRoleDel(name){
  if(!confirm('Удалить роль «'+name+'»? Назначения этой роли пользователям будут сняты.'))return;
  fetch('/bases/'+cfgRoleBase+'/configurator/admin/roles/delete',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:name})})
    .then(function(r){return r.json()}).then(function(r){
      if(r.error){alert('Ошибка: '+r.error);return}
      cfgAdmin('roles');
    });
}
</script>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(sb.String()))
}

// roleMatrixHTML builds the entity × operation checkbox matrix.
func roleMatrixHTML(data *configuratorData) string {
	ents := map[string][]string{}
	for _, c := range data.Catalogs {
		ents["catalog"] = append(ents["catalog"], c.Name)
	}
	for _, d := range data.Docs {
		ents["document"] = append(ents["document"], d.Name)
	}
	for _, rg := range data.Registers {
		ents["register"] = append(ents["register"], rg.Name)
	}
	for _, ar := range data.AccountRegisters {
		ents["register"] = append(ents["register"], ar.Name)
	}
	for _, ir := range data.InfoRegisters {
		ents["inforeg"] = append(ents["inforeg"], ir.Name)
	}
	for _, rp := range data.Reports {
		ents["report"] = append(ents["report"], rp.Name)
	}

	var sb strings.Builder
	any := false
	for _, sec := range rolePermSections {
		list := ents[sec.Kind]
		if len(list) == 0 {
			continue
		}
		any = true
		sb.WriteString(fmt.Sprintf(`<details style="margin-bottom:6px"><summary style="cursor:pointer;font-size:12px;font-weight:600;padding:4px 0">%s (%d)</summary>`, escHTML(sec.Title), len(list)))
		sb.WriteString(`<div style="overflow-x:auto"><table style="border-collapse:collapse;font-size:11px;margin:4px 0 8px">`)
		sb.WriteString(`<tr style="background:#eef2f7"><th style="text-align:left;padding:3px 8px;font-weight:600">Объект</th>`)
		for _, op := range sec.Ops {
			sb.WriteString(fmt.Sprintf(`<th style="padding:3px 8px;font-weight:600;text-align:center">%s<br><input type="checkbox" data-colsel="1" data-sec="%s" data-op="%s" onclick="cfgRoleCol(this)" title="Выделить столбец"></th>`,
				escHTML(op.Label), sec.Kind, op.Op))
		}
		sb.WriteString(`</tr>`)
		for ri, ent := range list {
			bg := ""
			if ri%2 == 1 {
				bg = ` style="background:#fafafa"`
			}
			sb.WriteString(fmt.Sprintf(`<tr%s><td style="padding:3px 8px">%s</td>`, bg, escHTML(ent)))
			for _, op := range sec.Ops {
				val := sec.Kind + "|" + ent + "|" + op.Op
				sb.WriteString(fmt.Sprintf(`<td style="text-align:center;padding:3px 8px"><input type="checkbox" name="perm" value="%s"></td>`, escHTML(val)))
			}
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</table></div></details>`)
	}
	if !any {
		return `<div style="font-size:11px;color:#999;padding:6px 0">Объекты конфигурации не загружены.</div>`
	}
	return sb.String()
}

// cfgAdminRoleSave creates or updates a role: writes the YAML to the config store
// and syncs it into the live _roles table.
func (h *handler) cfgAdminRoleSave(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		writeJSON(w, 400, map[string]any{"error": tr(lang, "Укажите имя роли")})
		return
	}
	origName := strings.TrimSpace(r.FormValue("orig_name"))
	desc := strings.TrimSpace(r.FormValue("description"))

	// Parse "kind|entity|op" triplets into permission maps.
	maps := map[string]map[string][]string{
		"catalog": {}, "document": {}, "register": {}, "inforeg": {}, "report": {},
	}
	for _, v := range r.Form["perm"] {
		parts := strings.SplitN(v, "|", 3)
		if len(parts) != 3 {
			continue
		}
		kind, ent, op := parts[0], parts[1], parts[2]
		m, ok := maps[kind]
		if !ok {
			continue
		}
		m[ent] = append(m[ent], op)
	}
	nilEmpty := func(m map[string][]string) map[string][]string {
		if len(m) == 0 {
			return nil
		}
		return m
	}

	role := &auth.Role{
		Name:        name,
		Description: desc,
		Permissions: auth.Permission{
			Catalogs:  nilEmpty(maps["catalog"]),
			Documents: nilEmpty(maps["document"]),
			Registers: nilEmpty(maps["register"]),
			InfoRegs:  nilEmpty(maps["inforeg"]),
			Reports:   nilEmpty(maps["report"]),
		},
	}

	out := roleYAMLOut{
		Name:        name,
		Description: desc,
		Permissions: rolePermYAML{
			Catalogs:  nilEmpty(maps["catalog"]),
			Documents: nilEmpty(maps["document"]),
			Registers: nilEmpty(maps["register"]),
			InfoRegs:  nilEmpty(maps["inforeg"]),
			Reports:   nilEmpty(maps["report"]),
		},
	}
	content, err := yaml.Marshal(out)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}

	targetPath := "roles/" + nameToFilename(name) + ".yaml"

	// On edit, remove any stale role file(s) for the original name (handles
	// rename and non-canonical filenames) before writing the new one.
	var stalePaths []string
	if origName != "" {
		for path, rname := range h.roleConfigFiles(r.Context(), b) {
			if rname == origName && path != targetPath {
				stalePaths = append(stalePaths, path)
			}
		}
	}
	if err := h.saveRoleConfigFile(r.Context(), b, targetPath, content, stalePaths, cfgLogin(r.Context()), name); err != nil {
		writeJSON(w, 500, map[string]any{"error": tr(lang, "Ошибка сохранения") + ": " + err.Error()})
		return
	}

	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	repo.EnsureSchema(r.Context())
	// If renamed, drop the old live role row (assignments cascade away).
	if origName != "" && origName != name {
		repo.DeleteRoleByName(r.Context(), origName)
	}
	if err := repo.SyncRoles(r.Context(), []*auth.Role{role}); err != nil {
		writeJSON(w, 500, map[string]any{"error": tr(lang, "Ошибка синхронизации") + ": " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// cfgAdminRoleDelete removes a role from the config store and the live table.
func (h *handler) cfgAdminRoleDelete(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if req.Name == "" {
		writeJSON(w, 400, map[string]any{"error": "empty name"})
		return
	}
	var paths []string
	for path, rname := range h.roleConfigFiles(r.Context(), b) {
		if rname == req.Name {
			paths = append(paths, path)
		}
	}
	if err := h.deleteRoleConfigFiles(r.Context(), b, paths, cfgLogin(r.Context()), req.Name); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.DeleteRoleByName(r.Context(), req.Name); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// cfgAdminUserRoles renders the role-assignment panel for a single user.
func (h *handler) cfgAdminUserRoles(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	uid := r.URL.Query().Get("uid")
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		w.Write([]byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := auth.NewRepo(db)
	repo.EnsureSchema(r.Context())

	users, _ := repo.List(r.Context())
	var login, fullName string
	for _, u := range users {
		if u.ID == uid {
			login = u.Login
			fullName = u.FullName
			break
		}
	}
	allRoles, _ := repo.ListRoles(r.Context())
	assigned, _ := repo.GetUserRoleIDs(r.Context(), uid)

	title := escHTML(login)
	if fullName != "" {
		title += ` <span style="color:#888;font-weight:400">` + escHTML(fullName) + `</span>`
	}

	var sb strings.Builder
	sb.WriteString(`<div style="padding:16px">
	<h3 style="margin:0 0 12px;font-size:15px">Роли пользователя: ` + title + `</h3>`)
	if len(allRoles) == 0 {
		sb.WriteString(`<div style="font-size:12px;color:#999;padding:8px 0">Ролей пока нет. Создайте роль в разделе «Роли и права».</div>`)
	} else {
		sb.WriteString(`<form id="cfg-uroles-form" onsubmit="return false"><table style="width:100%;border-collapse:collapse;font-size:12px">
		<tr style="background:#f1f5f9"><th style="width:36px;padding:6px 8px"></th><th style="text-align:left;padding:6px 8px;font-weight:600">Роль</th><th style="text-align:left;padding:6px 8px;font-weight:600">Описание</th></tr>`)
		for i, role := range allRoles {
			bg := ""
			if i%2 == 1 {
				bg = ` style="background:#f9fafb"`
			}
			chk := ""
			if assigned[role.ID] {
				chk = " checked"
			}
			sb.WriteString(fmt.Sprintf(`<tr%s><td style="padding:5px 8px;text-align:center"><input type="checkbox" name="role" value="%s"%s></td><td style="padding:5px 8px;font-weight:600">%s</td><td style="padding:5px 8px;color:#888">%s</td></tr>`,
				bg, escHTML(role.ID), chk, escHTML(role.Name), escHTML(role.Description)))
		}
		sb.WriteString(`</table></form>
		<div style="margin-top:12px;display:flex;gap:8px;align-items:center">
		  <button onclick="cfgURolesSave()" style="background:#16a34a;color:#fff;border:none;padding:6px 16px;border-radius:3px;cursor:pointer;font-size:12px">Сохранить</button>
		  <span id="cfg-uroles-err" style="color:#c00;font-size:11px"></span>
		</div>`)
	}
	sb.WriteString(`</div>
<script>
var cfgURoleBase='` + b.ID + `';
var cfgURoleUID='` + escJS(uid) + `';
function cfgURolesSave(){
  var ids=[];
  document.querySelectorAll('#cfg-uroles-form input[name=role]:checked').forEach(function(i){ids.push(i.value)});
  fetch('/bases/'+cfgURoleBase+'/configurator/admin/users/roles/save',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({userId:cfgURoleUID,roleIds:ids})})
    .then(function(r){return r.json()}).then(function(r){
      if(r.error){document.getElementById('cfg-uroles-err').textContent=r.error;return}
      cfgAdmin('users');
    });
}
</script>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(sb.String()))
}

// cfgAdminUserRolesSave applies the assignment diff for a user.
func (h *handler) cfgAdminUserRolesSave(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		UserID  string   `json:"userId"`
		RoleIDs []string `json:"roleIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	if req.UserID == "" {
		writeJSON(w, 400, map[string]any{"error": "empty user"})
		return
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)

	selected := make(map[string]bool, len(req.RoleIDs))
	for _, id := range req.RoleIDs {
		selected[id] = true
	}
	current, _ := repo.GetUserRoleIDs(r.Context(), req.UserID)
	allRoles, _ := repo.ListRoles(r.Context())
	for _, role := range allRoles {
		if selected[role.ID] && !current[role.ID] {
			repo.AssignRole(r.Context(), req.UserID, role.ID)
		} else if !selected[role.ID] && current[role.ID] {
			repo.UnassignRole(r.Context(), req.UserID, role.ID)
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ── Config-store helpers (file-based or _onebase_config table) ─────────────────

func (h *handler) saveConfigFile(ctx context.Context, b *Base, relPath string, content []byte) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		return configdb.New(db).SaveFile(ctx, relPath, content)
	}
	full, err := configdb.SafeJoin(b.Path, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, content, 0o644)
}

func (h *handler) saveRoleConfigFile(ctx context.Context, b *Base, relPath string, content []byte, stalePaths []string, author, roleName string) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			return err
		}
		return repo.ApplyFiles(ctx, []configdb.ConfigFile{{Path: relPath, Content: content}}, stalePaths, configdb.VersionOptions{
			AuthorLogin: author,
			Message:     "save role " + roleName,
		})
	}
	for _, p := range stalePaths {
		if err := h.deleteConfigFile(ctx, b, p); err != nil {
			return err
		}
	}
	return h.saveConfigFile(ctx, b, relPath, content)
}

func (h *handler) deleteConfigFile(ctx context.Context, b *Base, relPath string) error {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		return configdb.New(db).DeleteFile(ctx, relPath)
	}
	full, err := configdb.SafeJoin(b.Path, relPath)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (h *handler) deleteRoleConfigFiles(ctx context.Context, b *Base, relPaths []string, author, roleName string) error {
	if len(relPaths) == 0 {
		return nil
	}
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return err
		}
		defer db.Close()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			return err
		}
		return repo.DeleteFiles(ctx, relPaths, configdb.VersionOptions{
			AuthorLogin: author,
			Message:     "delete role " + roleName,
		})
	}
	for _, p := range relPaths {
		if err := h.deleteConfigFile(ctx, b, p); err != nil {
			return err
		}
	}
	return nil
}

// roleConfigFiles returns map[configPath]roleName for every roles/*.yaml entry.
func (h *handler) roleConfigFiles(ctx context.Context, b *Base) map[string]string {
	out := map[string]string{}
	readName := func(content []byte) string {
		var hdr struct {
			Name string `yaml:"name"`
		}
		yaml.Unmarshal(content, &hdr)
		return hdr.Name
	}
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return out
		}
		defer db.Close()
		rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path LIKE 'roles/%'`)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			var content []byte
			if rows.Scan(&path, &content) != nil {
				continue
			}
			out[path] = readName(content)
		}
		return out
	}
	dir := filepath.Join(b.Path, "roles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		content, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out["roles/"+e.Name()] = readName(content)
	}
	return out
}

// escJS escapes a string for a single-quoted JS literal inside a <script> block.
func escJS(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "<", `\x3c`)
	return r.Replace(s)
}

// escAttrJS escapes a string used as a single-quoted JS argument inside an HTML
// attribute, e.g. onclick="fn('VALUE')". JS-escapes \ and ', then HTML-encodes
// the attribute-significant characters (the browser decodes them before the JS
// string is parsed, so " stays a literal inside the single quotes).
func escAttrJS(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `&`, `&amp;`, `"`, `&quot;`, `<`, `&lt;`, `>`, `&gt;`)
	return r.Replace(s)
}
