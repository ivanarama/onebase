package launcher

import "html/template"

var cfgTmpl = template.Must(template.New("cfg").Funcs(template.FuncMap{
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
}).Parse(cfgCSS + cfgHead + cfgTabs + cfgTabTree + cfgTabConvert + cfgTabFiles + cfgFoot))

const cfgCSS = `
{{define "css"}}
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;font-size:13px;background:#f0f2f5;min-height:100vh}

/* top bar */
.topbar{background:linear-gradient(to bottom,#2c5f9e,#1a4a80);color:#fff;padding:8px 16px;display:flex;align-items:center;gap:12px}
.topbar a{color:#b8d4ff;text-decoration:none;font-size:12px}
.topbar a:hover{color:#fff}
.topbar h1{font-size:15px;font-weight:600;flex:1}

/* tabs */
.tabs{display:flex;gap:0;background:#fff;border-bottom:2px solid #d0d7e3;padding:0 16px}
.tab{padding:10px 20px;cursor:pointer;font-size:13px;color:#666;border-bottom:2px solid transparent;margin-bottom:-2px;text-decoration:none;display:inline-block}
.tab:hover{color:#1a4a80;background:#f5f8ff}
.tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}

/* content */
.content{padding:16px;max-width:1100px}

/* error */
.err-box{background:#fff0f0;border:1px solid #ffb3b3;color:#c00;padding:12px 16px;border-radius:6px;margin-bottom:12px}

/* tree */
.tree{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:8px 0}
details.meta-section{border-bottom:1px solid #eef0f5}
details.meta-section:last-child{border-bottom:none}
details.meta-section>summary{
  padding:9px 14px;cursor:pointer;font-weight:600;font-size:13px;
  color:#1a3a6a;list-style:none;display:flex;align-items:center;gap:6px;
  user-select:none
}
details.meta-section>summary::-webkit-details-marker{display:none}
details.meta-section>summary::before{content:'▶';font-size:10px;color:#888;transition:transform .15s}
details.meta-section[open]>summary::before{transform:rotate(90deg)}
details.meta-section>summary:hover{background:#f5f8ff}

.section-count{font-weight:400;font-size:11px;color:#888;margin-left:4px}

details.meta-obj{margin:0 0 0 24px;border-top:1px solid #f0f3f8}
details.meta-obj>summary{
  padding:7px 12px;cursor:pointer;font-size:13px;color:#333;
  list-style:none;display:flex;align-items:center;gap:6px
}
details.meta-obj>summary::-webkit-details-marker{display:none}
details.meta-obj>summary::before{content:'▶';font-size:9px;color:#aaa;transition:transform .15s}
details.meta-obj[open]>summary::before{transform:rotate(90deg)}
details.meta-obj>summary:hover{background:#f8f9fc}
.obj-icon{font-size:14px}
.badge-post{background:#dbeafe;color:#1d4ed8;font-size:10px;font-weight:600;padding:1px 6px;border-radius:10px;margin-left:4px}

/* fields */
.fields-block{margin:0 0 4px 48px;padding:4px 0}
.field-row{display:flex;align-items:center;gap:8px;padding:3px 8px;border-radius:3px;font-size:12px}
.field-row:hover{background:#f5f8ff}
.field-name{color:#222;min-width:140px}
.field-kind{font-size:10px;color:#999;min-width:60px}
.ft-str{color:#059669;font-size:11px}
.ft-num{color:#7c3aed;font-size:11px}
.ft-date{color:#b45309;font-size:11px}
.ft-bool{color:#0284c7;font-size:11px}
.ft-ref{color:#1a4a80;font-size:11px;font-weight:500}

/* table parts */
details.meta-tp{margin:4px 0 4px 48px}
details.meta-tp>summary{
  padding:4px 10px;cursor:pointer;font-size:12px;color:#555;
  list-style:none;display:flex;align-items:center;gap:5px
}
details.meta-tp>summary::-webkit-details-marker{display:none}
details.meta-tp>summary::before{content:'▶';font-size:9px;color:#ccc;transition:transform .15s}
details.meta-tp[open]>summary::before{transform:rotate(90deg)}

/* module */
.module-block{margin:6px 0 8px 48px}
details.meta-mod>summary{
  padding:4px 10px;cursor:pointer;font-size:12px;color:#555;
  list-style:none;display:flex;align-items:center;gap:5px
}
details.meta-mod>summary::-webkit-details-marker{display:none}
details.meta-mod>summary::before{content:'▶';font-size:9px;color:#ccc;transition:transform .15s}
details.meta-mod[open]>summary::before{transform:rotate(90deg)}
pre.os-code{
  background:#1e1e2e;color:#cdd6f4;font-family:'Cascadia Code','Fira Code','Courier New',monospace;
  font-size:12px;padding:14px 16px;border-radius:6px;
  overflow-x:auto;white-space:pre;max-height:400px;overflow-y:auto;margin:4px 10px 0
}
.hl-kw{color:#c792ea;font-weight:600}
.hl-fn{color:#82aaff}
.hl-sp{color:#ff5370;font-weight:600}
.hl-str{color:#c3e88d}
.hl-num{color:#f78c6c}
.hl-cmt{color:#546e7a;font-style:italic}

/* register / report sections */
.reg-block{padding:6px 14px 6px 48px}
.reg-label{font-size:11px;font-weight:600;color:#888;margin-bottom:3px;text-transform:uppercase;letter-spacing:.4px}

/* converter */
.convert-form{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:20px;margin-bottom:16px}
.convert-form h3{font-size:14px;font-weight:600;color:#1a3a6a;margin-bottom:14px}
.fg{margin-bottom:14px}
.fg label{display:block;font-size:12px;font-weight:600;color:#444;margin-bottom:5px}
.fg input[type=text]{width:100%;padding:7px 10px;border:1px solid #c8d0de;border-radius:4px;font-size:13px}
.fg input:focus{border-color:#1a4a80;outline:none}
.fg .hint{font-size:11px;color:#888;margin-top:3px}
.form-btns{display:flex;gap:10px;margin-top:6px}
.btn-primary{background:#1a4a80;color:#fff;border:none;padding:8px 18px;border-radius:4px;cursor:pointer;font-size:13px}
.btn-primary:hover{background:#15396a}
.btn-secondary{background:#e8ecf2;color:#333;border:1px solid #c8d0de;padding:8px 16px;border-radius:4px;cursor:pointer;font-size:13px}
.btn-secondary:hover{background:#d8dde8}
.convert-result{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:16px;margin-bottom:16px}
.convert-result h3{font-size:14px;font-weight:600;color:#1a3a6a;margin-bottom:10px}
pre.convert-out{background:#f5f7fa;border:1px solid #e2e6ed;padding:12px;border-radius:4px;font-size:12px;white-space:pre-wrap;max-height:320px;overflow-y:auto}
.applied-badge{background:#dcfce7;color:#15803d;padding:8px 14px;border-radius:4px;font-size:13px;margin-bottom:12px;font-weight:500}

/* files tab */
.files-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.file-card{background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:18px}
.file-card h3{font-size:14px;font-weight:600;color:#1a3a6a;margin-bottom:8px}
.file-card p{font-size:12px;color:#666;margin-bottom:14px;line-height:1.5}
</style>
{{end}}
`

