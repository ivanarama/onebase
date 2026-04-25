package launcher

import "html/template"

var tmpl = template.Must(template.New("root").Parse(tplLauncherHead + tplIndex + tplForm + tplMigrateResult + tplConfigResult))

const tplLauncherHead = `
{{define "lhead"}}<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Tahoma,Arial,sans-serif;font-size:13px;background:#ECE9D8;min-height:100vh}

/* toolbar */
.toolbar{background:linear-gradient(to bottom,#F5F4EE,#DDD9C7);border-bottom:1px solid #ACA899;padding:4px 6px;display:flex;align-items:center;gap:2px;flex-wrap:wrap}
.tbtn{display:inline-flex;align-items:center;gap:4px;padding:4px 10px;border:1px solid #ACA899;border-radius:2px;background:linear-gradient(to bottom,#F5F4EE,#E0DDD2);cursor:pointer;font-size:12px;color:#333;text-decoration:none;white-space:nowrap}
.tbtn:hover{background:linear-gradient(to bottom,#EAF3FF,#C5DCFF);border-color:#7EAFF5}
.tbtn.danger:hover{background:linear-gradient(to bottom,#FFE8E8,#FFBFBF);border-color:#FF9090}
.tbtn svg{width:16px;height:16px}
.tbtn-sep{width:1px;background:#ACA899;height:24px;margin:0 4px}

/* main layout */
.content{display:flex;height:calc(100vh - 37px)}
.list-panel{flex:1;padding:8px;overflow-y:auto}
.info-panel{width:240px;background:#F5F4EE;border-left:1px solid #ACA899;padding:10px;font-size:12px;color:#555}

/* base items */
.base-item{display:flex;align-items:flex-start;gap:8px;padding:8px 10px;margin-bottom:2px;border:1px solid transparent;border-radius:2px;cursor:pointer;background:#fff;transition:border-color .1s}
.base-item:hover{border-color:#7EAFF5;background:#EAF3FF}
.base-item.selected{border-color:#3070D8;background:#D5E3FF}
.base-item.running .status-dot{background:#22c55e}
.base-dot{width:20px;height:20px;border-radius:3px;background:#1a5fa8;display:flex;align-items:center;justify-content:center;flex-shrink:0;margin-top:1px}
.base-dot svg{width:13px;height:13px;fill:#fff}
.base-name{font-weight:600;font-size:13px;color:#1a1a1a}
.base-sub{font-size:11px;color:#666;margin-top:2px;word-break:break-all}
.status-dot{display:inline-block;width:7px;height:7px;border-radius:50%;background:#aaa;margin-right:4px;flex-shrink:0;margin-top:4px}

/* status badge */
.badge{display:inline-block;padding:1px 6px;border-radius:10px;font-size:11px;font-weight:600}
.badge-run{background:#dcfce7;color:#16a34a}
.badge-stop{background:#f1f5f9;color:#64748b}

/* forms */
.form-page{max-width:560px;margin:20px auto;background:#fff;border:1px solid #ACA899;padding:24px;border-radius:2px}
.form-page h2{font-size:16px;font-weight:600;margin-bottom:18px;color:#1a1a1a;border-bottom:1px solid #e2e8f0;padding-bottom:10px}
.fg{margin-bottom:14px}
.fg label{display:block;font-size:12px;font-weight:600;margin-bottom:4px;color:#444}
.fg input,.fg select{width:100%;padding:6px 8px;border:1px solid #ACA899;border-radius:2px;font-size:13px;outline:none;background:#fff}
.fg input:focus,.fg select:focus{border-color:#3070D8;box-shadow:0 0 0 2px rgba(48,112,216,.15)}
.fg .hint{font-size:11px;color:#888;margin-top:3px}
.form-row{display:flex;gap:12px}
.form-row .fg{flex:1}
.cbrow{display:flex;align-items:center;gap:6px;margin-bottom:14px}
.cbrow input{width:auto}
.form-btns{display:flex;gap:8px;margin-top:18px;padding-top:14px;border-top:1px solid #e2e8f0}
.btn-ok{background:#1a5fa8;color:#fff;border:1px solid #1a5fa8;padding:6px 16px;border-radius:2px;cursor:pointer;font-size:13px}
.btn-ok:hover{background:#1550a0}
.btn-cancel{background:#f5f4ee;color:#333;border:1px solid #ACA899;padding:6px 16px;border-radius:2px;cursor:pointer;font-size:13px;text-decoration:none;display:inline-block}
.btn-cancel:hover{background:#e8e6dc}
.err{background:#fff0f0;border:1px solid #ffb3b3;color:#c00;padding:8px 10px;border-radius:2px;margin-bottom:12px;font-size:13px}

/* result pages */
.result-page{max-width:640px;margin:20px auto;background:#fff;border:1px solid #ACA899;padding:20px;border-radius:2px}
.result-page h2{font-size:15px;margin-bottom:12px;font-weight:600}
pre{background:#1e1e1e;color:#d4d4d4;padding:14px;border-radius:2px;font-size:12px;overflow-x:auto;white-space:pre-wrap;word-break:break-all;max-height:400px;overflow-y:auto}
</style>
</head>
<body>
{{end}}
`

