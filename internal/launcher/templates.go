package launcher

import "html/template"

var tmpl = template.Must(template.New("root").Funcs(template.FuncMap{
	"maskDSN": maskDSN,
	"t": func(lang, key string) string {
		if launcherBundle != nil {
			return launcherBundle.T(lang, key)
		}
		return key
	},
}).Parse(tplLauncherHead + tplIndex + tplForm + tplConfigResult + tplUpdates + tplReportProblem))

const tplLauncherHead = `
{{define "lhead"}}<!DOCTYPE html>
<html lang="{{.Lang}}">
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
/* отметка о доступной версии платформы: заметная, но не кричащая — на канале
   build сборки выходят по нескольку раз в день */
.upd-badge{background:linear-gradient(to bottom,#EAF7EC,#C9E9CF);border-color:#7FBF8C;color:#166534;font-weight:600}
.upd-badge:hover{background:linear-gradient(to bottom,#DCF3E1,#B4DFBC);border-color:#5AA96A}
.upd-card{background:#fff;border:1px solid #ACA899;border-radius:2px;padding:14px;max-width:760px}
.upd-note{background:#FFF9E6;border:1px solid #E8D9A0;border-radius:2px;padding:8px 10px;font-size:12px;color:#6b5a1e;margin-bottom:8px}
.upd-avail{background:#EAF7EC;border:1px solid #A8D8B4;border-radius:2px;padding:8px 10px;font-size:12px;color:#166534}
.upd-notes{margin-top:8px;padding:8px 10px;background:#F7F7F4;border:1px solid #E0DDD2;border-radius:2px;font-size:11px;line-height:1.45;max-height:260px;overflow:auto;white-space:pre-wrap;word-break:break-word;font-family:'Segoe UI',Tahoma,Arial,sans-serif}

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
.input-browse{display:flex;gap:4px}.input-browse input{flex:1}
.btn-browse{flex-shrink:0;padding:6px 10px;border:1px solid #ACA899;border-radius:2px;background:linear-gradient(to bottom,#F5F4EE,#E0DDD2);cursor:pointer;font-size:13px;white-space:nowrap}
.btn-browse:hover{background:linear-gradient(to bottom,#EAF3FF,#C5DCFF);border-color:#7EAFF5}
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
.btn-danger{background:#b91c1c;color:#fff;border:1px solid #b91c1c;padding:6px 16px;border-radius:2px;cursor:pointer;font-size:13px}
.btn-danger:hover{background:#991b1b}

/* modal (диалог закрытия окна) */
.modal-back{position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.35);display:flex;align-items:center;justify-content:center;z-index:100}
.modal{background:#fff;border:1px solid #ACA899;border-radius:2px;box-shadow:0 6px 24px rgba(0,0,0,.25);width:520px;max-width:calc(100% - 40px);padding:18px 20px}
.modal h3{font-size:14px;font-weight:600;margin-bottom:12px}
.modal p{font-size:12px;color:#444;margin-bottom:10px;line-height:1.45}
.modal ul{margin:0 0 10px 20px;font-size:12px;color:#333}
.modal label{font-size:12px;color:#555;display:flex;align-items:center;gap:6px}
.modal label input{width:auto}
.modal-btns{display:flex;gap:8px;justify-content:flex-end;margin-top:16px;flex-wrap:wrap}

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
    <svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg> {{t $.Lang "Предприятие"}}
  </a>
  <div style="position:relative;display:flex">
    <a class="tbtn" href="/bases/{{.Selected.ID}}/start-isolated" onclick="return startIsolated(this,'{{.Selected.ID}}','')" title="{{t $.Lang "Отдельное окно с изолированным профилем — можно войти под другим пользователем"}}" style="border-top-right-radius:0;border-bottom-right-radius:0">
      <svg viewBox="0 0 24 24"><path d="M19 19H5V5h7V3H5c-1.11 0-2 .9-2 2v14c0 1.1.89 2 2 2h14c1.1 0 2-.9 2-2v-7h-2v7zM14 3v2h3.59l-9.83 9.83 1.41 1.41L19 6.41V10h2V3h-7z"/></svg> {{t $.Lang "Новое окно"}}
    </a>
    <a class="tbtn" href="#" onclick="return toggleIsoMenu(event)" title="{{t $.Lang "Выбрать способ открытия"}}" style="padding-left:5px;padding-right:5px;border-top-left-radius:0;border-bottom-left-radius:0;margin-left:-1px">▾</a>
    <div id="iso-menu" style="display:none;position:absolute;top:100%;left:0;z-index:50;background:#fff;border:1px solid #ACA899;border-radius:2px;box-shadow:0 3px 10px rgba(0,0,0,.15);min-width:230px">
      {{if .NativeOK}}<a href="#" onclick="return startIsolated(this,'{{.Selected.ID}}','native')" style="display:block;padding:7px 12px;font-size:12px;color:#333;text-decoration:none">{{t $.Lang "Нативное окно"}}</a>{{end}}
      <a href="#" onclick="return startIsolated(this,'{{.Selected.ID}}','browser')" style="display:block;padding:7px 12px;font-size:12px;color:#333;text-decoration:none">{{t $.Lang "Окно браузера (Edge/Chrome)"}}</a>
    </div>
  </div>
  <a class="tbtn" href="/bases/{{.Selected.ID}}/configurator">
    <svg viewBox="0 0 24 24"><path d="M22 9V7h-2V5c0-1.1-.9-2-2-2H4c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h14c1.1 0 2-.9 2-2v-2h2v-2h-2v-2h2v-2h-2V9h2zm-4 10H4V5h14v14z"/><path d="M6 13h5v4H6zm6-6h4v3h-4zm0 4h4v6h-4zM6 7h5v5H6z"/></svg> {{t $.Lang "Конфигуратор"}}
  </a>
  {{if .Selected.Running}}
  <a class="tbtn danger" href="/bases/{{.Selected.ID}}/stop" onclick="return doPost(this)">
    <svg viewBox="0 0 24 24"><path d="M6 6h12v12H6z"/></svg> {{t $.Lang "Остановить"}}
  </a>
  {{end}}
  <div class="tbtn-sep"></div>
  {{end}}
  <a class="tbtn" href="/bases/new">
    <svg viewBox="0 0 24 24"><path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z"/></svg> {{t $.Lang "Добавить"}}
  </a>
  <div style="flex:1"></div>
  <a class="tbtn" href="/report-problem{{if .Selected}}?base={{.Selected.ID}}{{end}}" title="{{t $.Lang "Собрать отчёт об ошибке одним файлом"}}">
    <svg viewBox="0 0 24 24"><path d="M20 8h-2.81a5.99 5.99 0 0 0-1.82-1.96L17 4.41 15.59 3l-2.17 2.17a6.02 6.02 0 0 0-2.83 0L8.41 3 7 4.41l1.62 1.63A5.99 5.99 0 0 0 6.81 8H4v2h2.09c-.05.33-.09.66-.09 1v1H4v2h2v1c0 .34.04.67.09 1H4v2h2.81a6 6 0 0 0 10.38 0H20v-2h-2.09c.05-.33.09-.66.09-1v-1h2v-2h-2v-1c0-.34-.04-.67-.09-1H20V8zm-6 8h-4v-2h4v2zm0-4h-4v-2h4v2z"/></svg> {{t $.Lang "Сообщить об ошибке"}}
  </a>
  <a class="tbtn danger" href="/killall{{if .Selected}}?sel={{.Selected.ID}}{{end}}" onclick="return confirmKillAll(this)" title="{{t $.Lang "Остановить все базы"}}">
    <svg viewBox="0 0 24 24"><path d="M6 6h12v12H6z"/></svg> {{t $.Lang "Стоп всё"}}
  </a>
  {{/* with, а не if: у части рендер-тестов данных об обновлении нет вовсе */}}
  {{with .Update}}{{if .Enabled}}
  <a class="tbtn{{if .ShowBadge}} upd-badge{{end}}" href="/updates"
     title="{{if .ShowBadge}}{{t $.Lang "Доступна новая версия платформы"}}{{else}}{{t $.Lang "Обновление платформы"}}{{end}}">
    <svg viewBox="0 0 24 24"><path d="M12 4l8 8h-5v8h-6v-8H4z"/></svg>{{if .ShowBadge}} {{.LatestTag}}{{end}}
  </a>
  {{end}}{{end}}
  <a class="tbtn danger" href="/quit" onclick="return quitLauncher()" title="{{t $.Lang "Завершить лаунчер"}}">
    <svg viewBox="0 0 24 24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>
  </a>
  {{if .Selected}}
  <a class="tbtn" href="/bases/{{.Selected.ID}}/move?dir=up" onclick="return doPost(this)" title="{{t $.Lang "Переместить вверх"}}">
    <svg viewBox="0 0 24 24"><path d="M7 14l5-5 5 5z"/></svg>
  </a>
  <a class="tbtn" href="/bases/{{.Selected.ID}}/move?dir=down" onclick="return doPost(this)" title="{{t $.Lang "Переместить вниз"}}">
    <svg viewBox="0 0 24 24"><path d="M7 10l5 5 5-5z"/></svg>
  </a>
  <a class="tbtn" href="/bases/{{.Selected.ID}}/edit">
    <svg viewBox="0 0 24 24"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34c-.39-.39-1.02-.39-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg> {{t $.Lang "Изменить"}}
  </a>
  <a class="tbtn danger" href="/bases/{{.Selected.ID}}/delete" onclick="return confirm('Удалить базу «{{.Selected.Name}}»?') && doPost(this)">
    <svg viewBox="0 0 24 24"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg> {{t $.Lang "Удалить"}}
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
      {{if .Running}}<span class="badge badge-run">{{t $.Lang "работает"}}</span>{{else}}<span class="badge badge-stop">{{t $.Lang "остановлена"}}</span>{{end}}
    </div>
    <div class="base-sub">
      {{if eq .ConfigSource "file"}}📁 {{.Path}}{{else}}🗄 {{t $.Lang "В базе данных"}}{{end}}
    </div>
    <div class="base-sub">{{if eq .DBType "sqlite"}}💾 {{.DBPath}}{{else}}{{maskDSN .DB}}{{end}} · :{{.Port}}</div>
  </div>
</div>
{{end}}
{{else}}
<div style="text-align:center;padding:60px 20px;color:#999">
  <div style="font-size:40px;margin-bottom:12px">🗄</div>
  <div style="font-size:14px;margin-bottom:6px;font-weight:600">{{t $.Lang "Нет информационных баз"}}</div>
  <div style="font-size:12px">{{t $.Lang "Нажмите «Добавить» для создания первой базы"}}</div>
</div>
{{end}}
</div>

{{if .Selected}}
<div class="info-panel">
  {{if .Selected.LogoBase64}}<div style="text-align:center;margin-bottom:8px"><img src="{{.Selected.LogoBase64}}" alt="Logo" style="max-height:80px;max-width:220px"></div>{{end}}
  <div style="font-weight:600;margin-bottom:8px;font-size:12px">{{.Selected.Name}}</div>
  <table style="width:100%;border-collapse:collapse">
  {{if .Selected.AppName}}<tr><td style="color:#888;padding:2px 0;width:90px">{{t $.Lang "Конфигурация"}}</td><td>{{.Selected.AppName}}</td></tr>{{end}}
  {{if .Selected.AppVersion}}<tr><td style="color:#888;padding:2px 0">{{t $.Lang "Версия"}}</td><td>{{.Selected.AppVersion}}</td></tr>{{end}}
  <tr><td style="color:#888;padding:2px 0;width:90px">{{t $.Lang "Режим"}}</td><td>{{if eq .Selected.ConfigSource "database"}}{{t $.Lang "База данных"}}{{else}}{{t $.Lang "Файлы"}}{{end}}</td></tr>
  {{if eq .Selected.ConfigSource "file"}}
  <tr><td style="color:#888;padding:2px 0">{{t $.Lang "Путь"}}</td><td style="word-break:break-all">{{.Selected.Path}}</td></tr>
  {{end}}
  {{if eq .Selected.DBType "sqlite"}}
  <tr><td style="color:#888;padding:2px 0">{{t $.Lang "Файл базы"}}</td><td style="word-break:break-all">{{.Selected.DBPath}}</td></tr>
  {{end}}
  <tr><td style="color:#888;padding:2px 0">{{t $.Lang "Порт"}}</td><td>:{{.Selected.Port}}</td></tr>
  <tr><td style="color:#888;padding:2px 0">{{t $.Lang "Состояние"}}</td><td>{{if .Selected.Running}}<span style="color:#16a34a;font-weight:600">{{t $.Lang "Работает"}}</span>{{else}}<span style="color:#888">{{t $.Lang "Остановлена"}}</span>{{end}}</td></tr>
  {{if not .Selected.LastOpened.IsZero}}<tr><td style="color:#888;padding:2px 0">{{t $.Lang "Открыта"}}</td><td>{{.Selected.LastOpened.Format "02.01.2006"}}</td></tr>{{end}}
  </table>
  {{if .Selected.Running}}
  <div style="margin-top:12px">
    <a href="{{.BaseURL}}" target="_blank" style="font-size:12px;color:#1a5fa8">{{t $.Lang "Открыть в браузере"}} ↗</a>
  </div>
  {{end}}
  <div style="margin-top:12px">
    <a href="#" onclick="return cleanProfiles('{{.Selected.ID}}')" style="font-size:11px;color:#94a3b8">{{t $.Lang "Очистить изолированные профили"}}</a>
  </div>
</div>
{{end}}
</div>

<div id="close-modal" class="modal-back" style="display:none">
  <div class="modal">
    <h3>{{t $.Lang "Закрытие окна информационных баз"}}</h3>
    <div id="close-modal-ask">
      <p>{{t $.Lang "Работают информационные базы:"}}</p>
      <ul id="close-modal-list"></ul>
      <p>{{t $.Lang "Продолжить их работу в фоновом режиме? Регламентные задания, обмены и подключённые пользователи не пострадают. Окно запуска закроется — откройте его снова, чтобы остановить базы."}}</p>
      <label><input type="checkbox" id="close-modal-remember"> {{t $.Lang "Больше не спрашивать"}}</label>
      <div class="modal-btns">
        <button class="btn-ok" onclick="return closeChoice('background')">{{t $.Lang "Продолжить в фоне"}}</button>
        <button class="btn-danger" onclick="return closeChoice('stop')">{{t $.Lang "Остановить все"}}</button>
        <button class="btn-cancel" onclick="return closeChoice('cancel')">{{t $.Lang "Отмена"}}</button>
      </div>
    </div>
    <p id="close-modal-progress" style="display:none">⏳ {{t $.Lang "Останавливаем базы…"}}</p>
  </div>
</div>

<script>
var _sel = '{{if .Selected}}{{.Selected.ID}}{{end}}';
var _runningCount = {{.RunningCount}};
// GUI-сборка под Windows умеет нативные WebView2-окна — «Предприятие»
// открывается в таком окне (без адресной строки), а не через window.open,
// который в WebView2 убегает во внешний браузер. В остальных сборках — браузер.
var _nativeOK = {{if .NativeOK}}true{{else}}false{{end}};
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
// Закрытие лаунчера. Базы намеренно переживают закрытие окна, но молча это
// делать нельзя: пользователь думает, что вышел, а серверы баз держат порт,
// файл БД и сеансы. Поэтому при живых базах спрашиваем — если только выбор уже
// не сохранён настройкой (см. closepolicy.go).
function quitLauncher() {
  fetch('/close-info')
    .then(function(r){ return r.json(); })
    .then(function(d){
      var running = (d && d.running) || [];
      var policy = (d && d.policy) || 'ask';
      if (running.length === 0 || policy === 'background') { quitNow(); return; }
      if (policy === 'stop') { stopAllThenQuit(); return; }
      showCloseModal(running);
    })
    // Состояние баз выяснить не удалось — ведём себя как раньше и закрываемся:
    // держать пользователя в окне из-за сбоя запроса неправильно.
    .catch(function(){ quitNow(); });
  return false;
}
function showCloseModal(running) {
  var list = document.getElementById('close-modal-list');
  list.innerHTML = '';
  for (var i = 0; i < running.length; i++) {
    var li = document.createElement('li');
    li.textContent = running[i].name + ' ({{t $.Lang "порт"}} ' + running[i].port + ')';
    list.appendChild(li);
  }
  document.getElementById('close-modal').style.display = 'flex';
  // Фокус на безопасном варианте: Enter не должен случайно останавливать базы.
  var first = document.querySelector('#close-modal .btn-ok');
  if (first) first.focus();
}
// Esc = «Отмена», как в системном диалоге закрытия окна. Пока идёт остановка
// баз, отменять уже нечего — вопрос снят с экрана.
document.addEventListener('keydown', function(ev) {
  if (ev.key !== 'Escape') return;
  var m = document.getElementById('close-modal');
  var ask = document.getElementById('close-modal-ask');
  if (m && m.style.display === 'flex' && ask && ask.style.display !== 'none') closeChoice('cancel');
});
function closeChoice(kind) {
  if (kind === 'cancel') {
    document.getElementById('close-modal').style.display = 'none';
    return false;
  }
  if (document.getElementById('close-modal-remember').checked) {
    // Настройку дописываем до выхода: после /quit процесс уже не ответит.
    fetch('/close-policy', {
      method: 'POST',
      headers: {'Content-Type': 'application/x-www-form-urlencoded'},
      body: 'policy=' + encodeURIComponent(kind)
    }).catch(function(){}).then(function(){ finishClose(kind); });
    return false;
  }
  finishClose(kind);
  return false;
}
function finishClose(kind) {
  if (kind === 'stop') stopAllThenQuit(); else quitNow();
}
function stopAllThenQuit() {
  var ask = document.getElementById('close-modal-ask');
  if (ask) ask.style.display = 'none';
  var prog = document.getElementById('close-modal-progress');
  if (prog) prog.style.display = 'block';
  document.getElementById('close-modal').style.display = 'flex';
  // Остановка идёт до выхода: убить лаунчер раньше — значит бросить базы
  // недоостановленными, ровно от чего пользователь и отказался.
  fetch('/killall', {method:'POST'}).catch(function(){}).then(function(){ quitNow(); });
}
function quitNow() {
  fetch('/quit', {method:'POST'}).catch(function(){});
  setTimeout(function(){ window.close(); }, 200);
}
function confirmKillAll(el) {
  if (_runningCount > 0 && !confirm('{{t $.Lang "Остановить все работающие базы?"}}\n\n' +
      '{{t $.Lang "Открытые окна Предприятия и подключённые пользователи потеряют связь."}}')) {
    return false;
  }
  return doPost(el);
}
function toggleIsoMenu(ev) {
  if (ev) { if (ev.preventDefault) ev.preventDefault(); if (ev.stopPropagation) ev.stopPropagation(); }
  var m = document.getElementById('iso-menu');
  if (m) m.style.display = (m.style.display === 'block') ? 'none' : 'block';
  return false;
}
document.addEventListener('click', function(){
  var m = document.getElementById('iso-menu');
  if (m && m.style.display === 'block') m.style.display = 'none';
});
function startIsolated(el, id, mode) {
  el.preventDefault ? el.preventDefault() : (el.returnValue = false);
  var m = document.getElementById('iso-menu'); if (m) m.style.display = 'none';
  // Окно открывает сервер лаунчера (нативное WebView2 или внешний браузер с
  // отдельным профилем), window.open не нужен — работает и из GUI-режима.
  fetch('/bases/' + id + '/start-isolated' + (mode ? ('?mode=' + mode) : ''), {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d && d.error) { alert('Ошибка запуска:\n' + d.error); }
      else { setTimeout(function(){ window.location.href = '/?sel=' + id; }, 500); }
    })
    .catch(function(e){ alert('Ошибка запуска: ' + e); });
  return false;
}
function cleanProfiles(id) {
  if (!confirm('Удалить незанятые изолированные профили браузера этой базы?')) return false;
  fetch('/bases/' + id + '/profiles/clean', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d && d.error) alert('Ошибка: ' + d.error);
      else alert('Удалено профилей: ' + (d.removed || 0));
    })
    .catch(function(e){ alert('Ошибка: ' + e); });
  return false;
}
// showStartError показывает ошибку запуска в уже открытом окне-заготовке.
// Раньше окно закрывалось, а alert уходил под него — в WebView2 это выглядело
// как «вечный белый экран» (закрытие попапа может не сработать, и ошибка
// оставалась невидимой за ним).
function showStartError(win, msg) {
  var text = String(msg);
  if (win) {
    try {
      win.document.open();
      win.document.write('<!DOCTYPE html><html><head><meta charset="utf-8"><title>onebase</title></head><body style="font-family:Segoe UI,Arial,sans-serif;padding:28px"><h3 style="color:#c00;margin:0 0 12px">Ошибка запуска базы</h3><pre id="err" style="white-space:pre-wrap;font:13px Consolas,monospace;color:#333"></pre></body></html>');
      win.document.getElementById('err').textContent = text;
      win.document.close();
      return;
    } catch (e) { try { win.close(); } catch (e2) {} }
  }
  alert('Ошибка запуска:\n' + text);
}
function startBase(el, id) {
  if (_nativeOK) return startBaseNative(el, id);
  el.preventDefault ? el.preventDefault() : (el.returnValue = false);
  var btn = el.target || el;
  var origText = btn.textContent || '';
  if (btn.innerHTML) btn.innerHTML = '⏳ Запуск...';
  var win = window.open('', '_blank');
  // Заготовка вместо белого экрана: первый запуск мигрирует схему БД до открытия
  // порта и может длиться дольше минуты — окно всё это время ждёт ответ /start.
  if (win) {
    try {
      win.document.write('<!DOCTYPE html><html><head><meta charset="utf-8"><title>onebase</title></head><body style="font-family:Segoe UI,Arial,sans-serif;padding:28px"><h3 style="margin:0 0 12px">⏳ Запуск базы…</h3><p style="color:#555;max-width:48em">При первом запуске создаётся схема базы данных — это может занять несколько минут. Окно откроется автоматически.</p></body></html>');
      win.document.close();
    } catch (e) {}
  }
  fetch('/bases/' + id + '/start', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.url) {
        if (win) win.location.href = d.url;
        setTimeout(function(){ window.location.href = '/?sel=' + id; }, 800);
      } else {
        showStartError(win, d.error || 'Неизвестная ошибка');
        if (btn.innerHTML) btn.innerHTML = origText;
      }
    })
    .catch(function(e){
      showStartError(win, e);
      if (btn.innerHTML) btn.innerHTML = origText;
    });
  return false;
}
// startBaseNative открывает базу в нативном WebView2-окне (сервер запускает его
// сам, без window.open — иначе WebView2 отдал бы URL внешнему браузеру с
// адресной строкой). Окно на общем профиле = обычный сеанс Предприятия.
function startBaseNative(el, id) {
  el.preventDefault ? el.preventDefault() : (el.returnValue = false);
  var btn = el.target || el;
  var origHTML = btn.innerHTML;
  if (btn.innerHTML) btn.innerHTML = '⏳ Запуск...';
  fetch('/bases/' + id + '/start-native', {method:'POST'})
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d && d.error) {
        alert('Ошибка запуска:\n' + d.error);
        if (btn.innerHTML) btn.innerHTML = origHTML;
      } else {
        setTimeout(function(){ window.location.href = '/?sel=' + id; }, 500);
      }
    })
    .catch(function(e){
      alert('Ошибка запуска: ' + e);
      if (btn.innerHTML) btn.innerHTML = origHTML;
    });
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
  <h2>{{if .IsNew}}{{t $.Lang "Добавить информационную базу"}}{{else}}{{t $.Lang "Изменить"}} — {{.Base.Name}}{{end}}</h2>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST" action="{{if .IsNew}}/bases{{else}}/bases/{{.Base.ID}}{{end}}">
    <div class="fg">
      <label>{{t $.Lang "Наименование"}}</label>
      <input name="name" value="{{.Base.Name}}" required autofocus>
    </div>
    <div class="fg">
      <label>{{t $.Lang "Тип хранения конфигурации"}}</label>
      <select name="config_source" onchange="togglePath(this.value)">
        <option value="database" {{if eq .Base.ConfigSource "database"}}selected{{end}}>{{t $.Lang "В базе данных (1С-режим)"}}</option>
        <option value="file" {{if eq .Base.ConfigSource "file"}}selected{{end}}>{{t $.Lang "Файловый (разработка)"}}</option>
      </select>
      <div class="hint">{{t $.Lang "«В базе данных» — конфигурация хранится в БД, редактирование через Выгрузку/Загрузку. «Файловый» — папка на диске под git."}}</div>
    </div>
    <div class="fg" id="path-row" style="{{if ne .Base.ConfigSource "file"}}display:none{{end}}">
      <label>{{t $.Lang "Путь к папке конфигурации"}}</label>
      <div class="input-browse">
        <input id="inp-path" name="path" value="{{.Base.Path}}" placeholder="/home/user/my-app" onblur="autoFillSQLitePath(this.value)">
        <button type="button" class="btn-browse" onclick="pickDir('inp-path','Выберите папку конфигурации')">📁</button>
      </div>
      <div class="hint">{{t $.Lang "Папка должна содержать catalogs/, documents/ и т.д."}}</div>
    </div>
    <div class="fg">
      <label>{{t $.Lang "Тип базы данных"}}</label>
      <select name="db_type" onchange="toggleDB(this.value)">
        <option value="postgres" {{if or (eq .Base.DBType "") (eq .Base.DBType "postgres")}}selected{{end}}>{{t $.Lang "Серверная (PostgreSQL)"}}</option>
        <option value="sqlite" {{if eq .Base.DBType "sqlite"}}selected{{end}}>{{t $.Lang "Файловая (SQLite)"}}</option>
      </select>
      <div class="hint">{{t $.Lang "«Файловая» — один файл .db, без установки сервера, идеальна для pet-проектов. «Серверная» — PostgreSQL."}}</div>
    </div>
    <div class="fg" id="dsn-row" style="{{if eq .Base.DBType "sqlite"}}display:none{{end}}">
      <label>{{t $.Lang "Строка подключения к PostgreSQL"}}</label>
      <input name="db" value="{{.Base.DB}}" placeholder="postgres://localhost/mydb?sslmode=disable">
      <div class="hint">{{t $.Lang "База данных будет создана автоматически, если не существует."}}</div>
    </div>
    <div class="fg" id="dbpath-row" style="{{if ne .Base.DBType "sqlite"}}display:none{{end}}">
      <label>{{t $.Lang "Путь к файлу SQLite"}}</label>
      <div class="input-browse">
        <input id="inp-dbpath" name="db_path" value="{{.Base.DBPath}}" placeholder="C:\onebase\mydb.db" onblur="normalizeDBPath('inp-dbpath')">
        <button type="button" class="btn-browse" onclick="pickSQLiteDir('inp-dbpath')">📁</button>
      </div>
      <div class="hint">{{t $.Lang "Файл будет создан, если не существует. Расширение .db рекомендуется."}}</div>
    </div>
    <div class="form-row">
      <div class="fg">
        <label>{{t $.Lang "Порт сервера"}}</label>
        <input name="port" type="number" value="{{if .Base.Port}}{{.Base.Port}}{{else}}8080{{end}}" min="1024" max="65535">
        <div class="hint">{{t $.Lang "У каждой базы должен быть уникальный порт. Первая база: 8080, вторая: 8081 и т.д."}}</div>
      </div>
    </div>
    <div class="form-row">
      <div class="fg">
        <label>{{t $.Lang "Доступ по сети"}}</label>
        <select name="host">
          <option value="127.0.0.1" {{if ne .Base.Host "0.0.0.0"}}selected{{end}}>{{t $.Lang "Только этот компьютер (127.0.0.1)"}}</option>
          <option value="0.0.0.0" {{if eq .Base.Host "0.0.0.0"}}selected{{end}}>{{t $.Lang "Все интерфейсы (0.0.0.0) — доступ из локальной сети"}}</option>
        </select>
        <div class="hint">{{t $.Lang "«Все интерфейсы» открывает базу другим машинам сети. Пока в базе нет пользователей, доступ будет без пароля — сначала заведите пользователя."}}</div>
      </div>
    </div>
    {{if .IsNew}}
    <div class="cbrow" id="scaffold-row">
      <input type="checkbox" name="scaffold" id="scaffold" value="1">
      <label for="scaffold" id="scaffold-label">{{t $.Lang "Создать пустую конфигурацию (новая база)"}}</label>
    </div>
    {{end}}
    <div class="form-btns">
      <button class="btn-ok" type="submit">{{if .IsNew}}{{t $.Lang "Добавить"}}{{else}}{{t $.Lang "Сохранить"}}{{end}}</button>
      <a class="btn-cancel" href="/">{{t $.Lang "Отмена"}}</a>
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
function toggleDB(v) {
  var dsn = document.getElementById('dsn-row');
  var dbp = document.getElementById('dbpath-row');
  if (v === 'sqlite') {
    dsn.style.display='none'; dbp.style.display='';
    // Автозаполнение пути SQLite из папки конфигурации
    var configPath = document.getElementById('inp-path');
    if (configPath && (configPath.value || '').trim()) {
      autoFillSQLitePath(configPath.value);
    }
  } else { dsn.style.display=''; dbp.style.display='none'; }
}
function pickDir(inputId, title) {
  var btn = event.target;
  var cur = document.getElementById(inputId).value || '';
  btn.disabled = true;
  btn.textContent = '...';
  fetch('/browse-dir?title=' + encodeURIComponent(title) + '&initial_path=' + encodeURIComponent(cur))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d.path) return;
      document.getElementById(inputId).value = d.path;
      // Автозаполнение пути SQLite при выборе папки конфигурации
      autoFillSQLitePath(d.path);
    })
    .finally(function(){ btn.disabled = false; btn.textContent = '📁'; });
}
function autoFillSQLitePath(configPath) {
  var dbType = document.querySelector('select[name=db_type]');
  if (!dbType || dbType.value !== 'sqlite') return;
  var dbp = document.getElementById('inp-dbpath');
  if (!dbp || (dbp.value || '').trim()) return;
  var name = (document.querySelector('input[name=name]').value || 'database')
    .replace(/[\\/:*?"<>|]/g, '_').trim() || 'database';
  var sep = configPath.indexOf('/') >= 0 && configPath.indexOf('\\') < 0 ? '/' : '\\';
  var trimmed = configPath.replace(/[\\/]+$/, '');
  dbp.value = trimmed + sep + name + '.db';
}
function pickFile(inputId, title, filter) {
  var btn = event.target;
  btn.disabled = true;
  btn.textContent = '...';
  fetch('/browse-file?title=' + encodeURIComponent(title) + '&filter=' + encodeURIComponent(filter))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (d.path) document.getElementById(inputId).value = d.path;
    })
    .finally(function(){ btn.disabled = false; btn.textContent = '📁'; });
}
function pickSQLiteDir(inputId) {
  var btn = event.target;
  var cur = document.getElementById(inputId).value || '';
  var dir = cur.replace(/\\[^\\]+$/, '');
  btn.disabled = true;
  btn.textContent = '...';
  fetch('/browse-dir?title=' + encodeURIComponent('Выберите папку для файла базы данных') + '&initial_path=' + encodeURIComponent(dir))
    .then(function(r){ return r.json(); })
    .then(function(d){
      if (!d.path) return;
      var name = (document.querySelector('input[name=name]').value || 'database')
        .replace(/[\\/:*?"<>|]/g, '_').trim() || 'database';
      var sep = d.path.slice(-1) === '\\' ? '' : '\\';
      document.getElementById(inputId).value = d.path + sep + name + '.db';
    })
    .finally(function(){ btn.disabled = false; btn.textContent = '📁'; });
}
// При потере фокуса нормализуем путь так же, как делает сервер:
// если введена папка (без расширения / со слэшем на конце) — добавляем <имя>.db.
function normalizeDBPath(inputId) {
  var inp = document.getElementById(inputId);
  var raw = (inp.value || '').trim();
  if (!raw) return;
  if (/\.db$/i.test(raw)) return;
  var sep = (raw.indexOf('/') >= 0 && raw.indexOf('\\') < 0) ? '/' : '\\';
  var trimmed = raw.replace(/[\\/]+$/, '');
  var hasExt = /\.[A-Za-z0-9]+$/.test(trimmed.split(/[\\/]/).pop() || '');
  var endedWithSep = raw !== trimmed;
  if (hasExt && !endedWithSep) return;
  var name = (document.querySelector('input[name=name]').value || 'database')
    .replace(/[\\/:*?"<>|]/g, '_').trim() || 'database';
  inp.value = trimmed + sep + name + '.db';
}
</script>
</body></html>
{{end}}
`

