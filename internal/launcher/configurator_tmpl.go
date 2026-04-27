package launcher

import "html/template"

var cfgTmpl = template.Must(template.New("cfg").Funcs(template.FuncMap{
	"dict": func(pairs ...any) map[string]any {
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			if k, ok := pairs[i].(string); ok {
				m[k] = pairs[i+1]
			}
		}
		return m
	},
	"fieldTypeLabel": func(typ, ref string) string {
		switch typ {
		case "string":
			return "строка"
		case "number":
			return "число"
		case "date":
			return "дата"
		case "bool":
			return "булево"
		case "reference":
			return "→ " + ref
		default:
			return typ
		}
	},
	"fieldTypeClass": func(typ string) string {
		switch typ {
		case "reference":
			return "ft-ref"
		case "number":
			return "ft-num"
		case "date":
			return "ft-date"
		case "bool":
			return "ft-bool"
		default:
			return "ft-str"
		}
	},
}).Parse(cfgCSS + cfgHead + cfgMain + cfgTabTree + cfgTabConvert + cfgTabFiles + cfgFoot))

// ── CSS ───────────────────────────────────────────────────────────────────────

const cfgCSS = `{{define "css"}}
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;font-size:13px;background:#f0f2f5;height:100vh;display:flex;flex-direction:column;overflow:hidden}

.topbar{background:linear-gradient(to bottom,#2c5f9e,#1a4a80);color:#fff;padding:7px 14px;display:flex;align-items:center;gap:12px;flex-shrink:0}
.topbar a{color:#b8d4ff;text-decoration:none;font-size:12px}.topbar a:hover{color:#fff}
.topbar h1{font-size:14px;font-weight:600;flex:1}

.tabs{display:flex;background:#fff;border-bottom:2px solid #d0d7e3;padding:0 14px;flex-shrink:0}
.tab{padding:8px 18px;cursor:pointer;font-size:13px;color:#666;border-bottom:2px solid transparent;margin-bottom:-2px;text-decoration:none;display:inline-block}
.tab:hover{color:#1a4a80;background:#f5f8ff}
.tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}

.cfg-body{flex:1;overflow:hidden;display:flex;flex-direction:column}

.err-box{background:#fff0f0;border:1px solid #ffb3b3;color:#c00;padding:10px 14px;margin:10px;border-radius:5px;font-size:13px}

/* ── Two-panel tree ─────────────────────────────────── */
.cfg-split{display:flex;flex:1;overflow:hidden}

.cfg-left{width:220px;flex-shrink:0;background:#fff;border-right:1px solid #d8dde8;overflow-y:auto;padding:6px 0}
.cfg-group{font-size:11px;font-weight:700;color:#888;text-transform:uppercase;letter-spacing:.5px;padding:10px 12px 4px;margin-top:4px}
.cfg-group:first-child{margin-top:0}
.cfg-item{padding:6px 12px 6px 20px;cursor:pointer;font-size:13px;color:#333;display:flex;align-items:center;gap:6px;border-left:2px solid transparent}
.cfg-item:hover{background:#f0f4ff;color:#1a4a80}
.cfg-item.sel{background:#e8eeff;color:#1a4a80;font-weight:600;border-left-color:#1a4a80}
.cfg-item .ic{font-size:13px;flex-shrink:0}
.cfg-item .bp{background:#dbeafe;color:#1d4ed8;font-size:9px;font-weight:700;padding:1px 5px;border-radius:8px;margin-left:2px}

.cfg-right{flex:1;overflow-y:auto;padding:16px}

.cfg-panel{display:none}
.cfg-panel.active{display:block}

/* ── Panel content ──────────────────────────────────── */
.panel-title{font-size:16px;font-weight:700;color:#1a3a6a;margin-bottom:4px;display:flex;align-items:center;gap:8px}
.panel-kind{font-size:11px;color:#888;font-weight:400;margin-bottom:14px}

.section-hd{font-size:11px;font-weight:700;color:#888;text-transform:uppercase;letter-spacing:.4px;margin:14px 0 6px;border-top:1px solid #eef0f5;padding-top:10px}
.section-hd:first-child{border-top:none;margin-top:0;padding-top:0}

.fields-tbl{width:100%;border-collapse:collapse;font-size:12px;margin-bottom:4px}
.fields-tbl th{text-align:left;padding:5px 8px;color:#999;font-weight:600;font-size:11px;border-bottom:1px solid #eef0f5}
.fields-tbl td{padding:5px 8px;border-bottom:1px solid #f7f8fb;color:#333}
.fields-tbl tr:last-child td{border-bottom:none}
.fields-tbl tr:hover td{background:#f8f9fc}
.ft-str{color:#059669}.ft-num{color:#7c3aed}.ft-date{color:#b45309}.ft-bool{color:#0284c7}.ft-ref{color:#1a4a80;font-weight:500}
.fields-tbl select{padding:3px 5px;border:1px solid #ccd0d8;border-radius:3px;font-size:12px;background:#fff;color:#333}
.fields-tbl select:focus{border-color:#1a4a80;outline:none}
.success-box{background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:10px 14px;margin:10px;border-radius:5px;font-size:13px}

.tp-block{margin-bottom:8px;background:#f8f9fc;border:1px solid #e8ecf4;border-radius:5px;overflow:hidden}
.tp-hd{padding:6px 10px;font-size:12px;font-weight:600;color:#334;background:#f0f3f8}

/* ── Module editor ───────────────────────────────────── */
.code-wrap{position:relative;margin-top:8px}
.clickable-code{cursor:text}
.clickable-code:hover::after{content:'✎';position:absolute;top:8px;right:10px;color:#546e7a;font-size:12px}
.edit-hint{font-size:11px;color:#94a3b8;margin-left:6px}
.module-tabs{display:flex;gap:0;margin-top:16px;border-bottom:1px solid #d8dde8}
.module-tab{padding:6px 14px;cursor:pointer;font-size:12px;color:#666;border-bottom:2px solid transparent;margin-bottom:-1px}
.module-tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}
.module-pane{display:none;margin-top:0}
.module-pane.active{display:block}

.module-editor-wrap{position:relative;margin-top:8px}
pre.os-code{
  background:#1e1e2e;color:#cdd6f4;
  font-family:'Cascadia Code','Fira Code','Consolas','Courier New',monospace;
  font-size:12px;line-height:1.6;padding:14px 16px;border-radius:6px;
  overflow:auto;white-space:pre;max-height:340px;tab-size:2
}
.os-edit{
  width:100%;min-height:220px;max-height:340px;
  background:#1e1e2e;color:#cdd6f4;
  font-family:'Cascadia Code','Fira Code','Consolas','Courier New',monospace;
  font-size:12px;line-height:1.6;padding:14px 16px;border-radius:6px;
  border:none;resize:vertical;outline:none;tab-size:2
}
.os-edit:focus{box-shadow:0 0 0 2px #3070d840}
.module-save-row{margin-top:8px;display:flex;align-items:center;gap:10px}
.btn-save{background:#1a4a80;color:#fff;border:none;padding:7px 16px;border-radius:4px;cursor:pointer;font-size:12px}
.btn-save:hover{background:#15396a}
.save-ok{color:#059669;font-size:12px}
.module-empty{color:#888;font-size:12px;padding:10px 0;font-style:italic}

/* ── Syntax colours ─────────────────────────────────── */
.hl-kw{color:#c792ea;font-weight:600}
.hl-fn{color:#82aaff}
.hl-sp{color:#ff5370;font-weight:600}
.hl-str{color:#c3e88d}
.hl-num{color:#f78c6c}
.hl-cmt{color:#546e7a;font-style:italic}

/* ── Converter / Files ───────────────────────────────── */
.pad{padding:16px}
.convert-form,.file-card{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:18px;margin-bottom:14px}
.convert-form h3,.file-card h3{font-size:13px;font-weight:700;color:#1a3a6a;margin-bottom:12px}
.fg{margin-bottom:12px}
.fg label{display:block;font-size:11px;font-weight:700;color:#555;margin-bottom:4px;text-transform:uppercase;letter-spacing:.3px}
.fg input[type=text],.fg textarea{width:100%;padding:7px 10px;border:1px solid #c8d0de;border-radius:4px;font-size:13px}
.fg input:focus,.fg textarea:focus{border-color:#1a4a80;outline:none}
.fg .hint{font-size:11px;color:#888;margin-top:3px}
.form-btns{display:flex;gap:8px}
.btn-primary{background:#1a4a80;color:#fff;border:none;padding:7px 16px;border-radius:4px;cursor:pointer;font-size:13px}
.btn-primary:hover{background:#15396a}
.btn-secondary{background:#e8ecf2;color:#333;border:1px solid #c8d0de;padding:7px 14px;border-radius:4px;cursor:pointer;font-size:13px}
.convert-result{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:14px;margin-bottom:14px}
pre.convert-out{background:#f5f7fa;border:1px solid #e2e6ed;padding:12px;border-radius:4px;font-size:12px;white-space:pre-wrap;max-height:280px;overflow-y:auto}
.applied{background:#dcfce7;color:#15803d;padding:8px 12px;border-radius:4px;font-size:13px;margin-bottom:12px;font-weight:500}
.files-grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}
.file-card p{font-size:12px;color:#666;margin-bottom:12px;line-height:1.5}
</style>
{{end}}`