const tplIndex = `
{{define "page-index"}}
{{template "lhead" .}}
<div class="toolbar">
  {{if .Selected}}
  <a class="tbtn" href="/bases/{{.Selected.ID}}/start" onclick="return startBase(this,'{{.Selected.ID}}')">
    <svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg> 1С:Предприятие
  </a>
  <a class="tbtn" href="/bases/{{.Selected.ID}}/configurator">
    <svg viewBox="0 0 24 24"><path d="M22 9V7h-2V5c0-1.1-.9-2-2-2H4c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h14c1.1 0 2-.9 2-2v-2h2v-2h-2v-2h2v-2h-2V9h2zm-4 10H4V5h14v14z"/><path d="M6 13h5v4H6zm6-6h4v3h-4zm0 4h4v6h-4zM6 7h5v5H6z"/></svg> Конфигуратор
  </a>
  <a class="tbtn" href="/bases/{{.Selected.ID}}/migrate" onclick="return doPost(this)">
    <svg viewBox="0 0 24 24"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6s-2.69 6-6 6-6-2.69-6-6H4c0 4.42 3.58 8 8 8s8-3.58 8-8-3.58-8-8-8z"/></svg> Обновить БД
  </a>
  {{if .Selected.Running}}
  <a class="tbtn danger" href="/bases/{{.Selected.ID}}/stop" onclick="return doPost(this)">
    <svg viewBox="0 0 24 24"><path d="M6 6h12v12H6z"/></svg> Остановить
  </a>
  {{end}}
  <div class="tbtn-sep"></div>
  {{end}}
  <a class="tbtn" href="/bases/new">
    <svg viewBox="0 0 24 24"><path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z"/></svg> Добавить
  </a>
  <div style="flex:1"></div>
  <a class="tbtn danger" href="/killall" onclick="return doPost(this)" title="Остановить все базы">
    <svg viewBox="0 0 24 24"><path d="M6 6h12v12H6z"/></svg> Стоп всё
  </a>
  <a class="tbtn danger" href="/quit" onclick="return quitLauncher()" title="Завершить лаунчер">
    <svg viewBox="0 0 24 24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>
  </a>
  {{if .Selected}}
  <a class="tbtn" href="/bases/{{.Selected.ID}}/edit">
    <svg viewBox="0 0 24 24"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34c-.39-.39-1.02-.39-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg> Изменить
  </a>
  <a class="tbtn danger" href="/bases/{{.Selected.ID}}/delete" onclick="return confirm('Удалить базу «{{.Selected.Name}}»?') && doPost(this)">
    <svg viewBox="0 0 24 24"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg> Удалить
  </a>
  {{end}}
</div>

<div class="content">
<div class="list-panel">
{{if .Bases}}
{{range .Bases}}
<div class="base-item {{if $.Selected}}{{if eq .ID $.Selected.ID}}selected{{end}}{{end}} {{if .Running}}running{{end}}"
     onclick="selectBase('{{.ID}}')">
  <div class="base-dot"><svg viewBox="0 0 24 24"><path d="M4 6H2v14c0 1.1.9 2 2 2h14v-2H4V6zm16-4H8c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm-1 9h-4v4h-2v-4H9V9h4V5h2v4h4v2z"/></svg></div>
  <div style="flex:1;min-width:0">
    <div class="base-name">
      <span class="status-dot"></span>{{.Name}}
      {{if .Running}}<span class="badge badge-run">работает</span>{{else}}<span class="badge badge-stop">остановлена</span>{{end}}
    </div>
    <div class="base-sub">
      {{if eq .ConfigSource "file"}}📁 {{.Path}}{{else}}🗄 В базе данных{{end}}
    </div>
    <div class="base-sub">{{.DB}} · :{{.Port}}</div>
  </div>
</div>
{{end}}
{{else}}
<div style="text-align:center;padding:60px 20px;color:#999">
  <div style="font-size:40px;margin-bottom:12px">🗄</div>
  <div style="font-size:14px;margin-bottom:6px;font-weight:600">Нет информационных баз</div>
  <div style="font-size:12px">Нажмите «Добавить» для создания первой базы</div>
</div>
{{end}}
</div>

{{if .Selected}}
<div class="info-panel">
  <div style="font-weight:600;margin-bottom:8px;font-size:12px">{{.Selected.Name}}</div>
  <table style="width:100%;border-collapse:collapse">
  <tr><td style="color:#888;padding:2px 0;width:90px">Режим</td><td>{{if eq .Selected.ConfigSource "database"}}База данных{{else}}Файлы{{end}}</td></tr>
  {{if eq .Selected.ConfigSource "file"}}
  <tr><td style="color:#888;padding:2px 0">Путь</td><td style="word-break:break-all">{{.Selected.Path}}</td></tr>
  {{end}}
  <tr><td style="color:#888;padding:2px 0">Порт</td><td>:{{.Selected.Port}}</td></tr>
  <tr><td style="color:#888;padding:2px 0">Состояние</td><td>{{if .Selected.Running}}<span style="color:#16a34a;font-weight:600">Работает</span>{{else}}<span style="color:#888">Остановлена</span>{{end}}</td></tr>
  {{if not .Selected.LastOpened.IsZero}}<tr><td style="color:#888;padding:2px 0">Открыта</td><td>{{.Selected.LastOpened.Format "02.01.2006"}}</td></tr>{{end}}
  </table>
  {{if .Selected.Running}}
  <div style="margin-top:12px">
    <a href="{{.BaseURL}}" target="_blank" style="font-size:12px;color:#1a5fa8">Открыть в браузере ↗</a>
  </div>
  {{end}}
</div>
{{end}}
</div>

<script>
var _sel = '{{if .Selected}}{{.Selected.ID}}{{end}}';
function selectBase(id) {
  if (_sel === id) return;
  window.location.href = '/?sel=' + id;
}
function doPost(el) {
  el.preventDefault ? el.preventDefault() : (el.returnValue = false);
  var form = document.createElement('form');
  form.method = 'POST';
  form.action = el.href || el.getAttribute('href');
  document.body.appendChild(form);
  form.submit();
  return false;
}
function quitLauncher() {
  fetch('/quit', {method:'POST'}).catch(function(){});
  setTimeout(function(){ window.close(); }, 200);
  return false;
}
function startBase(el, id) {
  el.preventDefault ? el.preventDefault() : (el.returnValue = false);
  fetch('/bases/' + id + '/start', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.url) {
        window.open(d.url, '_blank');
        setTimeout(function(){ window.location.reload(); }, 1000);
      } else if (d.error) {
        alert(d.error);
      }
    })
    .catch(function(e){ alert('Ошибка запуска: ' + e); });
  return false;
}
</script>
</body></html>
{{end}}
`