const cfgHead = `
{{define "cfg-head"}}<!DOCTYPE html>
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
  <span style="font-size:11px;color:#7aa8d8">{{.Base.DB}} · :{{.Base.Port}}</span>
</div>
<div class="tabs">
  <a class="tab {{if eq .Tab "tree"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=tree">🌳 Дерево метаданных</a>
  <a class="tab {{if eq .Tab "convert"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=convert">🔄 Конвертер 1С</a>
  <a class="tab {{if eq .Tab "files"}}active{{end}}" href="/bases/{{.Base.ID}}/configurator?tab=files">📁 Файлы</a>
</div>
<div class="content">
{{if .Error}}<div class="err-box">{{.Error}}</div>{{end}}
{{end}}
`

const cfgFoot = `
{{define "cfg-foot"}}
</div>
<script>
(function(){
var KW=['Процедура','КонецПроцедуры','Если','Тогда','ИначеЕсли','Иначе','КонецЕсли',
  'Для','Каждого','Из','Цикл','КонецЦикла','Пока','КонецПока','Возврат','Истина','Ложь',
  'И','ИЛИ','НЕ','Не','Новый',
  'Procedure','EndProcedure','Function','EndFunction','If','Then','ElseIf','Else','EndIf',
  'For','Each','In','Do','EndDo','While','EndWhile','Return','True','False','New',
  'And','Or','Not','Var','Break','Continue'];
var FN=['Error','Ошибка','Сообщить','ФорматСтроки'];
var SP=['this','Движения','Параметры'];

function esc(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}

function highlight(code){
  var r='',i=0,n=code.length;
  while(i<n){
    // comment
    if(code[i]==='/' && code[i+1]==='/'){
      var e=code.indexOf('\n',i); if(e<0)e=n;
      r+='<span class="hl-cmt">'+esc(code.slice(i,e))+'</span>'; i=e; continue;
    }
    // string
    if(code[i]==='"'){
      var j=i+1; while(j<n && code[j]!=='"')j++;
      r+='<span class="hl-str">'+esc(code.slice(i,j+1))+'</span>'; i=j+1; continue;
    }
    // number
    if(/[0-9]/.test(code[i])){
      var j=i; while(j<n && /[0-9.]/.test(code[j]))j++;
      r+='<span class="hl-num">'+esc(code.slice(i,j))+'</span>'; i=j; continue;
    }
    // identifier
    if(/[а-яёА-ЯЁa-zA-Z_]/.test(code[i])){
      var j=i; while(j<n && /[а-яёА-ЯЁa-zA-Z0-9_]/.test(code[j]))j++;
      var w=code.slice(i,j);
      if(KW.indexOf(w)>=0) r+='<span class="hl-kw">'+esc(w)+'</span>';
      else if(FN.indexOf(w)>=0) r+='<span class="hl-fn">'+esc(w)+'</span>';
      else if(SP.indexOf(w)>=0) r+='<span class="hl-sp">'+esc(w)+'</span>';
      else r+=esc(w);
      i=j; continue;
    }
    r+=esc(code[i]); i++;
  }
  return r;
}

document.querySelectorAll('pre.os-code').forEach(function(el){
  el.innerHTML=highlight(el.textContent);
});
})();
</script>
</body></html>
{{end}}
`