// ── Head / foot ───────────────────────────────────────────────────────────────

const cfgHead = `{{define "cfg-head"}}<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>Конфигуратор — {{if .AppName}}{{.AppName}}{{else}}{{.Base.Name}}{{end}}</title>
{{template "css" .}}
</head>
<body>
<div class="topbar">
  <a href="/?sel={{.Base.ID}}">← Лаунчер</a>
  <h1>Конфигуратор — {{if .AppName}}{{.AppName}}{{else}}{{.Base.Name}}{{end}}</h1>
  <span style="font-size:11px;color:#7aa8d8">{{.Base.DB}} · :{{.Base.Port}} · платформа {{.PlatformVer}}</span>
</div>
<div class="tabs">
  <a class="tab {{if eq .Tab "tree"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=tree">🌳 Дерево</a>
  <a class="tab {{if eq .Tab "convert"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=convert">🔄 Конвертер 1С</a>
  <a class="tab {{if eq .Tab "files"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=files">📁 Файлы</a>
</div>
<div class="cfg-body">
{{if .Error}}<div class="err-box">{{.Error}}</div>{{end}}
{{if .FieldsSaved}}<div class="success-box">✓ Типы полей для «{{.FieldsSavedEntity}}» сохранены. Перезапустите базу, чтобы изменения вступили в силу.</div>{{end}}
{{end}}`

