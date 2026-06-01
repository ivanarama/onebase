package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"

	"github.com/ivantit66/onebase/internal/metadata"
)

// formsTmpl — отдельный набор шаблонов для UI управляемых форм (план 37, этап 4).
// Не подмешан в cfgTmpl чтобы не раздувать огромный configurator_tmpl.go и
// не плодить конфликты define с другими страницами конфигуратора.
//
// Renders:
//   - "forms-editor" — страница split-pane Monaco + live preview
//   - "forms-list"   — список managed-форм проекта (минимальный)
var formsTmpl = template.Must(template.New("forms").Funcs(template.FuncMap{
	"esc": func(s string) string { return html.EscapeString(s) },
	// jsString — встраивание произвольной строки как JS-литерала через
	// json.Marshal. Возвращает с обрамляющими кавычками: `"...escaped..."`.
	// Корректно работает с кириллицей, переносами строк, кавычками,
	// бэкслешами — пригоден для прямой подстановки в JS-выражение без
	// дополнительных манипуляций (replace-цепочки и т.п.).
	// Возвращаемое значение помечается template.JS, чтобы html/template
	// не применил автоматический JS-escape поверх готового литерала.
	"jsString": func(s string) template.JS {
		b, _ := json.Marshal(s)
		return template.JS(b)
	},
}).Parse(tplFormsBase + tplFormsList + tplFormsEditor))