const tplForm = `
{{define "page-form"}}
{{template "lhead" .}}
<div class="form-page">
  <h2>{{if .IsNew}}Добавить информационную базу{{else}}Изменить — {{.Base.Name}}{{end}}</h2>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST" action="{{if .IsNew}}/bases{{else}}/bases/{{.Base.ID}}{{end}}">
    <div class="fg">
      <label>Наименование</label>
      <input name="name" value="{{.Base.Name}}" required autofocus>
    </div>
    <div class="fg">
      <label>Тип хранения конфигурации</label>
      <select name="config_source" onchange="togglePath(this.value)">
        <option value="database" {{if eq .Base.ConfigSource "database"}}selected{{end}}>В базе данных (1С-режим)</option>
        <option value="file" {{if eq .Base.ConfigSource "file"}}selected{{end}}>Файловый (разработка)</option>
      </select>
      <div class="hint">«В базе данных» — конфигурация хранится в PostgreSQL, редактирование через Выгрузку/Загрузку. «Файловый» — папка на диске под git.</div>
    </div>
    <div class="fg" id="path-row" style="{{if ne .Base.ConfigSource "file"}}display:none{{end}}">
      <label>Путь к папке конфигурации</label>
      <input name="path" value="{{.Base.Path}}" placeholder="/home/user/my-app">
      <div class="hint">Папка должна содержать catalogs/, documents/ и т.д.</div>
    </div>
    <div class="fg">
      <label>Строка подключения к PostgreSQL</label>
      <input name="db" value="{{.Base.DB}}" required placeholder="postgres://localhost/mydb?sslmode=disable">
      <div class="hint">База данных будет создана автоматически, если не существует.</div>
    </div>
    <div class="form-row">
      <div class="fg">
        <label>Порт сервера</label>
        <input name="port" type="number" value="{{if .Base.Port}}{{.Base.Port}}{{else}}8080{{end}}" min="1024" max="65535">
      </div>
    </div>
    {{if .IsNew}}
    <div class="cbrow" id="scaffold-row">
      <input type="checkbox" name="scaffold" id="scaffold" value="1">
      <label for="scaffold" id="scaffold-label">Создать пустую конфигурацию (новая база)</label>
    </div>
    {{end}}
    <div class="form-btns">
      <button class="btn-ok" type="submit">{{if .IsNew}}Добавить{{else}}Сохранить{{end}}</button>
      <a class="btn-cancel" href="/">Отмена</a>
    </div>
  </form>
</div>
<script>
function togglePath(v) {
  var r = document.getElementById('path-row');
  var sl = document.getElementById('scaffold-label');
  if (v === 'file') {
    r.style.display = '';
    if (sl) sl.textContent = 'Создать структуру папок и пустую конфигурацию';
  } else {
    r.style.display = 'none';
    if (sl) sl.textContent = 'Создать пустую конфигурацию (новая база)';
  }
}
</script>
</body></html>
{{end}}
`

const tplMigrateResult = `
{{define "page-migrate"}}
{{template "lhead" .}}
<div class="result-page">
  <h2>Обновление БД — {{.Name}}</h2>
  <pre>{{.Output}}</pre>
  {{if .Error}}<div class="err" style="margin-top:10px">{{.Error}}</div>{{end}}
  <div style="margin-top:14px"><a class="btn-cancel" href="/">← Назад</a></div>
</div>
</body></html>
{{end}}
`

const tplConfigResult = `
{{define "page-config-result"}}
{{template "lhead" .}}
<div class="result-page">
  <h2>{{.Title}}</h2>
  <p style="margin-bottom:12px;font-size:13px;color:#555">{{.Message}}</p>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <div style="margin-top:14px"><a class="btn-cancel" href="/">← Назад</a></div>
</div>
</body></html>
{{end}}
`