const cfgFoot = `{{define "cfg-foot"}}
</div>
<script>
// ── Reference picker toggle ────────────────────────────────────
function cfgToggleRef(sel, refId) {
  var r = document.getElementById(refId);
  if (r) r.style.display = sel.value === 'reference' ? '' : 'none';
}
// ── Click-to-edit module ───────────────────────────────────────
function startEdit(name) {
  var pre = document.getElementById('pre-'+name);
  var ta  = document.getElementById('ta-'+name);
  ta.value = pre.textContent;
  pre.style.display = 'none';
  ta.style.display  = 'block';
  ta.focus();
}
function endEdit(name) {
  var pre = document.getElementById('pre-'+name);
  var ta  = document.getElementById('ta-'+name);
  pre.innerHTML = hl(ta.value);
  pre.style.display = 'block';
  ta.style.display  = 'none';
}
// ── Panel selection ────────────────────────────────────────────
function selItem(el) {
  document.querySelectorAll('.cfg-item').forEach(function(e){e.classList.remove('sel')});
  document.querySelectorAll('.cfg-panel').forEach(function(e){e.classList.remove('active')});
  el.classList.add('sel');
  var panel = document.getElementById(el.dataset.id);
  if (panel) panel.classList.add('active');
}
(function(){
  var first = document.querySelector('.cfg-item');
  if (first) selItem(first);
})();

// ── Module tabs ────────────────────────────────────────────────
function modTab(el, panelId) {
  var wrap = el.closest('.module-editor-wrap');
  wrap.querySelectorAll('.module-tab').forEach(function(t){t.classList.remove('active')});
  wrap.querySelectorAll('.module-pane').forEach(function(p){p.classList.remove('active')});
  el.classList.add('active');
  document.getElementById(panelId).classList.add('active');
}

// ── Syntax highlight ───────────────────────────────────────────
(function(){
var KW=['Процедура','КонецПроцедуры','Функция','КонецФункции',
  'Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
  'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока',
  'Возврат','Прервать','Продолжить','Истина','Ложь','Неопределено','Новый',
  'И','ИЛИ','НЕ','Не',
  'Procedure','EndProcedure','Function','EndFunction',
  'If','Then','ElseIf','Else','EndIf',
  'For','Each','In','Do','EndDo','While','EndWhile',
  'Return','Break','Continue','True','False','Undefined','New',
  'And','Or','Not','Var'];
var FN=['Error','Ошибка','Сообщить','Формат','ФорматСтроки','СтрЗаменить'];
var SP=['this','Движения','Параметры'];

function esc(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}

function hl(code){
  var r='',i=0,n=code.length;
  while(i<n){
    if(code[i]==='/' && code[i+1]==='/'){
      var e=code.indexOf('\n',i);if(e<0)e=n;
      r+='<span class="hl-cmt">'+esc(code.slice(i,e))+'</span>';i=e;continue;
    }
    if(code[i]==='"'){
      var j=i+1;while(j<n && code[j]!=='"')j++;
      r+='<span class="hl-str">'+esc(code.slice(i,j+1))+'</span>';i=j+1;continue;
    }
    if(/[0-9]/.test(code[i])){
      var j=i;while(j<n && /[0-9.]/.test(code[j]))j++;
      r+='<span class="hl-num">'+esc(code.slice(i,j))+'</span>';i=j;continue;
    }
    if(/[а-яёА-ЯЁa-zA-Z_]/.test(code[i])){
      var j=i;while(j<n && /[а-яёА-ЯЁa-zA-Z0-9_]/.test(code[j]))j++;
      var w=code.slice(i,j);
      if(KW.indexOf(w)>=0)r+='<span class="hl-kw">'+esc(w)+'</span>';
      else if(FN.indexOf(w)>=0)r+='<span class="hl-fn">'+esc(w)+'</span>';
      else if(SP.indexOf(w)>=0)r+='<span class="hl-sp">'+esc(w)+'</span>';
      else r+=esc(w);
      i=j;continue;
    }
    r+=esc(code[i]);i++;
  }
  return r;
}
document.querySelectorAll('pre.os-code').forEach(function(el){
  el.innerHTML=hl(el.textContent);
});
})();
</script>
</body></html>
{{end}}`

