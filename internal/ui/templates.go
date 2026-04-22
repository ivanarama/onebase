package ui

import (
	"fmt"
	"html/template"
	"strings"
	"time"
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
}).Parse(tplHead + tplNav + tplIndex + tplList + tplForm))

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
.card{background:#fff;border-radius:10px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,.1);max-width:860px}
table{width:100%;border-collapse:collapse;font-size:14px}
th{text-align:left;padding:10px 12px;border-bottom:2px solid #e2e8f0;color:#64748b;font-weight:600}
td{padding:10px 12px;border-bottom:1px solid #f1f5f9;color:#334155;font-size:14px}
tr:last-child td{border-bottom:none}
tr:hover td{background:#f8fafc}
.btn{display:inline-block;padding:8px 18px;border-radius:7px;font-size:14px;font-weight:500;text-decoration:none;cursor:pointer;border:none;line-height:1}
.btn-primary{background:#3b82f6;color:#fff}.btn-primary:hover{background:#2563eb}
.btn-sm{padding:5px 12px;font-size:13px}
.form-group{margin-bottom:16px}
label{display:block;font-size:13px;font-weight:500;margin-bottom:5px;color:#475569}
input[type=text],input[type=datetime-local],select{width:100%;padding:9px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;outline:none;background:#fff}
input:focus,select:focus{border-color:#3b82f6;box-shadow:0 0 0 3px rgba(59,130,246,.15)}
.error{background:#fef2f2;border:1px solid #fecaca;color:#dc2626;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px}
.empty{color:#94a3b8;text-align:center;padding:48px;font-size:15px}
.row-top{display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;max-width:860px}
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
  {{range .Entities}}<a href="/ui/{{lower (str .Kind)}}/{{lower .Name}}">{{.Name}}</a>
  {{end}}{{end}}
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
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{range .Entity.Fields}}<th>{{.Name}}</th>{{end}}
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
<div style="display:flex;align-items:center;gap:16px;margin-top:8px">
  <button class="btn btn-primary" type="submit">Сохранить</button>
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" style="color:#64748b;font-size:14px">Отмена</a>
</div>
</form>
</div></main></body></html>
{{end}}
`
