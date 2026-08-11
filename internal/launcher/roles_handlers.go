package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/fsmode"
)

// ── Roles & permissions management for the configurator ───────────────────────

type roleOp struct{ Op, Label string }

type rolePermSection struct {
	Kind  string // singular: catalog/document/register/inforeg/report
	Key   string // канонический ключ секции в YAML роли
	Title string
	Ops   []roleOp
}

// rolePermSections defines which operations are editable per object kind.
// Права и операции вне этого списка (processors, row_access, field_access,
// disclose) редактор не показывает и не трогает — см. roles_yaml.go.
var rolePermSections = []rolePermSection{
	{"catalog", "catalogs", "Справочники", []roleOp{{"read", "Чтение"}, {"write", "Запись"}, {"delete", "Удаление"}}},
	{"document", "documents", "Документы", []roleOp{{"read", "Чтение"}, {"write", "Запись"}, {"delete", "Удаление"}, {"post", "Проведение"}, {"unpost", "Отмена"}}},
	{"register", "registers", "Регистры (накопления и бухгалтерии)", []roleOp{{"read", "Чтение"}, {"write", "Запись"}}},
	{"inforeg", "inforegs", "Регистры сведений", []roleOp{{"read", "Чтение"}, {"write", "Запись"}, {"delete", "Удаление"}}},
	{"report", "reports", "Отчёты", []roleOp{{"run", "Запуск"}}},
}