// renderFormsEditor — рендер страницы редактора одной формы.
func renderFormsEditor(w http.ResponseWriter, data *configuratorData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := formsTmpl.ExecuteTemplate(w, "forms-editor", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// renderFormsList — рендер страницы со списком форм.
func renderFormsList(w http.ResponseWriter, data *configuratorData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := formsTmpl.ExecuteTemplate(w, "forms-list", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

const tplFormsBase = `
{{define "forms-head"}}
<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>Управляемые формы — {{.Base.Name}}</title>
<style>
* {box-sizing:border-box}
body{margin:0;font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif;font-size:13px;color:#334;background:#f4f6fb}
header{background:linear-gradient(135deg,#1a4a80,#2d6cb3);color:#fff;padding:10px 18px;display:flex;align-items:center;gap:14px;box-shadow:0 1px 4px rgba(0,0,0,.1)}
header h1{margin:0;font-size:14px;font-weight:600}
header a{color:#cfe2ff;text-decoration:none;font-size:12px}
header a:hover{color:#fff}
.crumbs{margin-left:auto;font-size:12px;color:#cfe2ff}
.crumbs a{margin-right:6px}
main{padding:18px;max-width:1600px;margin:0 auto}
.panel{background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);padding:14px 18px;margin-bottom:14px}
.panel h2{margin:0 0 10px;font-size:14px;color:#1a4a80}
.btn{display:inline-block;padding:6px 12px;border-radius:5px;font-size:12px;border:1px solid #d0d7e3;background:#fff;color:#334;cursor:pointer;text-decoration:none;margin-right:4px}
.btn:hover{background:#f0f4ff;border-color:#1a4a80}
.btn-primary{background:#1a4a80;color:#fff;border-color:#1a4a80}
.btn-primary:hover{background:#2d6cb3;color:#fff}
.btn-danger{background:#dc2626;color:#fff;border-color:#dc2626}
.btn-danger:hover{background:#ef4444}
.btn-success{background:#16a34a;color:#fff;border-color:#16a34a}
.btn-success:hover{background:#22c55e}
table{width:100%;border-collapse:collapse;font-size:13px}
table th,table td{padding:8px 12px;text-align:left;border-bottom:1px solid #eef0f5}
table th{background:#f8fafc;font-weight:600;color:#475569;font-size:12px}
table tr:hover{background:#f4f6fb}
.empty{padding:24px;text-align:center;color:#94a3b8;font-size:13px}
.tag{display:inline-block;padding:2px 8px;border-radius:10px;font-size:11px;font-weight:500;margin-left:6px}
.tag-managed{background:#d1fae5;color:#059669}
.tag-autogen{background:#e0e7ff;color:#6366f1}
.flash-ok{background:#d1fae5;color:#059669;padding:8px 14px;border-radius:6px;margin-bottom:12px;font-size:13px}
.flash-err{background:#fee2e2;color:#dc2626;padding:8px 14px;border-radius:6px;margin-bottom:12px;font-size:13px}
</style>
{{end}}

{{define "forms-header"}}
<header>
  <h1>◇ Управляемые формы</h1>
  <a href="/bases/{{.Base.ID}}/configurator">← В конфигуратор</a>
  <span class="crumbs">
    <a href="/bases/{{.Base.ID}}/configurator/forms">Все формы</a>
    {{if .EditingForm}}/ <a href="/bases/{{.Base.ID}}/configurator/forms/edit?entity={{.EditingForm.Entity}}&name={{.EditingForm.Name}}">{{.EditingForm.Entity}}.{{.EditingForm.Name}}</a>{{end}}
  </span>
</header>
{{end}}
`

const tplFormsList = `
{{define "forms-list"}}
{{template "forms-head" .}}
<body>
{{template "forms-header" .}}
<main>

{{if .Error}}<div class="flash-err">{{.Error}}</div>{{end}}
{{if .FieldsSaved}}<div class="flash-ok">✓ Сохранено: {{.FieldsSavedEntity}}</div>{{end}}

<div class="panel">
  <h2>Все управляемые формы проекта</h2>
  <p style="color:#64748b;font-size:12px;margin-top:0">
    Управляемые формы (◇) описаны декларативно в YAML и переопределяют авто-генерируемые формы.
    Без YAML — каждая сущность рендерится по полям метаданных. Опциональность сохраняется:
    у одной сущности может быть авто-форма, у другой — managed, у третьей — обе (managed имеет приоритет).
  </p>
  {{if .ManagedForms}}
  <table>
    <thead><tr><th>Сущность</th><th>Форма</th><th>Тип</th><th>Модуль</th><th></th></tr></thead>
    <tbody>
    {{range .ManagedForms}}
    <tr>
      <td><b>{{.Entity}}</b></td>
      <td>{{.Name}} <span class="tag tag-managed">◇ managed</span></td>
      <td>{{if .Kind}}{{.Kind}}{{else}}—{{end}}</td>
      <td>{{if .HasOS}}есть{{else}}—{{end}}</td>
      <td style="text-align:right">
        <a class="btn" href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}">Редактировать</a>
      </td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="empty">
    <p>Управляемых форм ещё нет.</p>
    <p style="font-size:12px">Создайте форму вручную или импортируйте из 1С.</p>
  </div>
  {{end}}
</div>

<div class="panel">
  <h2>Создать форму</h2>
  <form action="/bases/{{.Base.ID}}/configurator/forms/edit" method="GET" style="display:flex;gap:8px;align-items:center;flex-wrap:wrap">
    <label>Сущность: <input type="text" name="entity" placeholder="Контрагент" required style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;font-size:13px"></label>
    <label>Имя формы: <input type="text" name="name" placeholder="ФормаОбъекта" required style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;font-size:13px"></label>
    <button type="submit" class="btn btn-primary">Создать</button>
  </form>
</div>

<div class="panel">
  <h2>Импорт из 1С</h2>
  <p style="color:#64748b;font-size:12px;margin-top:0">
    Загрузите Form.xml + Module.bsl (опционально). Архив ZIP со всей формой 1С тоже подойдёт.
    После импорта получите .form.yaml + .form.os + _resources/ с предупреждениями BSL.
  </p>
  <form action="/bases/{{.Base.ID}}/configurator/forms/import-1c" method="POST" enctype="multipart/form-data" style="display:grid;gap:8px;max-width:520px">
    <label>Сущность OneBase: <input type="text" name="entity" required style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;width:100%"></label>
    <label>Имя формы: <input type="text" name="name" value="Форма" style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;width:100%"></label>
    <label>ZIP с формой 1С (или Form.xml внутри): <input type="file" name="zip" accept=".zip"></label>
    <label>либо отдельные файлы:</label>
    <label>Form.xml: <input type="file" name="form_xml" accept=".xml"></label>
    <label>Module.bsl: <input type="file" name="module_bsl" accept=".bsl"></label>
    <button type="submit" class="btn btn-primary">Импортировать</button>
  </form>
</div>

</main>
</body>
</html>
{{end}}
`

const tplFormsEditor = `
{{define "forms-editor"}}
{{template "forms-head" .}}
<style>
.editor-grid{display:grid;grid-template-columns:1fr 1fr;gap:10px;height:calc(100vh - 230px);min-height:480px}
.editor-pane{display:flex;flex-direction:column;background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);overflow:hidden}
.editor-pane-hd{padding:8px 12px;background:#f8fafc;font-size:12px;font-weight:600;color:#475569;border-bottom:1px solid #eef0f5;display:flex;justify-content:space-between;align-items:center}
.editor-pane-body{flex:1;overflow:hidden;display:flex;flex-direction:column}
#yaml-editor,#os-editor{flex:1;min-height:300px}
#preview-frame{flex:1;border:none;background:#fff}
.editor-tools{padding:8px 12px;background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);margin-bottom:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center}
.editor-meta{margin-left:auto;color:#64748b;font-size:12px}
.warn-panel{background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);padding:10px 14px;margin-top:10px;max-height:220px;overflow-y:auto;font-size:12px;display:none}
.warn-panel.active{display:block}
.warn-item{padding:4px 0;border-bottom:1px solid #eef0f5}
.warn-item.error{color:#dc2626}
.warn-item.warn{color:#d97706}
.warn-item.info{color:#64748b}
.editor-tabs{display:flex;background:#f8fafc;border-bottom:1px solid #eef0f5}
.editor-tab{padding:8px 14px;cursor:pointer;font-size:12px;border-bottom:2px solid transparent;color:#64748b}
.editor-tab.active{color:#1a4a80;border-bottom-color:#1a4a80;background:#fff;font-weight:600}
</style>
<body>
{{template "forms-header" .}}
<main>

{{if .Error}}<div class="flash-err">{{.Error}}</div>{{end}}
{{if .FieldsSaved}}<div class="flash-ok">✓ Сохранено: {{.FieldsSavedEntity}}</div>{{end}}

<form id="save-form" action="/bases/{{.Base.ID}}/configurator/forms/save" method="POST">
<input type="hidden" name="entity" value="{{.EditingForm.Entity}}">
<input type="hidden" name="name" value="{{.EditingForm.Name}}">
<input type="hidden" name="yaml" id="yaml-hidden">
<input type="hidden" name="os" id="os-hidden">
</form>

<div class="editor-tools">
  <button class="btn btn-primary" onclick="saveForm()">Сохранить</button>
  <button class="btn btn-success" onclick="refreshPreview()">Просмотр</button>
  <button class="btn" onclick="validateForm()">Проверить</button>
  <form action="/bases/{{.Base.ID}}/configurator/forms/delete" method="POST" style="display:inline" onsubmit="return confirm('Удалить эту форму вместе с модулем и ресурсами?')">
    <input type="hidden" name="entity" value="{{.EditingForm.Entity}}">
    <input type="hidden" name="name" value="{{.EditingForm.Name}}">
    <button class="btn btn-danger" type="submit">Удалить</button>
  </form>
  <span class="editor-meta">{{.EditingForm.Entity}}.{{.EditingForm.Name}}{{if .EditingForm.Kind}} · {{.EditingForm.Kind}}{{end}}</span>
</div>

<div class="editor-grid">
  <div class="editor-pane">
    <div class="editor-pane-hd">
      YAML
      <span style="color:#94a3b8;font-weight:400">{{.EditingForm.YAMLPath}}</span>
    </div>
    <div class="editor-tabs">
      <div class="editor-tab active" data-tab="yaml" onclick="switchTab('yaml')">YAML</div>
      <div class="editor-tab" data-tab="os" onclick="switchTab('os')">Модуль (.form.os)</div>
    </div>
    <div class="editor-pane-body">
      <div id="yaml-editor"></div>
      <div id="os-editor" style="display:none"></div>
    </div>
  </div>
  <div class="editor-pane">
    <div class="editor-pane-hd">
      Просмотр (упрощённый)
      <button class="btn" onclick="refreshPreview()" style="padding:3px 8px;font-size:11px">Обновить</button>
    </div>
    <iframe id="preview-frame" sandbox="allow-same-origin allow-scripts"></iframe>
  </div>
</div>

<div id="warn-panel" class="warn-panel">
  <div style="display:flex;justify-content:space-between;margin-bottom:6px">
    <b>Результат проверки</b>
    <a href="javascript:void(0)" onclick="document.getElementById('warn-panel').classList.remove('active')" style="color:#64748b;text-decoration:none">×</a>
  </div>
  <div id="warn-items"></div>
</div>

<script>
// Самохостинг Monaco: web-воркер из встроенного /vendor/monaco/ (тот же origin).
window.MonacoEnvironment = { getWorkerUrl: function () {
  return 'data:text/javascript;charset=utf-8,' + encodeURIComponent(
    "self.MonacoEnvironment={baseUrl:'" + location.origin + "/vendor/monaco/'};" +
    "importScripts('" + location.origin + "/vendor/monaco/vs/base/worker/workerMain.js');");
}};
var _initialYAML = {{jsString .EditingForm.YAML}};
var _initialOS   = {{jsString .EditingForm.OS}};

function buildFallback() {
  // Monaco не загрузился — деградируем в textarea, чтобы форма всё равно
  // редактировалась и сохранялась (в т.ч. полностью офлайн).
  function ta(host, val) {
    var t = document.createElement('textarea');
    t.value = val;
    t.style.cssText = 'width:100%;height:100%;border:0;outline:0;resize:none;font-family:Consolas,monospace;font-size:12px;padding:8px;box-sizing:border-box';
    var h = document.getElementById(host);
    h.innerHTML = ''; h.appendChild(t);
    return t;
  }
  window._yamlTA = ta('yaml-editor', _initialYAML);
  window._osTA   = ta('os-editor', _initialOS);
  refreshPreview();
}

if (typeof require === 'undefined') {
  buildFallback();
} else {
  require.config({ paths: { vs: '/vendor/monaco/vs' }});
  require(['vs/editor/editor.main'], function () {
    window.yamlEditor = monaco.editor.create(document.getElementById('yaml-editor'), {
      value: _initialYAML,
      language: 'yaml', theme: 'vs-light', automaticLayout: true, minimap: { enabled: false }, fontSize: 12
    });
    window.osEditor = monaco.editor.create(document.getElementById('os-editor'), {
      value: _initialOS,
      language: 'plaintext', theme: 'vs-light', automaticLayout: true, minimap: { enabled: false }, fontSize: 12
    });
    refreshPreview();
  });
}

// Единые геттеры — прозрачно работают и с Monaco, и с textarea-fallback.
function getYAML() { return window.yamlEditor ? window.yamlEditor.getValue() : (window._yamlTA ? window._yamlTA.value : ''); }
function getOS()   { return window.osEditor ? window.osEditor.getValue() : (window._osTA ? window._osTA.value : ''); }

function switchTab(name) {
  document.querySelectorAll('.editor-tab').forEach(function (el) { el.classList.toggle('active', el.dataset.tab === name); });
  document.getElementById('yaml-editor').style.display = name === 'yaml' ? '' : 'none';
  document.getElementById('os-editor').style.display = name === 'os' ? '' : 'none';
  if (window.yamlEditor) window.yamlEditor.layout();
  if (window.osEditor) window.osEditor.layout();
}

function saveForm() {
  document.getElementById('yaml-hidden').value = getYAML();
  document.getElementById('os-hidden').value = getOS();
  document.getElementById('save-form').submit();
}

function refreshPreview() {
  var body = new URLSearchParams();
  body.append('yaml', getYAML());
  body.append('entity', '{{.EditingForm.Entity}}');
  fetch('/bases/{{.Base.ID}}/configurator/forms/preview', { method: 'POST', body: body, headers: { 'Content-Type': 'application/x-www-form-urlencoded' }})
    .then(function (r) { return r.text(); })
    .then(function (html) {
      document.getElementById('preview-frame').srcdoc = html;
    });
}

function validateForm() {
  if (!window.yamlEditor) return;
  var body = new URLSearchParams();
  body.append('yaml', window.yamlEditor.getValue());
  body.append('entity', '{{.EditingForm.Entity}}');
  fetch('/bases/{{.Base.ID}}/configurator/forms/validate', { method: 'POST', body: body, headers: { 'Content-Type': 'application/x-www-form-urlencoded' }})
    .then(function (r) { return r.json(); })
    .then(function (resp) {
      var panel = document.getElementById('warn-panel');
      var items = document.getElementById('warn-items');
      items.innerHTML = '';
      panel.classList.add('active');
      if (resp.ok && (!resp.items || resp.items.length === 0)) {
        items.innerHTML = '<div class="warn-item info">✓ YAML валиден, замечаний нет.</div>';
        return;
      }
      (resp.items || []).forEach(function (it) {
        var div = document.createElement('div');
        div.className = 'warn-item ' + (it.severity || 'info');
        div.textContent = (it.code ? '[' + it.code + '] ' : '') + it.message;
        items.appendChild(div);
      });
    })
    .catch(function (e) {
      var panel = document.getElementById('warn-panel');
      panel.classList.add('active');
      document.getElementById('warn-items').innerHTML = '<div class="warn-item error">Ошибка проверки: ' + e + '</div>';
    });
}
</script>

</main>
</body>
</html>
{{end}}
`

// ── Preview-рендер для iframe ─────────────────────────────────────────────────

// previewErrorHTML рендерит ошибку парсинга/валидации YAML в маленький HTML
// для srcdoc iframe. Не зависит от template — простая обёртка.
func previewErrorHTML(msg string) string {
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0;padding:18px;font-family:sans-serif;background:#fef2f2;color:#991b1b}h3{margin:0 0 8px;font-size:14px}pre{background:#fff;padding:10px;border-radius:6px;border:1px solid #fee2e2;white-space:pre-wrap;font-size:12px}</style></head><body><h3>Ошибка YAML</h3><pre>%s</pre></body></html>`,
		html.EscapeString(msg))
}

// renderManagedFormPreview генерирует упрощённый HTML-предпросмотр
// дерева элементов формы. Не использует metadata.Entity — отрисовывает
// абстрактные input/checkbox/group на основе FormModule.Elements.
//
// Этого достаточно для UI-редактора чтобы оценить структуру формы;
// полноценный рендер с реальными данными доступен после сохранения
// через рантайм-handler /ui/.../form (этап 3).
func renderManagedFormPreview(fm *metadata.FormModule) string {
	var buf bytes.Buffer
	buf.WriteString(`<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;padding:18px;font-family:-apple-system,sans-serif;background:#fff;color:#334;font-size:13px}
h2{margin:0 0 14px;color:#1a4a80;font-size:16px;display:flex;align-items:center;gap:8px}
.tag{font-size:11px;background:#d1fae5;color:#059669;padding:2px 8px;border-radius:10px}
fieldset{border:1px solid #e2e8f0;border-radius:8px;padding:12px 14px;margin-bottom:12px}
legend{font-weight:600;color:#475569;padding:0 6px;font-size:12px}
.tabs{margin-bottom:10px}
.tabs-hd{display:flex;border-bottom:2px solid #e2e8f0;margin-bottom:10px;gap:2px;flex-wrap:wrap}
.tab{padding:6px 12px;font-size:12px;color:#64748b;border-bottom:2px solid transparent;margin-bottom:-2px;cursor:pointer;user-select:none;background:none;border-left:none;border-right:none;border-top:none;font-family:inherit}
.tab:hover{color:#1a4a80;background:#f5f8ff}
.tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600;background:#fff}
.tab-page{display:none}
.tab-page.active{display:block}
.fg{margin-bottom:10px}
.fg label{display:block;color:#475569;margin-bottom:4px;font-size:12px}
.fg input,.fg select{width:100%;padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;font-size:13px;background:#fff}
.req{color:#dc2626}
.hint{display:block;color:#94a3b8;font-size:11px;margin-top:3px}
.deco{padding:6px 0;color:#475569;font-size:13px}
.btn{padding:6px 12px;border:1px solid #d0d7e3;background:#f8fafc;border-radius:5px;cursor:pointer;margin-right:4px;font-size:12px}
.tp-stub{background:#fef9c3;padding:8px;border-radius:6px;font-size:11px;color:#92400e;margin:6px 0}
.unknown{background:#fef2f2;padding:8px;border-radius:6px;font-size:11px;color:#991b1b;margin:6px 0}
</style></head><body>`)

	title := "Карточка"
	if fm.Title != nil && fm.Title["ru"] != "" {
		title = fm.Title["ru"]
	} else if fm.EntityName != "" {
		title = fm.EntityName
	}
	fmt.Fprintf(&buf, `<h2>%s <span class="tag">◇ managed</span></h2>`, html.EscapeString(title))

	tabsCounter := 0
	for _, el := range fm.Elements {
		renderPreviewElement(&buf, el, &tabsCounter)
	}

	// Inline-JS для переключения вкладок. Работает в iframe sandbox
	// allow-scripts; вложенные tabset-ы изолированы по data-tabset-id.
	buf.WriteString(`<script>
(function(){
  function activate(setId, idx){
    var hdr = document.querySelector('[data-tabset-hdr="'+setId+'"]');
    var body = document.querySelector('[data-tabset-body="'+setId+'"]');
    if(!hdr||!body) return;
    hdr.querySelectorAll('.tab').forEach(function(b,i){ b.classList.toggle('active', i===idx); });
    body.querySelectorAll(':scope > .tab-page').forEach(function(p,i){ p.classList.toggle('active', i===idx); });
  }
  document.querySelectorAll('.tab[data-tabset]').forEach(function(btn){
    btn.addEventListener('click', function(){
      activate(btn.dataset.tabset, parseInt(btn.dataset.idx,10));
    });
  });
})();
</script>`)
	buf.WriteString(`</body></html>`)
	return buf.String()
}

func renderPreviewElement(buf *bytes.Buffer, el *metadata.FormElement, tabsCounter *int) {
	if el == nil {
		return
	}
	title := el.Name
	if el.TitleMap != nil && el.TitleMap["ru"] != "" {
		title = el.TitleMap["ru"]
	} else if el.Title != "" {
		title = el.Title
	}
	switch el.Kind {
	case metadata.FormElementGroupBox:
		fmt.Fprintf(buf, `<fieldset><legend>%s</legend>`, html.EscapeString(title))
		for _, c := range el.Children {
			renderPreviewElement(buf, c, tabsCounter)
		}
		buf.WriteString(`</fieldset>`)
	case metadata.FormElementPages:
		// Уникальный id текущего tabset, чтобы вложенные СтраницыФормы
		// не конфликтовали при переключении.
		setID := *tabsCounter
		*tabsCounter++
		// Заголовки вкладок.
		fmt.Fprintf(buf, `<div class="tabs"><div class="tabs-hd" data-tabset-hdr="%d">`, setID)
		pageIdx := 0
		for _, p := range el.Children {
			if p.Kind != metadata.FormElementPage {
				continue
			}
			cls := "tab"
			if pageIdx == 0 {
				cls += " active"
			}
			ptitle := p.Name
			if p.TitleMap != nil && p.TitleMap["ru"] != "" {
				ptitle = p.TitleMap["ru"]
			}
			fmt.Fprintf(buf, `<button type="button" class="%s" data-tabset="%d" data-idx="%d">%s</button>`,
				cls, setID, pageIdx, html.EscapeString(ptitle))
			pageIdx++
		}
		buf.WriteString(`</div>`)
		// Содержимое всех страниц; неактивные — display:none через CSS.
		fmt.Fprintf(buf, `<div data-tabset-body="%d">`, setID)
		pageIdx = 0
		for _, p := range el.Children {
			if p.Kind != metadata.FormElementPage {
				continue
			}
			cls := "tab-page"
			if pageIdx == 0 {
				cls += " active"
			}
			fmt.Fprintf(buf, `<div class="%s">`, cls)
			for _, c := range p.Children {
				renderPreviewElement(buf, c, tabsCounter)
			}
			buf.WriteString(`</div>`)
			pageIdx++
		}
		buf.WriteString(`</div></div>`)
	case metadata.FormElementField:
		req := ""
		if el.Required {
			req = ` <span class="req">*</span>`
		}
		field := lastSegment(el.DataPath)
		if field == "" {
			field = el.Name
		}
		fmt.Fprintf(buf, `<div class="fg"><label>%s%s</label><input type="text" placeholder="%s"`, html.EscapeString(title), req, html.EscapeString(field))
		if el.ReadOnly {
			buf.WriteString(` readonly`)
		}
		buf.WriteString(`></div>`)
		if el.Hint != "" {
			fmt.Fprintf(buf, `<div class="hint" style="margin-top:-8px">%s</div>`, html.EscapeString(el.Hint))
		}
	case metadata.FormElementCheckbox:
		field := lastSegment(el.DataPath)
		if field == "" {
			field = el.Name
		}
		fmt.Fprintf(buf, `<div class="fg" style="display:flex;align-items:center;gap:8px"><input type="checkbox" id="cb-%s"`, html.EscapeString(field))
		if el.ReadOnly {
			buf.WriteString(` disabled`)
		}
		fmt.Fprintf(buf, `><label for="cb-%s" style="margin-bottom:0">%s</label></div>`, html.EscapeString(field), html.EscapeString(title))
	case metadata.FormElementLabel:
		fmt.Fprintf(buf, `<div class="deco">%s</div>`, html.EscapeString(title))
	case metadata.FormElementButton:
		fmt.Fprintf(buf, `<button type="button" class="btn">%s</button>`, html.EscapeString(title))
	case metadata.FormElementPicture:
		fmt.Fprintf(buf, `<div class="hint">[Картинка: %s]</div>`, html.EscapeString(el.Name))
	case metadata.FormElementTable, metadata.FormElementTablePart:
		fmt.Fprintf(buf, `<div class="tp-stub">Табличная часть «%s»: предпросмотр упрощённый.</div>`, html.EscapeString(title))
	case metadata.FormElementCommandBar:
		// командная панель — обычно рендерится в toolbar над формой;
		// в preview просто рисуем кнопки в ряд.
		for _, c := range el.Children {
			renderPreviewElement(buf, c, tabsCounter)
		}
	default:
		fmt.Fprintf(buf, `<div class="unknown">Элемент «%s» типа «%s»: предпросмотр не реализован.</div>`,
			html.EscapeString(el.Name), html.EscapeString(string(el.Kind)))
	}
}

// lastSegment — последний компонент пути "Объект.Контрагент" → "Контрагент".
func lastSegment(p string) string {
	if i := lastIndexByte(p, '.'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