// ── Main dispatcher ───────────────────────────────────────────────────────────

const cfgMain = `{{define "cfg-main"}}
{{template "cfg-head" .}}
{{if eq .Tab "tree"}}{{template "tab-tree" .}}{{end}}
{{if eq .Tab "convert"}}{{template "tab-convert" .}}{{end}}
{{if eq .Tab "files"}}{{template "tab-files" .}}{{end}}
{{template "cfg-foot" .}}
{{end}}`

// ── Tree tab ──────────────────────────────────────────────────────────────────

const cfgTabTree = `{{define "tab-tree"}}
{{if or .Catalogs .Docs .Registers .Reports}}
<div class="cfg-split">

{{/* ── Left panel ── */}}
<div class="cfg-left">
  {{if .Catalogs}}
  <div class="cfg-group">Справочники</div>
  {{range .Catalogs}}
  <div class="cfg-item" data-id="e-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📄</span>{{.Name}}
  </div>
  {{end}}
  {{end}}

  {{if .Docs}}
  <div class="cfg-group">Документы</div>
  {{range .Docs}}
  <div class="cfg-item" data-id="e-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📃</span>{{.Name}}{{if .Posting}}<span class="bp">✓</span>{{end}}
  </div>
  {{end}}
  {{end}}

  {{if .Registers}}
  <div class="cfg-group">Регистры</div>
  {{range .Registers}}
  <div class="cfg-item" data-id="r-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📊</span>{{.Name}}
  </div>
  {{end}}
  {{end}}

  {{if .Reports}}
  <div class="cfg-group">Отчёты</div>
  {{range .Reports}}
  <div class="cfg-item" data-id="rep-{{.Name}}" onclick="selItem(this)">
    <span class="ic">📈</span>{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}
  </div>
  {{end}}
  {{end}}
</div>

{{/* ── Right panel ── */}}
<div class="cfg-right">

  {{/* Catalogs */}}
  {{range .Catalogs}}
  <div class="cfg-panel" id="e-{{.Name}}">
    <div class="panel-title">📄 {{.Name}}</div>
    <div class="panel-kind">Справочник</div>
    {{template "entity-detail" (dict "Entity" . "BaseID" $.Base.ID "ConfigSource" $.Base.ConfigSource "ModuleSaved" $.ModuleSaved "ModuleSavedEntity" $.ModuleSavedEntity "AllEntityNames" $.AllEntityNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity)}}
  </div>
  {{end}}

  {{/* Documents */}}
  {{range .Docs}}
  <div class="cfg-panel" id="e-{{.Name}}">
    <div class="panel-title">
      📃 {{.Name}}
      {{if .Posting}}<span style="background:#dbeafe;color:#1d4ed8;font-size:11px;font-weight:600;padding:2px 8px;border-radius:10px">проводится</span>{{end}}
    </div>
    <div class="panel-kind">Документ</div>
    {{template "entity-detail" (dict "Entity" . "BaseID" $.Base.ID "ConfigSource" $.Base.ConfigSource "ModuleSaved" $.ModuleSaved "ModuleSavedEntity" $.ModuleSavedEntity "AllEntityNames" $.AllEntityNames "FieldsSaved" $.FieldsSaved "FieldsSavedEntity" $.FieldsSavedEntity)}}
  </div>
  {{end}}

  {{/* Registers */}}
  {{range .Registers}}
  <div class="cfg-panel" id="r-{{.Name}}">
    <div class="panel-title">📊 {{.Name}}</div>
    <div class="panel-kind">Регистр накопления</div>
    {{if .Dimensions}}
    <div class="section-hd">Измерения</div>
    <table class="fields-tbl">
      <tr><th>Поле</th><th>Тип</th></tr>
      {{range .Dimensions}}<tr><td>{{.Name}}</td><td class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</td></tr>{{end}}
    </table>
    {{end}}
    {{if .Resources}}
    <div class="section-hd">Ресурсы</div>
    <table class="fields-tbl">
      <tr><th>Поле</th><th>Тип</th></tr>
      {{range .Resources}}<tr><td>{{.Name}}</td><td class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</td></tr>{{end}}
    </table>
    {{end}}
    {{if .Attributes}}
    <div class="section-hd">Реквизиты</div>
    <table class="fields-tbl">
      <tr><th>Поле</th><th>Тип</th></tr>
      {{range .Attributes}}<tr><td>{{.Name}}</td><td class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</td></tr>{{end}}
    </table>
    {{end}}
  </div>
  {{end}}

  {{/* Reports */}}
  {{range .Reports}}
  <div class="cfg-panel" id="rep-{{.Name}}">
    <div class="panel-title">📈 {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</div>
    <div class="panel-kind">Отчёт</div>
    {{if .Params}}
    <div class="section-hd">Параметры</div>
    {{range .Params}}<div style="font-size:12px;padding:3px 0;color:#555">{{.}}</div>{{end}}
    {{end}}
    {{if .Query}}
    <div class="section-hd">Запрос</div>
    <pre class="os-code">{{.Query}}</pre>
    {{end}}
  </div>
  {{end}}

</div>{{/* cfg-right */}}
</div>{{/* cfg-split */}}

{{else}}
<div class="pad">
  <div style="background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:40px;text-align:center;color:#999">
    <div style="font-size:32px;margin-bottom:10px">📭</div>
    <div>Конфигурация пуста или не загружена.</div>
  </div>
</div>
{{end}}
{{end}}

{{define "entity-detail"}}
{{$e := .Entity}}
{{$baseID := .BaseID}}
{{$allEntities := .AllEntityNames}}
{{$fSaved := .FieldsSaved}}
{{$fSavedEnt := .FieldsSavedEntity}}

<form method="POST" action="/bases/{{$baseID}}/configurator/fields">
<input type="hidden" name="entity" value="{{$e.Name}}">
{{range $e.TableParts}}<input type="hidden" name="tp_names" value="{{.Name}}">{{end}}

{{if $e.Fields}}
<div class="section-hd">Реквизиты</div>
<table class="fields-tbl">
<tr><th>Поле</th><th>Тип</th><th style="min-width:150px">Объект</th></tr>
{{range $i, $f := $e.Fields}}
<input type="hidden" name="field.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="field.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$e.Name}}-f{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>строка</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>число</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>дата</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>булево</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>ссылка →</option>
    </select>
  </td>
  <td>
    <select name="field.{{$i}}.ref" id="cfr-{{$e.Name}}-f{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">— выбрать —</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{end}}
</table>
{{end}}

{{range $j, $tp := $e.TableParts}}
<div class="section-hd">📋 {{$tp.Name}} (табличная часть)</div>
<div class="tp-block">
<table class="fields-tbl">
<tr><th>Поле</th><th>Тип</th><th style="min-width:150px">Объект</th></tr>
{{range $i, $f := $tp.Fields}}
<input type="hidden" name="tp.{{$tp.Name}}.field.{{$i}}.name" value="{{$f.Name}}">
<tr>
  <td>{{$f.Name}}</td>
  <td>
    <select name="tp.{{$tp.Name}}.field.{{$i}}.type" onchange="cfgToggleRef(this,'cfr-{{$e.Name}}-tp{{$j}}f{{$i}}')">
      <option value="string"    {{if eq $f.Type "string"}}selected{{end}}>строка</option>
      <option value="number"    {{if eq $f.Type "number"}}selected{{end}}>число</option>
      <option value="date"      {{if eq $f.Type "date"}}selected{{end}}>дата</option>
      <option value="bool"      {{if eq $f.Type "bool"}}selected{{end}}>булево</option>
      <option value="reference" {{if eq $f.Type "reference"}}selected{{end}}>ссылка →</option>
    </select>
  </td>
  <td>
    <select name="tp.{{$tp.Name}}.field.{{$i}}.ref" id="cfr-{{$e.Name}}-tp{{$j}}f{{$i}}"{{if ne $f.Type "reference"}} style="display:none"{{end}}>
      <option value="">— выбрать —</option>
      {{range $allEntities}}<option value="{{.}}"{{if eq . $f.RefEntity}} selected{{end}}>{{.}}</option>{{end}}
    </select>
  </td>
</tr>
{{end}}
</table>
</div>
{{end}}

<div class="module-save-row" style="margin-bottom:14px">
  <button class="btn-save" type="submit">Сохранить типы полей</button>
  {{if and $fSaved (eq $fSavedEnt $e.Name)}}<span class="save-ok">✓ Сохранено</span>{{end}}
</div>
</form>

{{/* Module section */}}
<div class="section-hd">Модули</div>
<div class="module-editor-wrap">
  <div class="module-tabs">
    <div class="module-tab active" onclick="modTab(this,'mp-obj-{{$e.Name}}')">📝 Модуль объекта</div>
    <div class="module-tab" onclick="modTab(this,'mp-mgr-{{$e.Name}}')">📋 Модуль менеджера</div>
  </div>

  <div class="module-pane active" id="mp-obj-{{$e.Name}}">
    <form method="POST" action="/bases/{{.BaseID}}/configurator/module">
      <input type="hidden" name="entity" value="{{$e.Name}}">
      <input type="hidden" name="module_type" value="object">
      <div class="code-wrap" title="Кликните для редактирования">
        <pre class="os-code clickable-code" id="pre-{{$e.Name}}"
             onclick="startEdit('{{$e.Name}}')">{{if $e.Source}}{{$e.Source}}{{else}}// Кликните для редактирования&#10;Процедура ПриЗаписи()&#10;&#10;КонецПроцедуры{{end}}</pre>
        <textarea class="os-edit" id="ta-{{$e.Name}}" name="source"
                  style="display:none"
                  onblur="endEdit('{{$e.Name}}')">{{$e.Source}}</textarea>
      </div>
      <div class="module-save-row">
        <button class="btn-save" type="submit">Сохранить</button>
        <span class="edit-hint">✎ кликните на код для редактирования</span>
        {{if and $.ModuleSaved (eq $.ModuleSavedEntity $e.Name)}}<span class="save-ok">✓ Сохранено</span>{{end}}
      </div>
    </form>
  </div>

  <div class="module-pane" id="mp-mgr-{{$e.Name}}">
    <div class="module-empty" style="padding:12px 0">Модуль менеджера — в разработке.</div>
  </div>
</div>
{{end}}`

