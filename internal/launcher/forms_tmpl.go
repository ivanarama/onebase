package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"

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
		return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
	},
	// jsonObj — встраивание произвольного значения как JS-литерала (объект/массив)
	// через json.Marshal. nil/ошибка → "null". Помечается template.JS, чтобы
	// html/template не экранировал готовый JSON (для _tableParts в конструкторе).
	"jsonObj": func(v any) template.JS {
		b, err := json.Marshal(v)
		if err != nil {
			return template.JS("null")
		}
		return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
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
<html lang="{{.Lang}}">
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
  <a href="/bases/{{.Base.ID}}/configurator{{if .FormEditFrom}}?tab=tree&select={{.FormEditFrom}}{{else if .EditingForm}}?tab=tree&select=e-{{.EditingForm.Entity}}{{end}}">← В конфигуратор</a>
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
        <a class="btn" href="/bases/{{$.Base.ID}}/configurator/forms/edit?entity={{.Entity}}&name={{.Name}}&from=e-{{.Entity}}">Редактировать</a>
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
  <p style="color:#64748b;font-size:12px;margin-top:0">
    Вид формы определяет, где она подставится: <b>object</b> — карточка элемента/документа,
    <b>list</b> — список сущности (элементы формы задают состав и порядок колонок),
    <b>choice</b> — форма выбора. Имя формы на вид не влияет — его задаёт этот список.
  </p>
  <form action="/bases/{{.Base.ID}}/configurator/forms/edit" method="GET" style="display:flex;gap:8px;align-items:center;flex-wrap:wrap">
    <label>Сущность: <input type="text" name="entity" placeholder="Контрагент" required style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;font-size:13px"></label>
    <label>Имя формы: <input type="text" name="name" placeholder="ФормаОбъекта" required style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;font-size:13px"></label>
    <label>Вид формы: <select name="kind" style="padding:6px 10px;border:1px solid #d0d7e3;border-radius:5px;font-size:13px">
      <option value="object">карточка объекта (object)</option>
      <option value="list">список (list)</option>
      <option value="choice">выбор (choice)</option>
      <option value="folder">группа (folder)</option>
      <option value="custom">произвольная (custom)</option>
    </select></label>
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
.editor-grid{display:flex;flex-direction:column;height:calc(100vh - 230px);min-height:480px}
.editor-workspace{flex:1;min-height:0;display:flex;gap:10px}
.editor-pane{flex:1 1 auto;min-width:0;display:flex;flex-direction:column;background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);overflow:hidden}
.editor-grid.mode-yaml .editor-pane.right,.editor-grid.mode-os .editor-pane.right{display:none}
.editor-grid.layout-modern.mode-design .editor-pane.left,.editor-grid.layout-modern.mode-preview .editor-pane.left,.editor-grid.left-collapsed .editor-pane.left{display:none}
.editor-pane-hd{padding:8px 12px;background:#f8fafc;font-size:12px;font-weight:600;color:#475569;border-bottom:1px solid #eef0f5;display:flex;justify-content:space-between;align-items:center}
.pane-hd-title{display:flex;align-items:center;gap:8px;min-width:0}
.pane-hd-path{color:#94a3b8;font-weight:400;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.editor-pane-body{flex:1;overflow:hidden;display:flex;flex-direction:column}
#yaml-editor,#os-editor{flex:1;min-height:300px}
#preview-frame{flex:1;border:none;background:#fff}
.editor-tools{padding:8px 12px;background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);margin-bottom:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center}
.editor-meta{margin-left:auto;color:#64748b;font-size:12px}
.layout-toggle{display:flex;align-items:center;gap:3px;font-size:12px;color:#64748b}
.layout-toggle-label{margin-right:2px}
.layout-btn,.source-tab{border:1px solid #d0d7e3;background:#fff;color:#475569;border-radius:5px;padding:4px 8px;font-size:12px;font-family:inherit;cursor:pointer}
.layout-btn.active,.source-tab.active{background:#eef4ff;border-color:#9cbef0;color:#1a4a80;font-weight:600}
.source-tabs{display:none;align-items:center;gap:2px}
.editor-grid.layout-classic .source-tabs{display:flex}
.warn-panel{background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);padding:10px 14px;margin-top:10px;max-height:220px;overflow-y:auto;font-size:12px;display:none}
.warn-panel.active{display:block}
.warn-item{padding:4px 0;border-bottom:1px solid #eef0f5}
.warn-item.error{color:#dc2626}
.warn-item.warn{color:#d97706}
.warn-item.info{color:#64748b}
.editor-tabs{display:flex;background:#f8fafc;border:1px solid #eef0f5;border-bottom:0;border-radius:8px 8px 0 0;overflow:hidden}
.editor-tab{padding:8px 14px;cursor:pointer;font-size:12px;border:0;border-bottom:2px solid transparent;color:#64748b;background:transparent;font-family:inherit}
.editor-tab.active{color:#1a4a80;border-bottom-color:#1a4a80;background:#fff;font-weight:600}
/* Палитра реквизитов объекта — перетаскивание/клик вставляет поле (issue #134) */
.attr-palette,.struct-palette{background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.06);padding:8px 12px;margin-bottom:10px;display:flex;gap:6px;flex-wrap:wrap;align-items:center}
.attr-palette-label{font-size:12px;color:#64748b;margin-right:4px}
.attr-chip{display:inline-flex;align-items:center;background:#eef4ff;border:1px solid #c7d8f5;border-radius:14px;padding:3px 10px;font-size:12px;color:#1a4a80;cursor:grab;user-select:none}
.attr-chip:hover{background:#dce8ff;border-color:#9cbef0}
.attr-chip:active{cursor:grabbing}
.attr-chip.dragging{opacity:.4}
.struct-chip{background:#fef3e8;border-color:#f3d6b3;color:#9a5b1a}
.struct-chip:hover{background:#fde9d0;border-color:#e8c191}
.tablepart-chip{background:#fef9c3;border-color:#f3e0a0;color:#92400e}
.tablepart-chip:hover{background:#fdf3b8;border-color:#e9d27e}
#yaml-editor.attr-drop-target{outline:2px dashed #1a4a80;outline-offset:-2px}
/* Визуальный конструктор форм (#164): холст, drop-зоны, панель свойств */
.rp-tabs{display:flex;gap:2px}
.rp-tab{padding:4px 10px;font-size:12px;color:#64748b;cursor:pointer;border-radius:5px}
.rp-tab.active{color:#1a4a80;background:#eef4ff;font-weight:600}
#design-wrap{flex:1;display:flex;overflow:hidden;min-height:0;--forms-prop-w:320px}
.designer-split{flex:1;min-height:0;display:grid;grid-template-columns:minmax(320px,1fr) 6px var(--forms-prop-w,320px);overflow:hidden}
#canvas-host{min-width:0;overflow:auto;padding:12px;background:#fff}
.prop-splitter{background:#eef2f7;border-left:1px solid #e2e8f0;border-right:1px solid #e2e8f0;cursor:col-resize}
.prop-splitter:hover,.prop-splitter.dragging{background:#dbeafe;border-color:#bfdbfe}
.editor-grid.layout-classic .designer-split{grid-template-columns:1fr;grid-template-rows:minmax(240px,1fr) 6px var(--forms-prop-w,280px)}
.editor-grid.layout-classic .prop-splitter{cursor:row-resize;border-left:0;border-right:0;border-top:1px solid #e2e8f0;border-bottom:1px solid #e2e8f0}
.editor-grid.layout-classic .prop-panel{border-left:0;border-top:1px solid #eef0f5}
.fc-canvas{font-size:13px;color:#334}
.fc-children{display:flex;flex-direction:column;gap:1px;min-height:6px}
.fc-drop{height:6px;border-radius:4px;transition:background .1s,height .1s}
.fc-drop.fc-drop-over{background:#1a4a80;height:14px}
.fc-drop-page{font-size:11px;color:#b08442;border:1px dashed #d8c4a0;border-radius:5px;padding:2px 6px;margin:3px 0;text-align:center;background:#fffdf8;transition:background .1s}
.fc-drop-page.fc-drop-over{background:#fde9c8;color:#9a5b1a;border-color:#e0b87a}
.fc-el{border:1px solid transparent;border-radius:6px;padding:3px 5px;cursor:pointer}
.fc-el.fc-selected{outline:2px solid #1a4a80;background:#eef4ff}
.fc-dragging{opacity:.4}
[data-node-id]{cursor:grab}
.fc-pick:hover{background:#f5f8ff}
.fc-group{border:1px solid #e2e8f0;padding:5px 9px;margin:1px 0}
.fc-group>legend{font-weight:600;color:#475569;padding:0 5px;font-size:12px}
.fc-group-horizontal>.fc-children{flex-direction:row;flex-wrap:wrap;gap:8px;align-items:flex-start}
.fc-group-horizontal>.fc-children>.fc-drop{width:8px;height:auto;align-self:stretch;min-height:32px}
.fc-group-horizontal>.fc-children>.fc-drop.fc-drop-over{width:16px;height:auto}
.fc-group-horizontal>.fc-children>.fc-el{flex:1 1 220px;min-width:180px}
.fc-pages{border:1px dashed #c7d8f5;border-radius:6px;padding:4px}
.fc-page{border:1px solid #eef0f5;border-radius:6px;margin:3px 0;padding:4px 6px}
.fc-tab{font-size:11px;color:#1a4a80;font-weight:600;margin-bottom:3px}
.fc-field label{display:block;color:#475569;font-size:12px;margin-bottom:2px}
.fc-field input{width:100%;padding:5px 8px;border:1px solid #d0d7e3;border-radius:5px;background:#f8fafc;pointer-events:none}
.fc-req{color:#dc2626}
.fc-check{display:flex;align-items:center;gap:6px}
.fc-label{color:#475569}
.fc-btn button{padding:5px 10px;border:1px solid #d0d7e3;border-radius:5px;background:#f8fafc;pointer-events:none}
.fc-table .fc-tp{background:#fef9c3;color:#92400e;padding:6px 8px;border-radius:6px;font-size:12px}
.fc-pic{background:#eef2ff;color:#3730a3;border:1px dashed #c7d2fe;border-radius:6px;padding:10px;text-align:center;font-size:12px}
.fc-cols{display:flex;flex-wrap:wrap;gap:4px;padding:4px 2px 0}
.fc-col{font-size:11px;background:#f1f5f9;border:1px solid #e2e8f0;border-radius:4px;padding:2px 7px;color:#475569}
.fc-col.fc-selected{outline:2px solid #1a4a80;background:#eef4ff}
.fc-cols-empty{font-size:11px;color:#94a3b8;font-style:italic}
.fc-switch{display:flex;flex-wrap:wrap;gap:10px;padding:2px 0}
.fc-opt{font-size:12px;color:#475569;display:inline-flex;align-items:center;gap:4px;pointer-events:none}
.fc-unknown{background:#fef2f2;color:#991b1b;font-size:12px}
.fc-kind{color:#94a3b8;font-size:11px}
#canvas-host.fc-canvas-disabled{opacity:.5;pointer-events:none}
.fc-banner{background:#fee2e2;color:#dc2626;padding:6px 10px;border-radius:6px;font-size:12px;margin-bottom:8px;display:none}
.fc-banner.active{display:block}
.prop-panel{min-width:260px;border-left:1px solid #eef0f5;background:#fafbff;overflow:hidden;padding:0;font-size:12px;display:flex;flex-direction:column}
.prop-panel .prop-empty{color:#94a3b8}
.prop-tabs{display:flex;gap:2px;border-bottom:1px solid #e2e8f0;padding:8px 12px 0;background:#fafbff;flex:0 0 auto}
#prop-body{flex:1 1 auto;min-height:0;overflow:auto;padding:10px 12px}
.prop-tab{padding:4px 12px;font-size:12px;color:#64748b;cursor:pointer;border-bottom:2px solid transparent;user-select:none}
.prop-tab:hover{color:#1a4a80}
.prop-tab.active{color:#1a4a80;border-bottom-color:#1a4a80;font-weight:600}
.prop-panel h4{margin:0 0 8px;font-size:12px;color:#1a4a80}
.prop-panel h4 .prop-kind{color:#94a3b8;font-weight:400;margin-left:6px}
.prop-row{margin-bottom:8px}
.prop-row>label{display:block;color:#64748b;margin-bottom:2px}
.prop-row input[type=text],.prop-row input[type=number],.prop-row select{width:100%;padding:5px 8px;border:1px solid #d0d7e3;border-radius:5px;font-size:12px;background:#fff}
.prop-row.prop-check{display:flex;align-items:center;gap:6px}
.prop-row.prop-check>label{margin:0}
.prop-row.prop-section{font-weight:600;color:#1a4a80;border-top:1px solid #eef0f5;padding-top:8px;margin-top:10px}
.prop-hint{font-size:11px;color:#94a3b8;margin-bottom:6px}
.prop-row.prop-opt{display:flex;gap:4px;align-items:center}
.prop-row.prop-opt input{flex:1}
.prop-row.prop-opt .btn{padding:2px 8px}
.prop-actions{margin-top:12px;border-top:1px solid #eef0f5;padding-top:10px}
@media (max-width: 900px){
  .designer-split{grid-template-columns:1fr;grid-template-rows:minmax(260px,1fr) 6px var(--forms-prop-w,320px)}
  .prop-splitter{cursor:row-resize;border-left:0;border-right:0;border-top:1px solid #e2e8f0;border-bottom:1px solid #e2e8f0}
  .prop-panel{border-left:0;border-top:1px solid #eef0f5}
}
/* Тумблер темы кода: обе подписи в разметке, видимую выбирает класс на <html>
   (тот же приём, что в конфигураторе). Показываем тему, в которую переключит клик. */
#cfg-code-theme-toggle .cth-to-dark{display:none}
html.cfg-code-light #cfg-code-theme-toggle .cth-to-dark{display:inline}
html.cfg-code-light #cfg-code-theme-toggle .cth-to-light{display:none}
</style>
<script>
// Тема редактора кода общая с конфигуратором (один origin — один localStorage),
// но пока выбор не сделан явно, эта страница остаётся светлой, какой и была.
// Класс ставим до отрисовки, иначе редакторы мигнут чужой темой.
window.cfgCodeThemeDefault='light';
try{var _ct=localStorage.getItem('cfgCodeTheme');if(_ct?_ct==='light':window.cfgCodeThemeDefault==='light')document.documentElement.classList.add('cfg-code-light');}catch(e){}
</script>
<!-- Загрузчик Monaco: без него require не определён и редакторы молча
     деградировали в textarea (подсветки и сворачивания YAML не было никогда).
     Тот же вендоренный пакет, что в конфигураторе, — офлайн, тот же origin. -->
<script src="/vendor/monaco/vs/loader.js" onerror="window._monacoLoadErr='loader.js failed'"></script>
<script src="/static/code-theme.js"></script>
<body>
{{template "forms-header" .}}
<main>

{{if .Error}}<div class="flash-err">{{.Error}}</div>{{end}}
{{if .FieldsSaved}}<div class="flash-ok">✓ Сохранено: {{.FieldsSavedEntity}}</div>{{end}}

<form id="save-form" action="/bases/{{.Base.ID}}/configurator/forms/save" method="POST">
<input type="hidden" name="entity" value="{{.EditingForm.Entity}}">
<input type="hidden" name="name" value="{{.EditingForm.Name}}">
<input type="hidden" name="from" value="{{.FormEditFrom}}">
<input type="hidden" name="yaml" id="yaml-hidden">
<input type="hidden" name="os" id="os-hidden">
</form>

<div class="editor-tools">
  <button class="btn btn-primary" onclick="saveForm()">Сохранить</button>
  <button class="btn" onclick="validateForm()">Проверить</button>
  <form action="/bases/{{.Base.ID}}/configurator/forms/delete" method="POST" style="display:inline" onsubmit="return confirm('Удалить эту форму вместе с модулем и ресурсами?')">
    <input type="hidden" name="entity" value="{{.EditingForm.Entity}}">
    <input type="hidden" name="name" value="{{.EditingForm.Name}}">
    <input type="hidden" name="from" value="{{.FormEditFrom}}">
    <button class="btn btn-danger" type="submit">Удалить</button>
  </form>
  <div class="layout-toggle" id="editor-layout-toggle" aria-label="Вид редактора">
    <span class="layout-toggle-label">Вид:</span>
    <button type="button" class="layout-btn active" data-layout="modern" onclick="switchEditorLayout('modern')">Новый</button>
    <button type="button" class="layout-btn" data-layout="classic" onclick="switchEditorLayout('classic')">Классический</button>
  </div>
  <button type="button" class="btn" id="cfg-code-theme-toggle" onclick="cfgCodeThemeToggle()" title="Светлая или тёмная тема редактора кода"><span class="cth-to-light">&#9728;&#65039; Светлая тема</span><span class="cth-to-dark">&#127769; Тёмная тема</span></button>
  <span class="editor-meta">{{.EditingForm.Entity}}.{{.EditingForm.Name}}{{if .EditingForm.Kind}} · {{.EditingForm.Kind}}{{end}}</span>
</div>

{{if .EditingFormAttrs}}
<div class="attr-palette" id="attr-palette">
  <span class="attr-palette-label">Реквизиты объекта (клик или перетащите в YAML, чтобы добавить поле):</span>
  {{range .EditingFormAttrs}}
  <span class="attr-chip" draggable="true" data-attr="{{.Name}}" data-type="{{.Type}}" data-title="{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}" onclick="insertFieldFromChip(this)" title="Вставить поле для «{{.Name}}»">{{.Name}}</span>
  {{end}}
</div>
{{end}}

<div class="struct-palette" id="struct-palette">
  <span class="attr-palette-label">Структура (перетащите на холст):</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="ГруппаФормы" data-name="Группа" data-title="Группа" title="Группа полей">＋ Группа</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="СтраницыФормы" data-name="Страницы" title="Набор вкладок: бросьте на холст — появится набор с одной готовой вкладкой; ещё вкладки добавляйте кнопкой «+ страница» внутри">＋ Страницы (набор)</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="Страница" data-name="Страница" data-title="Страница" title="Вкладка: бросьте на «+ страница» внутри набора — добавится вкладка; на обычное место холста — обернётся в новый набор вкладок">＋ Страница (вкладка)</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="Надпись" data-name="Надпись" data-title="Надпись" title="Текстовая надпись">＋ Надпись</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="Кнопка" data-name="Кнопка" data-title="Кнопка" title="Кнопка (обработчик нажатия — отдельным шагом)">＋ Кнопка</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="ПолеКартинки" data-name="Картинка" data-title="Картинка" title="Поле картинки (путь укажите в свойствах)">＋ Картинка</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="Переключатель" data-name="Переключатель" data-title="Переключатель" title="Переключатель значений (radio/список): задайте поле и значения в свойствах">＋ Переключатель</span>
  <span class="attr-chip struct-chip" draggable="true" data-kind="КоманднаяПанель" data-name="КоманднаяПанель" title="Командная панель (контейнер для кнопок)">＋ Команд. панель</span>
</div>

{{if .EditingFormTableParts}}
<div class="struct-palette" id="tablepart-palette">
  <span class="attr-palette-label">Табличные части (перетащите на холст):</span>
  {{range .EditingFormTableParts}}
  <span class="attr-chip tablepart-chip" draggable="true" data-tp="{{.Name}}" data-title="{{if .Title}}{{.Title}}{{else}}{{.Name}}{{end}}" title="Добавить табличную часть «{{.Name}}»">▦ {{.Name}}</span>
  {{end}}
</div>
{{end}}

<div class="editor-grid layout-modern mode-design" id="editor-grid">
  <div class="editor-tabs" role="tablist" aria-label="Режим редактора формы">
    <button type="button" class="editor-tab" data-mode="yaml" data-tab="yaml" onclick="switchEditorMode('yaml')">YAML</button>
    <button type="button" class="editor-tab" data-mode="os" data-tab="os" onclick="switchEditorMode('os')">Модуль</button>
    <button type="button" class="editor-tab active" data-mode="design" data-rp="design" onclick="switchEditorMode('design')">Конструктор</button>
    <button type="button" class="editor-tab" data-mode="preview" data-rp="preview" onclick="switchEditorMode('preview')">Просмотр</button>
  </div>
  <div class="editor-workspace">
    <div class="editor-pane left">
      <div class="editor-pane-hd">
        <div class="pane-hd-title">
          <span>Исходники формы</span>
          <div class="source-tabs" aria-label="Исходник">
            <button type="button" class="source-tab active" data-source="yaml" onclick="switchSourceMode('yaml')">YAML</button>
            <button type="button" class="source-tab" data-source="os" onclick="switchSourceMode('os')">Модуль</button>
          </div>
        </div>
        <span class="pane-hd-path">{{.EditingForm.YAMLPath}}</span>
      </div>
      <div class="editor-pane-body">
        <div id="yaml-editor"></div>
        <div id="os-editor" style="display:none"></div>
      </div>
    </div>
    <div class="editor-pane right">
      <div class="editor-pane-hd">
        <span>Конструктор формы</span>
        <button class="btn" onclick="reloadCanvas()" style="padding:3px 8px;font-size:11px" title="Пере-синхронизировать холст с YAML">Обновить</button>
      </div>
      <div class="editor-pane-body">
        <div id="design-wrap">
          <div class="designer-split" id="designer-split">
            <div id="canvas-host">
              <div class="fc-banner" id="fc-banner"></div>
              <div class="empty" style="padding:18px">Загрузка холста…</div>
            </div>
            <div class="prop-splitter" id="prop-splitter" title="Изменить ширину панели свойств"></div>
            <div class="prop-panel" id="prop-panel">
              <div class="prop-tabs">
                <span class="prop-tab active" data-pt="element" onclick="switchPropTab('element')">Элемент</span>
                <span class="prop-tab" data-pt="form" onclick="switchPropTab('form')" title="Свойства формы: заголовок, вид, события, действия">Форма</span>
              </div>
              <div id="prop-body">
                <div class="prop-empty">Выберите элемент на холсте, чтобы изменить его свойства. Перетащите реквизит из палитры на холст, чтобы добавить поле.</div>
              </div>
            </div>
          </div>
        </div>
        <iframe id="preview-frame" sandbox="allow-same-origin allow-scripts" style="display:none;flex:1;border:none"></iframe>
      </div>
    </div>
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
    cfgCodeThemeDefine(monaco); // темы общие с конфигуратором
    window.yamlEditor = monaco.editor.create(document.getElementById('yaml-editor'), {
      value: _initialYAML,
      language: 'yaml', theme: cfgCodeThemeName(), automaticLayout: true, minimap: { enabled: false }, fontSize: 12
    });
    window.osEditor = monaco.editor.create(document.getElementById('os-editor'), {
      value: _initialOS,
      language: 'plaintext', theme: cfgCodeThemeName(), automaticLayout: true, minimap: { enabled: false }, fontSize: 12
    });
    refreshPreview();
    hookYamlChange();
    reloadCanvas();
  });
}

// Единые геттеры/сеттеры — прозрачно работают и с Monaco, и с textarea-fallback.
function getYAML() { return window.yamlEditor ? window.yamlEditor.getValue() : (window._yamlTA ? window._yamlTA.value : ''); }
function getOS()   { return window.osEditor ? window.osEditor.getValue() : (window._osTA ? window._osTA.value : ''); }
function setYAML(v) { if (window.yamlEditor) window.yamlEditor.setValue(v); else if (window._yamlTA) window._yamlTA.value = v; }
function setOS(v)  { if (window.osEditor) window.osEditor.setValue(v); else if (window._osTA) window._osTA.value = v; }

// osProcedures — имена процедур из модуля .form.os (для привязки событий, B1).
function osProcedures() {
  var src = getOS() || '', re = /Процедура\s+([A-Za-zА-Яа-яЁё0-9_]+)\s*\(/g, m, out = [];
  while ((m = re.exec(src)) !== null) out.push(m[1]);
  return out;
}
// ensureProcedure — дописывает пустую процедуру в .form.os, если её ещё нет
// (кнопка «Создать обработчик…», B1). Сохранится вместе с формой.
function ensureProcedure(name, eventName, info) {
  if (osProcedures().indexOf(name) >= 0) return;
  var src = getOS();
  if (src && !/\n$/.test(src)) src += '\n';
  var body = '\t\n';
  // Подсказка о контексте зависит от события, а не только от вида элемента:
  // события правки ячейки и активизации строки везут номер строки и колонку,
  // а добавление/удаление строки шлёт лишь имя ТЧ (obFireRowEvent).
  var kind = info && info.kind;
  if (kind === 'ТабличнаяЧасть' || kind === 'Колонка') {
    if (eventName === 'ПриИзменении' || eventName === 'ПриИзмененииСтроки' || eventName === 'ПриАктивизацииСтроки') {
      body = '\t// Доступно: ИмяТабличнойЧасти, НомерСтроки, ТекущаяКолонка, ТекущаяСтрока\n\t\n';
    } else {
      body = '\t// Доступно: ИмяТабличнойЧасти\n\t\n';
    }
  }
  setOS(src + '\nПроцедура ' + name + '()\n' + body + 'КонецПроцедуры\n');
}

// ── Палитра реквизитов: вставка поля ПолеВвода по клику/дропу (issue #134) ──
function _attrFieldSnippet(attr, title, base) {
  var t = String(title || attr).replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  var b = base || '      ';
  return b + '- kind: ПолеВвода\n' +
         b + '  name: Поле' + attr + '\n' +
         b + '  title:\n' +
         b + '    ru: "' + t + '"\n' +
         b + '  data_path: Объект.' + attr;
}
// Куда и с каким отступом вставлять новый элемент списка формы (issue #134).
// Раньше отступ копировался со строки под курсором, а вставка шла сразу после
// неё — поэтому дроп не на строку '- ' давал кривой отступ, а дроп в середину
// элемента разрывал его → невалидный YAML («mapping values are not allowed»).
// Теперь: отступ = как у ближайшего элемента списка ('- ') на/выше курсора, а
// вставка — ПОСЛЕ конца этого элемента (перед следующим '- ' или дедентом).
function _yamlInsertPoint() {
  var fb = { indent: '      ', afterLine: null };
  if (!window.yamlEditor) return fb;
  var model = window.yamlEditor.getModel();
  var pos = window.yamlEditor.getPosition();
  var total = model.getLineCount();
  var startLine = 0, indent = null;
  for (var ln = pos.lineNumber; ln >= 1; ln--) {
    var t = model.getLineContent(ln);
    var mi = t.match(/^(\s*)-\s/);
    if (mi) { startLine = ln; indent = mi[1]; break; }
    var mh = t.match(/^(\s*)(elements|children|groups|fields)\s*:\s*$/);
    if (mh) { return { indent: mh[1] + '  ', afterLine: ln }; }
  }
  if (startLine === 0) {
    for (var dn = pos.lineNumber; dn <= total; dn++) {
      var td = model.getLineContent(dn);
      var mhd = td.match(/^(\s*)(elements|children|groups|fields)\s*:\s*$/);
      if (mhd) { return { indent: mhd[1] + '  ', afterLine: dn }; }
    }
    return fb;
  }
  var endLine = total;
  for (var k = startLine + 1; k <= total; k++) {
    var s = model.getLineContent(k);
    if (!s.trim()) continue;
    var lead = (s.match(/^\s*/) || [''])[0].length;
    if (lead <= indent.length) { endLine = k - 1; break; }
  }
  return { indent: indent, afterLine: endLine };
}
function insertFieldText(attr, title) {
  var ip = _yamlInsertPoint();
  var snippet = _attrFieldSnippet(attr, title, ip.indent);
  if (window.yamlEditor) {
    var ed = window.yamlEditor, model = ed.getModel();
    var line = ip.afterLine != null ? ip.afterLine : ed.getPosition().lineNumber;
    var col = model.getLineMaxColumn(line);
    ed.executeEdits('insert-field', [{
      range: new monaco.Range(line, col, line, col),
      text: '\n' + snippet, forceMoveMarkers: true
    }]);
    ed.setPosition({ lineNumber: line + 1, column: model.getLineMaxColumn(line + 1) });
    ed.focus();
  } else if (window._yamlTA) {
    var ta = window._yamlTA, p = ta.selectionStart != null ? ta.selectionStart : ta.value.length;
    ta.value = ta.value.slice(0, p) + '\n' + snippet + ta.value.slice(p);
  }
  if (typeof refreshPreview === 'function') refreshPreview();
}
function insertFieldFromChip(chip) {
  insertFieldText(chip.getAttribute('data-attr'), chip.getAttribute('data-title'));
}
(function () {
  var pal = document.getElementById('attr-palette');
  if (!pal) return;
  pal.addEventListener('dragstart', function (e) {
    var chip = e.target.closest ? e.target.closest('.attr-chip') : null;
    if (!chip) return;
    chip.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'copy';
    e.dataTransfer.setData('text/onebase-attr',
      JSON.stringify({ attr: chip.getAttribute('data-attr'), title: chip.getAttribute('data-title'), type: chip.getAttribute('data-type') || '' }));
  });
  pal.addEventListener('dragend', function (e) {
    var chip = e.target.closest ? e.target.closest('.attr-chip') : null;
    if (chip) chip.classList.remove('dragging');
  });
  var host = document.getElementById('yaml-editor');
  if (!host) return;
  host.addEventListener('dragover', function (e) {
    if ((e.dataTransfer.types || []).indexOf('text/onebase-attr') < 0) return;
    e.preventDefault(); e.dataTransfer.dropEffect = 'copy';
    host.classList.add('attr-drop-target');
  });
  host.addEventListener('dragleave', function () { host.classList.remove('attr-drop-target'); });
  host.addEventListener('drop', function (e) {
    var raw = e.dataTransfer.getData('text/onebase-attr');
    host.classList.remove('attr-drop-target');
    if (!raw) return;
    e.preventDefault();
    var d; try { d = JSON.parse(raw); } catch (_) { return; }
    if (window.yamlEditor && window.yamlEditor.getTargetAtClientPoint) {
      var tgt = window.yamlEditor.getTargetAtClientPoint(e.clientX, e.clientY);
      if (tgt && tgt.position) window.yamlEditor.setPosition(tgt.position);
    }
    insertFieldText(d.attr, d.title);
  });
})();

// Палитра структурных элементов (#164, слайс C): тащит kind на холст СВОИМ mime
// text/onebase-struct, чтобы не пересекаться с реквизитами (text/onebase-attr).
(function () {
  var pal = document.getElementById('struct-palette');
  if (!pal) return;
  pal.addEventListener('dragstart', function (e) {
    var chip = e.target.closest ? e.target.closest('.struct-chip') : null;
    if (!chip) return;
    chip.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'copy';
    e.dataTransfer.setData('text/onebase-struct', JSON.stringify({
      kind: chip.getAttribute('data-kind'),
      name: chip.getAttribute('data-name') || '',
      title: chip.getAttribute('data-title') || ''
    }));
  });
  pal.addEventListener('dragend', function (e) {
    var chip = e.target.closest ? e.target.closest('.struct-chip') : null;
    if (chip) chip.classList.remove('dragging');
  });
})();

// Палитра табличных частей (#164, слайс D1): свой mime text/onebase-tablepart,
// drop вставляет kind:ТабличнаяЧасть с name=Таб<ТЧ> и data_path=Объект.<ТЧ>.
(function () {
  var pal = document.getElementById('tablepart-palette');
  if (!pal) return;
  pal.addEventListener('dragstart', function (e) {
    var chip = e.target.closest ? e.target.closest('.tablepart-chip') : null;
    if (!chip) return;
    chip.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'copy';
    e.dataTransfer.setData('text/onebase-tablepart', JSON.stringify({
      tp: chip.getAttribute('data-tp'),
      title: chip.getAttribute('data-title') || ''
    }));
  });
  pal.addEventListener('dragend', function (e) {
    var chip = e.target.closest ? e.target.closest('.tablepart-chip') : null;
    if (chip) chip.classList.remove('dragging');
  });
})();

function normalizeEditorLayout(layout) { return layout === 'classic' ? 'classic' : 'modern'; }
function isClassicLayout() { return _editorLayout === 'classic'; }
function propPanelSizeKey() { return isClassicLayout() ? 'onebase.forms.propPanelClassicSize' : 'onebase.forms.propPanelSize'; }
function applyPropPanelSize() {
  var root = document.getElementById('design-wrap');
  if (!root) return;
  var fallback = isClassicLayout() ? '280px' : '320px';
  try {
    root.style.setProperty('--forms-prop-w', localStorage.getItem(propPanelSizeKey()) || fallback);
  } catch (_) {
    root.style.setProperty('--forms-prop-w', fallback);
  }
}
function updateSourceEditors() {
  var src = (_editorMode === 'yaml' || _editorMode === 'os') ? _editorMode : _sourceMode;
  _sourceMode = src === 'os' ? 'os' : 'yaml';
  document.getElementById('yaml-editor').style.display = _sourceMode === 'yaml' ? '' : 'none';
  document.getElementById('os-editor').style.display = _sourceMode === 'os' ? '' : 'none';
  document.querySelectorAll('.source-tab').forEach(function (el) {
    el.classList.toggle('active', el.dataset.source === _sourceMode);
  });
  if (window.yamlEditor) window.yamlEditor.layout();
  if (window.osEditor) window.osEditor.layout();
}
function applyEditorLayout() {
  var grid = document.getElementById('editor-grid') || document.querySelector('.editor-grid');
  if (grid) {
    grid.classList.toggle('layout-modern', !isClassicLayout());
    grid.classList.toggle('layout-classic', isClassicLayout());
  }
  document.querySelectorAll('.layout-btn').forEach(function (el) {
    el.classList.toggle('active', el.dataset.layout === _editorLayout);
  });
  applyPropPanelSize();
  updateSourceEditors();
}
function switchEditorLayout(layout) {
  _editorLayout = normalizeEditorLayout(layout);
  try { localStorage.setItem('onebase.forms.editorLayout', _editorLayout); } catch (_) {}
  applyEditorLayout();
}
function switchSourceMode(mode) {
  _sourceMode = mode === 'os' ? 'os' : 'yaml';
  updateSourceEditors();
}
function switchEditorMode(mode) {
  _editorMode = mode || 'design';
  if (_editorMode === 'yaml' || _editorMode === 'os') _sourceMode = _editorMode;
  var grid = document.getElementById('editor-grid') || document.querySelector('.editor-grid');
  if (grid) {
    grid.classList.remove('mode-yaml', 'mode-os', 'mode-design', 'mode-preview', 'left-collapsed');
    grid.classList.add('mode-' + _editorMode);
  }
  document.querySelectorAll('.editor-tab').forEach(function (el) {
    el.classList.toggle('active', el.dataset.mode === _editorMode);
  });
  updateSourceEditors();
  _rightPane = _editorMode === 'preview' ? 'preview' : 'design';
  document.getElementById('design-wrap').style.display = _editorMode === 'design' ? 'flex' : 'none';
  document.getElementById('preview-frame').style.display = _editorMode === 'preview' ? 'block' : 'none';
  if (_editorMode === 'design') reloadCanvas();
  if (_editorMode === 'preview') refreshPreview();
}
function switchTab(name) { switchEditorMode(name); }

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

// ── Визуальный конструктор формы (#164) ──────────────────────────────────────
// Холст серверо-центричен: правка превращается в команду на /forms/edit-op,
// сервер хирургически правит дерево yaml.Node и возвращает {yaml, canvasHtml,
// selectedId, model}. Monaco и холст синхронизируются от одного ответа.
var _editOpURL = '/bases/{{.Base.ID}}/configurator/forms/edit-op';
var _entity = {{jsString .EditingForm.Entity}};
// Состав табличных частей объекта (имя ТЧ → колонки) для редактора колонок (D2).
var _tablePartsList = {{jsonObj .EditingFormTableParts}};
var _tableParts = {};
(_tablePartsList || []).forEach(function (tp) { _tableParts[tp.name] = tp.columns || []; });
var _selected = '';   // текущая цель правки: node-id элемента ИЛИ "form"
var _lastEl = '';     // последний выбранный элемент (для закладки «Элемент»)
var _model = {};      // node-id → свойства (для панели свойств)
var _form = {};       // корневые свойства формы (titleRu/kind/events/actions)
var _editorLayout = 'modern';
var _editorMode = 'design';
var _sourceMode = 'yaml';
var _rightPane = 'design';
var _syncing = false; // защита от рекурсии setYAML → reloadCanvas
try { _editorLayout = normalizeEditorLayout(localStorage.getItem('onebase.forms.editorLayout')); } catch (_) {}
applyEditorLayout();

function switchRightPane(which) {
  switchEditorMode(which === 'preview' ? 'preview' : 'design');
}

function initPropSplitter() {
  var root = document.getElementById('design-wrap');
  var split = document.getElementById('prop-splitter');
  if (!root || !split) return;
  applyPropPanelSize();
  var dragging = false;
  function clamp(v, min, max) { return Math.max(min, Math.min(max, v)); }
  split.addEventListener('mousedown', function (e) {
    dragging = true;
    split.classList.add('dragging');
    document.body.style.userSelect = 'none';
    e.preventDefault();
  });
  document.addEventListener('mousemove', function (e) {
    if (!dragging) return;
    var rect = root.getBoundingClientRect();
    var vertical = isClassicLayout() || (window.matchMedia && window.matchMedia('(max-width: 900px)').matches);
    var size = vertical
      ? clamp(rect.bottom - e.clientY, 220, Math.max(260, rect.height - 180))
      : clamp(rect.right - e.clientX, 260, Math.max(320, rect.width * 0.6));
    var value = Math.round(size) + 'px';
    root.style.setProperty('--forms-prop-w', value);
    try { localStorage.setItem(propPanelSizeKey(), value); } catch (_) {}
  });
  document.addEventListener('mouseup', function () {
    if (!dragging) return;
    dragging = false;
    split.classList.remove('dragging');
    document.body.style.userSelect = '';
  });
}
initPropSplitter();

// editOp — единая точка общения с сервером. mutating=true → результат пишем
// обратно в YAML (направление холст→YAML).
function editOp(params, mutating) {
  var body = new URLSearchParams();
  body.append('yaml', getYAML());
  body.append('entity', _entity);
  Object.keys(params).forEach(function (k) { if (params[k] != null) body.append(k, params[k]); });
  return fetch(_editOpURL, { method: 'POST', body: body, headers: { 'Content-Type': 'application/x-www-form-urlencoded' }})
    .then(function (r) { return r.json(); })
    .then(function (resp) {
      var banner = document.getElementById('fc-banner');
      var host = document.getElementById('canvas-host');
      if (!resp.ok) {
        banner.textContent = 'YAML не разобран — визуальные правки заблокированы: ' + (resp.errors || []).join('; ');
        banner.classList.add('active');
        host.classList.add('fc-canvas-disabled');
        return resp;
      }
      banner.classList.remove('active');
      host.classList.remove('fc-canvas-disabled');
      _model = resp.model || {};
      _form = resp.form || {};
      _selected = resp.selectedId || '';
      if (_selected && _selected !== 'form') _lastEl = _selected; // запоминаем элемент для закладки «Элемент»
      renderCanvasHTML(resp.canvasHtml || '');
      if (mutating && typeof resp.yaml === 'string') {
        _syncing = true; setYAML(resp.yaml); _syncing = false;
      }
      renderProps();
      return resp;
    })
    .catch(function (e) {
      var banner = document.getElementById('fc-banner');
      banner.textContent = 'Ошибка конструктора: ' + e;
      banner.classList.add('active');
    });
}

// reloadCanvas — перерисовать холст из текущего YAML (направление YAML→холст).
function reloadCanvas() {
  if (_editorMode !== 'design') return Promise.resolve();
  return editOp({ op: 'render', node: _selected }, false);
}

// syncFromYAML — живая синхронизация после правки YAML: на закладке «Конструктор»
// перерисовываем холст, на «Просмотр» — обновляем предпросмотр (раньше для этого
// была отдельная кнопка «Просмотр» в шапке).
function syncFromYAML() {
  if (_editorMode === 'preview') refreshPreview();
  else if (_editorMode === 'design') reloadCanvas();
}

// insertPagesSet — вставляет набор СтраницыФормы с одной готовой вкладкой, чтобы
// добавленная страница сразу была закладкой (а не висячей страницей). Двумя
// шагами: сначала набор-контейнер, затем страница в него (id набора = selectedId).
function insertPagesSet(parent, index) {
  return editOp({ op: 'insert', parent: parent, index: index, kind: 'СтраницыФормы', name: 'Страницы', title_ru: '' }, true)
    .then(function (r) {
      if (r && r.ok && r.selectedId) {
        return editOp({ op: 'insert', parent: r.selectedId, index: 0, kind: 'Страница', name: 'Страница', title_ru: 'Страница' }, true);
      }
      return r;
    });
}

function renderCanvasHTML(html) {
  var host = document.getElementById('canvas-host');
  var banner = document.getElementById('fc-banner');
  host.innerHTML = '';
  host.appendChild(banner);
  var wrap = document.createElement('div');
  wrap.innerHTML = html;
  while (wrap.firstChild) host.appendChild(wrap.firstChild);
  // Элементы холста перетаскиваемы — для переноса в другой контейнер (op:move).
  host.querySelectorAll('[data-node-id]').forEach(function (el) { el.draggable = true; });
}

// Делегирование на холсте: клик — выбор элемента; drop реквизита на зону — вставка.
(function () {
  var host = document.getElementById('canvas-host');
  if (!host) return;
  host.addEventListener('click', function (e) {
    // Клик по «+ страница» добавляет вкладку в набор (так нагляднее, чем drag).
    var pz = e.target.closest ? e.target.closest('.fc-drop-page') : null;
    if (pz && host.contains(pz)) {
      e.stopPropagation();
      editOp({ op: 'insert', parent: pz.getAttribute('data-parent'), index: pz.getAttribute('data-index'),
        kind: 'Страница', name: 'Страница', title_ru: 'Страница' }, true);
      return;
    }
    var el = e.target.closest ? e.target.closest('[data-node-id]') : null;
    if (!el || !host.contains(el)) {
      // Клик по пустому месту холста (не по элементу и не по drop-зоне) —
      // открыть свойства формы (B2). Drop-зоны игнорируем, чтобы не сбивать выбор.
      var dz = e.target.closest ? e.target.closest('.fc-drop, .fc-drop-page') : null;
      if (!dz) selectNode('form');
      return;
    }
    e.stopPropagation();
    selectNode(el.getAttribute('data-node-id'));
  });
  // Перетаскивание элемента холста — для переноса в другой контейнер. Свой mime
  // text/onebase-node, чтобы не путать с палитрами (attr/struct/tablepart).
  host.addEventListener('dragstart', function (e) {
    var el = e.target.closest ? e.target.closest('[data-node-id]') : null;
    if (!el || !host.contains(el)) return;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/onebase-node', el.getAttribute('data-node-id'));
    el.classList.add('fc-dragging');
  });
  host.addEventListener('dragend', function (e) {
    var el = e.target.closest ? e.target.closest('[data-node-id]') : null;
    if (el) el.classList.remove('fc-dragging');
  });
  host.addEventListener('dragover', function (e) {
    var dz = e.target.closest ? e.target.closest('.fc-drop, .fc-drop-page') : null;
    if (!dz) return;
    var types = e.dataTransfer.types || [];
    var hasStruct = types.indexOf('text/onebase-struct') >= 0;
    var hasAttr = types.indexOf('text/onebase-attr') >= 0;
    var hasTP = types.indexOf('text/onebase-tablepart') >= 0;
    var hasNode = types.indexOf('text/onebase-node') >= 0;
    if (!hasStruct && !hasAttr && !hasTP && !hasNode) return;
    if (dz.classList.contains('fc-drop-page') && !hasStruct) return; // page-зоны — только новая страница
    e.preventDefault(); e.dataTransfer.dropEffect = hasNode ? 'move' : 'copy';
    dz.classList.add('fc-drop-over');
  });
  host.addEventListener('dragleave', function (e) {
    var dz = e.target.closest ? e.target.closest('.fc-drop, .fc-drop-page') : null;
    if (dz) dz.classList.remove('fc-drop-over');
  });
  host.addEventListener('drop', function (e) {
    var dz = e.target.closest ? e.target.closest('.fc-drop, .fc-drop-page') : null;
    if (!dz) return;
    dz.classList.remove('fc-drop-over');
    var parent = dz.getAttribute('data-parent'), index = dz.getAttribute('data-index');
    // Перенос существующего узла холста (op:move). Только обычные зоны (.fc-drop):
    // страницы переставляются ↑/↓. Запрет переноса в себя/в собственного потомка.
    var node = e.dataTransfer.getData('text/onebase-node');
    if (node) {
      e.preventDefault();
      if (dz.classList.contains('fc-drop-page')) return;
      if (parent === node || parent.indexOf(node + '.') === 0) return;
      editOp({ op: 'move', node: node, parent: parent, index: index }, true);
      return;
    }
    // Структурный элемент (группа/страницы/страница/надпись) — свой mime.
    var sraw = e.dataTransfer.getData('text/onebase-struct');
    if (sraw) {
      e.preventDefault();
      var s; try { s = JSON.parse(sraw); } catch (_) { return; }
      if (dz.classList.contains('fc-drop-page')) {
        // Зона «+ страница» внутри набора — кладём только вкладку.
        if (s.kind !== 'Страница') return;
        editOp({ op: 'insert', parent: parent, index: index, kind: 'Страница', name: s.name || 'Страница', title_ru: s.title || 'Страница' }, true);
        return;
      }
      // Обычная зона: и «Страницы (набор)», и одиночная «Страница» дают готовый
      // набор с одной вкладкой — чтобы это всегда была закладка, а не висячая
      // страница (issue #164, обратная связь по живому тесту).
      if (s.kind === 'СтраницыФормы' || s.kind === 'Страница') { insertPagesSet(parent, index); return; }
      editOp({ op: 'insert', parent: parent, index: index, kind: s.kind, name: s.name || '', title_ru: s.title || '' }, true);
      return;
    }
    if (dz.classList.contains('fc-drop-page')) return; // в Pages напрямую кладём только страницы
    // Табличная часть — свой mime.
    var traw = e.dataTransfer.getData('text/onebase-tablepart');
    if (traw) {
      e.preventDefault();
      var tp; try { tp = JSON.parse(traw); } catch (_) { return; }
      editOp({ op: 'insert', parent: parent, index: index, kind: 'ТабличнаяЧасть',
        name: 'Таб' + tp.tp, data_path: 'Объект.' + tp.tp, title_ru: tp.title || tp.tp }, true);
      return;
    }
    var raw = e.dataTransfer.getData('text/onebase-attr');
    if (!raw) return;
    e.preventDefault();
    var d; try { d = JSON.parse(raw); } catch (_) { return; }
    editOp({
      op: 'insert', parent: parent, index: index,
      kind: fieldKind(d.type), name: 'Поле' + d.attr, data_path: 'Объект.' + d.attr, title_ru: d.title || d.attr
    }, true);
  });
  // «Умный» выбор элемента по типу реквизита: bool → Флажок, date → ПолеДаты,
  // остальное (в т.ч. enum/ссылка — они сами рисуются выпадающим списком) → ПолеВвода.
  function fieldKind(type) {
    var t = (type || '').toLowerCase();
    if (t === 'bool') return 'Флажок';
    if (t === 'date') return 'ПолеДаты';
    return 'ПолеВвода';
  }
})();

function selectNode(nodeId) {
  _selected = nodeId;
  if (nodeId && nodeId !== 'form') _lastEl = nodeId; // запоминаем для закладки «Элемент»
  document.querySelectorAll('#canvas-host .fc-selected').forEach(function (el) { el.classList.remove('fc-selected'); });
  var el = document.querySelector('#canvas-host [data-node-id="' + nodeId + '"]');
  if (el) el.classList.add('fc-selected');
  renderProps();
}

// switchPropTab — закладки панели свойств «Элемент | Форма» (вместо отдельной
// кнопки «Свойства формы»). «Форма» выбирает корневые свойства; «Элемент» —
// возвращает к последнему выбранному элементу.
function switchPropTab(which) {
  if (which === 'form') { if (_selected !== 'form') selectNode('form'); else renderProps(); }
  else { selectNode(_lastEl || ''); }
}
// toggleLeftPane — свернуть/развернуть левый блок (YAML + модуль), отдав место холсту.
function toggleLeftPane() {
  var grid = document.querySelector('.editor-grid');
  var collapsed = grid.classList.toggle('left-collapsed');
  var btn = document.getElementById('toggle-left-btn');
  if (btn) btn.textContent = collapsed ? '⮞ Показать код' : '⮜ Свернуть код';
  if (window.yamlEditor) window.yamlEditor.layout();
  if (window.osEditor) window.osEditor.layout();
}

// renderProps строит панель свойств в #prop-body из _model (или свойства формы).
// Закладки «Элемент | Форма» статичны; активная выводится из _selected.
function renderProps() {
  var tab = (_selected === 'form') ? 'form' : 'element';
  document.querySelectorAll('#prop-panel .prop-tab').forEach(function (t) { t.classList.toggle('active', t.dataset.pt === tab); });
  var panel = document.getElementById('prop-body');
  panel.innerHTML = '';
  if (_selected === 'form') { renderFormProps(panel); return; }
  var info = _model[_selected];
  if (!info) {
    var em = document.createElement('div'); em.className = 'prop-empty';
    em.textContent = 'Выберите элемент на холсте, чтобы изменить его свойства. Перетащите реквизит из палитры на холст, чтобы добавить поле.';
    panel.appendChild(em); return;
  }
  var h = document.createElement('h4');
  h.textContent = info.name || info.kind;
  var sk = document.createElement('span'); sk.className = 'prop-kind'; sk.textContent = info.kind;
  h.appendChild(sk); panel.appendChild(h);
  addTextProp(panel, 'Заголовок (ru)', 'title.ru', info.titleRu || '');
  addTextProp(panel, 'Имя', 'name', info.name || '');
  if (info.kind === 'ПолеКартинки') {
    addTextProp(panel, 'Картинка (путь)', 'picture', info.picture || '');
    addNumProp(panel, 'Ширина, px', 'width', info.width);
    addNumProp(panel, 'Высота, px', 'height', info.height);
  } else if (!info.container) {
    addTextProp(panel, 'Поле данных (data_path)', 'data_path', info.dataPath || '');
    addTextProp(panel, 'Подсказка', 'hint', info.hint || '');
    addCheckProp(panel, 'Обязательное', 'required', info.required);
    addCheckProp(panel, 'Только чтение', 'readonly', info.readonly);
    if (info.kind === 'ПолеВвода') {
      // Два ключа рядом и подписаны по назначению: сюда пишут шаблон, потому
      // что «маска» — первое, что ищут глазами, и до появления input_mask
      // шаблон уезжал в regexp-проверку (#763).
      addTextProp(panel, 'Шаблон ввода (00.00.00)', 'input_mask', info.inputMask || '');
      addTextProp(panel, 'Проверка значения (регулярное выражение)', 'mask', info.mask || '');
      addCheckRaw(panel, 'Файловое поле', info.fileType, function (ch) { setProp('type', ch ? 'file' : ''); });
    }
  }
  if (info.kind === 'ГруппаФормы') {
    addSelectRaw(panel, 'Расположение реквизитов', info.orientation === 'horizontal' ? 'horizontal' : 'vertical', [
      { value: 'vertical', label: 'Вертикально' },
      { value: 'horizontal', label: 'Горизонтально' }
    ], function (v) {
      if (v === 'horizontal') setProp('orientation', 'horizontal');
      else editOp({ op: 'delProp', node: _selected, key: 'orientation' }, true);
    });
  }
  if (info.kind === 'ТабличнаяЧасть') {
    addCheckRaw(panel, 'Простая таблица (без SlickGrid)', info.noGrid, function (ch) { setProp('no_grid', ch ? 'true' : ''); });
    // auto_sum умеет рантайм с #215.1 и используют пять примеров, но в панели
    // его не было — настройку ставили руками в YAML, поэтому и не находили.
    addCheckRaw(panel, 'Считать Сумма = Количество × Цена', info.autoSum, function (ch) { setProp('auto_sum', ch ? 'true' : ''); });
    addColumnsEditor(panel);
  }
  if (info.kind === 'Переключатель') { addOptionsEditor(panel, info); }
  addEventsSection(panel, info);
  addElementActions(panel, info);
}

// ── Свойства формы (batch B2/B3) ────────────────────────────────────────────
// Корневой псевдо-узел "form": заголовок и вид формы (внутри form:), события и
// штатные действия формы (верхний уровень). Все правки уходят как node="form" —
// сервер сам направляет ключ в нужный блок (см. setFormProp).
function renderFormProps(panel) {
  var f = _form || {};
  var h = document.createElement('h4'); h.textContent = 'Форма';
  var sk = document.createElement('span'); sk.className = 'prop-kind'; sk.textContent = f.kind || ''; h.appendChild(sk);
  panel.appendChild(h);
  addTextProp(panel, 'Заголовок (ru)', 'title.ru', f.titleRu || '');
  // Вид формы.
  var krow = document.createElement('div'); krow.className = 'prop-row';
  var kl = document.createElement('label'); kl.textContent = 'Вид формы'; krow.appendChild(kl);
  var ksel = document.createElement('select');
  ['object', 'list', 'choice', 'folder', 'custom'].forEach(function (k) { ksel.appendChild(new Option(k, k)); });
  ksel.value = f.kind || 'custom';
  ksel.addEventListener('change', function () { setProp('kind', ksel.value); });
  krow.appendChild(ksel); panel.appendChild(krow);
  // События формы и штатные действия.
  addEventsRows(panel, formEvents(), f.events || {}, 'Форма');
  addFormActionsSection(panel, f);
}
// Штатные действия формы (B3). Рантайм читает только actions.delete.visible —
// показываем галочку для кнопки «Удалить»; снятие пишет visible:false.
function addFormActionsSection(panel, f) {
  var hd = document.createElement('div'); hd.className = 'prop-row prop-section'; hd.textContent = 'Штатные действия';
  panel.appendChild(hd);
  var acts = f.actions || {};
  var delVisible = !(acts.delete === false);
  addCheckRaw(panel, 'Показывать кнопку «Удалить»', delVisible, function (ch) {
    setProp('actions.delete.visible', ch ? 'true' : 'false');
  });
}

// ── События элемента/формы (batch B1) ───────────────────────────────────────
// applicableEvents — какие события показывать для элемента данного вида.
function applicableEvents(kind) {
  switch (kind) {
    case 'Кнопка': case 'КнопкаКП': return ['Нажатие'];
    case 'ПолеВвода': case 'Флажок': case 'ПолеДаты': case 'Переключатель':
    case 'ПолеСписка': return ['ПриИзменении'];
    // Табличная часть отправляет шесть событий (browserEventAllowedForElement
    // на сервере — единственный источник правды). Панель предлагала одно, а
    // остальные пять работали в рантайме и писались руками в YAML.
    case 'ТабличнаяЧасть': return ['ПриИзменении', 'ПриИзмененииСтроки',
      'ПриДобавленииСтроки', 'ПослеДобавленияСтроки', 'ПриУдаленииСтроки',
      'ПриАктивизацииСтроки'];
    // Колонка ТЧ: обработчик срабатывает на правку ячейки именно этой колонки
    // (план 154, Р4). Обработчик самой ТЧ при этом не отключается.
    case 'Колонка': return ['ПриИзменении'];
    default: return [];
  }
}
// formEvents — события уровня формы (подмножество частых из form_module.go).
function formEvents() {
  return ['ПриОткрытии', 'ПриСоздании', 'ПередЗаписью', 'ПриЗаписи', 'ПослеЗаписи', 'ПередЗакрытием'];
}
function addEventsSection(panel, info) {
  addEventsRows(panel, applicableEvents(info.kind), info.events || {}, info.name || info.kind);
}
// addEventsRows строит по <select> на каждое событие: «— нет —» + процедуры из
// .form.os + «Создать обработчик…». Текущая привязка = cur[событие]. Запись —
// setProp events.<событие>; снятие — delProp; создание дописывает процедуру.
function addEventsRows(panel, evs, cur, defPrefix) {
  if (!evs.length) return;
  var hd = document.createElement('div'); hd.className = 'prop-row prop-section'; hd.textContent = 'События';
  panel.appendChild(hd);
  var procs = osProcedures();
  var CREATE = '@create';
  evs.forEach(function (ev) {
    var row = document.createElement('div'); row.className = 'prop-row';
    var l = document.createElement('label'); l.textContent = ev; row.appendChild(l);
    var sel = document.createElement('select');
    sel.appendChild(new Option('— нет —', ''));
    procs.forEach(function (p) { sel.appendChild(new Option(p, p)); });
    sel.appendChild(new Option('Создать обработчик…', CREATE));
    sel.value = cur[ev] || '';
    sel.addEventListener('change', function () {
      var v = sel.value;
      if (v === CREATE) {
        var name = window.prompt('Имя процедуры-обработчика:', (defPrefix || '') + ev);
        if (!name) { sel.value = cur[ev] || ''; return; }
        ensureProcedure(name, ev, _model[_selected]);
        setProp('events.' + ev, name);
      } else if (v === '') {
        editOp({ op: 'delProp', node: _selected, key: 'events.' + ev }, true);
      } else {
        setProp('events.' + ev, v);
      }
    });
    row.appendChild(sel); panel.appendChild(row);
  });
}

// ── Редактор набора значений Переключателя (batch C1) ───────────────────────
// Для enum-поля значения подставляются рантаймом автоматически — редактор нужен
// для произвольных (в т.ч. числовых) наборов. Опции пишутся целиком op:setOptions.
function addOptionsEditor(panel, info) {
  var vrow = document.createElement('div'); vrow.className = 'prop-row';
  var vl = document.createElement('label'); vl.textContent = 'Представление'; vrow.appendChild(vl);
  var vsel = document.createElement('select');
  vsel.appendChild(new Option('Переключатель (radio)', 'radio'));
  vsel.appendChild(new Option('Список (select)', 'select'));
  vsel.value = (info.view === 'select') ? 'select' : 'radio';
  vsel.addEventListener('change', function () {
    if (vsel.value === 'select') setProp('view', 'select');
    else editOp({ op: 'delProp', node: _selected, key: 'view' }, true);
  });
  vrow.appendChild(vsel); panel.appendChild(vrow);

  var hd = document.createElement('div'); hd.className = 'prop-row prop-section'; hd.textContent = 'Значения';
  panel.appendChild(hd);
  var note = document.createElement('div'); note.className = 'prop-hint';
  note.textContent = 'Для поля-перечисления значения подставляются автоматически; здесь задаются произвольные наборы.';
  panel.appendChild(note);
  var opts = (info.options || []).map(function (o) { return { value: o.value, label: o.label }; });
  var nodeAtEdit = _selected;
  function commit() { editOp({ op: 'setOptions', node: nodeAtEdit, options: JSON.stringify(opts) }, true); }
  var listWrap = document.createElement('div'); panel.appendChild(listWrap);
  function redraw() {
    listWrap.innerHTML = '';
    opts.forEach(function (o, i) {
      var row = document.createElement('div'); row.className = 'prop-row prop-opt';
      var vi = document.createElement('input'); vi.type = 'text'; vi.placeholder = 'значение'; vi.value = o.value;
      vi.addEventListener('change', function () { opts[i].value = vi.value; commit(); });
      var li = document.createElement('input'); li.type = 'text'; li.placeholder = 'представление'; li.value = o.label;
      li.addEventListener('change', function () { opts[i].label = li.value; commit(); });
      var rm = mkBtn('×', function () { opts.splice(i, 1); commit(); }); rm.className = 'btn btn-danger';
      row.appendChild(vi); row.appendChild(li); row.appendChild(rm); listWrap.appendChild(row);
    });
    var add = mkBtn('+ значение', function () { opts.push({ value: '', label: '' }); redraw(); });
    listWrap.appendChild(add);
  }
  redraw();
}
// Кнопки порядка и удаления элемента (follow-up #164, слайсы B1/B2): «выше/ниже»
// переставляют узел в соседний индекс того же родителя; «удалить» вырезает узел
// (контейнер — вместе с детьми, с подтверждением).
function addElementActions(panel, info) {
  var row = document.createElement('div'); row.className = 'prop-row prop-actions';
  row.appendChild(mkBtn('↑ Выше', function () { moveSelected(-1); }));
  row.appendChild(mkBtn('↓ Ниже', function () { moveSelected(1); }));
  var del = mkBtn('Удалить элемент', deleteSelected);
  del.className = 'btn btn-danger';
  row.appendChild(del);
  panel.appendChild(row);
}
function mkBtn(label, onClick) {
  var b = document.createElement('button');
  b.type = 'button'; b.className = 'btn'; b.textContent = label;
  b.addEventListener('click', onClick);
  return b;
}
// nodeAddr раскладывает node-id на родительский элемент-контейнер и индекс в
// его sequence. "elements.2" → {parent:"", index:2}; "elements.0.children.1" →
// {parent:"elements.0", index:1}. seqPath — путь самого sequence (для проверки
// соседей по _model). null для неструктурных адресов (напр. колонок ТЧ).
function nodeAddr(nodeId) {
  var dot = nodeId.lastIndexOf('.');
  if (dot < 0) return null;
  var idx = parseInt(nodeId.slice(dot + 1), 10);
  if (isNaN(idx)) return null;
  var seqPath = nodeId.slice(0, dot);
  var parent;
  if (seqPath === 'elements') parent = '';
  else if (seqPath.slice(-9) === '.children') parent = seqPath.slice(0, -9);
  else return null;
  return { parent: parent, index: idx, seqPath: seqPath };
}
function moveSelected(delta) {
  if (!_selected) return;
  var a = nodeAddr(_selected);
  if (!a) return;
  var finalIdx = a.index + delta;
  if (finalIdx < 0) return;
  // Вниз и уже последний — соседа нет, выходим.
  if (delta > 0 && !_model[a.seqPath + '.' + finalIdx]) return;
  // Сервер компенсирует сдвиг после удаления при переносе вперёд в том же
  // контейнере (см. formdoc.Move): чтобы оказаться на finalIdx, при движении
  // вниз передаём finalIdx+1, при движении вверх — finalIdx как есть.
  var serverIdx = delta > 0 ? finalIdx + 1 : finalIdx;
  var newId = a.seqPath + '.' + finalIdx;
  editOp({ op: 'move', node: _selected, parent: a.parent, index: serverIdx }, true).then(function (resp) {
    if (resp && resp.ok) selectNode(newId); // удержать выделение на переехавшем узле
  });
}
function deleteSelected() {
  if (!_selected) return;
  var info = _model[_selected] || {};
  var label = info.name || info.kind || 'элемент';
  var msg = info.container
    ? 'Удалить «' + label + '» вместе со вложенными элементами?'
    : 'Удалить «' + label + '»?';
  if (!window.confirm(msg)) return;
  editOp({ op: 'delete', node: _selected }, true);
}

// ── Редактор состава колонок ТЧ (#164, слайс D2) ────────────────────────────
// data_path выбранной ТЧ "Объект.Товары" → имя ТЧ "Товары" (ключ _tableParts).
function tablePartName() {
  var dp = (_model[_selected] || {}).dataPath || '';
  var i = dp.lastIndexOf('.');
  return i >= 0 ? dp.slice(i + 1) : dp;
}
// colKey — ключ сопоставления колонки реквизиту. Регистронезависимый, потому что
// таков рантайм (managedTPFieldIndexForColumn сравнивает через strings.EqualFold).
function colKey(s) { return (s || '').trim().toLowerCase(); }

// columnChildren — прямые дети kind:Колонка табличной части, в порядке YAML.
// Порядок восстанавливаем по числовому индексу в node-id, а не по обходу
// Object.keys(_model): модель приходит из Go-map, порядок ключей в JSON случаен.
function columnChildren(tpNodeId) {
  var prefix = tpNodeId + '.children.', out = [];
  Object.keys(_model).forEach(function (id) {
    if (id.indexOf(prefix) !== 0) return;
    var rest = id.slice(prefix.length);
    if (!/^[0-9]+$/.test(rest)) return;   // только прямые дети, не внуки
    if ((_model[id].kind || '') !== 'Колонка') return;
    out.push({ id: id, idx: parseInt(rest, 10), info: _model[id] });
  });
  out.sort(function (a, b) { return a.idx - b.idx; });
  return out;
}

// Уже объявленные колонки ТЧ: {map: реквизит → node-id, order: [реквизит]}.
// Сопоставление повторяет managedTPFieldIndexForColumn — data_path, затем field,
// затем имя элемента, первое совпадение с реальным реквизитом ТЧ. Раньше
// смотрели только data_path, поэтому колонка, объявленная ключом field, в
// рантайме была видна, а в панели стояла без галочки (#1123).
function presentColumns(tpNodeId, cols) {
  var known = {};
  (cols || []).forEach(function (c) { known[colKey(c.name)] = c.name; });
  var map = {}, order = [];
  columnChildren(tpNodeId).forEach(function (ch) {
    var dp = ch.info.dataPath || '', j = dp.lastIndexOf('.');
    var cands = [j >= 0 ? dp.slice(j + 1) : dp, ch.info.field, ch.info.name];
    for (var k = 0; k < cands.length; k++) {
      var name = known[colKey(cands[k])];
      if (!name) continue;
      if (!map[name]) { map[name] = ch.id; order.push(name); }
      return;
    }
  });
  return { map: map, order: order };
}
function addColumnsEditor(panel) {
  var tp = tablePartName();
  var cols = _tableParts[tp] || [];
  var hd = document.createElement('div'); hd.className = 'prop-row';
  var l = document.createElement('label'); l.textContent = 'Колонки (показывать):';
  hd.appendChild(l); panel.appendChild(hd);
  if (!cols.length) {
    var unknown = document.createElement('div'); unknown.className = 'prop-empty';
    unknown.textContent = 'Состав колонок неизвестен (метаданные ТЧ не загружены).';
    panel.appendChild(unknown); return;
  }
  var st = presentColumns(_selected, cols);
  // Явного состава нет — рантайм показывает ВСЕ реквизиты (managedTPColumnPlan:
  // «ничего не выбрано» = «показать всё»). Галочки в этом состоянии обязаны
  // стоять: снятые читались как «не показывается ничего», хотя пользователь
  // видит полную таблицу, — и подталкивали поставить одну, что на самом деле
  // убирает все остальные колонки (#1123).
  var explicit = st.order.length > 0;
  var note = document.createElement('div'); note.className = 'prop-empty';
  note.textContent = explicit
    ? 'Состав задан явно. Снимете все галочки — вернётся показ всех колонок.'
    : 'Состав не задан — показываются все колонки. Снимите галочку, чтобы задать состав явно.';
  panel.appendChild(note);
  cols.forEach(function (c) {
    var row = document.createElement('div'); row.className = 'prop-row prop-check';
    var cb = document.createElement('input'); cb.type = 'checkbox';
    cb.checked = explicit ? !!st.map[c.name] : true;
    cb.addEventListener('change', function () { toggleColumn(tp, cols, c, cb.checked, st, explicit); });
    var lab = document.createElement('label'); lab.textContent = c.title || c.name;
    row.appendChild(cb); row.appendChild(lab); panel.appendChild(row);
  });
}
// Включение колонки → insert kind:Колонка в конец ТЧ; выключение → delete её узла.
// Выделение удерживаем на ТЧ, чтобы можно было щёлкать чекбоксы подряд.
//
// Особый случай — снятие галочки, когда явного состава ещё нет: удалять нечего,
// а один insert оставил бы ровно одну колонку вместо «всех минус снятая».
// Поэтому здесь материализуем весь состав без снятой колонки, и одной командой
// insertColumns: серия запросов при обрыве на середине тихо спрятала бы
// колонки, которых никто не снимал (#1123).
function toggleColumn(tp, cols, col, on, st, explicit) {
  var tpId = _selected, p;
  if (!explicit && !on) {
    var rest = cols.filter(function (c) { return c.name !== col.name; });
    if (!rest.length) return;   // единственная колонка: «спрятать всё» состава не имеет
    p = editOp({ op: 'insertColumns', parent: tpId, columns: JSON.stringify(rest.map(function (c) {
      return { name: 'Кол' + c.name, title: c.title || '', data_path: 'Объект.' + tp + '.' + c.name };
    })) }, true);
  } else if (on) {
    p = editOp({ op: 'insert', parent: tpId, index: 9999, kind: 'Колонка',
      name: 'Кол' + col.name, data_path: 'Объект.' + tp + '.' + col.name, title_ru: col.title || '' }, true);
  } else if (st.map[col.name]) {
    p = editOp({ op: 'delete', node: st.map[col.name] }, true);
  } else { return; }
  p.then(function (resp) { if (resp && resp.ok) selectNode(tpId); });
}
function addTextProp(panel, label, key, val) {
  var row = document.createElement('div'); row.className = 'prop-row';
  var l = document.createElement('label'); l.textContent = label; row.appendChild(l);
  var inp = document.createElement('input'); inp.type = 'text'; inp.value = val;
  inp.addEventListener('change', function () { setProp(key, inp.value); });
  row.appendChild(inp); panel.appendChild(row);
}
function addCheckProp(panel, label, key, checked) {
  addCheckRaw(panel, label, checked, function (ch) { setProp(key, ch ? 'true' : ''); });
}
// addCheckRaw — чекбокс с произвольным обработчиком (для type=file, no_grid и т.п.,
// где значение не «true»/«»).
function addCheckRaw(panel, label, checked, onChange) {
  var row = document.createElement('div'); row.className = 'prop-row prop-check';
  var inp = document.createElement('input'); inp.type = 'checkbox'; inp.checked = !!checked;
  inp.addEventListener('change', function () { onChange(inp.checked); });
  var l = document.createElement('label'); l.textContent = label;
  row.appendChild(inp); row.appendChild(l); panel.appendChild(row);
}
function addNumProp(panel, label, key, val) {
  var row = document.createElement('div'); row.className = 'prop-row';
  var l = document.createElement('label'); l.textContent = label; row.appendChild(l);
  var inp = document.createElement('input'); inp.type = 'number'; inp.value = val || 0;
  inp.addEventListener('change', function () { setProp(key, inp.value); });
  row.appendChild(inp); panel.appendChild(row);
}
function addSelectRaw(panel, label, value, options, onChange) {
  var row = document.createElement('div'); row.className = 'prop-row';
  var l = document.createElement('label'); l.textContent = label; row.appendChild(l);
  var sel = document.createElement('select');
  (options || []).forEach(function (o) { sel.appendChild(new Option(o.label, o.value)); });
  sel.value = value || '';
  sel.addEventListener('change', function () { onChange(sel.value); });
  row.appendChild(sel); panel.appendChild(row);
}
function setProp(key, value) {
  if (!_selected) return;
  editOp({ op: 'setProp', node: _selected, key: key, value: value }, true);
}

// hookYamlChange — живая синхронизация YAML→холст (debounce), с защитой от
// рекурсии при программном setYAML из ответа edit-op.
var _yamlChangeTimer = null;
function hookYamlChange() {
  if (!window.yamlEditor || window._yamlHooked) return;
  window._yamlHooked = true;
  window.yamlEditor.onDidChangeModelContent(function () {
    if (_syncing) return;
    clearTimeout(_yamlChangeTimer);
    _yamlChangeTimer = setTimeout(syncFromYAML, 400);
  });
}

// Инициализация для textarea-fallback (Monaco инициализирует холст в своём
// callback). При вводе в textarea — тот же debounced reload.
if (window._yamlTA) {
  window._yamlTA.addEventListener('input', function () {
    if (_syncing) return;
    clearTimeout(_yamlChangeTimer);
    _yamlChangeTimer = setTimeout(syncFromYAML, 400);
  });
  reloadCanvas();
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

// previewTableParts — состав табличных частей объекта (имя ТЧ → реквизиты) для
// предпросмотра. Нужен ровно там, где колонки в форме явно не объявлены: рантайм
// в этом случае показывает все реквизиты, и предпросмотр обязан показывать то же
// (#1123). nil — метаданные недоступны, предпросмотр честно говорит об этом.
type previewTableParts map[string][]formScaffoldAttr

// renderManagedFormPreview генерирует упрощённый HTML-предпросмотр
// дерева элементов формы. Отрисовывает абстрактные input/checkbox/group на
// основе FormModule.Elements; из metadata.Entity берёт только состав табличных
// частей (tps) — чтобы ТЧ без явных колонок выглядела как в рантайме.
//
// Этого достаточно для UI-редактора чтобы оценить структуру формы;
// полноценный рендер с реальными данными доступен после сохранения
// через рантайм-handler /ui/.../form (этап 3).
func renderManagedFormPreview(fm *metadata.FormModule, tps previewTableParts) string {
	var buf bytes.Buffer
	buf.WriteString(`<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;padding:18px;font-family:-apple-system,sans-serif;background:#fff;color:#334;font-size:13px}
h2{margin:0 0 14px;color:#1a4a80;font-size:16px;display:flex;align-items:center;gap:8px}
.tag{font-size:11px;background:#d1fae5;color:#059669;padding:2px 8px;border-radius:10px}
fieldset{border:1px solid #e2e8f0;border-radius:8px;padding:12px 14px;margin-bottom:12px}
legend{font-weight:600;color:#475569;padding:0 6px;font-size:12px}
.group-horizontal>.group-body{display:flex;flex-wrap:wrap;gap:12px;align-items:flex-start}
.group-horizontal>.group-body>.fg{flex:1 1 220px;min-width:180px;margin-bottom:0}
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
.tp-prev{margin:8px 0}
.tp-prev-hd{font-size:12px;font-weight:600;color:#475569;margin-bottom:4px}
.tp-prev-tbl{width:100%;border-collapse:collapse;font-size:12px}
.tp-prev-tbl th,.tp-prev-tbl td{border:1px solid #e2e8f0;padding:5px 8px;text-align:left}
.tp-prev-tbl th{background:#f8fafc;color:#475569;font-weight:600}
.tp-prev-tbl td{height:24px}
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
		renderPreviewElement(&buf, el, &tabsCounter, tps)
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

func renderPreviewElement(buf *bytes.Buffer, el *metadata.FormElement, tabsCounter *int, tps previewTableParts) {
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
		cls := ""
		if el.Orientation == "horizontal" {
			cls = ` class="group-horizontal"`
		}
		fmt.Fprintf(buf, `<fieldset%s><legend>%s</legend><div class="group-body">`, cls, html.EscapeString(title))
		for _, c := range el.Children {
			renderPreviewElement(buf, c, tabsCounter, tps)
		}
		buf.WriteString(`</div></fieldset>`)
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
				renderPreviewElement(buf, c, tabsCounter, tps)
			}
			buf.WriteString(`</div>`)
			pageIdx++
		}
		buf.WriteString(`</div></div>`)
	case metadata.FormElementPage:
		// Отдельная страница вне набора СтраницыФормы (её можно бросить на холст) —
		// рисуем именованным блоком с детьми, а не «предпросмотр не реализован».
		fmt.Fprintf(buf, `<fieldset><legend>%s</legend>`, html.EscapeString(title))
		for _, c := range el.Children {
			renderPreviewElement(buf, c, tabsCounter, tps)
		}
		buf.WriteString(`</fieldset>`)
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
		// Колонки, выбранные в конструкторе (дочерние kind:Колонка), рисуем
		// реальной таблицей-каркасом с парой пустых строк.
		var cols []*metadata.FormElement
		for _, c := range el.Children {
			if c != nil && c.Kind == metadata.FormElementColumn {
				cols = append(cols, c)
			}
		}
		// Без явных колонок рантайм показывает ВСЕ реквизиты табличной части
		// (managedTPColumnPlan). Раньше предпросмотр в этом месте рисовал
		// подсказку вместо таблицы — то есть показывал пустоту там, где
		// пользователь увидит полный набор колонок, и подталкивал «исправить»
		// это галочкой, которая на самом деле убирает все колонки, кроме
		// отмеченной (#1123). Заголовки берём из метаданных ТЧ.
		headers := make([]string, 0, len(cols))
		fallback := ""
		if len(cols) > 0 {
			for _, c := range cols {
				headers = append(headers, columnLabel(c))
			}
		} else if attrs := tps[previewTablePartName(el)]; len(attrs) > 0 {
			for _, a := range attrs {
				name := a.Title
				if strings.TrimSpace(name) == "" {
					name = a.Name
				}
				headers = append(headers, name)
			}
			fallback = "Состав не задан — показываются все реквизиты табличной части."
		}
		fmt.Fprintf(buf, `<div class="tp-prev"><div class="tp-prev-hd">▦ %s</div>`, html.EscapeString(title))
		if len(headers) == 0 {
			// Метаданные ТЧ недоступны (предпросмотр без базы или ТЧ не найдена):
			// перечислить нечего, но соглашение назвать обязаны.
			buf.WriteString(`<div class="hint">Состав не задан — в рантайме показываются все реквизиты табличной части.</div>`)
		} else {
			if fallback != "" {
				fmt.Fprintf(buf, `<div class="hint">%s</div>`, html.EscapeString(fallback))
			}
			buf.WriteString(`<table class="tp-prev-tbl"><thead><tr>`)
			for _, h := range headers {
				fmt.Fprintf(buf, `<th>%s</th>`, html.EscapeString(h))
			}
			buf.WriteString(`</tr></thead><tbody>`)
			for r := 0; r < 2; r++ {
				buf.WriteString(`<tr>`)
				for range headers {
					buf.WriteString(`<td></td>`)
				}
				buf.WriteString(`</tr>`)
			}
			buf.WriteString(`</tbody></table>`)
		}
		buf.WriteString(`</div>`)
	case metadata.FormElementCommandBar:
		// командная панель — обычно рендерится в toolbar над формой;
		// в preview просто рисуем кнопки в ряд.
		for _, c := range el.Children {
			renderPreviewElement(buf, c, tabsCounter, tps)
		}
	default:
		fmt.Fprintf(buf, `<div class="unknown">Элемент «%s» типа «%s»: предпросмотр не реализован.</div>`,
			html.EscapeString(el.Name), html.EscapeString(string(el.Kind)))
	}
}

// previewTablePartName — имя табличной части элемента формы: последний сегмент
// data_path ("Объект.Товары" → "Товары"), иначе имя элемента. Тот же вывод, что
// у tablePartName() в клиенте конструктора, — ключ для previewTableParts.
func previewTablePartName(el *metadata.FormElement) string {
	if dp := strings.TrimSpace(el.DataPath); dp != "" {
		return lastSegment(dp)
	}
	return strings.TrimSpace(el.Name)
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