const cfgTabs = `
{{define "cfg-main"}}
{{template "cfg-head" .}}
{{if eq .Tab "tree"}}{{template "tab-tree" .}}{{end}}
{{if eq .Tab "convert"}}{{template "tab-convert" .}}{{end}}
{{if eq .Tab "files"}}{{template "tab-files" .}}{{end}}
{{template "cfg-foot" .}}
{{end}}
`

const cfgTabTree = `
{{define "tab-tree"}}
{{if and (not .Error) (or .Catalogs .Docs .Registers .Reports)}}
<div class="tree">

{{if .Catalogs}}
<details class="meta-section" open>
<summary>📂 Справочники <span class="section-count">({{len .Catalogs}})</span></summary>
{{range .Catalogs}}
<details class="meta-obj">
<summary><span class="obj-icon">📄</span> {{.Name}}</summary>
<div class="fields-block">
{{range .Fields}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
{{range .TableParts}}
<details class="meta-tp">
<summary>📋 {{.Name}} (табличная часть)</summary>
<div class="fields-block">
{{range .Fields}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
</details>
{{end}}
{{if .Source}}
<details class="meta-mod module-block">
<summary>📝 Модуль</summary>
<pre class="os-code">{{.Source}}</pre>
</details>
{{end}}
</details>
{{end}}
</details>
{{end}}

{{if .Docs}}
<details class="meta-section" open>
<summary>📂 Документы <span class="section-count">({{len .Docs}})</span></summary>
{{range .Docs}}
<details class="meta-obj">
<summary>
  <span class="obj-icon">📃</span> {{.Name}}
  {{if .Posting}}<span class="badge-post">проводится</span>{{end}}
</summary>
<div class="fields-block">
{{range .Fields}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
{{range .TableParts}}
<details class="meta-tp">
<summary>📋 {{.Name}} (табличная часть)</summary>
<div class="fields-block">
{{range .Fields}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
</details>
{{end}}
{{if .Source}}
<details class="meta-mod module-block">
<summary>📝 Модуль</summary>
<pre class="os-code">{{.Source}}</pre>
</details>
{{end}}
</details>
{{end}}
</details>
{{end}}

{{if .Registers}}
<details class="meta-section">
<summary>📂 Регистры накопления <span class="section-count">({{len .Registers}})</span></summary>
{{range .Registers}}
<details class="meta-obj">
<summary><span class="obj-icon">📊</span> {{.Name}}</summary>
{{if .Dimensions}}
<div class="reg-block">
<div class="reg-label">Измерения</div>
{{range .Dimensions}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
{{end}}
{{if .Resources}}
<div class="reg-block">
<div class="reg-label">Ресурсы</div>
{{range .Resources}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
{{end}}
{{if .Attributes}}
<div class="reg-block">
<div class="reg-label">Реквизиты</div>
{{range .Attributes}}
<div class="field-row">
  <span class="field-name">{{.Name}}</span>
  <span class="{{fieldTypeClass .Type}}">{{fieldTypeLabel .Type .RefEntity}}</span>
</div>
{{end}}
</div>
{{end}}
</details>
{{end}}
</details>
{{end}}

{{if .Reports}}
<details class="meta-section">
<summary>📂 Отчёты <span class="section-count">({{len .Reports}})</span></summary>
{{range .Reports}}
<details class="meta-obj">
<summary><span class="obj-icon">📈</span> {{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}</summary>
{{if .Params}}
<div class="reg-block">
<div class="reg-label">Параметры</div>
{{range .Params}}
<div class="field-row"><span class="field-name">{{.}}</span></div>
{{end}}
</div>
{{end}}
{{if .Query}}
<details class="meta-mod module-block">
<summary>🔍 Запрос</summary>
<pre class="os-code">{{.Query}}</pre>
</details>
{{end}}
</details>
{{end}}
</details>
{{end}}

</div>
{{else if not .Error}}
<div style="background:#fff;border:1px solid #d8dde8;border-radius:6px;padding:40px;text-align:center;color:#999">
  <div style="font-size:32px;margin-bottom:10px">📭</div>
  <div>Конфигурация пуста или не загружена.</div>
  <div style="margin-top:6px;font-size:12px">Перейдите на вкладку <strong>Файлы</strong> и загрузите конфигурацию, или используйте <strong>Конвертер 1С</strong>.</div>
</div>
{{end}}
{{end}}
`