// tplUpdates — страница обновления платформы (план 92). Отдельная страница, а
// не модалка: здесь и переключатель канала, и список изменений, и предупреждение
// про остановку баз — в модальном окне это читалось бы хуже.
const tplUpdates = `
{{define "page-updates"}}
{{template "lhead" .}}
<div class="toolbar">
  <a class="tbtn" href="/">← {{t $.Lang "Назад"}}</a>
  <div style="flex:1"></div>
</div>
<div class="content">
<div class="list-panel">
  <div class="upd-card">
    <h2 style="font-size:15px;margin-bottom:10px">{{t $.Lang "Обновление платформы"}}</h2>

    {{if not .U.Enabled}}
    <div class="upd-note">{{t $.Lang "Обновление платформы отключено политикой администратора"}}
      <div style="color:#888;margin-top:4px">{{.U.BinDir}}/onebase.policy.yaml</div>
    </div>
    {{else}}

    <table style="width:100%;border-collapse:collapse;font-size:12px;margin-bottom:12px">
      <tr><td style="color:#888;padding:3px 0;width:150px">{{t $.Lang "Установленная версия"}}</td><td style="font-weight:600">{{.U.Current}}</td></tr>
      <tr><td style="color:#888;padding:3px 0">{{t $.Lang "Канал"}}</td><td>
        {{if .U.ChannelLocked}}
          {{.U.Channel}} <span style="color:#888">({{t $.Lang "задан администратором"}})</span>
        {{else}}
          <label style="margin-right:10px"><input type="radio" name="ch" value="build" {{if eq .U.Channel "build"}}checked{{end}} onclick="setChannel('build')"> build — {{t $.Lang "сборки из main, несколько раз в день"}}</label>
          <label><input type="radio" name="ch" value="stable" {{if eq .U.Channel "stable"}}checked{{end}} onclick="setChannel('stable')"> stable — {{t $.Lang "только выпуски"}}</label>
        {{end}}
      </td></tr>
      <tr><td style="color:#888;padding:3px 0">{{t $.Lang "Источник"}}</td><td>{{.U.Repo}}</td></tr>
      {{if not .U.CheckedAt.IsZero}}
      <tr><td style="color:#888;padding:3px 0">{{t $.Lang "Проверено"}}</td><td>{{.U.CheckedAt.Local.Format "02.01.2006 15:04"}}</td></tr>
      {{end}}
    </table>

    {{if .U.CheckError}}
    <div class="upd-note">{{t $.Lang "Проверить обновления не удалось"}}: {{.U.CheckError}}</div>
    {{end}}

    {{if .U.LatestTag}}
      {{if .U.Available}}
        <div class="upd-avail">
          {{if .U.SameScheme}}
            {{t $.Lang "Доступна новая версия"}}: <b>{{.U.LatestTag}}</b>
          {{else}}
            {{t $.Lang "Канал предлагает версию"}} <b>{{.U.LatestTag}}</b> — {{t $.Lang "это переключение канала, а не более новая сборка"}}
          {{end}}
          {{if not .U.LatestAt.IsZero}}<span style="color:#888"> · {{.U.LatestAt.Local.Format "02.01.2006 15:04"}}</span>{{end}}
          {{if .U.LatestURL}} · <a href="{{.U.LatestURL}}" target="_blank" style="color:#1a5fa8">{{t $.Lang "страница выпуска"}} ↗</a>{{end}}
        </div>
        {{if .U.LatestNotes}}<pre class="upd-notes">{{.U.LatestNotes}}</pre>{{end}}
      {{else}}
        <div class="upd-note">{{t $.Lang "Установлена актуальная версия"}} — {{.U.LatestTag}}</div>
      {{end}}
    {{end}}

    {{if not .U.CanWrite}}
    <div class="upd-note">{{t $.Lang "Нет прав на запись в каталог платформы — обновление доступно только администратору"}}
      <div style="color:#888;margin-top:4px">{{.U.BinDir}}</div>
    </div>
    {{end}}

    <div style="margin-top:14px;display:flex;gap:6px;flex-wrap:wrap">
      {{if .U.NetAllowed}}
      <a class="tbtn" href="#" onclick="return updAction('/updates/check', '{{t $.Lang "Проверяю..."}}')">{{t $.Lang "Проверить"}}</a>
      {{if and .U.Available (ne .U.StagedTag .U.LatestTag)}}
      <a class="tbtn" href="#" onclick="return updAction('/updates/download', '{{t $.Lang "Скачиваю, это займёт несколько минут..."}}')">{{t $.Lang "Скачать"}}</a>
      {{end}}
      {{end}}
      {{if .U.CanApply}}
      <a class="tbtn upd-badge" href="#" onclick="return updApply()">{{t $.Lang "Перезапустить и обновить"}} → {{.U.StagedTag}}</a>
      {{end}}
      {{if and .U.PrevTag .U.CanWrite}}
      <a class="tbtn danger" href="#" onclick="return updRollback('{{.U.PrevTag}}')">{{t $.Lang "Вернуть версию"}} {{.U.PrevTag}}</a>
      {{end}}
    </div>

    {{if .U.StagedTag}}
    <div class="upd-note" style="margin-top:12px">{{t $.Lang "Обновление скачано и проверено"}}: {{.U.StagedTag}}.
      {{if gt .U.RunningCount 0}}
      <b>{{t $.Lang "Применение остановит запущенные базы"}} ({{.U.RunningCount}}) {{t $.Lang "и перезапустит платформу"}}.</b>
      {{else}}
      {{t $.Lang "Применение перезапустит платформу"}}.
      {{end}}
      <div style="color:#888;margin-top:4px">{{t $.Lang "Затрагивает всех пользователей этой машины. Откат возможен сразу после обновления, но базы, открытые новой версией, обратно не мигрируются."}}</div>
    </div>
    {{end}}

    <div id="upd-status" style="margin-top:12px;font-size:12px;color:#555"></div>
    {{end}}
  </div>
</div>
</div>

{{if .U.Enabled}}
<script>
function updStatus(text) { document.getElementById('upd-status').textContent = text || ''; }
function updPost(url, busy, done) {
  updStatus(busy);
  fetch(url, {method:'POST'}).then(function(r){ return r.json().then(function(j){ return {ok:r.ok, body:j}; }); })
    .then(function(res){
      if (!res.ok) { updStatus(res.body.error || 'ошибка'); return; }
      done(res.body);
    })
    .catch(function(e){ updStatus(String(e)); });
  return false;
}
function updAction(url, busy) {
  return updPost(url, busy, function(){ window.location.reload(); });
}
function setChannel(ch) {
  return updPost('/updates/channel?value=' + encodeURIComponent(ch), '', function(){ window.location.reload(); });
}
function updApply() {
  return updPost('/updates/apply', '{{t $.Lang "Обновляю и перезапускаю..."}}', function(){
    // Окно закроется само: процесс поднимет новую версию и откроет своё окно.
    updStatus('{{t $.Lang "Платформа перезапускается — это окно закроется."}}');
  });
}
function updRollback(tag) {
  if (!confirm('{{t $.Lang "Вернуть предыдущую версию платформы?"}} ' + tag)) return false;
  return updPost('/updates/rollback', '{{t $.Lang "Откатываю и перезапускаю..."}}', function(){
    updStatus('{{t $.Lang "Платформа перезапускается — это окно закроется."}}');
  });
}
</script>
{{end}}
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
  <div style="margin-top:14px"><a class="btn-cancel" href="{{if .BackURL}}{{.BackURL}}{{else}}/{{end}}">← {{t $.Lang "Назад"}}</a></div>
</div>
</body></html>
{{end}}
`
