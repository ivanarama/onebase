package ui

import (
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
}).Parse(tplHead + tplNav + tplIndex + tplList + tplForm + tplRegister + tplReport))

const tplHead = `
{{define "head"}}<!DOCTYPE html>
<html lang="ru"><head><meta charset="UTF-8"><title>onebase</title><style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;display:flex;min-height:100vh;background:#f5f5f5}
aside{width:220px;background:#1e293b;color:#fff;padding:20px;flex-shrink:0;min-height:100vh}
aside h1{font-size:18px;font-weight:700;margin-bottom:24px;color:#7dd3fc}
aside .sec{font-size:11px;text-transform:uppercase;color:#94a3b8;margin:16px 0 6px;letter-spacing:.05em}
aside a{display:block;padding:6px 10px;color:#cbd5e1;text-decoration:none;border-radius:6px;font-size:14px;margin-bottom:2px}
aside a:hover{background:#334155;color:#fff}
main{flex:1;padding:32px}
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
<aside>
  <h1>⚡ onebase</h1>
  <a href="/ui">Главная</a>
  {{range .Nav}}
  <div class="sec">{{.Kind}}</div>
  {{range .Items}}<a href="{{.URL}}">{{.Label}}</a>
  {{end}}{{end}}
  <div class="sec">Администрирование</div>
  <a href="/ui/admin/users">Пользователи</a>
  <form method="POST" action="/logout" style="margin:6px 0 0">
    <button type="submit" style="background:none;border:none;color:#94a3b8;font-size:14px;padding:6px 10px;cursor:pointer;width:100%;text-align:left;border-radius:6px" onmouseover="this.style.background='#334155';this.style.color='#fff'" onmouseout="this.style.background='none';this.style.color='#94a3b8'">Выйти</button>
  </form>
</aside>
{{end}}
`

const tplIndex = `
{{define "page-index"}}
{{template "head" .}}{{template "nav" .}}
<main><h2>Добро пожаловать</h2>
<div class="card"><p style="color:#64748b;font-size:15px">Выберите объект в меню слева для просмотра и создания записей.</p></div>
</main></body></html>
{{end}}
`

const tplList = `
{{define "page-list"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Entity.Name}}</h2>
  <a class="btn btn-primary" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">+ Создать</a>
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
  {{range .Entity.Fields}}
  <th>
    <a href="?sort={{.Name}}&dir={{nextDir $params .Name}}{{filterQuery $params}}">
      {{.Name}} {{sortIcon $params .Name}}
    </a>
  </th>
  {{end}}
  <th style="width:90px"></th>
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
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
</div></main></body></html>
{{end}}
`

const tplForm = `
{{define "page-form"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{if .IsNew}}Создать{{else}}Редактировать{{end}} — {{.Entity.Name}}</h2>
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

{{range .Entity.TableParts}}{{$tp := .}}{{$tpName := .Name}}
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
      {{range $tp.Fields}}
        <td><input type="text" name="tp.{{$tpName}}.{{$i}}.{{.Name}}" value="{{index $row .Name}}"
          {{if eq (str .Type) "number"}}data-tp-num="{{.Name}}"{{end}}
          oninput="recalcTpRow(this)"></td>
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

<div style="display:flex;align-items:center;gap:16px;margin-top:16px">
  <button class="btn btn-primary" type="submit">Сохранить</button>
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" style="color:#64748b;font-size:14px">Отмена</a>
</div>
</form>
</div>
<script>
function addTpRow(tpName, fields, numFields, idx) {
  var tbody = document.getElementById('tp-body-' + tpName);
  var tr = document.createElement('tr');
  fields.forEach(function(fn) {
    var td = document.createElement('td');
    var inp = document.createElement('input');
    inp.type = 'text';
    inp.name = 'tp.' + tpName + '.' + idx + '.' + fn;
    if (numFields.indexOf(fn) !== -1) {
      inp.setAttribute('data-tp-num', fn);
      inp.setAttribute('oninput', 'recalcTpRow(this)');
    }
    td.appendChild(inp);
    tr.appendChild(td);
  });
  var td = document.createElement('td');
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'del-btn';
  btn.textContent = '×';
  btn.onclick = function(){ tr.remove(); };
  td.appendChild(btn);
  tr.appendChild(td);
  tbody.appendChild(tr);
}

// Recalculate: if a row has exactly 3 numeric fields (qty, price, sum),
// set the last one to the product of the first two.
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
</main></body></html>
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
</main></body></html>
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
  <td style="font-size:12px;color:#94a3b8">{{index $row "recorder_type"}}</td>
  {{range $.Register.Dimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Resources}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Attributes}}<td>{{index $row .Name}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">Движений нет</p>{{end}}
</div></main></body></html>
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
</div></main></body></html>
{{end}}
`