const cfgTabConvert = `
{{define "tab-convert"}}
<div class="convert-form">
  <h3>🔄 Конвертация конфигурации 1С → onebase</h3>
  <form method="POST" action="/bases/{{.Base.ID}}/configurator/convert">
    <div class="fg">
      <label>Путь к папке выгрузки 1С-конфигурации</label>
      <input type="text" name="src_dir" value="{{.ConvertSrcDir}}"
             placeholder="C:\Users\user\1C\Конфигурация" autofocus>
      <div class="hint">
        В 1С: Конфигуратор → Конфигурация → Выгрузить конфигурацию в файлы → укажите папку.
      </div>
    </div>
    <div class="form-btns">
      <button class="btn-primary" type="submit" name="apply" value="0">Просмотр</button>
      <button class="btn-secondary" type="submit" name="apply" value="1">Конвертировать и применить</button>
    </div>
  </form>
</div>

{{if .ConvertApplied}}
<div class="applied-badge">✓ Конфигурация применена к базе</div>
{{end}}

{{if .ConvertResult}}
<div class="convert-result">
  <h3>Результат конвертации</h3>
  <pre class="convert-out">{{.ConvertResult}}</pre>
</div>
{{end}}
{{end}}
`

const cfgTabFiles = `
{{define "tab-files"}}
<div class="files-grid">
  <div class="file-card">
    <h3>📤 Выгрузить конфигурацию</h3>
    <p>Экспортирует файлы конфигурации в папку<br>
       <code>~/.onebase/workspace/{{.Base.ID}}/</code><br>
       и открывает её в проводнике.</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/export">
      <button class="btn-primary" type="submit">Выгрузить</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">Файловый режим — файлы хранятся в:<br><code>{{.Base.Path}}</code></p>
    {{end}}
  </div>
  <div class="file-card">
    <h3>📥 Загрузить конфигурацию</h3>
    <p>Загружает файлы из указанной папки в базу данных и применяет миграцию.</p>
    {{if eq .Base.ConfigSource "database"}}
    <form method="POST" action="/bases/{{.Base.ID}}/config/import">
      <div class="fg">
        <label>Путь к папке</label>
        <input type="text" name="path" placeholder="~/.onebase/workspace/{{.Base.ID}}">
      </div>
      <button class="btn-primary" type="submit">Загрузить</button>
    </form>
    {{else}}
    <p style="color:#888;font-size:12px">Файловый режим — редактируйте файлы напрямую в папке проекта.<br>Сервер перезагружает конфигурацию при каждом изменении файлов.</p>
    {{end}}
  </div>
</div>
{{end}}
`