// Role saves and deletes are read-modify-write operations over two stores. A
// single launcher process must not let two of them choose the same source
// snapshot and then publish conflicting files.
var roleConfigMutationMu sync.Mutex

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
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		httpErrorDiv(w, "Не удалось подготовить схему ролей", err)
		return
	}
	roles, err := repo.ListRoles(r.Context())
	if err != nil {
		httpErrorDiv(w, "Не удалось прочитать список ролей", err)
		return
	}

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
		fmt.Fprintf(&sb, `<tr%s><td style="padding:5px 8px;font-weight:600">%s</td><td style="padding:5px 8px;color:#555">%s</td><td style="padding:5px 8px;color:#888">%s</td><td style="padding:5px 8px;white-space:nowrap"><button onclick="cfgRoleEdit('%s')" style="background:#2563eb;color:#fff;border:none;padding:3px 10px;border-radius:3px;cursor:pointer;font-size:11px;margin-right:4px">Изменить</button><button onclick="cfgRoleDel('%s')" style="color:#c00;background:none;border:none;cursor:pointer;font-size:11px" title="Удалить">✕</button></td></tr>`,
			bg, escHTML(role.Name), escHTML(role.Description), escHTML(permSummary(role.Permissions)), escAttrJS(role.Name), escAttrJS(role.Name))
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
	sb.WriteString(roleMatrixHTML(data, staleRolePerms(roles, data)))
	sb.WriteString(`
	    <div style="margin-top:12px;display:flex;gap:8px;align-items:center">`)
	if data != nil && data.Error == "" {
		sb.WriteString(`<button type="button" onclick="cfgRoleSave()" style="background:#16a34a;color:#fff;border:none;padding:6px 16px;border-radius:3px;cursor:pointer;font-size:12px">Сохранить</button>`)
	} else {
		sb.WriteString(`<button type="button" disabled title="Конфигурация не загружена" style="background:#94a3b8;color:#fff;border:none;padding:6px 16px;border-radius:3px;font-size:12px">Сохранить</button>`)
	}
	sb.WriteString(`
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
	triplets.forEach(function(t){set[String(t).toLowerCase()]=true});
	document.querySelectorAll('#cfg-role-form input[name=perm]').forEach(function(i){
	  if(set[String(i.value).toLowerCase()])i.checked=true;
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
	writeBody(w, []byte(sb.String()))
}

// roleObjectsByKind собирает имена объектов конфигурации по видам прав.
func roleObjectsByKind(data *configuratorData) map[string][]string {
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
	return ents
}

// staleRolePerms собирает права ролей на объекты, которых в конфигурации уже
// нет: справочник создали, выдали на него права, потом удалили — строка в роли
// осталась. «Проверка конфигурации» такие ссылки показывает (CheckCrossRefs), а
// в матрице строки для них не было: снять право через интерфейс было нечем.
//
// Сравнение регистронезависимое — как в проверке конфигурации; в матрицу имя
// попадает в том написании, в каком лежит в роли, иначе чекбокс не совпадёт с
// триплетом роли и право осталось бы неснимаемым.
//
// Незагруженная конфигурация (data.Error) стирает все объекты разом, и тогда
// «нет в конфигурации» получили бы вообще все права роли — админ снял бы по
// подсказке рабочие права. Пока конфигурация не прочитана, отличить удалённый
// объект от невидимого нельзя, поэтому не помечаем ничего.
func staleRolePerms(roles []*auth.Role, data *configuratorData) map[string][]string {
	if data == nil || data.Error != "" {
		return nil
	}
	known := map[string]map[string]bool{}
	for kind, list := range roleObjectsByKind(data) {
		set := make(map[string]bool, len(list))
		for _, name := range list {
			set[strings.ToLower(name)] = true
		}
		known[kind] = set
	}

	seen := map[string]bool{}
	stale := map[string][]string{}
	for _, role := range roles {
		for kind, m := range rolePermMaps(role.Permissions) {
			for entity, ops := range m {
				if entity == "" || known[kind][strings.ToLower(entity)] {
					continue
				}
				if !roleHasManagedOp(kind, ops) {
					continue
				}
				if key := kind + "|" + entity; !seen[key] {
					seen[key] = true
					stale[kind] = append(stale[kind], entity)
				}
			}
		}
	}
	for kind := range stale {
		sort.Strings(stale[kind])
	}
	return stale
}

func roleHasManagedOp(kind string, ops []string) bool {
	for _, section := range rolePermSections {
		if section.Kind != kind {
			continue
		}
		for _, raw := range ops {
			for _, op := range auth.SplitPermissionOps(raw) {
				for _, managed := range section.Ops {
					if op == managed.Op {
						return true
					}
				}
			}
		}
		return false
	}
	return false
}

// rolePermMaps раскладывает права по видам объектов, которыми управляет матрица.
func rolePermMaps(p auth.Permission) map[string]map[string][]string {
	return map[string]map[string][]string{
		"catalog":  p.Catalogs,
		"document": p.Documents,
		"register": p.Registers,
		"inforeg":  p.InfoRegs,
		"report":   p.Reports,
	}
}

// roleMatrixHTML builds the entity × operation checkbox matrix. Объекты из
// stale дописываются к своим секциям отдельными помеченными строками — только
// так остаточное право на удалённый объект можно снять из интерфейса.
func roleMatrixHTML(data *configuratorData, stale map[string][]string) string {
	ents := roleObjectsByKind(data)

	var sb strings.Builder
	any := false
	anyStale := false
	for _, sec := range rolePermSections {
		list, missing := ents[sec.Kind], stale[sec.Kind]
		if len(list)+len(missing) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&sb, `<details style="margin-bottom:6px"><summary style="cursor:pointer;font-size:12px;font-weight:600;padding:4px 0">%s (%d)</summary>`, escHTML(sec.Title), len(list)+len(missing))
		sb.WriteString(`<div style="overflow-x:auto"><table style="border-collapse:collapse;font-size:11px;margin:4px 0 8px">`)
		sb.WriteString(`<tr style="background:#eef2f7"><th style="text-align:left;padding:3px 8px;font-weight:600">Объект</th>`)
		for _, op := range sec.Ops {
			fmt.Fprintf(&sb, `<th style="padding:3px 8px;font-weight:600;text-align:center">%s<br><input type="checkbox" data-colsel="1" data-sec="%s" data-op="%s" onclick="cfgRoleCol(this)" title="Выделить столбец"></th>`,
				escHTML(op.Label), sec.Kind, op.Op)
		}
		sb.WriteString(`</tr>`)
		row := func(ent string, ri int, gone bool) {
			style := ""
			if gone {
				style = ` style="background:#fff7ed"`
			} else if ri%2 == 1 {
				style = ` style="background:#fafafa"`
			}
			label := escHTML(ent)
			if gone {
				anyStale = true
				label = `<span style="color:#9a3412">` + label +
					` <span title="Объекта нет в конфигурации">⚠ нет в конфигурации</span></span>`
			}
			fmt.Fprintf(&sb, `<tr%s><td style="padding:3px 8px">%s</td>`, style, label)
			for _, op := range sec.Ops {
				val := sec.Kind + "|" + ent + "|" + op.Op
				fmt.Fprintf(&sb, `<td style="text-align:center;padding:3px 8px"><input type="checkbox" name="perm" value="%s"></td>`, escHTML(val))
			}
			sb.WriteString(`</tr>`)
		}
		for ri, ent := range list {
			row(ent, ri, false)
		}
		for _, ent := range missing {
			row(ent, 0, true)
		}
		sb.WriteString(`</table></div></details>`)
	}
	if !any {
		return `<div style="font-size:11px;color:#999;padding:6px 0">Объекты конфигурации не загружены.</div>`
	}
	if anyStale {
		sb.WriteString(`<div style="font-size:11px;color:#9a3412;background:#fff7ed;border:1px solid #fed7aa;border-radius:3px;padding:6px 8px;margin-top:6px">` +
			`⚠ Строки «нет в конфигурации» — права на удалённые объекты; их показывает «Проверка конфигурации». ` +
			`Снятие галочек убирает только показанные операции; права вне матрицы редактируются в YAML.</div>`)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if !validObjectName(name) {
		writeJSON(w, 400, map[string]any{"error": tr(lang, "Укажите имя роли")})
		return
	}
	origName := strings.TrimSpace(r.FormValue("orig_name"))
	if origName != "" && !validObjectName(origName) {
		writeJSON(w, 400, map[string]any{"error": tr(lang, "Недопустимое имя объекта")})
		return
	}
	if origName != "" && origName != name && strings.EqualFold(origName, name) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "case-only role rename is not supported"})
		return
	}
	roleConfigMutationMu.Lock()
	defer roleConfigMutationMu.Unlock()

	// The form is a full matrix snapshot. If metadata cannot be loaded now,
	// treating the missing rows as unchecked would revoke all managed rights.
	// The server-side check is authoritative; a hidden client flag is not.
	data := h.loadCfgData(r.Context(), b, "tree")
	if data == nil || data.Error != "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": tr(lang, "Конфигурация не загружена")})
		return
	}
	desc := strings.TrimSpace(r.FormValue("description"))

	// Parse "kind|entity|op" triplets into permission maps.
	edits := make([]roleSectionEdit, 0, len(rolePermSections))
	byKind := make(map[string]int, len(rolePermSections))
	for _, sec := range rolePermSections {
		managed := make(map[string]bool, len(sec.Ops))
		for _, op := range sec.Ops {
			managed[op.Op] = true
		}
		byKind[sec.Kind] = len(edits)
		edits = append(edits, roleSectionEdit{
			kind: sec.Kind, key: sec.Key, managed: managed, perms: map[string][]string{},
		})
	}
	for _, v := range r.Form["perm"] {
		parts := strings.SplitN(v, "|", 3)
		if len(parts) != 3 {
			continue
		}
		kind, ent, op := parts[0], strings.TrimSpace(parts[1]), parts[2]
		i, ok := byKind[kind]
		if !ok || ent == "" || !edits[i].managed[op] {
			continue
		}
		if !containsRoleOp(edits[i].perms[ent], op) {
			edits[i].perms[ent] = append(edits[i].perms[ent], op)
		}
	}

	// Матрица владеет лишь частью файла роли, поэтому правим существующий YAML,
	// а не собираем его заново: processors, row_access (план 79), field_access
	// (план 88) и операции вроде disclose иначе исчезали бы при первом же
	// сохранении роли из конфигуратора — вместе с комментариями.
	targetPath := "roles/" + nameToFilename(name) + ".yaml"
	if err := configdb.ValidatePath(targetPath); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	contents, roleFiles, err := h.roleConfigSnapshot(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	var existingPath string
	var existing []byte
	if origName != "" {
		existingPath, existing = roleConfigFileFromSnapshot(contents, roleFiles, origName)
		if existingPath == "" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "role source not found in configuration"})
			return
		}
	}
	if existingPath != "" && existingPath != targetPath && strings.EqualFold(existingPath, targetPath) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "case-only role path rename is not supported"})
		return
	}
	if roleConfigTargetOccupied(roleFiles, existingPath, targetPath, name) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": tr(lang, "Имя роли уже используется")})
		return
	}
	// _roles has an exact UNIQUE(name), while authorization treats names
	// case-insensitively. Check that stronger invariant before touching config.
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	liveRoles, err := repo.ListRoles(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if liveRoleTargetOccupied(liveRoles, origName, name) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "role name is already in use"})
		return
	}

	content, err := applyRoleMatrixToYAML(existing, name, desc, edits)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// Живая роль в _roles должна повторять записанный файл целиком, иначе
	// сохранение из интерфейса снимет построчный доступ и маскирование в
	// рантайме, оставив их в конфигурации.
	role, err := auth.ParseRole(content)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	role.Name, role.Description = name, desc

	// On edit, remove any stale role file(s) for the original name (handles
	// rename and non-canonical filenames) before writing the new one.
	var stalePaths []string
	if existingPath != "" && existingPath != targetPath {
		stalePaths = append(stalePaths, existingPath)
	}
	if origName != "" {
		for path, rname := range roleFiles {
			if rname == origName && path != targetPath && path != existingPath {
				stalePaths = append(stalePaths, path)
			}
		}
	}
	if err := h.saveRoleConfigFile(r.Context(), b, targetPath, content, stalePaths, cfgLogin(r.Context()), name); err != nil {
		writeJSON(w, 500, map[string]any{"error": tr(lang, "Ошибка сохранения") + ": " + err.Error()})
		return
	}

	// If renamed, drop the old live role row (assignments cascade away).
	//
	// Сбой удаления нельзя пропускать дальше: старая строка роли переживёт
	// переименование вместе со всеми назначениями, и пользователи останутся с
	// правами роли, которой в конфигурации уже нет. В таблице ролей её при
	// этом не видно — расхождение обнаружится только при разборе инцидента.
	if origName != "" && origName != name {
		if err := repo.DeleteRoleByName(r.Context(), origName); err != nil {
			writeJSON(w, 500, map[string]any{"error": tr(lang, "Ошибка синхронизации") + ": " + err.Error()})
			return
		}
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
	req.Name = strings.TrimSpace(req.Name)
	if !validObjectName(req.Name) {
		writeJSON(w, 400, map[string]any{"error": "empty name"})
		return
	}
	roleConfigMutationMu.Lock()
	defer roleConfigMutationMu.Unlock()

	_, roleFiles, err := h.roleConfigSnapshot(r.Context(), b)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	var paths []string
	for path, rname := range roleFiles {
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
		writeBody(w, []byte(`<div style="padding:16px;color:#c00">Нет подключения к БД</div>`))
		return
	}
	repo := auth.NewRepo(db)
	// Ошибки чтения здесь нельзя проглатывать: при сбое списки остаются
	// пустыми, и панель ниже рисует «Ролей пока нет. Создайте роль» — админ
	// читает это как факт и заводит дубли уже существующих ролей. Пустой
	// список снятых галочек так же неотличим от «у пользователя нет ролей».
	if err := repo.EnsureSchema(r.Context()); err != nil {
		panelError(w, err)
		return
	}

	users, err := repo.List(r.Context())
	if err != nil {
		panelError(w, err)
		return
	}
	var login, fullName string
	for _, u := range users {
		if u.ID == uid {
			login = u.Login
			fullName = u.FullName
			break
		}
	}
	allRoles, err := repo.ListRoles(r.Context())
	if err != nil {
		panelError(w, err)
		return
	}
	assigned, err := repo.GetUserRoleIDs(r.Context(), uid)
	if err != nil {
		panelError(w, err)
		return
	}

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
			fmt.Fprintf(&sb, `<tr%s><td style="padding:5px 8px;text-align:center"><input type="checkbox" name="role" value="%s"%s></td><td style="padding:5px 8px;font-weight:600">%s</td><td style="padding:5px 8px;color:#888">%s</td></tr>`,
				bg, escHTML(role.ID), chk, escHTML(role.Name), escHTML(role.Description))
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
	writeBody(w, []byte(sb.String()))
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
	// Диф считается от текущего состояния, поэтому сбой чтения тут опаснее
	// сбоя записи: при пустом current ни одна роль не попадёт в ветку снятия,
	// цикл отработает «успешно», и админ получит {"ok":true} на запрос, где он
	// снимал роль. Пустой allRoles точно так же превращает сохранение в no-op.
	current, err := repo.GetUserRoleIDs(r.Context(), req.UserID)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	allRoles, err := repo.ListRoles(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// Транзакции auth.Repo наружу не даёт, поэтому отказ на середине оставляет
	// часть ролей применённой. Поэтому в ошибке называется роль, на которой
	// всё встало: по ней видно, где остановился диф. Молчаливое {"ok":true}
	// было бы хуже любого частичного результата — админ считал бы, что роль
	// снята, а она осталась. Панель при следующем открытии перечитывает
	// фактическое состояние из БД.
	lang := resolveLang(r)
	applyErr := func(roleName string, err error) {
		writeJSON(w, 500, map[string]any{
			"error": tr(lang, "Ошибка сохранения") + " (" + roleName + "): " + err.Error(),
		})
	}
	for _, role := range allRoles {
		if selected[role.ID] && !current[role.ID] {
			if err := repo.AssignRole(r.Context(), req.UserID, role.ID); err != nil {
				applyErr(role.Name, err)
				return
			}
		} else if !selected[role.ID] && current[role.ID] {
			if err := repo.UnassignRole(r.Context(), req.UserID, role.ID); err != nil {
				applyErr(role.Name, err)
				return
			}
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// panelError рисует ошибку вместо HTML-панели администрирования. Отдельная
// функция, а не пустая панель: пустой список ролей или пользователей админ
// читает как факт («ролей нет»), и это подталкивает его к неверному действию.
func panelError(w http.ResponseWriter, err error) {
	writeBody(w, []byte(`<div style="padding:16px;color:#c00">`+escHTML(err.Error())+`</div>`))
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
	if err := os.MkdirAll(filepath.Dir(full), fsmode.Dir); err != nil { //nolint:gosec // G703: путь построен configdb.SafeJoin — он и есть guard от traversal, gosec его не распознаёт
		return err
	}
	return os.WriteFile(full, content, fsmode.File) //nolint:gosec // G703: путь построен configdb.SafeJoin — он и есть guard от traversal, gosec его не распознаёт
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
	return saveRoleConfigFileOnDisk(b.Path, relPath, content, stalePaths)
}

// saveRoleConfigFileOnDisk stages and syncs complete content before mutation.
// Existing/renamed roles keep their inode and security metadata; ordinary
// rewrite failures restore the previous bytes (and source path for a rename).
func saveRoleConfigFileOnDisk(root, relPath string, content []byte, stalePaths []string) error {
	target, err := configdb.SafeJoin(root, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), fsmode.Dir); err != nil {
		return err
	}
	type staleRolePath struct {
		full string
		info os.FileInfo
	}
	staleFiles := make([]staleRolePath, 0, len(stalePaths))
	seenStale := make(map[string]bool, len(stalePaths))
	for _, stalePath := range stalePaths {
		if stalePath == relPath {
			continue
		}
		if strings.EqualFold(stalePath, relPath) {
			return fmt.Errorf("case-only role path rename is unsafe: %s -> %s", stalePath, relPath)
		}
		if seenStale[stalePath] {
			continue
		}
		seenStale[stalePath] = true
		stale, err := configdb.SafeJoin(root, stalePath)
		if err != nil {
			return err
		}
		info, err := os.Lstat(stale)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("role source %s is not a regular file", stalePath)
		}
		staleFiles = append(staleFiles, staleRolePath{full: stale, info: info})
	}
	if len(staleFiles) > 1 {
		return fmt.Errorf("refusing non-atomic removal of %d old role files", len(staleFiles))
	}

	mode := fsmode.File
	targetExists := false
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("role target %s is not a regular file", relPath)
		}
		targetExists = true
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return statErr
	} else if len(staleFiles) > 0 {
		// A rename should retain the source file's permissions. Snapshot
		// validation makes stalePaths contain at most one matching role file.
		mode = staleFiles[0].info.Mode().Perm()
	}
	if targetExists && len(staleFiles) > 0 {
		return fmt.Errorf("role target %s already exists during rename", relPath)
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".onebase-role-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	n, writeErr := tmp.Write(content)
	if writeErr != nil || n != len(content) {
		if writeErr == nil {
			writeErr = fmt.Errorf("short role write: %d of %d bytes", n, len(content))
		}
		closeErr := tmp.Close()
		closed = true
		return errors.Join(writeErr, closeErr)
	}
	if err := tmp.Sync(); err != nil {
		closeErr := tmp.Close()
		closed = true
		return errors.Join(err, closeErr)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true

	// Replacing an existing inode with the temp file would discard its owner,
	// POSIX ACL/xattrs or Windows DACL. Rewrite that inode only after the full
	// new content has been staged and synced; ordinary write failures are
	// compensated in place with the previous bytes.
	if targetExists || len(staleFiles) == 1 {
		if err := os.Remove(tmpName); err != nil {
			return err
		}
		tmpName = ""
	}
	if targetExists {
		previous, err := os.ReadFile(target) //nolint:gosec // target is guarded by SafeJoin
		if err != nil {
			return err
		}
		return rewriteRoleFileInPlace(target, content, previous)
	}
	if len(staleFiles) == 1 {
		stale := staleFiles[0]
		previous, err := os.ReadFile(stale.full) //nolint:gosec // stale.full is guarded by SafeJoin
		if err != nil {
			return err
		}
		// Moving the source inode first preserves all of its security metadata
		// across a rename. If the following rewrite fails, restore both bytes
		// and path.
		if err := os.Rename(stale.full, target); err != nil {
			return err
		}
		if err := rewriteRoleFileInPlace(target, content, previous); err != nil {
			return errors.Join(err, os.Rename(target, stale.full))
		}
		return nil
	}

	// A newly created role has no metadata to preserve. Temp and target share a
	// directory, so publication is atomic and readers never see partial YAML.
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

func rewriteRoleFileInPlace(path string, content, rollback []byte) error {
	if err := writeRoleBytesInPlace(path, content); err != nil {
		restoreErr := writeRoleBytesInPlace(path, rollback)
		return errors.Join(err, restoreErr)
	}
	return nil
}

func writeRoleBytesInPlace(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0) //nolint:gosec // validated internal role path
	if err != nil {
		return err
	}
	n, writeErr := f.Write(content)
	if writeErr != nil || n != len(content) {
		if writeErr == nil {
			writeErr = fmt.Errorf("short role write: %d of %d bytes", n, len(content))
		}
		return errors.Join(writeErr, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
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

// roleConfigSnapshot reads and parses the complete editable role set. Missing
// roles/ is a valid empty set; every other storage or YAML error is returned so
// a read-modify-write handler cannot mistake an unreadable role for absence.
func (h *handler) roleConfigSnapshot(ctx context.Context, b *Base) (map[string][]byte, map[string]string, error) {
	contents := make(map[string][]byte)
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return nil, nil, err
		}
		defer db.Close()
		files, err := configdb.New(db).ListByPrefix(ctx, "roles/")
		if err != nil {
			return nil, nil, err
		}
		for _, file := range files {
			if err := configdb.ValidatePath(file.Path); err != nil {
				return nil, nil, fmt.Errorf("invalid role config path %q: %w", file.Path, err)
			}
			rel := strings.TrimPrefix(file.Path, "roles/")
			if !strings.HasSuffix(rel, ".yaml") {
				continue
			}
			if rel == "" || strings.Contains(rel, "/") {
				return nil, nil, fmt.Errorf("nested role YAML is not supported: %s", file.Path)
			}
			contents[file.Path] = append([]byte(nil), file.Content...)
		}
	} else {
		dir, err := configdb.SafeJoin(b.Path, "roles")
		if err != nil {
			return nil, nil, err
		}
		dirInfo, err := os.Stat(dir)
		if os.IsNotExist(err) {
			return contents, map[string]string{}, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("stat roles directory: %w", err)
		}
		if !dirInfo.IsDir() {
			return nil, nil, fmt.Errorf("roles path is not a directory: %s", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("read roles directory: %w", err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return nil, nil, fmt.Errorf("stat role %s: %w", entry.Name(), err)
			}
			if !info.Mode().IsRegular() {
				return nil, nil, fmt.Errorf("role %s is not a regular file", entry.Name())
			}
			relPath := "roles/" + entry.Name()
			full, err := configdb.SafeJoin(b.Path, relPath)
			if err != nil {
				return nil, nil, err
			}
			content, err := os.ReadFile(full) //nolint:gosec // path is guarded by SafeJoin
			if err != nil {
				return nil, nil, fmt.Errorf("read role %s: %w", relPath, err)
			}
			contents[relPath] = content
		}
	}

	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	roleFiles := make(map[string]string, len(paths))
	for _, path := range paths {
		for existingPath := range roleFiles {
			if strings.EqualFold(existingPath, path) {
				return nil, nil, fmt.Errorf("role path collision: %s and %s", existingPath, path)
			}
		}
		role, err := parseRoleYAMLStrict(contents[path])
		if err != nil {
			return nil, nil, fmt.Errorf("parse role %s: %w", path, err)
		}
		name := strings.TrimSpace(role.Name)
		if name == "" || name != role.Name || !validObjectName(name) {
			return nil, nil, fmt.Errorf("role %s has an invalid name", path)
		}
		if err := configdb.ValidatePath("roles/" + nameToFilename(name) + ".yaml"); err != nil {
			return nil, nil, fmt.Errorf("role %s has an unsafe name: %w", path, err)
		}
		for existingPath, existingName := range roleFiles {
			if strings.EqualFold(existingName, name) {
				return nil, nil, fmt.Errorf("role name collision %q in %s and %s", name, existingPath, path)
			}
		}
		roleFiles[path] = name
	}
	return contents, roleFiles, nil
}

func roleConfigFileFromSnapshot(contents map[string][]byte, roleFiles map[string]string, names ...string) (string, []byte) {
	paths := make([]string, 0, len(roleFiles))
	for path := range roleFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, name := range names {
		if name == "" {
			continue
		}
		for _, path := range paths {
			if roleFiles[path] == name {
				return path, contents[path]
			}
		}
	}
	return "", nil
}

func roleConfigTargetOccupied(roleFiles map[string]string, sourcePath, targetPath, targetName string) bool {
	for path, name := range roleFiles {
		if path == sourcePath {
			continue
		}
		if strings.EqualFold(path, targetPath) || strings.EqualFold(name, targetName) {
			return true
		}
	}
	return false
}

func liveRoleTargetOccupied(roles []*auth.Role, originalName, targetName string) bool {
	for _, role := range roles {
		if originalName != "" && role.Name == originalName {
			continue
		}
		if strings.EqualFold(role.Name, targetName) {
			return true
		}
	}
	return false
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