// ── Converter tab ─────────────────────────────────────────────────────────────

const cfgTabConvert = `{{define "tab-convert"}}
<div class="pad">
<div class="convert-form">
  <h3>🔄 Конвертация конфигурации 1С → onebase</h3>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/convert">
    <div class="fg">
      <label>Путь к папке выгрузки 1С</label>
      <input type="text" name="src_dir" value="{{.ConvertSrcDir}}"
             placeholder="C:\Users\...\1C\МояКонфигурация" autofocus>
      <div class="hint">В 1С: Конфигуратор → Конфигурация → Выгрузить конфигурацию в файлы</div>
    </div>
    <div class="form-btns">
      <button class="btn-primary" type="submit" name="apply" value="0">Просмотр</button>
      <button class="btn-secondary" type="submit" name="apply" value="1">Конвертировать и применить</button>
    </div>
  </form>
</div>
{{if .ConvertApplied}}<div class="applied">✓ Конфигурация применена к базе</div>{{end}}
{{if .ConvertResult}}
<div class="convert-result">
  <h3>Результат</h3>
  <pre class="convert-out">{{.ConvertResult}}</pre>
</div>
{{end}}
</div>
{{end}}`

// ── Files tab ─────────────────────────────────────────────────────────────────

const cfgTabFiles = `{{define "tab-files"}}
<div class="pad">
<div class="files-grid">
  <div class="file-card">
    <h3>📤 Выгрузить конфигурацию</h3>
    <p>Экспортирует файлы в<br><code>~/.onebase/workspace/{{.Base.ID}}/</code><br>и открывает папку.</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/export">
      <button class="btn-primary" type="submit">Выгрузить</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">Файловый режим — файлы в:<br><code>{{.Base.Path}}</code></p>
    {{end}}
  </div>
  <div class="file-card">
    <h3>📥 Загрузить конфигурацию</h3>
    <p>Загружает файлы из папки в базу данных и применяет миграцию.</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/import">
      <div class="fg">
        <label>Путь к папке</label>
        <input type="text" name="path" placeholder="~/.onebase/workspace/{{.Base.ID}}">
      </div>
      <button class="btn-primary" type="submit">Загрузить</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">Редактируйте файлы напрямую. Сервер перезагружает конфигурацию автоматически.</p>
    {{end}}
  </div>
</div>
</div>
{{end}}`
