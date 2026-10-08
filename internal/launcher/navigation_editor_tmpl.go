package launcher

import (
	"encoding/json"
	"html/template"
	"net/http"

	"github.com/ivantit66/onebase/internal/ui"
)

var navigationEditorTmpl = template.Must(template.New("navigation-editor").Funcs(template.FuncMap{
	"t": tr,
	"js": func(v any) (template.JS, error) {
		b, err := json.Marshal(v)
		return template.JS(b), err //nolint:gosec // JSON escapes <, > and &: data cannot terminate the script element.
	},
}).Parse(navigationEditorHTML))

func renderNavigationEditor(w http.ResponseWriter, d navigationEditorData) {
	page := struct {
		navigationEditorData
		Labels      map[string]string `json:"labels"`
		IconSprite  string            `json:"icon_sprite"`
		IconNames   []string          `json:"icon_names"`
		IconAliases json.RawMessage   `json:"icon_aliases"`
	}{d, map[string]string{
		"newSection": tr(d.Lang, "Новый раздел"), "newGroup": tr(d.Lang, "Новая папка"),
		"select": tr(d.Lang, "Выбрать"), "add": tr(d.Lang, "Добавить в меню"),
		"up": tr(d.Lang, "Выше"), "down": tr(d.Lang, "Ниже"),
		"in": tr(d.Lang, "Внутрь папки"), "out": tr(d.Lang, "Из папки"),
		"remove": tr(d.Lang, "Убрать из меню"), "changed": tr(d.Lang, "Меню изменено"),
		"saved": tr(d.Lang, "Меню сохранено"), "error": tr(d.Lang, "Ошибка"),
		"replace":       tr(d.Lang, "Заменить текущий черновик меню? Файл изменится только после сохранения."),
		"removeConfirm": tr(d.Lang, "Убрать узел и его пункты из меню? Объекты останутся в составе и появятся в разделе «Другое»."),
		"empty":         tr(d.Lang, "Используется меню по составу. Создайте раздел или импортируйте текущий порядок."),
		"primaryTitle":  tr(d.Lang, "Основное название"), "icon": tr(d.Lang, "Иконка"),
		"parent": tr(d.Lang, "Расположение"), "translations": tr(d.Lang, "Переводы названия"),
		"translationHint": tr(d.Lang, "Переводы имеют приоритет при просмотре на соответствующем языке."),
		"unexpected":      tr(d.Lang, "Неожиданный ответ сервера"),
		"unsaved":         tr(d.Lang, "Изменения меню не сохранены"),
	}, ui.LucideSpriteURL(), ui.LucideNames(), json.RawMessage(ui.LucideAliasesJSON())}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := navigationEditorTmpl.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

const navigationEditorHTML = `<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{t .Lang "Редактор меню"}} — {{.Title}}</title>
<style>
*{box-sizing:border-box}body{margin:0;font:14px system-ui,sans-serif;background:#f4f6fb;color:#334155}
header{padding:14px 22px;background:#1a4a80;color:white;display:flex;gap:22px;align-items:center}header h1{font-size:18px;margin:0}header a{color:#dbeafe}
main{max-width:1500px;margin:auto;padding:18px}.toolbar{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:16px;align-items:center}
button,select,input{font:inherit}button{cursor:pointer;border:1px solid #cbd5e1;border-radius:5px;padding:6px 10px;background:white;color:#334155}button:hover{background:#eff6ff}button:disabled{opacity:.45;cursor:default}button.primary{background:#1a4a80;color:white}
button:focus-visible,input:focus-visible,select:focus-visible{outline:3px solid #60a5fa;outline-offset:2px}.layout{display:grid;grid-template-columns:minmax(0,1fr)300px;gap:18px}.panel{background:white;padding:16px;border-radius:8px;margin-bottom:16px;border:1px solid #e2e8f0}.panel h2{font-size:16px;margin:0 0 12px}
.menu-row{display:flex;align-items:center;gap:5px;padding:6px;border-bottom:1px solid #e2e8f0;flex-wrap:wrap}.menu-row[data-kind=group]{margin-left:22px}.menu-row[data-kind=item]{margin-left:44px}.menu-row.selected{background:#eff6ff}.menu-row button{font-size:12px;padding:4px 7px}.menu-name{flex:1;min-width:120px;text-align:left;font-weight:600}.menu-row[data-kind=item] .menu-name{font-weight:400}.menu-row.drop-target{outline:2px solid #2563eb}.properties{display:flex;gap:12px;flex-wrap:wrap;margin-top:14px}.properties label{display:flex;flex-direction:column;gap:5px;min-width:180px}.properties input,.properties select,#menu-filter{border:1px solid #cbd5e1;border-radius:5px;padding:6px}#menu-filter{width:100%;margin-bottom:12px}.palette-item{display:flex;align-items:center;gap:8px;padding:8px 0;border-bottom:1px solid #e2e8f0}.palette-item span{flex:1;overflow-wrap:anywhere}.hint{color:#64748b;font-size:13px}.preview h3{margin:12px 0 5px;font-size:15px}.preview details{margin-left:12px}.preview ul{margin:5px 0;padding-left:25px}.notice{white-space:pre-wrap;margin-bottom:10px}.notice.error{color:#b91c1c}.notice.ok{color:#166534}.sr-only{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0)}
@media(max-width:850px){.layout{grid-template-columns:1fr}.menu-row[data-kind=item]{margin-left:20px}.menu-row[data-kind=group]{margin-left:10px}}
</style></head><body>
<header><h1>{{t .Lang "Редактор меню"}} — {{.Title}}</h1><a href="/bases/{{.BaseID}}/configurator?tab=tree&select={{if .Subsystem}}sub-{{.Subsystem}}{{else}}home-page{{end}}">{{t .Lang "Конфигуратор"}}</a></header>
<main id="menu-editor">
<p class="hint">{{t .Lang "Объединяйте объекты в смысловые разделы и папки. Состав подсистемы не меняется. Импорт и предпросмотр не сохраняют изменения."}}</p>
<div class="toolbar">
<button id="menu-save" class="primary">{{t .Lang "Сохранить"}}</button>
<button id="menu-add-section">{{t .Lang "Добавить раздел"}}</button>
<button id="menu-add-group">{{t .Lang "Добавить папку"}}</button>
<button id="menu-import-legacy">{{t .Lang "Создать из состава"}}</button>
<button id="menu-import-tree">{{t .Lang "Импортировать порядок дерева"}}</button>
<label>{{t .Lang "Язык предпросмотра"}} <select id="menu-lang"><option value="ru">Русский</option><option value="en">English</option>{{if and (ne .Lang "ru") (ne .Lang "en")}}<option value="{{.Lang}}">{{.Lang}}</option>{{end}}</select></label>
</div>
<div id="menu-status" class="notice" role="alert"></div><div id="menu-live" class="sr-only" aria-live="polite"></div>
<div class="layout"><div>
<section class="panel"><h2>{{t .Lang "Структура меню"}}</h2><div id="menu-tree"></div><div id="menu-properties" class="properties"></div><p class="hint">{{t .Lang "Перемещайте узлы кнопками или перетаскиванием. Alt + стрелки: выше, ниже, внутрь папки, из папки."}}</p></section>
<section class="panel preview"><h2>{{t .Lang "Предпросмотр меню"}}</h2><div id="menu-preview"></div></section>
</div><aside class="panel"><h2>{{t .Lang "Доступные объекты"}}</h2><input id="menu-filter" type="search" placeholder="{{t .Lang "Поиск"}}" aria-label="{{t .Lang "Поиск"}}"><div id="menu-palette"></div></aside></div>
</main><script>window.OB_MENU_EDITOR={{js .}};</script><script src="/static/navigation-editor.js"></script>
</body></html>`
