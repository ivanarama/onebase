package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

var tmpl = template.Must(template.New("root").Funcs(template.FuncMap{
	"lower": strings.ToLower,
	"str":   func(v any) string { return fmt.Sprintf("%v", v) },
	"isRef": func(t any) bool { return strings.HasPrefix(fmt.Sprintf("%v", t), "reference:") },
	"fmtDate": func(v any) string {
		if t, ok := v.(time.Time); ok {
			return t.Format("02.01.2006")
		}
		if s, ok := v.(string); ok && len(s) >= 10 {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t.Format("02.01.2006")
			}
		}
		return fmt.Sprintf("%v", v)
	},
	"filterVal": func(params storage.ListParams, fieldName string) storage.FilterValue {
		return filterValue(params, fieldName)
	},
	"sortDir": func(params storage.ListParams, fieldName string) string {
		if params.Sort == fieldName {
			if strings.ToLower(params.Dir) == "desc" {
				return "desc"
			}
			return "asc"
		}
		return ""
	},
	"sortIcon": func(params storage.ListParams, fieldName string) string {
		if params.Sort != fieldName {
			return "⇅"
		}
		if strings.ToLower(params.Dir) == "desc" {
			return "↓"
		}
		return "↑"
	},
	"nextDir": func(params storage.ListParams, fieldName string) string {
		if params.Sort == fieldName && strings.ToLower(params.Dir) != "desc" {
			return "desc"
		}
		return "asc"
	},
	"hasFilter": func(params storage.ListParams) bool {
		return len(params.Filters) > 0
	},
	"filterQuery": func(params storage.ListParams) string {
		var parts []string
		for k, v := range params.Filters {
			if v.From != "" {
				parts = append(parts, "f."+k+".from="+v.From)
			}
			if v.To != "" {
				parts = append(parts, "f."+k+".to="+v.To)
			}
			if v.Value != "" {
				parts = append(parts, "f."+k+"="+v.Value)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return "&" + strings.Join(parts, "&")
	},
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
	"rowIdx": func(row map[string]any) int {
		if v, ok := row["строка"]; ok {
			switch t := v.(type) {
			case int:
				return t
			case int32:
				return int(t)
			case int64:
				return int(t)
			}
		}
		return 0
	},
	"jsJSON": func(v any) template.JS {
		b, err := json.Marshal(v)
		if err != nil {
			return template.JS("null")
		}
		return template.JS(b)
	},
}).Parse(tplHead + tplNav + tplIndex + tplList + tplForm + tplRegister + tplReport + tplAbout + tplDeleteMarked + tplInfoReg))

const tplHead = `
{{define "head"}}<!DOCTYPE html>
<html lang="ru"><head><meta charset="UTF-8">
<title>{{if .Cfg.AppName}}{{.Cfg.AppName}}{{else}}onebase{{end}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;display:flex;flex-direction:column;min-height:100vh;background:#f5f5f5}
.topbar{background:#1e293b;color:#fff;padding:0 16px;display:flex;align-items:center;height:38px;flex-shrink:0;position:sticky;top:0;z-index:100}
.topbar-title{font-size:14px;font-weight:600;color:#7dd3fc;flex:1}
.sys-menu{position:relative}
.sys-btn{background:none;border:none;color:#cbd5e1;cursor:pointer;font-size:15px;padding:6px 10px;border-radius:5px;line-height:1}
.sys-btn:hover{background:#334155;color:#fff}
.sys-drop{display:none;position:absolute;right:0;top:calc(100% + 4px);background:#fff;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.18);min-width:170px;overflow:hidden;z-index:200}
.sys-drop.open{display:block}
.sys-drop a,.sys-drop button{display:block;padding:10px 16px;color:#334155;text-decoration:none;font-size:14px;width:100%;text-align:left;background:none;border:none;cursor:pointer;border-bottom:1px solid #f1f5f9}
.sys-drop a:last-child,.sys-drop button:last-child{border-bottom:none}
.sys-drop a:hover,.sys-drop button:hover{background:#f1f5f9}
.app-body{display:flex;flex:1;overflow:hidden}
aside{width:210px;background:#1e293b;color:#fff;padding:16px 0;flex-shrink:0;overflow-y:auto}
aside .sec{font-size:11px;text-transform:uppercase;color:#94a3b8;margin:14px 12px 4px;letter-spacing:.05em}
aside a{display:block;padding:6px 14px;color:#cbd5e1;text-decoration:none;font-size:14px;margin:1px 6px;border-radius:5px}
aside a:hover{background:#334155;color:#fff}
main{flex:1;padding:28px;overflow-y:auto}
h2{font-size:22px;font-weight:600;margin-bottom:20px;color:#1e293b}
h3{font-size:16px;font-weight:600;margin:24px 0 10px;color:#1e293b}
.card{background:#fff;border-radius:10px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,.1);max-width:900px}
table{width:100%;border-collapse:collapse;font-size:14px}
th{text-align:left;padding:10px 12px;border-bottom:2px solid #e2e8f0;color:#64748b;font-weight:600}
th a{color:#64748b;text-decoration:none}
th a:hover{color:#1e293b}
td{padding:10px 12px;border-bottom:1px solid #f1f5f9;color:#334155;font-size:14px}
tr:last-child td{border-bottom:none}
tr:hover td{background:#f8fafc}
.btn{display:inline-block;padding:8px 18px;border-radius:7px;font-size:14px;font-weight:500;text-decoration:none;cursor:pointer;border:none;line-height:1}
.btn-primary{background:#3b82f6;color:#fff}.btn-primary:hover{background:#2563eb}
.btn-post{background:#e8b400;color:#1a1a1a;font-weight:700}.btn-post:hover{background:#d4a200}
.btn-secondary{background:#e2e8f0;color:#374151}.btn-secondary:hover{background:#cbd5e1}
.btn-cancel{background:transparent;color:#64748b;border:1px solid #e2e8f0}.btn-cancel:hover{background:#f1f5f9}
.btn-sm{padding:5px 12px;font-size:13px}
.btn-danger{background:#ef4444;color:#fff}.btn-danger:hover{background:#dc2626}
.form-group{margin-bottom:16px}
label{display:block;font-size:13px;font-weight:500;margin-bottom:5px;color:#475569}
input[type=text],input[type=datetime-local],input[type=date],input[type=number],select{width:100%;padding:9px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;outline:none;background:#fff}
input:focus,select:focus{border-color:#3b82f6;box-shadow:0 0 0 3px rgba(59,130,246,.15)}
.error{background:#fef2f2;border:1px solid #fecaca;color:#dc2626;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px}
.empty{color:#94a3b8;text-align:center;padding:48px;font-size:15px}
.row-top{display:flex;justify-content:space-between;align-items:center;margin-bottom:16px;max-width:900px}
details{margin-bottom:16px;max-width:900px;background:#fff;border-radius:10px;box-shadow:0 1px 3px rgba(0,0,0,.1)}
details summary{padding:12px 20px;font-weight:600;font-size:14px;cursor:pointer;color:#475569;list-style:none}
details summary::-webkit-details-marker{display:none}
details summary::before{content:"▶ ";font-size:11px}
details[open] summary::before{content:"▼ "}
.filter-body{padding:0 20px 16px;display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px}
.filter-body label{font-size:12px;color:#64748b;margin-bottom:3px}
.filter-body input,.filter-body select{padding:7px 10px;font-size:13px}
.filter-actions{padding:0 20px 16px;display:flex;gap:10px}
.tp-table{width:100%;border-collapse:collapse;font-size:13px;margin-bottom:8px}
.tp-table th{background:#f1f5f9;padding:7px 10px;text-align:left;font-size:12px;color:#64748b}
.tp-table td{padding:4px 6px;border-bottom:1px solid #f1f5f9}
.tp-table input,.tp-table select{padding:5px 8px;font-size:13px;border:1px solid #e2e8f0;border-radius:5px;width:100%}
.tp-table .del-btn{background:none;border:none;color:#ef4444;cursor:pointer;font-size:16px;padding:0 4px}
</style></head><body>
{{end}}
`

const tplNav = `
{{define "nav"}}
<header class="topbar">
  <span class="topbar-title">⚡ {{if .Cfg.AppName}}{{.Cfg.AppName}}{{else}}onebase{{end}}</span>
  <div class="sys-menu">
    <button class="sys-btn" onclick="var d=document.getElementById('sysd');d.classList.toggle('open')">&#9881; Система &#9660;</button>
    <div class="sys-drop" id="sysd">
      <a href="/ui/about">О программе</a>
      <a href="/ui/admin/users">Пользователи</a>
      <a href="/ui/admin/sessions">Активные пользователи</a>
      <a href="/ui/delete-marked">Удалить помеченные</a>
      <a href="/ui/admin/cleanup">Очистка регистров</a>
      <form method="POST" action="/logout"><button type="submit">Выйти</button></form>
    </div>
  </div>
</header>
<div class="app-body">
<aside>
  <a href="/ui" style="display:block;padding:12px 14px 8px;color:#7dd3fc;font-weight:700;font-size:15px;text-decoration:none">Главная</a>
  {{range .Nav}}
  <div class="sec">{{.Kind}}</div>
  {{range .Items}}<a href="{{.URL}}">{{.Label}}</a>
  {{end}}{{end}}
</aside>
{{end}}
`

const tplIndex = `
{{define "page-index"}}
{{template "head" .}}{{template "nav" .}}
<main><h2>Добро пожаловать</h2>
<div class="card"><p style="color:#64748b;font-size:15px">Выберите объект в меню слева для просмотра и создания записей.</p></div>
</main></div></body></html>
{{end}}
`

const tplList = `
{{define "page-list"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Entity.Name}}</h2>
  <div style="display:flex;gap:8px">
    <a class="btn btn-primary" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">+ Создать</a>
  </div>
</div>

{{$entity := .Entity}}{{$params := .Params}}{{$refOpts := .RefFilterOptions}}
<details{{if hasFilter $params}} open{{end}}>
  <summary>Отбор</summary>
  <form method="GET" action="">
  <div class="filter-body">
  {{range $entity.Fields}}{{$f := .}}
    {{if eq (str .Type) "date"}}
      <div>
        <label>{{.Name}} с</label>
        <input type="date" name="f.{{.Name}}.from" value="{{(filterVal $params .Name).From}}">
      </div>
      <div>
        <label>{{.Name}} по</label>
        <input type="date" name="f.{{.Name}}.to" value="{{(filterVal $params .Name).To}}">
      </div>
    {{else if isRef (str .Type)}}
      <div>
        <label>{{.Name}}</label>
        <select name="f.{{.Name}}">
          <option value="">— все —</option>
          {{range index $refOpts .Name}}
          <option value="{{index . "id"}}" {{if eq (index . "id") (filterVal $params $f.Name).Value}}selected{{end}}>{{index . "_label"}}</option>
          {{end}}
        </select>
      </div>
    {{else}}
      <div>
        <label>{{.Name}}</label>
        <input type="text" name="f.{{.Name}}" value="{{(filterVal $params .Name).Value}}">
      </div>
    {{end}}
  {{end}}
  </div>
  <div class="filter-actions">
    <button class="btn btn-primary btn-sm" type="submit">Применить</button>
    <a class="btn btn-sm" href="?" style="background:#e2e8f0;color:#475569">Сбросить</a>
  </div>
  {{if $params.Sort}}<input type="hidden" name="sort" value="{{$params.Sort}}"><input type="hidden" name="dir" value="{{$params.Dir}}">{{end}}
  </form>
</details>

<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{if eq (str .Entity.Kind) "document"}}<th style="width:36px">✓</th>{{end}}
  {{range .Entity.Fields}}
  <th>
    <a href="?sort={{.Name}}&dir={{nextDir $params .Name}}{{filterQuery $params}}">
      {{.Name}} {{sortIcon $params .Name}}
    </a>
  </th>
  {{end}}
  <th style="width:90px"></th>
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}
<tr {{if index $row "deletion_mark"}}style="opacity:0.45;text-decoration:line-through;cursor:pointer"{{else}}style="cursor:pointer"{{end}}
  onclick="listRowClick(event,this)"
  oncontextmenu="listCtxMenu(event,this)"
  data-mark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=1"
  data-del-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete"
  data-open-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}">
  {{if eq (str $.Entity.Kind) "document"}}
    <td style="text-align:center">
      {{if index $row "posted"}}<span style="color:#16a34a;font-weight:700" title="Проведён">✓</span>{{else}}<span style="color:#94a3b8" title="Не проведён">—</span>{{end}}
    </td>
  {{end}}
  {{range $.Entity.Fields}}
    {{if eq (str .Type) "date"}}<td>{{fmtDate (index $row .Name)}}</td>
    {{else}}<td>{{index $row .Name}}</td>{{end}}
  {{end}}
  <td><a class="btn btn-sm btn-primary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}">Открыть</a></td>
</tr>{{end}}
</tbody></table>
{{else}}
<p class="empty">Записей нет — <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">создать первую</a></p>
{{end}}
</div></main>
<script>
var _isAdmin={{if .IsAdmin}}true{{else}}false{{end}};
var _listSel=null;
function listRowClick(e,tr){
  if(e.target.closest('a,button'))return;
  if(_listSel)_listSel.querySelectorAll('td').forEach(function(td){td.style.background='';});
  _listSel=tr;
  tr.querySelectorAll('td').forEach(function(td){td.style.background='#dbeafe';});
}
function listCtxMenu(e,tr){
  if(e.target.closest('a,button'))return;
  e.preventDefault();
  listRowClick(e,tr);
  var old=document.getElementById('_lctx');if(old)old.remove();
  var m=document.createElement('div');
  m.id='_lctx';
  m.style.cssText='position:fixed;z-index:999;background:#fff;border:1px solid #c8d0de;border-radius:6px;box-shadow:0 4px 16px rgba(0,0,0,.18);padding:4px 0;min-width:190px;font-size:13px';
  m.style.left=e.clientX+'px';m.style.top=e.clientY+'px';
  var items=[{label:'Открыть',fn:function(){window.location.href=tr.dataset.openUrl;}}];
  items.push({label:'Пометить на удаление',danger:true,fn:function(){listSubmit(tr.dataset.markUrl,'Пометить на удаление?');}});
  if(_isAdmin)items.push({label:'Удалить навсегда',danger:true,fn:function(){listSubmit(tr.dataset.delUrl,'Удалить запись навсегда?');}});
  items.forEach(function(item){
    var mi=document.createElement('div');
    mi.textContent=item.label;
    mi.style.cssText='padding:8px 14px;cursor:pointer'+(item.danger?';color:#dc2626':'');
    mi.onmouseenter=function(){mi.style.background='#f8fafc';};
    mi.onmouseleave=function(){mi.style.background='';};
    mi.onclick=function(){m.remove();item.fn();};
    m.appendChild(mi);
  });
  document.body.appendChild(m);
  setTimeout(function(){
    document.addEventListener('click',function h(){m.remove();document.removeEventListener('click',h);},{once:true});
  },0);
}
function listSubmit(url,msg){
  if(!url)return;
  if(confirm(msg)){var f=document.createElement('form');f.method='POST';f.action=url;document.body.appendChild(f);f.submit();}
}
document.addEventListener('keydown',function(e){
  if(e.key==='Delete'&&_listSel)listSubmit(_listSel.dataset.markUrl,'Пометить на удаление?');
});
</script>
</div></body></html>
{{end}}
`

const tplForm = `
{{define "page-form"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;max-width:900px">
  <h2 style="margin-bottom:0">{{if .IsNew}}Создать{{else}}Редактировать{{end}} — {{.Entity.Name}}</h2>
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" title="Закрыть" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
</div>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<div class="card">
<form method="POST">
{{range .Entity.Fields}}{{$fn := .Name}}
<div class="form-group">
  <label>{{$fn}}</label>
  {{if isRef (str .Type)}}
    <select name="{{$fn}}">
      <option value="">— выбрать —</option>
      {{range index $.RefOptions $fn}}
      <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values $fn)}}selected{{end}}>{{index . "_label"}}</option>
      {{end}}
    </select>
  {{else if eq (str .Type) "date"}}
    <input type="datetime-local" name="{{$fn}}" value="{{index $.Values $fn}}">
  {{else if eq (str .Type) "bool"}}
    <select name="{{$fn}}">
      <option value="false" {{if eq (index $.Values $fn) "false"}}selected{{end}}>Нет</option>
      <option value="true"  {{if eq (index $.Values $fn) "true"}}selected{{end}}>Да</option>
    </select>
  {{else}}
    <input type="text" name="{{$fn}}" value="{{index $.Values $fn}}" placeholder="{{$fn}}">
  {{end}}
</div>
{{end}}

{{range .Entity.TableParts}}{{$tp := .}}{{$tpName := .Name}}{{$tpRef := index $.TPRefOptions $tpName}}
<h3>{{$tpName}}</h3>
<table class="tp-table">
  <thead><tr>
    {{range .Fields}}<th>{{.Name}}</th>{{end}}
    <th style="width:40px"></th>
  </tr></thead>
  <tbody id="tp-body-{{$tpName}}">
  {{$existingRows := index $.TablePartRows $tpName}}
  {{range $i, $row := $existingRows}}
    <tr>
      {{range $tp.Fields}}{{$fn := .Name}}
        <td>
        {{if isRef (str .Type)}}
          <select name="tp.{{$tpName}}.{{$i}}.{{$fn}}">
            <option value="">— выбрать —</option>
            {{range index $tpRef $fn}}
            <option value="{{index . "id"}}" {{if eq (str (index . "id")) (str (index $row $fn))}}selected{{end}}>{{index . "_label"}}</option>
            {{end}}
          </select>
        {{else if eq (str .Type) "number"}}
          <input type="number" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="{{index $row $fn}}"
            data-tp-num="{{$fn}}" oninput="recalcTpRow(this)">
        {{else}}
          <input type="text" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="{{index $row $fn}}"
            oninput="recalcTpRow(this)">
        {{end}}
        </td>
      {{end}}
      <td><button type="button" class="del-btn" onclick="this.closest('tr').remove()">×</button></td>
    </tr>
  {{end}}
  </tbody>
</table>
<button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin-bottom:8px"
  onclick="addTpRow('{{$tpName}}', [{{range .Fields}}'{{.Name}}',{{end}}], [{{range .Fields}}{{if eq (str .Type) "number"}}'{{.Name}}',{{end}}{{end}}], document.getElementById('tp-body-{{$tpName}}').rows.length)">
  + Добавить строку
</button>
{{end}}

<div style="display:flex;align-items:center;gap:8px;margin-top:20px;flex-wrap:wrap">
  {{if .Entity.Posting}}
    <button class="btn btn-post" type="submit" name="_action" value="post_and_close">Провести и закрыть</button>
    {{if not .IsNew}}
      {{if eq (index .Values "posted") "true"}}
        <button class="btn btn-sm" style="background:#e2e8f0;color:#374151" form="form-unpost" type="submit">Отменить проведение</button>
      {{else}}
        <button class="btn btn-primary" type="submit" name="_action" value="post">Провести</button>
      {{end}}
    {{end}}
  {{end}}
  <button class="btn btn-secondary" type="submit" name="_action" value="">Записать</button>
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" class="btn btn-cancel">Отмена</a>
  {{if and (not .IsNew) .Entity.Posting}}
    {{if eq (index .Values "posted") "true"}}
      <span style="color:#16a34a;font-weight:600;font-size:13px;margin-left:8px">✓ Проведён</span>
    {{else}}
      <span style="color:#94a3b8;font-size:13px;margin-left:8px">Не проведён</span>
    {{end}}
  {{end}}
</div>
</form>
{{if and (not .IsNew) .Entity.Posting}}
{{if eq (index .Values "posted") "true"}}
<form id="form-unpost" method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/{{.ID}}/unpost"></form>
{{end}}
{{end}}
{{if not .IsNew}}
<div style="margin-top:10px">
  <form method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/{{.ID}}/delete"
        onsubmit="return confirm('{{if .IsAdmin}}Удалить запись навсегда?{{else}}Пометить запись на удаление?{{end}}')">
    <button class="btn btn-danger btn-sm" type="submit">{{if .IsAdmin}}Удалить{{else}}Пометить на удаление{{end}}</button>
  </form>
</div>
{{end}}
</div>
<script>
window._tpRefOpts = {{jsJSON .TPRefOptions}};
function addTpRow(tpName, fields, numFields, idx) {
  var tbody = document.getElementById('tp-body-' + tpName);
  var tr = document.createElement('tr');
  var refOpts = (window._tpRefOpts && window._tpRefOpts[tpName]) || {};
  fields.forEach(function(fn) {
    var td = document.createElement('td');
    if (refOpts[fn] !== undefined) {
      var sel = document.createElement('select');
      sel.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      var defOpt = document.createElement('option');
      defOpt.value = ''; defOpt.textContent = '— выбрать —';
      sel.appendChild(defOpt);
      (refOpts[fn] || []).forEach(function(opt) {
        var o = document.createElement('option');
        o.value = opt.id; o.textContent = opt._label || opt.id;
        sel.appendChild(o);
      });
      td.appendChild(sel);
    } else {
      var inp = document.createElement('input');
      inp.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      if (numFields.indexOf(fn) !== -1) {
        inp.type = 'number';
        inp.setAttribute('data-tp-num', fn);
        inp.setAttribute('oninput', 'recalcTpRow(this)');
      } else {
        inp.type = 'text';
      }
      td.appendChild(inp);
    }
    tr.appendChild(td);
  });
  var tdDel = document.createElement('td');
  var btn = document.createElement('button');
  btn.type = 'button'; btn.className = 'del-btn'; btn.textContent = '×';
  btn.onclick = function(){ tr.remove(); };
  tdDel.appendChild(btn);
  tr.appendChild(tdDel);
  tbody.appendChild(tr);
}

// If a row has exactly 3 numeric fields (qty, price, sum), auto-calculate the last.
function recalcTpRow(inp) {
  var tr = inp.closest('tr');
  var nums = tr.querySelectorAll('[data-tp-num]');
  if (nums.length === 3) {
    var a = parseFloat(nums[0].value) || 0;
    var b = parseFloat(nums[1].value) || 0;
    nums[2].value = (a * b).toFixed(2);
  }
}
</script>
</main></div></body></html>
{{end}}
`

const tplReport = `
{{define "page-report"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{if .Report.Title}}{{.Report.Title}}{{else}}{{.Report.Name}}{{end}}</h2>
{{if .Report.Params}}
<div class="card" style="margin-bottom:16px">
<form method="POST">
  <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px;margin-bottom:16px">
  {{range .Report.Params}}{{$pname := .Name}}
    <div class="form-group" style="margin-bottom:0">
      <label>{{.DisplayLabel}}</label>
      {{if eq .Type "date"}}
        <input type="date" name="{{$pname}}" value="{{index $.ParamValues $pname}}">
      {{else if eq .Type "number"}}
        <input type="number" name="{{$pname}}" value="{{index $.ParamValues $pname}}">
      {{else if eq .Type "select"}}
        <select name="{{$pname}}">
          {{range .Options}}<option value="{{.}}" {{if eq . (str (index $.ParamValues $pname))}}selected{{end}}>{{if .}}{{.}}{{else}}— все —{{end}}</option>{{end}}
        </select>
      {{else}}
        <input type="text" name="{{$pname}}" value="{{index $.ParamValues $pname}}">
      {{end}}
    </div>
  {{end}}
  </div>
  <button class="btn btn-primary" type="submit">Сформировать</button>
</form>
</div>
{{end}}
{{if .QueryError}}<div class="error">Ошибка запроса: {{.QueryError}}</div>{{end}}
{{if .Cols}}
<div class="card">
{{if .Rows}}
<table><thead><tr>{{range .Cols}}<th>{{.}}</th>{{end}}</tr></thead>
<tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{range $.Cols}}<td>{{index $row .}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">Нет данных</p>{{end}}
</div>
{{end}}
</main></div></body></html>
{{end}}
`

const tplRegister = `
{{define "page-register-movements"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Register.Name}} — движения</h2>
  <a class="btn btn-sm" href="/ui/register/{{lower .Register.Name}}/balances" style="background:#e2e8f0;color:#475569">Остатки →</a>
</div>
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th>Вид движения</th>
  <th>Регистратор</th>
  {{range .Register.Dimensions}}<th>{{.Name}}</th>{{end}}
  {{range .Register.Resources}}<th>{{.Name}}</th>{{end}}
  {{range .Register.Attributes}}<th>{{.Name}}</th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  <td>{{$v := index $row "вид_движения"}}{{if eq (str $v) "Приход"}}<span style="color:#16a34a;font-weight:600">▲ {{$v}}</span>{{else}}<span style="color:#dc2626;font-weight:600">▼ {{$v}}</span>{{end}}</td>
  <td style="font-size:12px;color:#475569">{{if index $row "recorder_label"}}{{index $row "recorder_label"}}{{else}}{{index $row "recorder_type"}}{{end}}</td>
  {{range $.Register.Dimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Resources}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Attributes}}<td>{{index $row .Name}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">Движений нет</p>{{end}}
</div></main></div></body></html>
{{end}}

{{define "page-register-balances"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Register.Name}} — остатки</h2>
  <a class="btn btn-sm" href="/ui/register/{{lower .Register.Name}}" style="background:#e2e8f0;color:#475569">← Движения</a>
</div>
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{range .Register.Dimensions}}<th>{{.Name}}</th>{{end}}
  {{range .Register.Resources}}<th>{{.Name}}</th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{range $.Register.Dimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Resources}}<td style="font-weight:600">{{index $row .Name}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">Остатков нет</p>{{end}}
</div></main></div></body></html>
{{end}}
`

const tplDeleteMarked = `
{{define "page-delete-marked"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>Удалить помеченные</h2>
{{if .Deleted}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">
  Удалено: {{.Deleted}}{{if .Skipped}} &nbsp;·&nbsp; Пропущено (есть ссылки): {{.Skipped}}{{end}}
</div>{{end}}
{{if .Entries}}
<div class="card" style="max-width:900px;margin-bottom:16px">
<table><thead><tr>
  <th>Объект</th><th>Наименование</th><th>Статус</th>
</tr></thead><tbody>
{{range .Entries}}<tr>
  <td><a href="/ui/{{lower .Kind}}/{{lower .EntityName}}/{{.ID}}">{{.EntityName}}</a></td>
  <td>{{.Label}}</td>
  <td>{{if .HasRefs}}<span style="color:#ef4444">Есть ссылки — не будет удалён</span>{{else}}<span style="color:#16a34a">Будет удалён</span>{{end}}</td>
</tr>{{end}}
</tbody></table>
</div>
<form method="POST" action="/ui/delete-marked"
      onsubmit="return confirm('Удалить все помеченные записи без ссылок?')">
  <button class="btn btn-danger" type="submit">Удалить помеченные без ссылок</button>
  <a class="btn btn-secondary" href="/ui" style="margin-left:8px">Отмена</a>
</form>
{{else}}
<div class="card" style="max-width:600px">
  <p class="empty">Помеченных на удаление записей нет.</p>
</div>
{{end}}
</main></div></body></html>
{{end}}
`

const tplAbout = `
{{define "page-about"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>О программе</h2>
<div class="card" style="max-width:560px">
  <table style="width:100%;border-collapse:collapse">
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;width:180px;font-size:14px">Платформа</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-weight:600;font-size:14px">onebase {{if .Cfg.PlatVersion}}{{.Cfg.PlatVersion}}{{else}}dev{{end}}</td>
    </tr>
    {{if .Cfg.AppName}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">Конфигурация</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">{{.Cfg.AppName}}{{if .Cfg.AppVersion}} <span style="color:#94a3b8">v{{.Cfg.AppVersion}}</span>{{end}}</td>
    </tr>
    {{end}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">База данных</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:13px;color:#475569;word-break:break-all">{{.Cfg.DSN}}</td>
    </tr>
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">Метаданные</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">
        Справочники: {{.Catalogs}} &nbsp;·&nbsp;
        Документы: {{.Documents}} &nbsp;·&nbsp;
        Регистры: {{.Registers}} &nbsp;·&nbsp;
        Отчёты: {{.Reports}}
      </td>
    </tr>
  </table>
</div>
</main></div></body></html>
{{end}}
`

const tplInfoReg = `
{{define "page-inforeg-list"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.InfoReg.Name}}{{if .InfoReg.Periodic}} <span style="font-size:13px;color:#64748b;font-weight:400">(периодический)</span>{{end}}</h2>
  <a class="btn" href="/ui/inforeg/{{lower .InfoReg.Name}}/new">+ Добавить запись</a>
</div>
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{if .InfoReg.Periodic}}<th>Период</th>{{end}}
  {{range .InfoReg.Dimensions}}<th>{{.Name}}</th>{{end}}
  {{range .InfoReg.Resources}}<th>{{.Name}}</th>{{end}}
  <th></th>
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{if $.InfoReg.Periodic}}<td>{{index $row "period"}}</td>{{end}}
  {{range $.InfoReg.Dimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.InfoReg.Resources}}<td style="font-weight:600">{{index $row .Name}}</td>{{end}}
  <td>
    <form method="POST" action="/ui/inforeg/{{lower $.InfoReg.Name}}/delete" style="display:inline"
          onsubmit="return confirm('Удалить запись?')">
      {{if $.InfoReg.Periodic}}<input type="hidden" name="period" value="{{index $row "period"}}">{{end}}
      {{range $.InfoReg.Dimensions}}<input type="hidden" name="{{.Name}}" value="{{index $row .Name}}">{{end}}
      <button class="btn btn-danger btn-sm" type="submit">×</button>
    </form>
  </td>
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">Записей нет</p>{{end}}
</div></main></div></body></html>
{{end}}

{{define "page-inforeg-form"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{.InfoReg.Name}} — новая запись</h2>
{{if .Error}}<div style="background:#fef2f2;border:1px solid #fecaca;color:#dc2626;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">{{.Error}}</div>{{end}}
<div class="card" style="max-width:560px">
<form method="POST">
  {{if .InfoReg.Periodic}}
  <div class="form-row">
    <label>Период</label>
    <input type="date" name="period" value="{{index .Values "period"}}" required>
  </div>
  {{end}}
  {{range .InfoReg.Dimensions}}
  <div class="form-row">
    <label>{{.Name}} <span style="color:#94a3b8;font-size:11px">[измерение]</span></label>
    <input type="text" name="{{.Name}}" value="{{index $.Values .Name}}">
  </div>
  {{end}}
  {{range .InfoReg.Resources}}
  <div class="form-row">
    <label>{{.Name}}</label>
    <input type="text" name="{{.Name}}" value="{{index $.Values .Name}}">
  </div>
  {{end}}
  <div style="margin-top:20px;display:flex;gap:8px">
    <button class="btn" type="submit">Записать</button>
    <a class="btn btn-secondary" href="/ui/inforeg/{{lower .InfoReg.Name}}">Отмена</a>
  </div>
</form>
</div></main></div></body></html>
{{end}}
`

