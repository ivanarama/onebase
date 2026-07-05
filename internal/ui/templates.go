package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

var tmpl = template.Must(newTemplate(nil))

func newTemplate(bundle *i18n.Bundle) (*template.Template, error) {
	return template.New("root").Funcs(templateFuncs(bundle)).Parse(templateSource())
}

func templateFuncs(bundle *i18n.Bundle) template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"str": func(v any) string {
			if v == nil {
				return ""
			}
			return fmt.Sprintf("%v", v)
		},
		"journalRowStyle":  journalRowStyle,
		"journalCellStyle": journalCellStyle,
		"formRowClass":     formRowClass,
		"formCellClass":    formCellClass,
		"add":              func(a, b int) int { return a + b },
		// lucideIcon рендерит инлайн-SVG иконки навигации по имени Lucide (план 72).
		"lucideIcon": LucideIcon,
		"t": func(lang, key string) string {
			if bundle != nil {
				return bundle.T(lang, key)
			}
			return key
		},
		// refID extracts UUID from a *Ref (implements GetRefUUID), otherwise returns fmt.Sprintf.
		// Used in TP row templates so the "selected" comparison works after enrichTPRowsWithRefs.
		"refID": func(v any) string {
			if v == nil {
				return ""
			}
			type uuidGetter interface{ GetRefUUID() string }
			if rp, ok := v.(uuidGetter); ok {
				return rp.GetRefUUID()
			}
			return fmt.Sprintf("%v", v)
		},
		"isRef":       func(t any) bool { return strings.HasPrefix(fmt.Sprintf("%v", t), "reference:") },
		"isEnum":      func(t any) bool { return strings.HasPrefix(fmt.Sprintf("%v", t), "enum:") },
		"tileView":    resolveTileView,
		"listColumns": resolveListColumns,
		"treeColumn":  isTreeListColumn,
		"hasValue": func(v any) bool {
			if v == nil {
				return false
			}
			if s, ok := v.(string); ok {
				return s != ""
			}
			return true
		},
		"enumLabel": func(labels map[string]map[string]string, field, value string) string {
			if m, ok := labels[field]; ok {
				if lbl, ok := m[value]; ok && lbl != "" {
					return lbl
				}
			}
			return value
		},
		"isRichText": func(t any) bool { return fmt.Sprintf("%v", t) == string(metadata.FieldTypeRichText) },
		"isImage":    func(t any) bool { return fmt.Sprintf("%v", t) == string(metadata.FieldTypeImage) },
		// entityHasRichText — есть ли среди реквизитов шапки сущности richtext-поле.
		// Quill (vendor-ассеты + init) грузятся на форме только при true, чтобы не
		// тянуть редактор на формы без richtext-полей.
		"entityHasRichText": func(e *metadata.Entity) bool {
			if e == nil {
				return false
			}
			for _, f := range e.Fields {
				if metadata.IsRichText(f.Type) {
					return true
				}
			}
			return false
		},
		// richPlain — текстовая проекция richtext-значения для ячейки списка
		// (усечённая, чтобы HTML не разъезжал таблицу).
		"richPlain": func(v any) string {
			if v == nil {
				return ""
			}
			s := richtext.Plaintext(fmt.Sprintf("%v", v))
			const maxRunes = 100
			r := []rune(s)
			if len(r) > maxRunes {
				return string(r[:maxRunes]) + "…"
			}
			return s
		},
		// dpField извлекает имя поля из data_path вида "Объект.Контрагент"
		// (план 37, managed-формы). Если префикса нет — возвращает строку как есть.
		"dpField": func(s string) string {
			if i := strings.LastIndex(s, "."); i >= 0 {
				return s[i+1:]
			}
			return s
		},
		// dpRoot — корневой компонент data_path: "Объект.Товары.Цена" → "Объект".
		"dpRoot": func(s string) string {
			if i := strings.Index(s, "."); i >= 0 {
				return s[:i]
			}
			return s
		},
		// fieldByName ищет metadata.Field в entity.Fields по имени;
		// нужен managed-шаблону чтобы определить тип ввода (ref/enum/date/bool).
		"fieldByName": func(entity *metadata.Entity, name string) *metadata.Field {
			if entity == nil {
				return nil
			}
			for i := range entity.Fields {
				if entity.Fields[i].Name == name {
					return &entity.Fields[i]
				}
			}
			return nil
		},
		// fieldTitleRU достаёт ru-вариант из map[string]string или возвращает fallback.
		"fieldTitleRU": func(m map[string]string, fallback string) string {
			if v, ok := m["ru"]; ok && v != "" {
				return v
			}
			for _, v := range m {
				if v != "" {
					return v
				}
			}
			return fallback
		},
		// hasChildren — удобство для шаблона: проверка на пустой Children.
		"hasChildren": func(el *metadata.FormElement) bool {
			return el != nil && len(el.Children) > 0
		},
		// hasHandler — есть ли у элемента обработчик указанного события
		// (план 37, этап 8). Если есть — шаблон управляемой формы навешивает
		// onclick/onchange="obFire(...)" вызывающий /ui/{kind}/{entity}/form-event.
		"hasHandler": func(el *metadata.FormElement, eventName string) bool {
			if el == nil || el.Handlers == nil {
				return false
			}
			_, ok := el.Handlers[metadata.FormEventType(eventName)]
			return ok
		},
		// hasFormHandler — есть ли у формы (а не элемента) обработчик события.
		// Используется в managed-шаблоне для авто-вызова ПриОткрытииФормы при
		// загрузке страницы.
		"hasFormHandler": func(form *metadata.FormModule, eventName string) bool {
			if form == nil || form.Handlers == nil {
				return false
			}
			_, ok := form.Handlers[metadata.FormEventType(eventName)]
			return ok
		},
		// deleteHidden — скрыта ли кнопка «Удалить» формы через
		// actions.delete.visible=false (issue #151). По умолчанию (нет actions
		// или visible) — false: кнопка показывается по праву CanDelete.
		"deleteHidden": func(form *metadata.FormModule) bool {
			if form == nil || form.Actions == nil {
				return false
			}
			a, ok := form.Actions["delete"]
			if !ok || a == nil || a.Visible == nil {
				return false
			}
			return !*a.Visible
		},
		// tablePartByName ищет metadata.TablePart в Entity по имени.
		// Возвращает указатель на копию (или nil) — нужно managed-шаблону
		// для рендера ТабличнойЧасти с реальными колонками.
		"tablePartByName": func(entity *metadata.Entity, name string) *metadata.TablePart {
			if entity == nil {
				return nil
			}
			for i := range entity.TableParts {
				if entity.TableParts[i].Name == name {
					return &entity.TableParts[i]
				}
			}
			return nil
		},
		// tpCommandButtons возвращает дочерние элементы-кнопки табличной части —
		// команды ТЧ (план 46). Рендерятся как тулбар над таблицей; кнопка с
		// обработчиком Нажатие вызывает obFire(name,'Нажатие',{_tp:...}).
		"tpCommandButtons": func(el *metadata.FormElement) []*metadata.FormElement {
			if el == nil {
				return nil
			}
			var out []*metadata.FormElement
			for _, ch := range el.Children {
				if ch != nil && ch.Kind == metadata.FormElementButton {
					out = append(out, ch)
				}
			}
			return out
		},
		// hasGridTP checks if any TablePart element in the form has use_grid flag (plan 48).
		// Used to conditionally include SlickGrid CSS/JS.
		"hasGridTP": func(form *metadata.FormModule) bool {
			if form == nil {
				return false
			}
			found := false
			form.Walk(func(el *metadata.FormElement) bool {
				// SlickGrid включён для ТЧ по умолчанию; no_grid возвращает простую таблицу.
				if el != nil && el.Kind == metadata.FormElementTablePart && !el.NoGrid {
					found = true
					return false
				}
				return true
			})
			return found
		},
		// formAttrVT returns ValueTable columns for a form attribute by name.
		"formAttrVT": func(form *metadata.FormModule, name string) []*metadata.FormAttributeColumn {
			if form == nil {
				return nil
			}
			for _, attr := range form.Attributes {
				if attr.Name == name && strings.EqualFold(attr.TypeRef, "ValueTable") {
					return attr.Columns
				}
			}
			return nil
		},

		// dict собирает map[string]any из чередующихся ключей и значений —
		// стандартный приём передать несколько аргументов в подшаблон
		// (Go template принимает только один параметр).
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict требует чётное число аргументов")
			}
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: ключ #%d не string", i)
				}
				m[k] = values[i+1]
			}
			return m, nil
		},
		// navLabel вставляет zero-width space на границах слов PascalCase-имени
		// (перед заглавной после строчной), чтобы длинные имена объектов
		// переносились по словам в боковой панели, а не обрезались.
		"navLabel": func(s string) string {
			const zwsp = '​' // zero-width space — невидимая точка переноса
			var b strings.Builder
			var prev rune
			for i, r := range s {
				if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(prev) {
					b.WriteRune(zwsp)
				}
				b.WriteRune(r)
				prev = r
			}
			return b.String()
		},
		"fmtDate": func(v any) string {
			fmtT := func(t time.Time) string {
				lt := t.In(time.Local)
				h, m, sec := lt.Clock()
				if h != 0 || m != 0 || sec != 0 {
					return lt.Format("02.01.2006 15:04:05")
				}
				return lt.Format("02.01.2006")
			}
			if t, ok := v.(time.Time); ok {
				return fmtT(t)
			}
			if s, ok := v.(string); ok && len(s) >= 10 {
				// Strip Go monotonic clock suffix " m=+..."
				if i := strings.Index(s, " m=+"); i >= 0 {
					s = s[:i]
				}
				for _, layout := range []string{
					time.RFC3339, time.RFC3339Nano,
					"2006-01-02 15:04:05 -0700 MST",
					"2006-01-02 15:04:05.999999999 -0700 MST",
					"2006-01-02T15:04:05", "2006-01-02 15:04:05",
					"2006-01-02T15:04", "2006-01-02",
				} {
					if t, err := time.Parse(layout, s); err == nil {
						return fmtT(t)
					}
				}
				if len(s) >= 10 {
					if t, err := time.ParseInLocation("2006-01-02", s[:10], time.Local); err == nil {
						return fmtT(t)
					}
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
		"isActivityField": func(e *metadata.Entity, f metadata.Field) bool {
			return e != nil && e.Activity != nil && f.Name == e.Activity.Field
		},
		"activityQuery": func(params storage.ListParams, scope string) template.URL {
			var parts []string
			parts = append(parts, "activity="+url.QueryEscape(scope))
			if params.Search != "" {
				parts = append(parts, "q="+url.QueryEscape(params.Search))
			}
			if params.Sort != "" {
				parts = append(parts, "sort="+url.QueryEscape(params.Sort))
				if params.Dir != "" {
					parts = append(parts, "dir="+url.QueryEscape(params.Dir))
				}
			}
			for k, v := range params.Filters {
				if v.From != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+".from="+url.QueryEscape(v.From))
				}
				if v.To != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+".to="+url.QueryEscape(v.To))
				}
				if v.Value != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+"="+url.QueryEscape(v.Value))
				}
			}
			return template.URL(strings.Join(parts, "&"))
		},
		"listQuerySuffix": func(params storage.ListParams) template.URL {
			var parts []string
			if params.ActivityScope != "" {
				parts = append(parts, "activity="+url.QueryEscape(params.ActivityScope))
			}
			if params.Search != "" {
				parts = append(parts, "q="+url.QueryEscape(params.Search))
			}
			for k, v := range params.Filters {
				if v.From != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+".from="+url.QueryEscape(v.From))
				}
				if v.To != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+".to="+url.QueryEscape(v.To))
				}
				if v.Value != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+"="+url.QueryEscape(v.Value))
				}
			}
			if len(parts) == 0 {
				return ""
			}
			return template.URL("?" + strings.Join(parts, "&"))
		},
		"filterQuery": func(params storage.ListParams) template.URL {
			var parts []string
			if params.ActivityScope != "" {
				parts = append(parts, "activity="+url.QueryEscape(params.ActivityScope))
			}
			for k, v := range params.Filters {
				if v.From != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+".from="+url.QueryEscape(v.From))
				}
				if v.To != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+".to="+url.QueryEscape(v.To))
				}
				if v.Value != "" {
					parts = append(parts, "f."+url.QueryEscape(k)+"="+url.QueryEscape(v.Value))
				}
			}
			if len(parts) == 0 {
				return ""
			}
			return template.URL("&" + strings.Join(parts, "&"))
		},
		"reportParamQuery": func(params any, values map[string]any) string {
			type param interface{ GetName() string }
			// Use reflection-free approach: just iterate over values map
			parts := []string{}
			if values != nil {
				for k, v := range values {
					if v != nil && fmt.Sprintf("%v", v) != "" {
						parts = append(parts, k+"="+url.QueryEscape(fmt.Sprintf("%v", v)))
					}
				}
			}
			if len(parts) == 0 {
				return ""
			}
			return "?" + strings.Join(parts, "&")
		},
		// variantQuery дописывает выбранный вариант компоновки (__variant) к уже
		// собранной query-строке параметров отчёта — чтобы выгрузка в Excel
		// соответствовала выбранному на форме варианту.
		"variantQuery": func(existing string, variant any) string {
			vs, _ := variant.(string)
			if vs == "" {
				return existing
			}
			sep := "?"
			if existing != "" {
				sep = "&"
			}
			return existing + sep + "__variant=" + url.QueryEscape(vs)
		},
		"presetQuery": func(existing string, preset any) string {
			ps, _ := preset.(string)
			if ps == "" {
				return existing
			}
			sep := "?"
			if existing != "" {
				sep = "&"
			}
			return existing + sep + "__preset=" + url.QueryEscape(ps)
		},
		"settingsQuery": func(existing string, raw any, active any) string {
			if active == nil {
				return existing
			}
			rs, _ := raw.(string)
			if rs == "" {
				return existing
			}
			sep := "?"
			if existing != "" {
				sep = "&"
			}
			return existing + sep + "__settings=" + url.QueryEscape(rs)
		},
		"reportGroupChecked":   reportGroupChecked,
		"reportMeasureChecked": reportMeasureChecked,
		"mul":                  func(a, b int) int { return a * b },
		"int": func(v any) int {
			switch t := v.(type) {
			case int:
				return t
			case int64:
				return int(t)
			case float64:
				return int(t)
			case decimal.Decimal:
				return int(t.IntPart())
			}
			return 0
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
		"wcell":       widgetCell,
		"echartsJSON": echartsJSON,
		"splitCamel":  splitCamel,
		"fmtCell":     fmtReportCell,
		// pageRaw помечает уже санитизированный HTML страницы (план 66) как
		// безопасный. Источник — только ДобавитьСыройHTML, прошедший sanitizePageHTML.
		"pageRaw": func(s string) template.HTML { return template.HTML(s) },
		// pageChart конвертирует чарт-блок страницы в widget.ChartData для echartsJSON.
		"pageChart": pageChartData,
	}
}

func templateSource() string {
	return tplHead + tplNav + tplIndex + tplList + tplForm + tplManagedForm + tplRegister + tplReport + tplProcessor + tplAgentSettings + tplPOS + tplAbout + tplDeleteMarked + tplInfoReg + tplConstants + tplHistory + tplJournal + tplScheduled + tplAccountReg + tplQueryBuilder + tplAllFunctions + tplQueryConsole + tplCodeConsole + tplGengen + tplForbidden + tplPageCustom + tplAppShell
}

const tplHead = `
{{define "head"}}<!DOCTYPE html>
<html lang="ru"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="theme-color" content="#1e293b">
<link rel="manifest" href="/manifest.webmanifest">
<link rel="apple-touch-icon" href="/icons/icon-192.png">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-title" content="onebase">
<title>{{if .Cfg.AppName}}{{.Cfg.AppName}}{{else}}onebase{{end}}</title>
<script>
// Вкладочная оболочка (issue #129/#130): когда страница открыта во фрейме
// оболочки /ui/app, прячем хром (топбар/подсистемы) — навигация идёт из оболочки.
window.__obEmbedded = (window.self !== window.top);
if (window.__obEmbedded) {
  document.documentElement.className += ' ob-embedded';
  // Фаза 2: открытие записи/новой формы/отчёта внутри вкладки — это новая
  // вкладка рядом, а не замена текущей (пагинация/сортировка/фильтры остаются
  // в той же вкладке — у них тот же путь списка, без id-сегмента).
  var obOpenableForm = function (href) {
    if (!/^\/ui\//.test(href)) return false;
    if (/^\/ui\/(admin|about|logout|login|logo|debug|app|_)/.test(href)) return false;
    if (href.indexOf('_popup=1') >= 0) return false;
    if (/^\/ui\/(report|processor)\/[^\/?#]+/.test(href)) return true;     // отчёт/обработка
    if (/^\/ui\/[^\/?#]+\/[^\/?#]+\/[^\/?#]+/.test(href)) return true;      // {kind}/{entity}/{id|new}
    return false;
  };
  window.obOpenInShell = function (href, title, allowDup) {
    if (!obOpenableForm(href)) return false;
    var shell = null;
    try { if (window.parent && window.parent.obOpenTab) shell = window.parent; } catch (_) {}
    if (!shell) return false;
    try { shell.postMessage({ source: 'obOpenTab', url: href, title: title || 'Форма', allowDup: !!allowDup }, '*'); } catch (_) {}
    return true;
  };
  document.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target.closest ? e.target.closest('a[href]') : null;
    if (!a || a.target === '_blank') return;
    var href = a.getAttribute('href') || '';
    var title = (a.getAttribute('title') || a.textContent || '').replace(/\s+/g, ' ').trim() || 'Форма';
    if (!window.obOpenInShell(href, title)) return;
    e.preventDefault();
  });
  // Фаза 3: сообщаем оболочке о несохранённых правках, чтобы она предупредила при
  // закрытии вкладки/окна (защита от потери ввода).
  (function () {
    var dirty = false;
    function report(d) {
      if (d === dirty) return; dirty = d;
      try { if (window.parent && window.parent.obOpenTab) window.parent.postMessage({ source: 'obDirty', dirty: d }, '*'); } catch (_) {}
    }
    function onEdit(e) { var t = e.target; if (t && t.matches && t.matches('input,textarea,select')) report(true); }
    document.addEventListener('input', onEdit, true);
    document.addEventListener('change', onEdit, true);
    document.addEventListener('submit', function () { report(false); }, true); // правки уходят на сервер
  })();
}
</script>
<style>
.ob-embedded .topbar,.ob-embedded .subsys-bar,.ob-embedded #ob-nav{display:none!important}
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:system-ui,sans-serif;display:flex;flex-direction:column;min-height:100vh;background:#f5f5f5}
.topbar{background:#1e293b;color:#fff;padding:0 16px;display:flex;align-items:center;height:38px;flex-shrink:0;position:sticky;top:0;z-index:100}
.topbar-title{font-size:14px;font-weight:600;color:#7dd3fc;flex:1}
.sys-menu{position:relative}
.sys-btn{background:none;border:none;color:#cbd5e1;cursor:pointer;font-size:15px;padding:6px 10px;border-radius:5px;line-height:1}
.sys-btn:hover{background:#334155;color:#fff}
.sys-drop{display:none;position:absolute;right:0;top:calc(100% + 4px);background:#fff;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.18);min-width:170px;padding:4px 0;z-index:200}
.sys-drop.open{display:block}
.sys-drop>a,.sys-drop>button,.sys-drop>.sys-sub>a{display:block;padding:10px 16px;color:#334155;text-decoration:none;font-size:14px;width:100%;text-align:left;background:none;border:none;cursor:pointer;border-bottom:1px solid #f1f5f9}
.sys-drop>:last-child>a,.sys-drop>a:last-child,.sys-drop>button:last-child{border-bottom:none}
.sys-drop>a:hover,.sys-drop>button:hover,.sys-drop>.sys-sub:hover>a{background:#f1f5f9}
.sys-sub{position:relative}
.sys-sub>.sys-submenu{display:none;position:absolute;right:100%;top:-4px;background:#fff;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.18);min-width:220px;padding:4px 0;z-index:200}
.sys-sub:hover>.sys-submenu{display:block}
.sys-submenu a{display:block;padding:10px 16px;color:#334155;text-decoration:none;font-size:14px;border-bottom:1px solid #f1f5f9;white-space:nowrap}
.sys-submenu a:last-child{border-bottom:none}
.sys-submenu a:hover{background:#f1f5f9}
.tbl{width:100%;border-collapse:collapse}
.tbl th{text-align:left;padding:8px 10px;border-bottom:2px solid #e2e8f0;color:#64748b;font-weight:600;font-size:12px;position:sticky;top:0;background:#fff}
.tbl td{padding:6px 10px;border-bottom:1px solid #f1f5f9;color:#334155;font-size:13px}
.tbl tr:hover td{background:#f8fafc}
.report-composed{width:100%;border-collapse:collapse;font-size:13px}
.report-composed thead th{position:sticky;top:38px;background:#fff;z-index:5;text-align:left;padding:8px 10px;border-bottom:2px solid #e2e8f0;color:#64748b;font-weight:600;font-size:12px}
.report-composed td{padding:6px 10px;border-bottom:1px solid #f1f5f9;color:#334155}
.report-composed td.num{font-variant-numeric:tabular-nums}
.report-composed tr.grp:hover td{background:#f8fafc}
.report-composed tr.grand td{font-weight:700;border-top:2px solid #e2e8f0}
.report-composed.rep-lines-v td,.report-composed.rep-lines-v th{border-bottom:none;border-right:1px solid #eef2f7}
.report-composed.rep-lines-v td:last-child,.report-composed.rep-lines-v th:last-child{border-right:none}
.report-composed.rep-lines-both td,.report-composed.rep-lines-both th{border-right:1px solid #eef2f7}
.report-composed.rep-lines-both td:last-child,.report-composed.rep-lines-both th:last-child{border-right:none}
.report-composed.rep-lines-none td,.report-composed.rep-lines-none th{border-bottom:none}
.report-composed.rep-zebra tbody tr:nth-child(even){background:#fafbfc}
.app-body{display:flex;flex:1;overflow:hidden}
aside{width:210px;background:#1e293b;color:#fff;padding:16px 0;flex-shrink:0;overflow-y:auto}
aside .sec{font-size:11px;text-transform:uppercase;color:#94a3b8;margin:14px 12px 4px;letter-spacing:.05em}
aside a{display:block;padding:6px 14px;color:#cbd5e1;text-decoration:none;font-size:14px;margin:1px 6px;border-radius:5px;line-height:1.3;overflow-wrap:break-word}
aside a:hover{background:#334155;color:#fff}
aside details.navsec{margin:0;max-width:none;background:transparent;border-radius:0;box-shadow:none}
aside details.navsec>summary{padding:0;font-size:11px;text-transform:uppercase;color:#94a3b8;margin:14px 12px 4px;letter-spacing:.05em;cursor:pointer;list-style:none;user-select:none}
aside details.navsec>summary::-webkit-details-marker{display:none}
aside details.navsec>summary::before{content:"\25B8";display:inline-block;width:1em;color:#64748b}
aside details.navsec[open]>summary::before{content:"\25BE"}
aside details.navsec>summary:hover{color:#cbd5e1}
main{flex:1;padding:28px;overflow-y:auto}
h2{font-size:22px;font-weight:600;margin-bottom:20px;color:#1e293b}
h3{font-size:16px;font-weight:600;margin:24px 0 10px;color:#1e293b}
.card{background:#fff;border-radius:10px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,.1);max-width:1400px}
.main-list .card,.main-list .row-top,.main-list details,.main-list .breadcrumb{max-width:1600px}
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
.row-top{display:flex;justify-content:space-between;align-items:center;margin-bottom:16px;max-width:1400px}
details{margin-bottom:16px;max-width:1400px;background:#fff;border-radius:10px;box-shadow:0 1px 3px rgba(0,0,0,.1)}
details summary{padding:12px 20px;font-weight:600;font-size:14px;cursor:pointer;color:#475569;list-style:none}
details summary::-webkit-details-marker{display:none}
details summary::before{content:"▶ ";font-size:11px}
details[open] summary::before{content:"▼ "}
.filter-body{padding:0 20px 16px;display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px}
.filter-body label{font-size:12px;color:#64748b;margin-bottom:3px}
.filter-body input,.filter-body select{padding:7px 10px;font-size:13px}
.filter-actions{padding:0 20px 16px;display:flex;gap:10px}
.tp-table{width:100%;border-collapse:collapse;font-size:13px;margin-bottom:8px;border:1px solid #e2e8f0;border-radius:8px;overflow:hidden}
.tp-table th{background:#f8fafc;padding:8px 10px;text-align:left;font-size:12px;color:#475569;font-weight:600;border-bottom:2px solid #e2e8f0;white-space:nowrap}
.tp-table td{padding:5px 6px;border-bottom:1px solid #e2e8f0;border-right:1px solid #eef2f7;vertical-align:middle}
.tp-table tbody tr:nth-child(even) td{background:#f1f5f9}
.tp-table tbody tr:hover td{background:#eef4ff}
/* Инпуты прозрачны, чтобы зебра/выделение строки просвечивали; на фокусе — белый фон. */
.tp-table input,.tp-table select{padding:5px 8px;font-size:13px;border:1px solid transparent;border-radius:5px;width:100%;background:transparent;transition:border-color .15s,box-shadow .15s,background .15s}
.tp-table input:hover,.tp-table select:hover{border-color:#e2e8f0}
.tp-table input:focus,.tp-table select:focus{outline:none;background:#fff;border-color:#3b82f6;box-shadow:0 0 0 2px rgba(59,130,246,.15)}
.tp-table input[type=number]{text-align:right;font-variant-numeric:tabular-nums;-moz-appearance:textfield}
.tp-table input[type=number]::-webkit-inner-spin-button,.tp-table input[type=number]::-webkit-outer-spin-button{-webkit-appearance:none;margin:0}
.tp-table .del-btn{background:none;border:none;color:#94a3b8;cursor:pointer;font-size:15px;padding:0 4px;transition:color .15s}
.tp-table .del-btn:hover{color:#ef4444}
.tp-table .tp-footer td{font-weight:600;border-top:2px solid #e2e8f0;background:#f8fafc;font-variant-numeric:tabular-nums}
.subsys-bar{background:#0f172a;display:flex;padding:0 12px;gap:2px;flex-shrink:0}
.subsys-bar a{display:inline-block;padding:7px 18px;color:#94a3b8;text-decoration:none;font-size:13px;font-weight:500;border-bottom:3px solid transparent;transition:color .15s}
.subsys-bar a:hover{color:#e2e8f0;background:rgba(255,255,255,.04)}
.subsys-bar a.active{color:#7dd3fc;border-bottom-color:#3b82f6}
.subsys-bar a .ob-icon{width:15px;height:15px;vertical-align:-3px;margin-right:6px;opacity:.85}
.subsys-bar a:hover .ob-icon,.subsys-bar a.active .ob-icon{opacity:1}
.breadcrumb{display:flex;align-items:center;gap:6px;font-size:13px;color:#64748b;margin-bottom:12px;max-width:1400px;flex-wrap:wrap}
.breadcrumb a{color:#3b82f6;text-decoration:none}.breadcrumb a:hover{text-decoration:underline}
.breadcrumb span{color:#94a3b8;padding:0 2px}
/* Чтобы контент не накрывало панелью сообщений */
body{padding-bottom:32px}
/* ИИ-помощник: плавающая кнопка и панель чата (план 51, F3) */
#ob-ai-btn{position:fixed;right:18px;bottom:44px;z-index:320;width:48px;height:48px;border-radius:50%;background:#2563eb;color:#fff;border:none;cursor:pointer;font-size:22px;box-shadow:0 4px 14px rgba(37,99,235,.4)}
#ob-ai-btn:hover{background:#1d4ed8}
#ob-ai-panel{position:fixed;right:18px;bottom:44px;z-index:321;width:370px;max-width:calc(100vw - 24px);height:520px;max-height:calc(100vh - 80px);background:#fff;border:1px solid #cbd5e1;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.22);display:none;flex-direction:column;overflow:hidden;font-family:system-ui,sans-serif}
#ob-ai-panel.open{display:flex}
#ob-ai-head{background:#2563eb;color:#fff;padding:10px 14px;display:flex;align-items:center;gap:8px;font-weight:600;font-size:14px}
#ob-ai-head .sp{flex:1}
#ob-ai-head button{background:none;border:none;color:#fff;cursor:pointer;font-size:18px;line-height:1}
#ob-ai-log{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:10px;background:#f8fafc}
#ob-ai-log .m{max-width:85%;padding:8px 11px;border-radius:12px;font-size:13px;line-height:1.4;white-space:pre-wrap;word-break:break-word}
#ob-ai-log .m.u{align-self:flex-end;background:#2563eb;color:#fff;border-bottom-right-radius:3px}
#ob-ai-log .m.a{align-self:flex-start;background:#fff;border:1px solid #e2e8f0;color:#1e293b;border-bottom-left-radius:3px}
#ob-ai-log .m.err{align-self:stretch;background:#fef2f2;border:1px solid #fecaca;color:#b91c1c}
#ob-ai-log .hint{color:#94a3b8;font-size:12px;text-align:center;margin:auto 0}
#ob-ai-foot{border-top:1px solid #e2e8f0;padding:8px;display:flex;gap:6px;background:#fff}
#ob-ai-input{flex:1;resize:none;border:1px solid #cbd5e1;border-radius:8px;padding:8px;font-size:13px;font-family:inherit;max-height:90px}
#ob-ai-send{background:#2563eb;color:#fff;border:none;border-radius:8px;padding:0 14px;cursor:pointer;font-size:14px}
#ob-ai-send:disabled{opacity:.5;cursor:default}
/* Панель сообщений (как «Окно сообщений» в 1С) */
#ob-msg-bar{position:fixed;left:0;right:0;bottom:0;z-index:300;background:#fff;border-top:1px solid #cbd5e1;box-shadow:0 -2px 8px rgba(0,0,0,.08);font-family:system-ui,sans-serif;font-size:13px;color:#1e293b;transform:translateY(calc(100% - 30px));transition:transform .18s ease}
#ob-msg-bar.open{transform:translateY(0)}
#ob-msg-bar.hidden{display:none}
#ob-msg-head{height:30px;display:flex;align-items:center;padding:0 10px;cursor:pointer;background:#f1f5f9;user-select:none;gap:10px}
#ob-msg-head .ttl{font-weight:600;color:#334155;flex:1;display:flex;align-items:center;gap:8px}
#ob-msg-head .cnt{background:#ef4444;color:#fff;border-radius:10px;padding:1px 8px;font-size:11px;font-weight:700;min-width:18px;text-align:center;display:none}
#ob-msg-head .cnt.show{display:inline-block}
#ob-msg-head .arr{color:#64748b;font-size:11px;width:14px;text-align:center}
#ob-msg-bar.open #ob-msg-head .arr{transform:rotate(180deg)}
#ob-msg-head button{background:none;border:none;color:#64748b;cursor:pointer;font-size:12px;padding:4px 8px;border-radius:5px}
#ob-msg-head button:hover{background:#e2e8f0;color:#1e293b}
#ob-msg-list{max-height:200px;overflow-y:auto;padding:6px 0;background:#fff}
#ob-msg-list .it{padding:5px 14px;border-bottom:1px solid #f1f5f9;display:flex;gap:10px;align-items:flex-start;font-family:Consolas,monospace;font-size:12px;white-space:pre-wrap;word-break:break-word}
#ob-msg-list .it:last-child{border-bottom:none}
#ob-msg-list .it .t{color:#94a3b8;flex-shrink:0;font-size:11px;padding-top:1px}
#ob-msg-list .empty{padding:10px 14px;color:#94a3b8;font-style:italic}
/* ===== Мобильная адаптация (этап 45). Правила в @media применяются только на
   узких экранах — десктопная вёрстка выше остаётся неизменной. ===== */
.nav-toggle{display:none}
@media (max-width:820px){
  .nav-toggle{display:inline-flex;align-items:center;justify-content:center;background:none;border:none;color:#cbd5e1;font-size:20px;cursor:pointer;padding:4px 12px 4px 0;line-height:1}
  .nav-toggle:hover{color:#fff}
  .app-body{display:block;overflow:visible}
  aside{position:fixed;left:0;top:0;bottom:0;width:78vw;max-width:300px;z-index:401;transform:translateX(-100%);transition:transform .2s ease;box-shadow:2px 0 16px rgba(0,0,0,.3)}
  body.nav-open aside{transform:translateX(0)}
  body.nav-open::before{content:"";position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:400}
  main{padding:14px;overflow-y:visible}
  h2{font-size:19px;margin-bottom:14px}
  .card{padding:16px;max-width:none}
  .row-top{flex-wrap:wrap;gap:8px;align-items:stretch;max-width:none}
  aside a{padding:10px 14px}
  .btn{padding:10px 18px}
  .filter-body{grid-template-columns:1fr}
  /* широкие таблицы-гриды скроллятся по горизонтали внутри своей области, а не «едет» вся страница */
  main table{display:block;overflow-x:auto;white-space:nowrap;-webkit-overflow-scrolling:touch}
  /* …но верстальные key/value-таблицы (О программе, карточка задания, вывод
     обработки) остаются обычными: текст переносится, ширина не схлопывается. */
  main table.tbl-plain{display:table;white-space:normal;overflow:visible}
}
/* ===== Плиточный режим списка (Фаза 1a) ===== */
.view-switch{display:inline-flex;border:1px solid #e2e8f0;border-radius:7px;overflow:hidden;flex-shrink:0}
.view-switch .view-btn{padding:6px 11px;color:#64748b;text-decoration:none;font-size:15px;line-height:1;background:#fff;border-right:1px solid #e2e8f0}
.view-switch .view-btn:last-child{border-right:none}
.view-switch .view-btn:hover{background:#f1f5f9}
.view-switch .view-btn.active{background:#3b82f6;color:#fff}
.tile-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(210px,1fr));gap:14px}
.tile-card{position:relative;display:flex;flex-direction:column;border:1px solid #e2e8f0;border-radius:10px;padding:14px;background:#fff;cursor:pointer;transition:box-shadow .15s,border-color .15s}
.tile-card:hover{box-shadow:0 4px 14px rgba(0,0,0,.1);border-color:#cbd5e1}
.tile-card.tile-selected{border-color:#3b82f6;box-shadow:0 0 0 2px rgba(59,130,246,.25)}
.tile-card.tile-deleted{opacity:.5}
.tile-card.tile-deleted .tile-title{text-decoration:line-through}
.tile-img{width:100%;aspect-ratio:4/3;border-radius:8px;background:#f1f5f9 center/cover no-repeat;margin-bottom:10px;display:flex;align-items:center;justify-content:center;color:#cbd5e1;font-size:34px;flex-shrink:0}
.tile-title{font-size:15px;font-weight:600;color:#1e293b;margin-bottom:8px;word-break:break-word}
.tile-subtitle{font-size:13px;color:#64748b;margin-top:-4px;margin-bottom:8px;word-break:break-word}
.tile-posted{color:#16a34a;font-weight:700}
.tile-field{font-size:12.5px;color:#475569;margin-bottom:3px;display:flex;gap:5px;flex-wrap:wrap}
.tile-label{color:#94a3b8;flex-shrink:0}
.tile-val{color:#334155;word-break:break-word}
.tile-foot{margin-top:auto;padding-top:10px}
/* Поле-картинка на форме */
.img-field{display:flex;flex-direction:column;gap:8px;align-items:flex-start}
.img-preview img{max-width:240px;max-height:240px;border-radius:8px;border:1px solid #e2e8f0;display:block;background:#f8fafc}
.img-actions{display:flex;gap:8px;align-items:center}
.img-field label.btn{cursor:pointer;margin:0}
</style>
<script>
(function(){
  if(window.__obAiInit)return;window.__obAiInit=true;
  function init(){
    if(document.getElementById('ob-ai-btn'))return;
    fetch('/ui/ai/enabled').then(function(r){return r.json();}).then(function(d){
      if(d&&d.enabled)buildUI();
    }).catch(function(){});
  }
  function buildUI(){
    var btn=document.createElement('button');btn.id='ob-ai-btn';btn.title='ИИ-помощник';btn.textContent='🤖';
    var panel=document.createElement('div');panel.id='ob-ai-panel';
    panel.innerHTML='<div id="ob-ai-head"><span>🤖 ИИ-помощник</span><span class="sp"></span><button type="button" id="ob-ai-close" title="Закрыть">×</button></div>'+
      '<div id="ob-ai-log"><div class="hint">Спросите про данные, отчёт или как что-то сделать.</div></div>'+
      '<div id="ob-ai-foot"><textarea id="ob-ai-input" rows="1" placeholder="Ваш вопрос…"></textarea><button id="ob-ai-send" type="button" title="Отправить">▶</button></div>';
    document.body.appendChild(btn);document.body.appendChild(panel);
    var log=document.getElementById('ob-ai-log'),input=document.getElementById('ob-ai-input'),send=document.getElementById('ob-ai-send');
    var history=[];var busy=false;
    function open(){panel.classList.add('open');btn.style.display='none';input.focus();}
    function close(){panel.classList.remove('open');btn.style.display='';}
    btn.addEventListener('click',open);
    document.getElementById('ob-ai-close').addEventListener('click',close);
    function addMsg(role,text){
      var h=log.querySelector('.hint');if(h)h.remove();
      var d=document.createElement('div');d.className='m '+(role==='user'?'u':role==='error'?'err':'a');d.textContent=text;log.appendChild(d);log.scrollTop=log.scrollHeight;return d;
    }
    function doSend(){
      var t=input.value.trim();if(!t||busy)return;
      input.value='';addMsg('user',t);history.push({role:'user',content:t});
      busy=true;send.disabled=true;var pend=addMsg('assistant','…');
      fetch('/ui/ai/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({messages:history})})
        .then(function(r){return r.json();})
        .then(function(d){
          if(d&&d.ok){pend.textContent=d.text;history.push({role:'assistant',content:d.text});}
          else{history.pop();pend.className='m err';pend.textContent=(d&&d.error)||'Ошибка';}
        })
        .catch(function(){history.pop();pend.className='m err';pend.textContent='Ошибка сети';})
        .finally(function(){busy=false;send.disabled=false;log.scrollTop=log.scrollHeight;input.focus();});
    }
    send.addEventListener('click',doSend);
    input.addEventListener('keydown',function(e){if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();doSend();}});
    btn.style.display='';
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',init);else init();
})();
</script>
<script>
(function(){
  if(window.__obMsgInit)return;window.__obMsgInit=true;
  function init(){
    if(document.getElementById('ob-msg-bar'))return;
    var bar=document.createElement('div');bar.id='ob-msg-bar';bar.className='hidden';
    bar.innerHTML='<div id="ob-msg-head"><span class="ttl">Сообщения <span class="cnt" id="ob-msg-cnt">0</span></span><button type="button" id="ob-msg-clear" title="Очистить">Очистить</button><span class="arr">▲</span></div><div id="ob-msg-list"><div class="empty">Сообщений нет</div></div>';
    document.body.appendChild(bar);
    var list=document.getElementById('ob-msg-list'),cnt=document.getElementById('ob-msg-cnt'),head=document.getElementById('ob-msg-head'),btnClear=document.getElementById('ob-msg-clear');
    var prevSig=sessionStorage.getItem('obMsgSig')||'',prevOpen=sessionStorage.getItem('obMsgOpen')==='1',lastHtml='';
    function fmtTime(ts){try{var d=new Date(ts);var h=String(d.getHours()).padStart(2,'0'),m=String(d.getMinutes()).padStart(2,'0'),s=String(d.getSeconds()).padStart(2,'0');return h+':'+m+':'+s;}catch(e){return '';}}
    function escapeHtml(s){return String(s).replace(/[&<>"']/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});}
    function render(msgs){
      if(!msgs||!msgs.length){bar.classList.add('hidden');bar.classList.remove('open');list.innerHTML='<div class="empty">Сообщений нет</div>';lastHtml='';cnt.classList.remove('show');prevSig='';sessionStorage.removeItem('obMsgSig');return;}
      bar.classList.remove('hidden');
      var html='';for(var i=0;i<msgs.length;i++){var m=msgs[i];html+='<div class="it"><span class="t">'+fmtTime(m.time)+'</span><span>'+escapeHtml(m.text)+'</span></div>';}
      if(html!==lastHtml){
        // не перерисовывать пока пользователь выделяет текст внутри панели — иначе сбрасывается выделение
        var sel=window.getSelection?window.getSelection():null;
        if(!(sel&&!sel.isCollapsed&&sel.anchorNode&&list.contains(sel.anchorNode))){
          list.innerHTML=html;lastHtml=html;list.scrollTop=list.scrollHeight;
        }
      }
      cnt.textContent=msgs.length;cnt.classList.add('show');
      var sig=msgs.length?msgs[msgs.length-1].time+'|'+msgs.length:'';
      if(sig!==prevSig){bar.classList.add('open');prevOpen=true;sessionStorage.setItem('obMsgOpen','1');}
      else if(prevOpen){bar.classList.add('open');}
      prevSig=sig;sessionStorage.setItem('obMsgSig',sig);
    }
    head.addEventListener('click',function(e){if(e.target===btnClear)return;bar.classList.toggle('open');prevOpen=bar.classList.contains('open');sessionStorage.setItem('obMsgOpen',prevOpen?'1':'0');});
    btnClear.addEventListener('click',function(e){e.stopPropagation();fetch('/ui/messages/clear',{method:'POST'}).then(function(){render([]);});});
    function load(){fetch('/ui/messages').then(function(r){return r.json();}).then(function(d){render(d.messages||[]);}).catch(function(){});}
    load();setInterval(load,3000);
    document.addEventListener('submit',function(){setTimeout(load,400);},true);
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',init);else init();
})();
</script>
<script>if('serviceWorker'in navigator){window.addEventListener('load',function(){navigator.serviceWorker.register('/sw.js').catch(function(){});});}</script>
<script>
// onebaseDevice — тонкий мост браузер→локальный device-agent кассира.
// Сервер onebase к агенту не ходит (агент за NAT на машине кассира); ходит
// сам браузер кассира — он на той же машине, что и агент. Адрес и токен агента
// per-машина, поэтому живут в localStorage (см. «Настройки агента»).
window.onebaseDevice={
  get base(){return (localStorage.getItem('obAgentURL')||'http://127.0.0.1:8765').replace(/\/+$/,'');},
  get token(){return localStorage.getItem('obAgentToken')||'';},
  async call(path,body){
    const r=await fetch(this.base+path,{method:'POST',headers:{'Content-Type':'application/json','X-Agent-Token':this.token},body:JSON.stringify(body||{})});
    let d={};try{d=await r.json();}catch(e){}
    if(!r.ok)throw new Error(d.error||('HTTP '+r.status));
    return d;
  },
  health(){return fetch(this.base+'/health').then(function(r){return r.json();});},
  printReceipt(driver,params,receipt){return this.call('/print',{driver,params,receipt});},
  drawer(driver,params){return this.call('/drawer',{driver,params});},
  display(driver,params,lines){return this.call('/display',{driver,params,lines});},
  weight(driver,params){return this.call('/weight',{driver,params});},
  pay(driver,params,amount){return this.call('/pay',{driver,params,amount});},
  fiscal(driver,params,receipt){return this.call('/fiscal',{driver,params,receipt});},
  // events — SSE-поток сканера ШК в форму. EventSource не шлёт заголовки,
  // поэтому токен и параметры устройства передаются строкой запроса.
  events(driver,params,onCode){
    const q=new URLSearchParams(Object.assign({driver:driver,token:this.token},params||{}));
    const es=new EventSource(this.base+'/events?'+q.toString());
    es.onmessage=function(e){onCode(e.data,es);};
    return es;
  }
};
</script>
</head><body>
<script>
/* План 74: real-time-шина уведомлений сервер->браузер.
   Любая страница слушает window-событие 'onebase:<имя>'. Событие
   "уведомление" со строкой показывается тостом без дополнительного кода. */
(function(){
  if(window.__obEventsInit)return; window.__obEventsInit=true;
  function toast(text){
    var box=document.getElementById('ob-toasts');
    if(!box){box=document.createElement('div');box.id='ob-toasts';
      box.style.cssText='position:fixed;right:16px;bottom:16px;z-index:9999;display:flex;flex-direction:column;gap:8px;max-width:360px';
      (document.body||document.documentElement).appendChild(box);}
    var el=document.createElement('div');
    el.style.cssText='background:#1f2937;color:#fff;padding:10px 14px;border-radius:8px;box-shadow:0 6px 16px rgba(0,0,0,.25);font-size:14px;line-height:1.35;opacity:0;transition:opacity .2s';
    el.textContent=text; box.appendChild(el);
    requestAnimationFrame(function(){el.style.opacity='1';});
    setTimeout(function(){el.style.opacity='0';setTimeout(function(){el.remove();},250);},6000);
  }
  /* План 75 (телефония/CTI): входящий звонок -> «скрин-поп» на любой странице.
     Конфигурация публикует ОтправитьУведомление(логин,"звонок.входящий",
     {номер,клиент,ссылка,id}); здесь рисуем тост с именем клиента и ссылкой на
     карточку. Слушатель безвреден вне телефонии: срабатывает только на это
     событие. DOM собираем textContent/href — без innerHTML (защита от XSS). */
  function callToast(d){
    d=d||{};
    var box=document.getElementById('ob-toasts');
    if(!box){box=document.createElement('div');box.id='ob-toasts';
      box.style.cssText='position:fixed;right:16px;bottom:16px;z-index:9999;display:flex;flex-direction:column;gap:8px;max-width:360px';
      (document.body||document.documentElement).appendChild(box);}
    var el=document.createElement('div');
    el.style.cssText='position:relative;background:#065f46;color:#fff;padding:12px 28px 12px 14px;border-radius:8px;box-shadow:0 6px 16px rgba(0,0,0,.3);font-size:14px;line-height:1.4';
    var head=document.createElement('div'); head.style.cssText='font-weight:600;margin-bottom:4px';
    head.textContent='📞 Входящий звонок'; el.appendChild(head);
    var line=document.createElement('div');
    line.textContent=(d['номер']||'')+(d['клиент']?(' — '+d['клиент']):''); el.appendChild(line);
    var url=d['ссылка'];
    if(typeof url==='string'&&url.charAt(0)==='/'){
      var a=document.createElement('a'); a.href=url; a.textContent='Открыть карточку клиента';
      a.style.cssText='display:inline-block;margin-top:6px;color:#a7f3d0;text-decoration:underline'; el.appendChild(a);
    }
    var x=document.createElement('button'); x.textContent='×'; x.setAttribute('aria-label','Закрыть');
    x.style.cssText='position:absolute;top:4px;right:8px;background:none;border:none;color:#fff;font-size:18px;line-height:1;cursor:pointer';
    x.onclick=function(){el.remove();}; el.appendChild(x);
    box.appendChild(el);
    setTimeout(function(){if(el.parentNode)el.remove();},20000);
  }
  window.addEventListener('onebase:звонок.входящий',function(ev){callToast(ev.detail);});
  function connect(){
    if(typeof EventSource==='undefined')return;
    var es=new EventSource('/ui/events'); window.__obEvents=es;
    es.onmessage=function(ev){
      var msg; try{msg=JSON.parse(ev.data);}catch(e){return;}
      if(!msg||!msg.name)return;
      window.dispatchEvent(new CustomEvent('onebase:'+msg.name,{detail:msg.data}));
      if(msg.name==='уведомление'||msg.name==='notify'){
        toast(typeof msg.data==='string'?msg.data:JSON.stringify(msg.data));
      }
    };
    es.onerror=function(){}; /* EventSource переподключается сам */
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',connect);else connect();
})();
</script>
{{end}}
`

const tplNav = `
{{define "nav"}}
<header class="topbar">
  <button class="nav-toggle" type="button" aria-label="{{t $.Lang "Меню"}}" aria-controls="ob-nav" aria-expanded="false" onclick="obNavToggle()">&#9776;</button>
  <a href="/ui/" class="topbar-title" style="text-decoration:none;color:inherit" title="{{t $.Lang "Главная"}}">{{if .Cfg.Logo}}<img src="/ui/logo" alt="" style="height:22px;max-width:90px;vertical-align:middle;margin-right:6px;border-radius:2px">{{end}}⚡ {{if .Cfg.AppName}}{{.Cfg.AppName}}{{else}}onebase{{end}}</a>
  <form method="post" action="/ui/form-mode" style="display:inline;margin:0">
    {{if eq (printf "%v" .FormOpenMode) "tabs"}}
      <input type="hidden" name="mode" value="pages">
      <button type="submit" class="sys-btn" title="{{t $.Lang "Открывать формы отдельными страницами"}}">&#9645; {{t $.Lang "Страницы"}}</button>
    {{else}}
      <input type="hidden" name="mode" value="tabs">
      <button type="submit" class="sys-btn" title="{{t $.Lang "Открывать формы во вкладках"}}">&#10697; {{t $.Lang "Вкладки"}}</button>
    {{end}}
  </form>
  <div class="sys-menu">
    <button class="sys-btn" onclick="var d=document.getElementById('sysd');d.classList.toggle('open')">&#9881; {{t $.Lang "Система"}} &#9660;</button>
    <div class="sys-drop" id="sysd">
      <a href="/ui/about">{{t $.Lang "О программе"}}</a>
      {{if .IsAdmin}}
      <a href="/ui/admin/users">{{t $.Lang "Пользователи"}}</a>
      <a href="/ui/admin/roles">{{t $.Lang "Роли и права"}}</a>
      <a href="/ui/admin/sessions">{{t $.Lang "Активные пользователи"}}</a>
      <a href="/ui/admin/audit">{{t $.Lang "Журнал изменений"}}</a>
      <a href="/ui/admin/webhooks">{{t $.Lang "Журнал веб-хуков"}}</a>
      <a href="/ui/admin/scheduled">{{t $.Lang "Регламентные задания"}}</a>
      <a href="/ui/delete-marked">{{t $.Lang "Удалить помеченные"}}</a>
      <a href="/ui/admin/cleanup">{{t $.Lang "Очистка регистров"}}</a>
      {{end}}
      <a href="/ui/pos">{{t $.Lang "Рабочее место кассира (РМК)"}}</a>
      {{if .IsAdmin}}<a href="/ui/settings/agent">{{t $.Lang "Настройки агента оборудования"}}</a>{{end}}
      {{if .IsAdmin}}<a href="/ui/admin/extforms">{{t $.Lang "Внешние печатные формы"}}</a>{{end}}
      {{if .IsAdmin}}<a href="/ui/admin/extreports">{{t $.Lang "Внешние отчёты"}}</a>{{end}}
      {{if .IsAdmin}}<a href="/ui/admin/extprocessors">{{t $.Lang "Внешние обработки"}}</a>{{end}}
      {{if .IsAdmin}}<a href="/ui/all-functions">{{t $.Lang "Все функции"}}</a>{{end}}
      {{if .IsAdmin}}<div class="sys-sub"><a href="#" onclick="event.preventDefault()">{{t $.Lang "Инструменты разработчика"}} &#9654;</a>
      <div class="sys-submenu">
        <a href="/ui/dev/query-console">{{t $.Lang "Консоль запросов"}}</a>
        <a href="/ui/dev/code-console">{{t $.Lang "Консоль кода"}}</a>
        <a href="/ui/dev/gengen">{{t $.Lang "Gengen"}}</a>
      </div>
    </div>{{end}}
      <div style="border-top:1px solid #f1f5f9;padding:10px 16px">
        <div style="font-size:12px;color:#64748b;margin-bottom:6px;font-weight:600">{{t $.Lang "Режим открытия форм"}}</div>
        <form method="post" action="/ui/form-mode" style="margin:0;padding:0">
          <label style="display:block;font-size:13px;padding:2px 0;cursor:pointer"><input type="radio" name="mode" value="pages" {{if eq (printf "%v" .FormOpenModePersonal) "pages"}}checked{{end}}> {{t $.Lang "Отдельные страницы"}}</label>
          <label style="display:block;font-size:13px;padding:2px 0;cursor:pointer"><input type="radio" name="mode" value="tabs" {{if eq (printf "%v" .FormOpenModePersonal) "tabs"}}checked{{end}}> {{t $.Lang "Вкладки"}}</label>
          <label style="display:block;font-size:13px;padding:2px 0;cursor:pointer"><input type="radio" name="mode" value="default" {{if eq (printf "%v" .FormOpenModePersonal) ""}}checked{{end}}> {{t $.Lang "По умолчанию (глобально)"}}</label>
          <button type="submit" class="sys-btn" style="margin-top:6px">{{t $.Lang "Применить"}}</button>
        </form>
      </div>
      {{if .HasAuth}}{{if not .DenyPasswdChange}}<a href="/ui/profile/passwd">{{t $.Lang "Сменить пароль"}}</a>{{end}}{{end}}
      <form method="POST" action="/logout" style="margin:0;padding:0"><button type="submit" style="display:block;width:100%;padding:10px 16px;color:#dc2626;text-decoration:none;font-size:14px;text-align:left;background:none;border:none;border-top:1px solid #f1f5f9;cursor:pointer">{{t $.Lang "Выйти"}}</button></form>
    </div>
  </div>
</header>
{{if .Cfg.DemoMode}}
<div style="background:#f59e0b;color:#fff;text-align:center;padding:6px 16px;font-size:13px;font-weight:600;letter-spacing:.02em">
  ⚠️ {{t $.Lang "ДЕМО-РЕЖИМ"}}{{if .Cfg.DemoMessage}} — {{.Cfg.DemoMessage}}{{end}}
</div>
{{end}}
{{if .Subsystems}}
<nav class="subsys-bar">
  {{$home := t $.Lang "Главная"}}
  <a href="/ui/" class="{{if not .CurrentSubsystem}}active{{end}}">{{$home}}</a>
  {{/* #215.2: не дублируем ведущую «Главная», если в базе есть одноимённая
       подсистема (её представление совпадает с меткой домашней ссылки). */}}
  {{range .Subsystems}}{{if ne (.DisplayName $.Lang) $home}}<a href="/ui/?subsystem={{.Name}}" class="{{if eq .Name $.CurrentSubsystem}}active{{end}}">{{lucideIcon .Icon}}{{.DisplayName $.Lang}}</a>{{end}}{{end}}
</nav>
{{end}}
<div class="app-body">
<aside id="ob-nav">
  {{if not .Subsystems}}<a href="/ui/" style="display:block;padding:12px 14px 8px;color:#7dd3fc;font-weight:700;font-size:15px;text-decoration:none">{{t $.Lang "Главная"}}</a>{{end}}
  {{if .CollapsibleNav}}
  {{range .Nav}}
  <details class="navsec" data-navsec="{{.Kind}}"{{if .Open}} open{{end}}>
    <summary>{{.Kind}}</summary>
    {{range .Items}}<a href="{{.URL}}" title="{{.Label}}">{{navLabel .Label}}</a>
    {{end}}
  </details>
  {{end}}
  {{else}}
  {{range .Nav}}
  <div class="sec">{{.Kind}}</div>
  {{range .Items}}<a href="{{.URL}}" title="{{.Label}}">{{navLabel .Label}}</a>
  {{end}}{{end}}
  {{end}}
</aside>
<script>
(function(){
  if(window.__obNavInit)return;window.__obNavInit=true; // ob-nav drawer
  // Управление мобильным drawer. Состояние — класс body.nav-open; синхронно
  // обновляем aria-expanded кнопки ☰ (для скринридеров). На десктопе меню видно
  // всегда, обработчики безвредны (nav-open там не выставляется).
  function setNav(open){
    document.body.classList.toggle('nav-open',open);
    var btn=document.querySelector('.nav-toggle');
    if(btn)btn.setAttribute('aria-expanded',open?'true':'false');
  }
  window.obNavToggle=function(){setNav(!document.body.classList.contains('nav-open'));};
  // Тап по затемнению (вне меню и вне кнопки ☰) закрывает drawer.
  document.addEventListener('click',function(e){
    if(!document.body.classList.contains('nav-open'))return;
    if(e.target.closest&&e.target.closest('.nav-toggle'))return;
    var as=document.getElementById('ob-nav');
    if(as&&as.contains(e.target))return;
    setNav(false);
  },true);
  // Esc закрывает drawer (клавиатурная доступность).
  document.addEventListener('keydown',function(e){
    if(e.key==='Escape'&&document.body.classList.contains('nav-open'))setNav(false);
  });
})();
</script>
{{if .CollapsibleNav}}
<script>
(function(){
  try{
    document.querySelectorAll('aside details.navsec').forEach(function(d){
      var key='navsec:'+d.getAttribute('data-navsec');
      var saved=localStorage.getItem(key);
      if(saved==='1')d.open=true; else if(saved==='0')d.open=false;
      d.addEventListener('toggle',function(){localStorage.setItem(key,d.open?'1':'0');});
    });
  }catch(e){}
})();
</script>
{{end}}
{{end}}
`

const tplIndex = `
{{define "page-index"}}
{{template "head" .}}{{template "nav" .}}
<style>
.dash{display:flex;flex-direction:column;gap:14px;max-width:1280px}
.dash-row{display:flex;gap:14px;flex-wrap:wrap}
.dash-row > *{flex:1 1 220px;min-width:0}
/* Виджеты с табличным/широким содержимым шире компактных KPI */
.dash-row > .w-card-list,.dash-row > .w-card-chart,.dash-row > .w-card-recent{flex:1 1 360px}
.dash-grid{display:grid;grid-template-columns:repeat(12,1fr);gap:14px}
.w-card{background:#fff;border-radius:10px;padding:18px 20px;box-shadow:0 1px 3px rgba(0,0,0,.08);display:flex;flex-direction:column;min-height:120px}
.w-title{font-size:12px;text-transform:uppercase;letter-spacing:.05em;color:#64748b;font-weight:600;margin-bottom:8px}
.w-kpi-value{font-size:32px;font-weight:700;color:#0f172a;line-height:1.1;white-space:nowrap}
.w-kpi-sub{font-size:12px;color:#94a3b8;margin-top:6px}
.w-list{overflow-x:auto}
.w-list table{margin-top:4px;font-size:13px}
.w-list th{padding:6px 8px;font-size:11px;color:#64748b;border-bottom:1px solid #e2e8f0;text-align:left;background:transparent}
.w-list td{padding:6px 8px;border-bottom:1px solid #f1f5f9;font-size:13px;color:#334155}
.w-list td.right{text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap}
.w-list tr:last-child td{border-bottom:none}
.w-chart{min-height:240px}
.w-chart-canvas{width:100%;height:240px}
.w-actions-row{display:flex;flex-wrap:wrap;gap:8px;margin-top:4px}
.w-actions-row a{display:inline-block;padding:7px 14px;border-radius:7px;background:#3b82f6;color:#fff;text-decoration:none;font-size:13px;font-weight:500}
.w-actions-row a:hover{background:#2563eb}
.w-recent-row{display:flex;align-items:center;gap:10px;padding:6px 0;border-bottom:1px solid #f1f5f9;font-size:13px}
.w-recent-row:last-child{border-bottom:none}
.w-recent-row .e{color:#64748b;font-size:11px;text-transform:uppercase;letter-spacing:.04em;flex-shrink:0;min-width:140px;max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.w-recent-row a{color:#3b82f6;text-decoration:none;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.w-recent-row a:hover{text-decoration:underline}
.w-recent-row .ts{color:#94a3b8;font-size:11px;flex-shrink:0;font-variant-numeric:tabular-nums}
.w-error{background:#fef2f2;color:#dc2626;font-size:12px;padding:6px 10px;border-radius:6px;margin-top:6px;font-family:Consolas,monospace}
.w-empty{color:#94a3b8;font-size:13px;padding:6px 0;font-style:italic}
.w-default-hint{color:#64748b;font-size:13px;margin:6px 0 14px}
</style>
<main>
  <h2 style="margin-bottom:14px">{{.HomeTitle}}</h2>
  {{if .DefaultedHome}}<div class="w-default-hint">Стартовая страница не настроена — показаны последние документы из аудита. Создайте <code>config/home_page.yaml</code> и виджеты в <code>widgets/</code>, чтобы оформить дашборд.</div>{{end}}
  <div class="dash">
    {{range .WidgetRows}}
    <div class="dash-row">
      {{range .}}{{template "widget-card" .}}{{end}}
    </div>
    {{end}}
  </div>
</main></div>
<script>
window.__obWidgetCharts = window.__obWidgetCharts || {};
{{range .WidgetResults}}{{if and (eq .Type "chart") .Chart}}window.__obWidgetCharts["{{.Name}}"] = {{echartsJSON .Chart}};
{{end}}{{end}}
</script>
<script src="/vendor/echarts/echarts.min.js"></script>
<script>
(function(){
  function initCharts(){
    if(!window.echarts)return;
    var nodes=document.querySelectorAll('.w-chart-canvas[data-widget]');
    for(var i=0;i<nodes.length;i++){
      var node=nodes[i];
      if(node.getAttribute('data-ob-init'))continue; // не переинициализировать → нет повторного «моргания»
      var name=node.getAttribute('data-widget');
      var opt=window.__obWidgetCharts[name];
      if(!opt)continue;
      node.setAttribute('data-ob-init','1');
      try{
        var c=echarts.init(node);
        opt.animation=false; // мгновенная отрисовка без анимации входа (библиотека грузится с задержкой)
        if(opt.yAxis&&opt.yAxis.type==="value"){opt.yAxis.axisLabel={formatter:function(v){if(Math.abs(v)>=1e6)return(v/1e6).toFixed(1)+"M";if(Math.abs(v)>=1e3)return(v/1e3).toFixed(1)+"k";return v%1===0?v:v.toFixed(2)}};}
        c.setOption(opt);
        (function(c){window.addEventListener('resize',function(){c.resize();});})(c);
      }catch(e){console.error('chart init failed',e);}
    }
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',initCharts);else initCharts();
})();
</script>
</body></html>
{{end}}

{{define "widget-card"}}
<div class="w-card w-card-{{.Type}}">
  {{if .Title}}<div class="w-title">{{.Title}}</div>{{end}}
  {{if .Error}}<div class="w-error">{{.Error}}</div>
  {{else if eq .Type "kpi"}}{{template "widget-kpi-body" .}}
  {{else if eq .Type "list"}}{{template "widget-list-body" .}}
  {{else if eq .Type "chart"}}{{template "widget-chart-body" .}}
  {{else if eq .Type "actions"}}{{template "widget-actions-body" .}}
  {{else if eq .Type "recent"}}{{template "widget-recent-body" .}}
  {{end}}
</div>
{{end}}

{{define "widget-kpi-body"}}
  {{if .KPI}}<div class="w-kpi-value">{{.KPI.Display}}</div>{{else}}<div class="w-empty">нет данных</div>{{end}}
{{end}}

{{define "widget-list-body"}}
<div class="w-list">
  {{if .Rows}}
  <table>
    <thead><tr>{{range .Columns}}<th{{if eq .Align "right"}} style="text-align:right"{{end}}>{{.Label}}</th>{{end}}</tr></thead>
    <tbody>
    {{range .Rows}}
      {{$row := .}}
      <tr>
        {{range $.Columns}}
        <td{{if eq .Align "right"}} class="right"{{end}}>{{wcell $row .Field .Format}}</td>
        {{end}}
      </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<div class="w-empty">нет данных</div>{{end}}
</div>
{{end}}

{{define "widget-chart-body"}}
<div class="w-chart">
  {{if .Chart}}<div class="w-chart-canvas" data-widget="{{.Name}}"></div>
  {{else}}<div class="w-empty">нет данных</div>{{end}}
</div>
{{end}}

{{define "widget-actions-body"}}
<div class="w-actions-row">
  {{range .Actions}}<a href="{{.URL}}">{{.Label}}</a>{{else}}<div class="w-empty">нет действий</div>{{end}}
</div>
{{end}}

{{define "widget-recent-body"}}
{{if .Rows}}
  {{range .Rows}}
  {{$label := splitCamel (str (index . "entity_name"))}}
  <div class="w-recent-row">
    <span class="e" title="{{$label}}">{{$label}}</span>
    <a href="{{index . "_url"}}">{{index . "_title"}}</a>
    <span class="ts">{{fmtDate (index . "_ts")}}</span>
  </div>
  {{end}}
{{else}}<div class="w-empty">нет записей</div>{{end}}
{{end}}
`

const tplList = `
{{define "page-list"}}
{{template "head" .}}{{template "nav" .}}
<main class="main-list">
<div class="row-top">
  <h2>{{.Entity.DisplayName $.Lang}}</h2>
  <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
    <div class="view-switch">
      <a class="view-btn{{if and (not .TreeView) (not .TilesView)}} active{{end}}" href="?{{if .ParentStr}}parent={{.ParentStr}}&{{end}}{{if $.CurrentSubsystem}}subsystem={{$.CurrentSubsystem}}{{end}}{{filterQuery .Params}}" title="{{t $.Lang "Список"}}">☰</a>
      <a class="view-btn{{if .TilesView}} active{{end}}" href="?view=tiles{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}{{filterQuery .Params}}" title="{{t $.Lang "Плитка"}}">▦</a>
      {{if .Entity.Hierarchical}}<a class="view-btn{{if .TreeView}} active{{end}}" href="?view=tree{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}" title="{{t $.Lang "Дерево"}}">📂</a>{{end}}
    </div>
    {{if and .Entity.Activity (not .TreeView)}}
    <div class="view-switch" title="{{t $.Lang "Активность"}}">
      <a class="view-btn{{if eq .Params.ActivityScope "active"}} active{{end}}" href="?{{activityQuery .Params "active"}}{{if .TilesView}}&view=tiles{{end}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Активные"}}</a>
      <a class="view-btn{{if eq .Params.ActivityScope "inactive"}} active{{end}}" href="?{{activityQuery .Params "inactive"}}{{if .TilesView}}&view=tiles{{end}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Скрытые"}}</a>
      <a class="view-btn{{if eq .Params.ActivityScope "all"}} active{{end}}" href="?{{activityQuery .Params "all"}}{{if .TilesView}}&view=tiles{{end}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Все"}}</a>
    </div>
    {{end}}
    {{if not .TreeView}}
      {{if .Feed}}<a class="btn btn-secondary btn-sm" href="?lm=pages{{if .TilesView}}&view=tiles{{end}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if .Params.Search}}&q={{.Params.Search}}{{end}}{{filterQuery .Params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}" title="{{t $.Lang "Показывать постранично"}}">▤ {{t $.Lang "Страницы"}}</a>
      {{else}}<a class="btn btn-secondary btn-sm" href="?lm=feed{{if .TilesView}}&view=tiles{{end}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if .Params.Search}}&q={{.Params.Search}}{{end}}{{filterQuery .Params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}" title="{{t $.Lang "Лента с догрузкой по скроллу"}}">≣ {{t $.Lang "Лента"}}</a>{{end}}
    {{end}}
    {{if .Entity.Hierarchical}}
      {{if .UpURL}}<a class="btn btn-secondary btn-sm" href="{{.UpURL}}">{{t $.Lang "↑ Наверх"}}</a>{{end}}
      {{if .CanWrite}}
      <a class="btn btn-primary" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new?{{if .ParentStr}}parent={{.ParentStr}}&{{end}}subsystem={{$.CurrentSubsystem}}">{{t $.Lang "+ Элемент"}}</a>
      <a class="btn btn-secondary" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new?is_folder=true{{if .ParentStr}}&parent={{.ParentStr}}{{end}}">{{t $.Lang "📁 Группа"}}</a>
      {{end}}
    {{else}}
      {{if .CanWrite}}<a class="btn btn-primary" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "+ Создать"}}</a>{{end}}
    {{end}}
    <button type="button" id="list-actions-btn" class="btn btn-secondary" onclick="listActionsBtnClick(event)" title="{{t $.Lang "Команды для выбранной строки"}}">⚙ {{t $.Lang "Действия"}} ▾</button>
    <a class="btn btn-sm" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/excel{{listQuerySuffix .Params}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
  </div>
</div>
<form method="GET" style="display:flex;gap:8px;margin-bottom:12px;max-width:460px">
  <input type="text" name="q" value="{{.Params.Search}}" placeholder="{{t $.Lang "Поиск..."}}" style="flex:1;padding:7px 12px;border:1px solid #e2e8f0;border-radius:6px;font-size:14px" oninput="clearTimeout(window._srch);window._srch=setTimeout(()=>this.form.submit(),320)">
  {{if .Params.Search}}<a class="btn btn-sm" href="?" style="background:#e2e8f0;color:#475569;align-self:center">✕</a>{{end}}
  {{if .Entity.Activity}}<input type="hidden" name="activity" value="{{.Params.ActivityScope}}">{{end}}
  {{if $.CurrentSubsystem}}<input type="hidden" name="subsystem" value="{{$.CurrentSubsystem}}">{{end}}
</form>
{{if .Breadcrumbs}}
<nav class="breadcrumb">
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Корень"}}</a>
  {{range .Breadcrumbs}}<span>›</span><a href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{.ID}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{.Label}}</a>{{end}}
</nav>
{{end}}

{{$entity := .Entity}}{{$params := .Params}}{{$refOpts := .RefFilterOptions}}
<details{{if hasFilter $params}} open{{end}}>
  <summary>{{t $.Lang "Отбор"}}</summary>
  <form method="GET" action="">
  <div class="filter-body">
  {{range $entity.Fields}}{{$f := .}}
    {{if isActivityField $entity .}}
    {{else}}
    {{if eq (str .Type) "date"}}
      <div>
        <label>{{.DisplayName $.Lang}} {{t $.Lang "с"}}</label>
        <input type="date" name="f.{{.Name}}.from" value="{{(filterVal $params .Name).From}}">
      </div>
      <div>
        <label>{{.DisplayName $.Lang}} {{t $.Lang "по"}}</label>
        <input type="date" name="f.{{.Name}}.to" value="{{(filterVal $params .Name).To}}">
      </div>
    {{else if isRef (str .Type)}}
      <div>
        <label>{{.DisplayName $.Lang}}</label>
        <select name="f.{{.Name}}">
          <option value="">{{t $.Lang "— все —"}}</option>
          {{range index $refOpts .Name}}
          <option value="{{index . "id"}}" {{if eq (index . "id") (filterVal $params $f.Name).Value}}selected{{end}}>{{index . "_label"}}</option>
          {{end}}
        </select>
      </div>
    {{else}}
      <div>
        <label>{{.DisplayName $.Lang}}</label>
        <input type="text" name="f.{{.Name}}" value="{{(filterVal $params .Name).Value}}">
      </div>
    {{end}}
    {{end}}
  {{end}}
  </div>
  <div class="filter-actions">
    <button class="btn btn-primary btn-sm" type="submit">{{t $.Lang "Применить"}}</button>
    <a class="btn btn-sm" href="?" style="background:#e2e8f0;color:#475569">{{t $.Lang "Сбросить"}}</a>
  </div>
  {{if $entity.Activity}}<input type="hidden" name="activity" value="{{$params.ActivityScope}}">{{end}}
  {{if $params.Sort}}<input type="hidden" name="sort" value="{{$params.Sort}}"><input type="hidden" name="dir" value="{{$params.Dir}}">{{end}}
  {{if $.CurrentSubsystem}}<input type="hidden" name="subsystem" value="{{$.CurrentSubsystem}}">{{end}}
  </form>
</details>

<div class="card">
{{if .TreeView}}
{{/* ===== TREE VIEW ===== */}}
{{if .TreeRows}}
{{$treeCols := listColumns .Entity}}
<div style="overflow-x:auto">
<table><thead><tr>
  {{range $treeCols}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  <th style="width:90px"></th>
</tr></thead><tbody>
{{range .TreeRows}}{{$row := .}}{{$isFolder := index $row "is_folder"}}{{$depth := index $row "_depth"}}
<tr {{if index $row "deletion_mark"}}style="opacity:0.45;text-decoration:line-through;cursor:pointer"{{else}}style="cursor:pointer"{{end}}
  onclick="listRowClick(event,this)"
  ondblclick="listRowDblClick(event,this)"
  oncontextmenu="listCtxMenu(event,this)"
  data-tree-id="{{index $row "id"}}"
  data-tree-parent="{{index $row "parent_id"}}"
  data-predefined="{{if index $row "_is_predefined"}}1{{end}}"
  data-is-folder="{{if $isFolder}}1{{end}}"
  data-folder-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{index $row "id"}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}"
  data-mark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=1"
  data-del-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete"
  data-posted="{{if index $row "posted"}}1{{end}}"
  data-marked="{{if index $row "deletion_mark"}}1{{end}}"
  data-unpost-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/unpost"
  data-unmark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=0"
  data-activity-enabled="{{if $.Entity.Activity}}1{{end}}"
  data-activity-inactive="{{if index $row "_activity_inactive"}}1{{end}}"
  data-activity-hide-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/activity?active=0"
  data-activity-show-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/activity?active=1"
  data-open-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">
  {{range $i, $col := $treeCols}}
    {{if treeColumn $treeCols $i}}
      <td>
        <span style="display:inline-block;width:{{mul (int $depth) 20}}px"></span>
        {{if $isFolder}}
          <button type="button" class="tree-toggle" data-folder-id="{{index $row "id"}}" title="{{t $.Lang "Свернуть/Развернуть"}}"
            style="background:none;border:none;cursor:pointer;padding:0 2px;font-size:13px">▼</button>
          📁
        {{else}}📄{{end}}
        {{if eq (str $col.Type) "date"}}{{fmtDate (index $row $col.Name)}}{{else if isRichText (str $col.Type)}}{{richPlain (index $row $col.Name)}}{{else if isEnum (str $col.Type)}}{{enumLabel $.EnumLabels $col.Name (str (index $row $col.Name))}}{{else}}{{fmtCell (index $row $col.Name)}}{{end}}{{if index $row "_is_predefined"}} <span title="{{t $.Lang "Предопределённый"}}" style="color:#f59e0b;font-size:11px">★</span>{{end}}
      </td>
    {{else if eq (str $col.Type) "date"}}<td>{{fmtDate (index $row $col.Name)}}</td>
    {{else if isRichText (str $col.Type)}}<td style="color:#64748b">{{richPlain (index $row $col.Name)}}</td>
    {{else if isEnum (str $col.Type)}}<td>{{enumLabel $.EnumLabels $col.Name (str (index $row $col.Name))}}</td>
    {{else}}<td>{{fmtCell (index $row $col.Name)}}</td>{{end}}
  {{end}}
  <td>
    {{if $isFolder}}
      <a class="btn btn-sm btn-secondary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{index $row "id"}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "▶ Войти"}}</a>
    {{else}}
      <a class="btn btn-sm btn-primary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Открыть"}}</a>
    {{end}}
  </td>
</tr>{{end}}
</tbody></table>
</div>
{{else}}
<p class="empty">{{t $.Lang "Записей нет"}} — <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">{{t $.Lang "создать первую"}}</a></p>
{{end}}
{{else if .TilesView}}
{{/* ===== TILES VIEW (плитка) ===== */}}
{{if .Rows}}
{{$tile := tileView .Entity}}
<div class="tile-grid">
{{range .Rows}}{{$row := .}}{{$isFolder := index $row "is_folder"}}
<div class="tile-card{{if index $row "deletion_mark"}} tile-deleted{{end}}"
  onclick="listRowClick(event,this)"
  ondblclick="listRowDblClick(event,this)"
  oncontextmenu="listCtxMenu(event,this)"
  data-predefined="{{if index $row "_is_predefined"}}1{{end}}"
  data-is-folder="{{if $isFolder}}1{{end}}"
  data-folder-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{index $row "id"}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}"
  data-mark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=1"
  data-del-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete"
  data-posted="{{if index $row "posted"}}1{{end}}"
  data-marked="{{if index $row "deletion_mark"}}1{{end}}"
  data-unpost-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/unpost"
  data-unmark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=0"
  data-activity-enabled="{{if $.Entity.Activity}}1{{end}}"
  data-activity-inactive="{{if index $row "_activity_inactive"}}1{{end}}"
  data-activity-hide-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/activity?active=0"
  data-activity-show-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/activity?active=1"
  data-open-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">
  {{range $f := $tile.ImageFields}}{{$iv := index $row $f.Name}}
  <div class="tile-img"{{if $iv}} style="background-image:url('/ui/_image/{{$iv}}')"{{end}}>{{if not $iv}}🖼{{end}}</div>
  {{end}}
  {{with $tile.TitleField}}
    <div class="tile-title">{{if $.Entity.Hierarchical}}{{if $isFolder}}📁 {{else}}📄 {{end}}{{end}}{{fmtCell (index $row .Name)}}{{if index $row "_is_predefined"}} <span title="{{t $.Lang "Предопределённый элемент"}}" style="color:#f59e0b;font-size:11px">★</span>{{end}}{{if eq (str $.Entity.Kind) "document"}}{{if index $row "posted"}} <span class="tile-posted" title="{{t $.Lang "Проведён"}}">✓</span>{{end}}{{end}}</div>
  {{end}}
  {{with $tile.SubtitleField}}{{$v := index $row .Name}}{{if hasValue $v}}
    <div class="tile-subtitle">{{if eq (str .Type) "date"}}{{fmtDate $v}}{{else if isRichText (str .Type)}}{{richPlain $v}}{{else if isEnum (str .Type)}}{{enumLabel $.EnumLabels .Name (str $v)}}{{else}}{{fmtCell $v}}{{end}}</div>
  {{end}}{{end}}
  {{range $f := $tile.Fields}}{{$v := index $row $f.Name}}{{if hasValue $v}}
    <div class="tile-field"><span class="tile-label">{{$f.DisplayName $.Lang}}:</span> {{if eq (str $f.Type) "date"}}<span class="tile-val">{{fmtDate $v}}</span>{{else if isRichText (str $f.Type)}}<span class="tile-val">{{richPlain $v}}</span>{{else if isEnum (str $f.Type)}}<span class="tile-val">{{enumLabel $.EnumLabels $f.Name (str $v)}}</span>{{else if isImage (str $f.Type)}}<span class="tile-val">{{if $v}}<img src="/ui/_image/{{$v}}" style="height:28px;width:28px;object-fit:cover;border-radius:5px;vertical-align:middle" alt="">{{else}}—{{end}}</span>{{else}}<span class="tile-val">{{fmtCell $v}}</span>{{end}}</div>
  {{end}}{{end}}
  <div class="tile-foot">
    {{if and $isFolder $.Entity.Hierarchical}}
      <a class="btn btn-sm btn-secondary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{index $row "id"}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "▶ Войти"}}</a>
    {{else}}
      <a class="btn btn-sm btn-primary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Открыть"}}</a>
    {{end}}
  </div>
</div>{{end}}
</div>
{{else}}
<p class="empty">{{if .Params.Search}}{{t $.Lang "Ничего не найдено по запросу"}} «{{.Params.Search}}» — <a href="?">{{t $.Lang "сбросить поиск"}}</a>{{else}}{{t $.Lang "Записей нет"}} — <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">{{t $.Lang "создать первую"}}</a>{{end}}</p>
{{end}}
{{else}}
{{/* ===== LIST VIEW (default) ===== */}}
{{if .Rows}}
<div style="overflow-x:auto">
<table><thead><tr>
  {{if eq (str .Entity.Kind) "document"}}<th style="width:36px">✓</th>{{end}}
  {{range listColumns .Entity}}
  <th>
    <a href="?sort={{.Name}}&dir={{nextDir $params .Name}}{{filterQuery $params}}">
      {{.DisplayName $.Lang}} {{sortIcon $params .Name}}
    </a>
  </th>
  {{end}}
  <th style="width:90px"></th>
</tr></thead><tbody id="list-body">
{{range .Rows}}{{$row := .}}{{$isFolder := index $row "is_folder"}}
<tr {{if index $row "deletion_mark"}}style="opacity:0.45;text-decoration:line-through;cursor:pointer"{{else}}style="cursor:pointer"{{end}}
  onclick="listRowClick(event,this)"
  ondblclick="listRowDblClick(event,this)"
  oncontextmenu="listCtxMenu(event,this)"
  data-predefined="{{if index $row "_is_predefined"}}1{{end}}"
  data-is-folder="{{if $isFolder}}1{{end}}"
  data-folder-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{index $row "id"}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}"
  data-mark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=1"
  data-del-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete"
  data-posted="{{if index $row "posted"}}1{{end}}"
  data-marked="{{if index $row "deletion_mark"}}1{{end}}"
  data-unpost-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/unpost"
  data-unmark-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/delete?mark=0"
  data-activity-enabled="{{if $.Entity.Activity}}1{{end}}"
  data-activity-inactive="{{if index $row "_activity_inactive"}}1{{end}}"
  data-activity-hide-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/activity?active=0"
  data-activity-show-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}/activity?active=1"
  data-open-url="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">
  {{if eq (str $.Entity.Kind) "document"}}
    <td style="text-align:center">
      {{if index $row "posted"}}<span style="color:#16a34a;font-weight:700" title="{{t $.Lang "Проведён"}}">✓</span>{{else}}<span style="color:#94a3b8" title="{{t $.Lang "Не проведён"}}">—</span>{{end}}
    </td>
  {{end}}
  {{range listColumns $.Entity}}
    {{if eq (str .Type) "date"}}<td style="white-space:nowrap">{{fmtDate (index $row .Name)}}</td>
    {{else if isImage (str .Type)}}<td>{{$iv := index $row .Name}}{{if $iv}}<img src="/ui/_image/{{$iv}}" style="height:34px;width:34px;object-fit:cover;border-radius:5px;vertical-align:middle" alt="">{{else}}<span style="color:#cbd5e1">—</span>{{end}}</td>
    {{else if isRichText (str .Type)}}<td style="white-space:nowrap;color:#64748b">{{richPlain (index $row .Name)}}</td>
    {{else if isEnum (str .Type)}}<td style="white-space:nowrap">{{enumLabel $.EnumLabels .Name (str (index $row .Name))}}</td>
    {{else}}<td style="white-space:nowrap">{{if and (eq .Name "Наименование") $.Entity.Hierarchical}}{{if $isFolder}}📁 {{else}}📄 {{end}}{{end}}{{fmtCell (index $row .Name)}}{{if and (eq .Name "Наименование") (index $row "_is_predefined")}} <span title="{{t $.Lang "Предопределённый элемент"}}" style="color:#f59e0b;font-size:11px">★</span>{{end}}</td>{{end}}
  {{end}}
  <td>
    {{if and $isFolder $.Entity.Hierarchical}}
      <a class="btn btn-sm btn-secondary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent={{index $row "id"}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "▶ Войти"}}</a>
    {{else}}
      <a class="btn btn-sm btn-primary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Открыть"}}</a>
    {{end}}
  </td>
</tr>{{end}}
</tbody></table>
</div>
{{else}}
<p class="empty">{{if .Params.Search}}{{t $.Lang "Ничего не найдено по запросу"}} «{{.Params.Search}}» — <a href="?">{{t $.Lang "сбросить поиск"}}</a>{{else}}{{t $.Lang "Записей нет"}} — <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">{{t $.Lang "создать первую"}}</a>{{end}}</p>
{{end}}
{{end}}
</div>
{{if and .Feed (not .TreeView)}}
{{/* Лента: догрузка по скроллу. Без JS «Показать ещё» = переход на след. страницу. */}}
{{if .HasNext}}
<div id="feed-more" data-next="{{.NextPage}}" data-pages="{{.TotalPages}}" data-container="{{if .TilesView}}.tile-grid{{else}}#list-body{{end}}" data-item="{{if .TilesView}}.tile-card{{else}}tr{{end}}" style="margin-top:14px;text-align:center">
  <a class="btn btn-secondary btn-sm" href="?page={{.NextPage}}&lm=feed{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if .TilesView}}&view=tiles{{end}}{{if .Params.Search}}&q={{.Params.Search}}{{end}}{{filterQuery .Params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Показать ещё"}}</a>
</div>
{{end}}
{{if gt .Total 0}}<div style="color:#94a3b8;font-size:12px;margin-top:8px;text-align:center">{{t $.Lang "Загружено:"}} <span id="feed-loaded">{{len .Rows}}</span> {{t $.Lang "из"}} {{.Total}}</div>{{end}}
{{else if gt .TotalPages 1}}
<div style="display:flex;align-items:center;gap:8px;margin-top:12px;flex-wrap:wrap">
  {{if .HasPrev}}<a class="btn btn-secondary btn-sm" href="?page={{.PrevPage}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if .TilesView}}&view=tiles{{end}}{{if .Params.Search}}&q={{.Params.Search}}{{end}}{{filterQuery .Params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "← Назад"}}</a>{{end}}
  <span style="color:#64748b;font-size:13px">{{t $.Lang "Стр."}} {{.Page}} {{t $.Lang "из"}} {{.TotalPages}} ({{.Total}} {{t $.Lang "записей"}})</span>
  {{if .HasNext}}<a class="btn btn-secondary btn-sm" href="?page={{.NextPage}}{{if .ParentStr}}&parent={{.ParentStr}}{{end}}{{if .TilesView}}&view=tiles{{end}}{{if .Params.Search}}&q={{.Params.Search}}{{end}}{{filterQuery .Params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Вперёд →"}}</a>{{end}}
</div>
{{else if gt .Total 0}}
<div style="color:#94a3b8;font-size:12px;margin-top:8px">{{t $.Lang "Всего:"}} {{.Total}}</div>
{{end}}
</main>
<script>
var _isAdmin={{if .IsAdmin}}true{{else}}false{{end}};
var _canWrite={{if .CanWrite}}true{{else}}false{{end}};
var _canDelete={{if .CanDelete}}true{{else}}false{{end}};
var _canUnpost={{if .CanUnpost}}true{{else}}false{{end}};
var _listSel=null;
function listTitle(){
  var h=document.querySelector('h2');
  return h ? h.textContent.replace(/\s+/g,' ').trim() : 'Форма';
}
function listOpen(url,title){
  if(!url)return;
  try{ if(window.obOpenInShell && window.obOpenInShell(url,title||listTitle()))return; }catch(e){}
  window.location.href=url;
}
function listRowClick(e,tr){
  if(e.target.closest('a,button'))return;
  if(_listSel){_listSel.querySelectorAll('td').forEach(function(td){td.style.background='';});_listSel.classList.remove('tile-selected');}
  _listSel=tr;
  tr.querySelectorAll('td').forEach(function(td){td.style.background='#dbeafe';});
  tr.classList.add('tile-selected');
}
function listRowDblClick(e,tr){
  if(e.target.closest('a,button'))return;
  if(tr.dataset.isFolder==='1'){window.location.href=tr.dataset.folderUrl;}
  else{listOpen(tr.dataset.openUrl);}
}
// Tree view: collapse/expand subtrees
document.querySelectorAll('.tree-toggle').forEach(function(btn){
  btn.addEventListener('click',function(e){
    e.stopPropagation();
    var fid=btn.dataset.folderId;
    var expanded=btn.getAttribute('data-expanded')!=='0';
    treeSetVisible(fid,!expanded);
    btn.setAttribute('data-expanded',expanded?'0':'1');
    btn.textContent=expanded?'▶':'▼';
  });
});
function treeSetVisible(parentId,visible){
  document.querySelectorAll('[data-tree-parent="'+parentId+'"]').forEach(function(row){
    row.style.display=visible?'':'none';
    var childId=row.dataset.treeId;
    if(childId){treeSetVisible(childId,visible&&row.dataset.isFolder!=='1'||row.querySelector('.tree-toggle[data-expanded="1"]')!==null);}
  });
}
// listMenuItems — единый источник пунктов меню строки списка. Возвращает массив
// {label, fn, danger, disabled}. Используется и контекстным меню (ПКМ), и кнопкой
// «Действия» на панели — чтобы команды были доступны на мобильных без правой кнопки.
function listMenuItems(tr){
  var isPredefined=tr.dataset.predefined==='1';
  var isFolder=tr.dataset.isFolder==='1';
  var items=[];
  if(isFolder){
    items.push({label:'{{t $.Lang "▶ Войти в группу"}}',fn:function(){window.location.href=tr.dataset.folderUrl;}});
    items.push({label:'{{t $.Lang "Редактировать"}}',fn:function(){listOpen(tr.dataset.openUrl);}});
  } else {
    items.push({label:'{{t $.Lang "Открыть"}}',fn:function(){listOpen(tr.dataset.openUrl);}});
  }
  if(_canWrite&&tr.dataset.activityEnabled==='1'){
    if(tr.dataset.activityInactive==='1')items.push({label:'{{t $.Lang "Вернуть в выбор"}}',fn:function(){listSubmit(tr.dataset.activityShowUrl,'{{t $.Lang "Вернуть в выбор?"}}');}});
    else items.push({label:'{{t $.Lang "Скрыть из выбора"}}',fn:function(){listSubmit(tr.dataset.activityHideUrl,'{{t $.Lang "Скрыть из выбора?"}}');}});
  }
  if(_canDelete){
    if(!isPredefined)items.push({label:'{{t $.Lang "Пометить на удаление"}}',danger:true,fn:function(){listSubmit(tr.dataset.markUrl,'Пометить на удаление?');}});
    else items.push({label:'{{t $.Lang "Предопределённый — нельзя удалить"}}',disabled:true});
  }
  if(_canUnpost&&tr.dataset.posted==='1')items.push({label:'{{t $.Lang "Отменить проведение"}}',fn:function(){listSubmit(tr.dataset.unpostUrl,'{{t $.Lang "Отменить проведение?"}}');}});
  if(_canDelete&&tr.dataset.marked==='1'&&!isPredefined)items.push({label:'{{t $.Lang "Снять пометку на удаление"}}',fn:function(){listSubmit(tr.dataset.unmarkUrl,'{{t $.Lang "Снять пометку на удаление?"}}');}});
  if(_isAdmin&&!isPredefined)items.push({label:'{{t $.Lang "Удалить навсегда"}}',danger:true,fn:function(){listSubmit(tr.dataset.delUrl,'Удалить запись навсегда?');}});
  return items;
}
// showListMenu — рендерит выпадающее меню #_lctx по координатам (x,y) во viewport.
function showListMenu(items,x,y){
  var old=document.getElementById('_lctx');if(old)old.remove();
  var m=document.createElement('div');
  m.id='_lctx';
  m.style.cssText='position:fixed;z-index:999;background:#fff;border:1px solid #c8d0de;border-radius:6px;box-shadow:0 4px 16px rgba(0,0,0,.18);padding:4px 0;min-width:190px;font-size:13px';
  m.style.left=x+'px';m.style.top=y+'px';
  items.forEach(function(item){
    var mi=document.createElement('div');
    mi.textContent=item.label;
    if(item.disabled){
      mi.style.cssText='padding:8px 14px;color:#94a3b8;cursor:default;font-style:italic';
    } else {
      mi.style.cssText='padding:8px 14px;cursor:pointer'+(item.danger?';color:#dc2626':'');
      mi.onmouseenter=function(){mi.style.background='#f8fafc';};
      mi.onmouseleave=function(){mi.style.background='';};
      mi.onclick=function(){m.remove();item.fn();};
    }
    m.appendChild(mi);
  });
  document.body.appendChild(m);
  setTimeout(function(){
    document.addEventListener('click',function h(){m.remove();document.removeEventListener('click',h);},{once:true});
  },0);
}
function listCtxMenu(e,tr){
  if(e.target.closest('a,button'))return;
  e.preventDefault();
  listRowClick(e,tr);
  showListMenu(listMenuItems(tr),e.clientX,e.clientY);
}
// Кнопка «Действия» на панели: открывает то же меню под кнопкой по выбранной строке.
// На мобильных нет ПКМ — это единственный способ вызвать команды строки.
function listActionsBtnClick(e){
  e.preventDefault();
  if(!_listSel){alert('{{t $.Lang "Сначала выберите строку списка"}}');return;}
  var r=e.currentTarget.getBoundingClientRect();
  showListMenu(listMenuItems(_listSel),r.left,r.bottom);
}
function listSubmit(url,msg){
  if(!url)return;
  if(confirm(msg)){var f=document.createElement('form');f.method='POST';f.action=url;document.body.appendChild(f);f.submit();}
}
document.addEventListener('keydown',function(e){
  if(e.key==='Delete'&&_listSel&&_canDelete)listSubmit(_listSel.dataset.markUrl,'Пометить на удаление?');
});
// Лента (feed): догрузка следующих страниц по скроллу. Тянет обычную страницу
// списка и переносит из неё строки/карточки в текущий контейнер. Деградация без
// JS: «Показать ещё» — это обычная ссылка на следующую страницу.
(function(){
  var more=document.getElementById('feed-more');
  if(!more)return;
  var loading=false,done=false;
  function stop(){done=true;if(more&&more.parentNode)more.parentNode.removeChild(more);}
  function loadNext(){
    if(loading||done)return;
    var n=parseInt(more.getAttribute('data-next'),10);
    var pages=parseInt(more.getAttribute('data-pages'),10);
    if(!n||n>pages){stop();return;}
    var sel=more.getAttribute('data-container');
    var c=document.querySelector(sel);
    if(!c){stop();return;}
    loading=true;
    var sp=new URLSearchParams(window.location.search);
    sp.set('page',n);sp.set('lm','feed');
    fetch(window.location.pathname+'?'+sp.toString(),{credentials:'same-origin'})
      .then(function(r){return r.text();})
      .then(function(html){
        var doc=new DOMParser().parseFromString(html,'text/html');
        var items=doc.querySelectorAll(sel+' > '+more.getAttribute('data-item'));
        if(!items.length){stop();return;}
        items.forEach(function(el){c.appendChild(document.importNode(el,true));});
        var loaded=document.getElementById('feed-loaded');if(loaded)loaded.textContent=c.children.length;
        n++;more.setAttribute('data-next',n);
        loading=false;
        if(n>pages){stop();return;}
        var rect=more.getBoundingClientRect();
        if(rect.top<(window.innerHeight||document.documentElement.clientHeight)+300)loadNext();
      })
      .catch(function(){loading=false;});
  }
  more.addEventListener('click',function(e){var a=e.target.closest('a');if(a){e.preventDefault();loadNext();}});
  if('IntersectionObserver' in window){
    new IntersectionObserver(function(ents){ents.forEach(function(en){if(en.isIntersecting)loadNext();});},{rootMargin:'300px'}).observe(more);
  }
})();
</script>
</div></body></html>
{{end}}
`

const tplForm = `
{{define "page-form"}}
{{template "head" .}}{{if not .IsPopup}}{{template "nav" .}}{{end}}
<main>
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;max-width:1400px">
  <h2 style="margin-bottom:0">{{if .IsNew}}{{t $.Lang "Создать"}}{{else}}{{t $.Lang "Редактировать"}}{{end}} — {{.Entity.DisplayName $.Lang}}</h2>
  {{if .IsPopup}}
  <a href="javascript:void(0)" onclick="try{parent.postMessage({source:'obRefCancel'}, '*')}catch(e){}" title="{{t $.Lang "Закрыть"}}" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
  {{else}}
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" title="{{t $.Lang "Закрыть"}}" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
  {{end}}
</div>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}

{{if not .IsPopup}}
{{/* Top toolbar */}}
<div style="display:flex;align-items:center;gap:8px;margin-bottom:16px;flex-wrap:wrap">
  {{if .Entity.Posting}}
    {{if not .IsNew}}
      {{if eq (index .Values "posted") "true"}}
        <span style="color:#16a34a;font-weight:600;font-size:13px">✓ {{t $.Lang "Проведён"}}</span>
      {{else}}
        <span style="color:#94a3b8;font-size:13px">{{t $.Lang "Не проведён"}}</span>
      {{end}}
    {{end}}
  {{end}}
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form">{{t $.Lang "Записать"}}</button>{{end}}
  {{if .Entity.Posting}}
    {{if ne (index .Values "deletion_mark") "true"}}
      {{if $.CanPost}}<button class="btn btn-primary" type="submit" name="_action" value="post" form="main-form">{{t $.Lang "Провести"}}</button>{{end}}
      {{if $.CanPost}}<button class="btn btn-post" type="submit" name="_action" value="post_and_close" form="main-form">{{t $.Lang "Провести и закрыть"}}</button>{{end}}
    {{end}}
    {{if not .IsNew}}
      {{if eq (index .Values "posted") "true"}}
        {{if $.CanUnpost}}<button class="btn btn-sm" style="background:#e2e8f0;color:#374151" form="form-unpost" type="submit">{{t $.Lang "Отменить проведение"}}</button>{{end}}
      {{end}}
    {{end}}
  {{end}}
  {{if and .CanDelete (not .IsNew) (eq (index .Values "deletion_mark") "true")}}
    <form method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/delete?mark=0" style="display:inline">
      <button class="btn btn-sm btn-secondary" type="submit">{{t $.Lang "Снять пометку на удаление"}}</button>
    </form>
  {{end}}
  {{if not .IsNew}}
    <a href="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/history" class="btn btn-sm btn-secondary">{{t $.Lang "История"}}</a>
    {{if or .AllPrintForms .HasPrintProc}}
    <div style="position:relative">
      <button type="button" class="btn btn-sm btn-secondary" onclick="var d=this.nextElementSibling;d.style.display=d.style.display==='none'?'block':'none'">{{t $.Lang "Печать"}} ▾</button>
      <div style="display:none;position:absolute;top:100%;left:0;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.1);min-width:200px;z-index:50;margin-top:4px">
        {{range .AllPrintForms}}
        <div style="display:flex;align-items:center;border-bottom:1px solid #f1f5f9">
          <a href="/ui/{{lower (str $.Entity.Kind)}}/{{$.Entity.Name}}/{{$.ID}}/print/{{.Name}}" target="_blank"
             style="flex:1;display:block;padding:9px 16px;color:#334155;text-decoration:none;font-size:13px">{{.Name}}{{if .External}} <span style="color:#94a3b8;font-size:11px">({{t $.Lang "внешняя"}})</span>{{end}}</a>
          <a href="/ui/{{lower (str $.Entity.Kind)}}/{{$.Entity.Name}}/{{$.ID}}/print/{{.Name}}/pdf" target="_blank"
             style="padding:9px 14px;color:#16a34a;text-decoration:none;font-size:12px;font-weight:600">PDF</a>
        </div>
        {{end}}
        {{if .HasPrintProc}}
        <a href="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/print/_module" target="_blank"
           style="display:block;padding:9px 16px;color:#334155;text-decoration:none;font-size:13px;border-bottom:1px solid #f1f5f9">📋 {{t $.Lang "Печать (модуль)"}}</a>
        {{end}}
      </div>
    </div>
    {{end}}
    {{if .Receivers}}
    <div style="position:relative">
      <button type="button" class="btn btn-sm btn-secondary" onclick="var d=this.nextElementSibling;d.style.display=d.style.display==='none'?'block':'none'">{{t $.Lang "Ввести на основании"}} ▾</button>
      <div style="display:none;position:absolute;top:100%;left:0;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.1);min-width:200px;z-index:50;margin-top:4px">
        {{range .Receivers}}
        <a href="/ui/{{lower (str .Kind)}}/{{.Name}}/new?based_on={{$.Entity.Name}}&based_on_id={{$.ID}}"
           style="display:block;padding:9px 16px;color:#334155;text-decoration:none;font-size:13px;border-bottom:1px solid #f1f5f9">{{.DisplayName $.Lang}}</a>
        {{end}}
      </div>
    </div>
    {{end}}
    {{if .CanDelete}}
    <form method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/delete"
          onsubmit="return confirm('{{if .IsAdmin}}{{t $.Lang "Удалить запись навсегда?"}}{{else}}{{t $.Lang "Пометить запись на удаление?"}}{{end}}')" style="margin-left:auto">
      <button class="btn btn-danger btn-sm" type="submit">{{if .IsAdmin}}{{t $.Lang "Удалить"}}{{else}}{{t $.Lang "Пометить на удаление"}}{{end}}</button>
    </form>
    {{end}}
  {{end}}
</div>

{{/* Movement links (collapsed) */}}
{{if and (not .IsNew) .DocMovements}}
<div style="margin-bottom:12px;display:flex;gap:6px;flex-wrap:wrap">
  {{range $regName, $rows := .DocMovements}}
  <details style="display:inline">
    <summary style="display:inline;cursor:pointer;font-size:12px;padding:4px 10px;background:#f0f4ff;color:#1a4a80;border-radius:4px;font-weight:600;list-style:none">
      {{$regName}} ({{len $rows}}) ▾
    </summary>
    <div style="position:absolute;z-index:100;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.12);margin-top:4px;min-width:300px;max-height:300px;overflow:auto">
      <table class="list-tbl" style="font-size:12px;margin:0">
        <tr><th>{{t $.Lang "№"}}</th><th>{{t $.Lang "Вид"}}</th>{{$first := index $rows 0}}{{range $k, $v := $first}}{{if and (ne $k "line_number") (ne $k "вид_движения")}}<th>{{$k}}</th>{{end}}{{end}}</tr>
        {{range $i, $row := $rows}}
        <tr>
          <td>{{add $i 1}}</td>
          <td>{{if eq (index $row "вид_движения") "Приход"}}<span style="color:#16a34a">▲</span>{{else if eq (index $row "вид_движения") "Расход"}}<span style="color:#dc2626">▼</span>{{else}}—{{end}}</td>
          {{range $k, $v := $row}}{{if and (ne $k "line_number") (ne $k "вид_движения")}}<td>{{$v}}</td>{{end}}{{end}}
        </tr>
        {{end}}
      </table>
    </div>
  </details>
  {{end}}
</div>
{{end}}
{{end}}{{/* end if not .IsPopup */}}

<div class="card">
<form id="main-form" method="POST">
{{if and (not .IsNew) (index .Values "_version")}}<input type="hidden" name="_version" value="{{index .Values "_version"}}">{{end}}
{{if .IsPopup}}<input type="hidden" name="_popup" value="1">{{end}}
{{if .Entity.Hierarchical}}
<div class="form-group">
  <label>{{t $.Lang "Тип"}}</label>
  <select name="is_folder">
    <option value="false" {{if ne (index $.Values "is_folder") "true"}}selected{{end}}>{{t $.Lang "Элемент"}}</option>
    <option value="true" {{if eq (index $.Values "is_folder") "true"}}selected{{end}}>{{t $.Lang "Группа"}}</option>
  </select>
</div>
<div class="form-group">
  <label>{{t $.Lang "Родительская группа"}}</label>
  <select name="parent_id">
    <option value="">{{t $.Lang "— корень —"}}</option>
    {{range .FolderOptions}}
    <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values "parent_id")}}selected{{end}}>{{index . "_label"}}</option>
    {{end}}
  </select>
</div>
{{end}}
{{range .Entity.Fields}}{{$fn := .Name}}{{$flabel := .DisplayName $.Lang}}
<div class="form-group">
  <label>{{$flabel}}</label>
  {{if isRef (str .Type)}}
    <div style="display:flex;gap:6px;align-items:center">
      <select id="ref-{{$fn}}" name="{{$fn}}" style="flex:1" data-ref-entity="{{.RefEntity}}"{{if .InlineCreateEnabled false}} data-ref-allow-create="1"{{end}}>
        <option value="">{{t $.Lang "— выбрать —"}}</option>
        {{range index $.RefOptions $fn}}
        <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values $fn)}}selected{{end}}>{{index . "_label"}}</option>
        {{end}}
      </select>
      <button type="button" onclick="openRefPicker('ref-{{$fn}}')" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      <button type="button" onclick="openRefCurrent('ref-{{$fn}}')" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
    </div>
  {{else if isEnum (str .Type)}}
    <select name="{{$fn}}">
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range index $.EnumOptions $fn}}
      <option value="{{.Value}}" {{if eq .Value (index $.Values $fn)}}selected{{end}}>{{.Label}}</option>
      {{end}}
    </select>
  {{else if eq (str .Type) "date"}}
    <input type="datetime-local" name="{{$fn}}" value="{{index $.Values $fn}}">
  {{else if eq (str .Type) "bool"}}
    <select name="{{$fn}}">
      <option value="false" {{if eq (index $.Values $fn) "false"}}selected{{end}}>{{t $.Lang "Нет"}}</option>
      <option value="true"  {{if eq (index $.Values $fn) "true"}}selected{{end}}>{{t $.Lang "Да"}}</option>
    </select>
  {{else if isRichText (str .Type)}}
    {{/* textarea — скрытое form-backing поле (хранит санитизированный HTML).
         Без JS остаётся видимым и рабочим (прогрессивное улучшение). С JS
         Quill (этап 2) инициализируется над .richtext-editor и синхронизирует
         содержимое обратно в textarea перед submit — серверный санитайзер
         обрабатывает результат. */}}
    <textarea name="{{$fn}}" class="richtext-field" rows="8" style="width:100%">{{index $.Values $fn}}</textarea>
    <div class="richtext-editor"></div>
  {{else if isImage (str .Type)}}
    {{/* Поле-картинка: скрытый input хранит ссылку (UUID), превью + загрузка/очистка.
         Без JS остаётся скрытый input — значение не теряется при записи. */}}
    {{$iv := index $.Values $fn}}
    <div class="img-field">
      <input type="hidden" name="{{$fn}}" value="{{$iv}}">
      <div class="img-preview"{{if not $iv}} style="display:none"{{end}}><img src="{{if $iv}}/ui/_image/{{$iv}}{{end}}" alt=""></div>
      <div class="img-actions">
        <label class="btn btn-sm btn-secondary">{{t $.Lang "Загрузить…"}}<input type="file" accept="image/*" style="display:none" onchange="obImageUpload(this,'/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/_image')"></label>
        <button type="button" class="btn btn-sm img-clear-btn" onclick="obImageClear(this)"{{if not $iv}} style="display:none"{{end}}>{{t $.Lang "Очистить"}}</button>
      </div>
    </div>
  {{else}}
    <input type="text" name="{{$fn}}" value="{{index $.Values $fn}}" placeholder="{{$flabel}}">
  {{end}}
</div>
{{end}}

{{range .Entity.TableParts}}{{$tp := .}}{{$tpName := .Name}}{{$tpRef := index $.TPRefOptions $tpName}}
<h3>{{$tp.DisplayName $.Lang}}</h3>
<table class="tp-table">
  <thead><tr>
    {{range .Fields}}<th>{{.DisplayName $.Lang}}</th>{{end}}
    <th style="width:40px"></th>
  </tr></thead>
  <tbody id="tp-body-{{$tpName}}">
  {{$existingRows := index $.TablePartRows $tpName}}
  {{range $i, $row := $existingRows}}
    <tr>
      {{range $tp.Fields}}{{$fn := .Name}}
        <td>
        {{if isRef (str .Type)}}
          <div style="display:flex;gap:4px;align-items:center">
            <select name="tp.{{$tpName}}.{{$i}}.{{$fn}}" style="flex:1" data-ref-entity="{{.RefEntity}}"{{if .InlineCreateEnabled true}} data-ref-allow-create="1"{{end}}>
              <option value="">{{t $.Lang "— выбрать —"}}</option>
              {{range index $tpRef $fn}}
              <option value="{{index . "id"}}" {{if eq (str (index . "id")) (refID (index $row $fn))}}selected{{end}}>{{index . "_label"}}</option>
              {{end}}
            </select>
            <button type="button" onclick="openRefPicker(this.parentElement.querySelector('select'))" style="padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
            <button type="button" onclick="openRefCurrent(this.parentElement.querySelector('select'))" style="padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
          </div>
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
  <tfoot id="tp-foot-{{$tpName}}" class="tp-footer" style="display:none"><tr>
    {{range .Fields}}{{if eq (str .Type) "number"}}<td class="tp-total" data-tp-total="{{$tpName}}.{{.Name}}" style="text-align:right;font-variant-numeric:tabular-nums">0</td>{{else}}<td></td>{{end}}{{end}}<td></td>
  </tr></tfoot>
</table>
<button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin-bottom:8px"
  onclick="addTpRow('{{$tpName}}', [{{range .Fields}}'{{.Name}}',{{end}}], [{{range .Fields}}{{if eq (str .Type) "number"}}'{{.Name}}',{{end}}{{end}}], document.getElementById('tp-body-{{$tpName}}').rows.length)">
  + {{t $.Lang "Добавить строку"}}
</button>
{{end}}

<div style="margin-top:16px">
  {{if .IsPopup}}
  {{if .CanWrite}}<button class="btn btn-primary" type="submit" name="_action" value="" form="main-form">{{t $.Lang "Записать и выбрать"}}</button>{{end}}
  <a href="javascript:void(0)" onclick="try{parent.postMessage({source:'obRefCancel'}, '*')}catch(e){}" class="btn btn-cancel">{{t $.Lang "Отмена"}}</a>
  {{else}}
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form">{{t $.Lang "Записать"}}</button>{{end}}
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" class="btn btn-cancel">{{t $.Lang "Отмена"}}</a>
  {{end}}
</div>
</form>
{{if and (not .IsNew) .Entity.Posting}}
{{if eq (index .Values "posted") "true"}}
<form id="form-unpost" method="POST" action="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/{{.ID}}/unpost"></form>
{{end}}
{{end}}
<script>
// Несохранённые изменения: звёздочка в заголовке вкладки браузера (аналог «*» в
// 1С) и предупреждение при любом уходе со страницы (крестик, клик по ссылке,
// закрытие/обновление). Сохранение формы сбрасывает флаг.
(function(){
  window._obFormDirty = false;
  var base = document.title;
  function mark(){ window._obFormDirty = true; if (document.title.charAt(0) !== '●') document.title = '● ' + base; }
  var f = document.getElementById('main-form');
  if (f) {
    f.addEventListener('input',  mark, true);
    f.addEventListener('change', mark, true);
    f.addEventListener('submit', function(){ window._obFormDirty = false; });
  }
  window.addEventListener('beforeunload', function(e){
    if (window._obFormDirty) { e.preventDefault(); e.returnValue = ''; return ''; }
  });
})();
</script>
{{template "ob-attachments" .}}
</div>
{{template "form-shared-js" .}}
</main></div></body></html>
{{end}}

{{/* ob-attachments — панель вложений к записи (issue #152). Общая для
     page-form и page-managed-form; бэкенд один (handlers_attachments.go,
     роуты POST /ui/<kind>/<entity>/<id>/attachments). Показывается для
     сохранённой записи объекта (не новая, не попап, не обработка). */}}
{{define "ob-attachments"}}
{{if and (not .IsNew) (not .IsPopup) (not .IsProcessor)}}
<div class="card" style="margin-top:16px">
  <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">
    <h3 style="margin:0;font-size:14px;font-weight:600;color:#374151">{{t $.Lang "Вложения"}}</h3>
    <span id="att-count" style="color:#94a3b8;font-size:12px"></span>
  </div>
  <div id="att-list" style="margin-bottom:12px"></div>
  <form id="att-upload-form" method="POST" enctype="multipart/form-data"
        action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/attachments">
    <input type="file" name="file" id="att-file-input" style="display:none" onchange="document.getElementById('att-upload-form').submit()">
    <button type="button" class="btn btn-sm btn-secondary" onclick="document.getElementById('att-file-input').click()">
      + {{t $.Lang "Прикрепить файл"}}
    </button>
  </form>
</div>
<script>
(function(){
  function fmtSize(b) {
    if(b<1024) return b+' Б';
    if(b<1024*1024) return (b/1024).toFixed(1)+' КБ';
    return (b/1024/1024).toFixed(1)+' МБ';
  }
  function loadAtts() {
    fetch('/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/attachments')
      .then(r=>r.json()).then(atts=>{
        var cnt = document.getElementById('att-count');
        var list = document.getElementById('att-list');
        cnt.textContent = atts.length ? atts.length+' файл(ов)' : '';
        if(!atts.length){ list.innerHTML='<p style="color:#94a3b8;font-size:13px;margin:0">Нет вложений</p>'; return; }
        // Имя файла (a.filename) приходит от пользователя и НЕ доверенное —
        // строим DOM безопасно: имя кладём через textContent, чтобы исключить
        // хранимый XSS (например имя «<img src=x onerror=...>.txt»). id —
        // серверный UUID, подставляем только в кодированном виде.
        list.innerHTML='';
        atts.forEach(function(a){
          var row=document.createElement('div');
          row.style.cssText='display:flex;align-items:center;gap:8px;padding:6px 0;border-bottom:1px solid #f1f5f9';
          var nameEl=document.createElement('span');
          nameEl.style.cssText='flex:1;font-size:13px;word-break:break-all';
          nameEl.textContent=String(a.filename==null?'':a.filename);
          var sizeEl=document.createElement('span');
          sizeEl.style.cssText='color:#94a3b8;font-size:12px;white-space:nowrap';
          sizeEl.textContent=fmtSize(a.size_bytes);
          var aid=encodeURIComponent(String(a.id));
          var dl=document.createElement('a');
          dl.href='/ui/attachments/'+aid+'/download';
          dl.className='btn btn-sm btn-secondary';
          dl.style.cssText='padding:3px 10px;font-size:12px';
          dl.textContent='↓';
          var delForm=document.createElement('form');
          delForm.method='POST';
          delForm.action='/ui/attachments/'+aid+'/delete';
          delForm.style.margin='0';
          delForm.addEventListener('submit',function(e){ if(!confirm('Удалить вложение?')) e.preventDefault(); });
          var delBtn=document.createElement('button');
          delBtn.type='submit';
          delBtn.className='btn btn-sm btn-danger';
          delBtn.style.cssText='padding:3px 8px;font-size:12px';
          delBtn.textContent='×';
          delForm.appendChild(delBtn);
          row.appendChild(nameEl); row.appendChild(sizeEl); row.appendChild(dl); row.appendChild(delForm);
          list.appendChild(row);
        });
      }).catch(function(){});
  }
  loadAtts();
})();
</script>
{{end}}
{{end}}

{{/* form-shared-js — общий <script> блок, используется page-form и
     page-managed-form. Внутри: глобалы window._tpRefOpts/_tpRefMeta,
     функции addTpRow / recalcTpRow / openRefPicker / openRefCreate. */}}
{{define "form-shared-js"}}
{{if entityHasRichText .Entity}}
{{/* Quill (WYSIWYG для richtext-полей, план 65 этап 2). Вендор-ассеты грузятся
     ТОЛЬКО когда у сущности есть richtext-реквизит. Прогрессивное улучшение:
     без JS textarea.richtext-field остаётся видимым и рабочим; при загрузке
     Quill монтируется на соседний .richtext-editor, textarea скрывается и
     служит form-backing полем (Quill пишет в неё HTML перед submit — серверный
     санитайзер обрабатывает результат). */}}
<link rel="stylesheet" href="/vendor/quill/quill.snow.css">
<script src="/vendor/quill/quill.js"></script>
<script>
(function(){
  function initRichText(){
    if (typeof Quill === "undefined") return; // ассет не загрузился — textarea остаётся
    var fields = document.querySelectorAll("textarea.richtext-field");
    fields.forEach(function(ta){
      // Контейнер Quill — соседний .richtext-editor; нет (read-only) → пропуск.
      var holder = ta.nextElementSibling;
      if (!holder || !holder.classList || !holder.classList.contains("richtext-editor")) return;
      if (holder.getAttribute("data-ql-ready") === "1") return;
      holder.setAttribute("data-ql-ready", "1");
      var q = new Quill(holder, {
        theme: "snow",
        modules: { toolbar: [
          [{ "header": [1, 2, 3, false] }],
          ["bold", "italic", "underline", "strike"],
          [{ "list": "ordered" }, { "list": "bullet" }],
          ["blockquote", "link", "image"],
          ["clean"]
        ]}
      });
      // Инициализация содержимым из textarea (санитизированный HTML с сервера).
      // Грузим через clipboard.convert → Delta, а НЕ через root.innerHTML:
      // innerHTML вставляет сырой DOM мимо парсера Quill, контент не попадает
      // в Delta/Parchment-модель, и при повторном открытии сохранённого
      // документа семантические <ul>/<ol> (без data-list) не распознаются
      // нативным list-blot → списки искажаются при первом же редактировании.
      // clipboard.convert прогоняет HTML через matcher (<ul>→bullet,<ol>→ordered)
      // и строит корректную Delta. "silent" — без записи в историю undo.
      q.setContents(q.clipboard.convert({ html: ta.value }), "silent");
      ta.style.display = "none";
      // Синхронизация: на каждое изменение и принудительно перед submit.
      // normalizeLists: Quill 2.x все списки рендерит как <ol> с
      // <li data-list="bullet|ordered">, а маркер рисует CSS-псевдоэлементом.
      // Наш санитайзер вырезает data-list → маркированный список схлопнулся бы в
      // нумерованный. Поэтому перед записью переводим Quill-разметку в
      // семантические <ul>/<ol> (оба в allowlist санитайзера).
      function normalizeLists(html){
        var box = document.createElement("div");
        box.innerHTML = html;
        box.querySelectorAll("ol").forEach(function(ol){
          var items = Array.prototype.slice.call(ol.children).filter(function(el){
            return el.tagName === "LI";
          });
          if (!items.length) return;
          // Тип top-level списка определяем по первому <li>: data-list="bullet"
          // → <ul>, иначе оставляем <ol>. (Вложенность в Quill 2.x — это НЕ
          // отдельные <ol>, а плоские <li class="ql-indent-N"> в том же списке;
          // вложенные уровни/отступы вне MVP — классы ql-indent вырезает
          // санитайзер, список схлопывается в один уровень.)
          var isBullet = items[0].getAttribute("data-list") === "bullet";
          if (isBullet) {
            var ul = document.createElement("ul");
            while (ol.firstChild) ul.appendChild(ol.firstChild);
            ol.parentNode.replaceChild(ul, ol);
          }
        });
        // Убираем служебные атрибуты/узлы Quill (вырезались бы санитайзером,
        // но чище отдать уже без них).
        box.querySelectorAll("li[data-list]").forEach(function(li){ li.removeAttribute("data-list"); });
        box.querySelectorAll(".ql-ui").forEach(function(n){ n.remove(); });
        return box.innerHTML;
      }
      function sync(){ ta.value = normalizeLists(q.root.innerHTML); }
      q.on("text-change", sync);
      var form = ta.form;
      if (form) form.addEventListener("submit", sync);
    });
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initRichText);
  } else {
    initRichText();
  }
})();
</script>
{{end}}
<script>
// Поле типа image: загрузка картинки и очистка. Работают по DOM от элемента
// внутри .img-field; ссылка (UUID) кладётся в скрытый input поля и сохраняется
// вместе с формой (как обычное строковое значение).
function obImageUpload(input, url){
  var file = input.files && input.files[0];
  if(!file){ return; }
  var wrap = input.closest('.img-field');
  var fd = new FormData(); fd.append('file', file);
  fetch(url, {method:'POST', body:fd, credentials:'same-origin'})
    .then(function(resp){ if(!resp.ok){ return resp.text().then(function(t){ throw new Error(t||('HTTP '+resp.status)); }); } return resp.json(); })
    .then(function(data){
      if(!wrap || !data || !data.ref){ return; }
      wrap.querySelector('input[type=hidden]').value = data.ref;
      var prev = wrap.querySelector('.img-preview');
      if(prev){ var img=prev.querySelector('img'); if(img){ img.src='/ui/_image/'+data.ref; } prev.style.display=''; }
      var clr = wrap.querySelector('.img-clear-btn'); if(clr){ clr.style.display=''; }
    })
    .catch(function(e){ alert('{{t $.Lang "Ошибка загрузки картинки"}}: '+e.message); })
    .finally(function(){ input.value=''; });
}
function obImageClear(btn){
  var wrap = btn.closest('.img-field'); if(!wrap){ return; }
  var hidden = wrap.querySelector('input[type=hidden]'); if(hidden){ hidden.value=''; }
  var prev = wrap.querySelector('.img-preview');
  if(prev){ prev.style.display='none'; var img=prev.querySelector('img'); if(img){ img.removeAttribute('src'); } }
  btn.style.display='none';
}
window._tpRefOpts = {{jsJSON .TPRefOptions}};
window._tpRefMeta = {{jsJSON .TPRefMeta}};
function addTpRow(tpName, fields, numFields, idx) {
  var tbody = document.getElementById('tp-body-' + tpName);
  var tr = document.createElement('tr');
  var refOpts = (window._tpRefOpts && window._tpRefOpts[tpName]) || {};
  var refMeta = (window._tpRefMeta && window._tpRefMeta[tpName]) || {};
  // У ТЧ с командной панелью (план 46) первая колонка — чекбокс выделения;
  // добавляем и при ручном создании строки, чтобы колонки совпали с thead.
  if (tbody && tbody.getAttribute('data-tp-cmd') === '1') {
    var tdSel = document.createElement('td');
    tdSel.style.textAlign = 'center';
    var cbSel = document.createElement('input');
    cbSel.type = 'checkbox'; cbSel.className = '_tp-sel';
    tdSel.appendChild(cbSel);
    tr.appendChild(tdSel);
  }
  fields.forEach(function(fn) {
    var td = document.createElement('td');
    if (refOpts[fn] !== undefined) {
      var wrapper = document.createElement('div');
      wrapper.style.cssText = 'display:flex;gap:4px;align-items:center';
      var sel = document.createElement('select');
      sel.name = 'tp.' + tpName + '.' + idx + '.' + fn;
      sel.style.flex = '1';
      // Метаданные для picker'а: entity нужен для лупы (/_ref-open) и
      // кнопки «+ Создать» внутри picker'а; allowCreate управляет показом «+».
      var meta = refMeta[fn];
      if (meta && meta.entity) {
        sel.setAttribute('data-ref-entity', meta.entity);
        if (meta.allowCreate) sel.setAttribute('data-ref-allow-create', '1');
      }
      var defOpt = document.createElement('option');
      defOpt.value = ''; defOpt.textContent = '— выбрать —';
      sel.appendChild(defOpt);
      (refOpts[fn] || []).forEach(function(opt) {
        var o = document.createElement('option');
        o.value = opt.id; o.textContent = opt._label || opt.id;
        sel.appendChild(o);
      });
      var pickBtn = document.createElement('button');
      pickBtn.type = 'button'; pickBtn.textContent = '...';
      pickBtn.title = 'Выбрать из списка';
      pickBtn.style.cssText = 'padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0';
      (function(s){ pickBtn.onclick = function(){ openRefPicker(s); }; })(sel);
      wrapper.appendChild(sel);
      wrapper.appendChild(pickBtn);
      // Лупа — открыть карточку текущего значения (только если есть entity).
      if (meta && meta.entity) {
        var openBtn = document.createElement('button');
        openBtn.type = 'button'; openBtn.textContent = '🔍';
        openBtn.title = 'Открыть карточку';
        openBtn.style.cssText = 'padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0';
        (function(s){ openBtn.onclick = function(){ openRefCurrent(s); }; })(sel);
        wrapper.appendChild(openBtn);
      }
      td.appendChild(wrapper);
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
// Then recalculate totals in tfoot (Phase 0 CSS-refresh).
function recalcTpRow(inp) {
  var tr = inp.closest('tr');
  var nums = tr.querySelectorAll('[data-tp-num]');
  if (nums.length === 3) {
    var a = parseFloat(nums[0].value) || 0;
    var b = parseFloat(nums[1].value) || 0;
    nums[2].value = (a * b).toFixed(2);
  }
  // Update totals in tfoot
  recalcTpTotals(inp);
}
function recalcTpTotals(inp) {
  var tbody = inp.closest('tbody');
  if (!tbody) return;
  var table = tbody.closest('table');
  if (!table) return;
  var tfoot = table.querySelector('tfoot');
  if (!tfoot) return;
  var totals = {};
  var numFields = [];
  tbody.querySelectorAll('[data-tp-num]').forEach(function(el) {
    var fn = el.getAttribute('data-tp-num');
    if (totals[fn] === undefined) { totals[fn] = 0; numFields.push(fn); }
    totals[fn] += parseFloat(el.value) || 0;
  });
  var hasData = false;
  numFields.forEach(function(fn) {
    tfoot.querySelectorAll('[data-tp-total]').forEach(function(cell) {
      var key = cell.getAttribute('data-tp-total');
      if (key && key.split('.').pop() === fn) {
        cell.textContent = totals[fn].toLocaleString('ru-RU', {minimumFractionDigits:0, maximumFractionDigits:2});
      }
    });
    if (totals[fn] !== 0) hasData = true;
  });
  tfoot.style.display = hasData ? '' : 'none';
}
// Init totals on page load (Phase 0)
document.addEventListener('DOMContentLoaded', function() {
  document.querySelectorAll('.tp-table tfoot').forEach(function(tfoot) {
    var table = tfoot.closest('table');
    if (!table) return;
    var tbody = table.querySelector('tbody');
    if (!tbody || !tbody.rows.length) return;
    // Trigger recalc by finding first numeric input
    var firstNum = tbody.querySelector('[data-tp-num]');
    if (firstNum) recalcTpTotals(firstNum);
  });
});
// openRefPicker — единая точка для всех действий со ссылочным полем по
// аналогии с 1С: модалка показывает список, кнопку «+ Создать» (если поле
// помечено data-ref-allow-create) и иконку-лупу у каждой строки для
// перехода в карточку (data-ref-entity нужно для резолва kind на сервере).
// Внешние кнопки «+» и «лупа» рядом с select не нужны — всё внутри picker'а.
function openRefPicker(selOrId) {
  var sel = (typeof selOrId === 'string') ? document.getElementById(selOrId) : selOrId;
  if (!sel) return;
  var refEntity = sel.getAttribute('data-ref-entity') || '';
  var allowCreate = sel.getAttribute('data-ref-allow-create') === '1';
  var opts = [];
  for (var i = 0; i < sel.options.length; i++) {
    var o = sel.options[i];
    if (o.value) opts.push({id: o.value, label: o.text});
  }
  var old = document.getElementById('_ref-picker-modal');
  if (old) old.remove();
  var modal = document.createElement('div');
  modal.id = '_ref-picker-modal';
  modal.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;display:flex;align-items:center;justify-content:center';
  var inner = '<div style="background:#fff;border-radius:10px;padding:20px;width:480px;max-width:95vw;max-height:80vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.18)">';
  inner += '<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px"><div style="font-weight:600;font-size:15px;color:#1e293b">Выбор из списка</div>';
  if (allowCreate && refEntity) {
    inner += '<button type="button" id="_rp-create" style="padding:5px 12px;border:1px solid #16a34a;border-radius:6px;background:#f0fdf4;cursor:pointer;font-size:12px;font-weight:600;color:#16a34a" title="Создать новый">+ Создать</button>';
  }
  inner += '</div>';
  inner += '<input id="_rp-search" type="text" placeholder="Поиск..." autocomplete="off" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;margin-bottom:10px;outline:none">';
  inner += '<div id="_rp-list" style="overflow-y:auto;flex:1;border:1px solid #e2e8f0;border-radius:7px">';
  if (opts.length === 0) {
    inner += '<div style="padding:16px;color:#94a3b8;font-size:13px;text-align:center">Список пуст</div>';
  }
  inner += '</div>';
  inner += '<div style="margin-top:12px;text-align:right"><button type="button" id="_rp-cancel" style="padding:6px 18px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px">Отмена</button></div>';
  inner += '</div>';
  modal.innerHTML = inner;
  document.body.appendChild(modal);
  var list = document.getElementById('_rp-list');
  if (opts.length > 0 && list) {
    for (var i = 0; i < opts.length; i++) {
      var item = document.createElement('div');
      item.className = '_rp-item';
      item.setAttribute('data-id', opts[i].id);
      item.style.cssText = 'padding:9px 14px;cursor:pointer;border-bottom:1px solid #f1f5f9;font-size:14px;color:#1e293b';
      item.textContent = opts[i].label;
      list.appendChild(item);
    }
  }
  window._rpTarget = sel;
  var search = document.getElementById('_rp-search');
  search.focus();
  search.addEventListener('input', function() {
    var q = this.value.toLowerCase();
    var items = document.querySelectorAll('._rp-item');
    for (var i = 0; i < items.length; i++) {
      items[i].style.display = items[i].textContent.toLowerCase().indexOf(q) >= 0 ? '' : 'none';
    }
  });
  document.getElementById('_rp-list').addEventListener('click', function(e) {
    var item = e.target.closest('._rp-item');
    if (!item) return;
    if (window._rpTarget) {
      window._rpTarget.value = item.getAttribute('data-id');
      try { window._rpTarget.dispatchEvent(new Event('change', {bubbles:true})); } catch(e) {}
    }
    modal.remove();
  });
  var createBtn = document.getElementById('_rp-create');
  if (createBtn) {
    createBtn.addEventListener('click', function() {
      modal.remove();
      openRefCreate(sel, refEntity);
    });
  }
  document.getElementById('_rp-cancel').addEventListener('click', function() { modal.remove(); });
  modal.addEventListener('click', function(e) { if (e.target === modal) modal.remove(); });
}

// openRefCurrent — «провалиться» в карточку текущего выбранного значения
// ссылочного поля (паттерн 1С: кнопка-лупа открывает выбранный элемент).
// Если поле пустое — короткое уведомление. refEntity берём с data-атрибута.
function openRefCurrent(selOrId) {
  var sel = (typeof selOrId === 'string') ? document.getElementById(selOrId) : selOrId;
  if (!sel) return;
  var refEntity = sel.getAttribute('data-ref-entity') || '';
  if (!refEntity || !sel.value) return;
  var refURL = '/ui/_ref-open/' + encodeURIComponent(refEntity) + '/' + encodeURIComponent(sel.value);
  // В оболочке вкладок (issue #129) «провалиться в карточку» открывает новую
  // вкладку рядом, а не отдельное окно браузера. Дивертим только если родитель —
  // действительно оболочка (есть obOpenTab); иначе (напр. picker-модал на
  // обычной странице) обычный window.open.
  try {
    if (window.__obEmbedded && window.parent && window.parent.obOpenTab) {
      window.parent.postMessage({ source: 'obOpenTab', url: refURL, title: refEntity }, '*');
      return;
    }
  } catch (e) {}
  window.open(refURL, '_blank');
}

// openRefCreate — открывает модалку с iframe для inline-создания элемента
// справочника, не покидая текущей формы (паттерн 1С «+» рядом с ссылочным
// полем). После сохранения iframe шлёт postMessage с id и подписью —
// родительская страница добавляет option в target select и закрывает модалку.
function openRefCreate(targetSelect, refEntity) {
  if (!targetSelect || !refEntity) return;
  var old = document.getElementById('_ref-create-modal');
  if (old) old.remove();
  var modal = document.createElement('div');
  modal.id = '_ref-create-modal';
  modal.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.5);z-index:10000;display:flex;align-items:center;justify-content:center';
  var box = document.createElement('div');
  box.style.cssText = 'background:#fff;border-radius:10px;width:780px;max-width:95vw;height:78vh;max-height:680px;display:flex;flex-direction:column;box-shadow:0 12px 40px rgba(0,0,0,.22);overflow:hidden';
  var iframe = document.createElement('iframe');
  iframe.src = '/ui/_ref-create/' + encodeURIComponent(refEntity);
  iframe.style.cssText = 'flex:1;border:0;width:100%';
  box.appendChild(iframe);
  modal.appendChild(box);
  document.body.appendChild(modal);

  function handler(ev) {
    var d = ev.data;
    if (!d || typeof d !== 'object') return;
    if (d.source === 'obRefCreate' && d.id) {
      // Добавляем option если такого ещё нет, и выбираем его.
      var exists = false;
      for (var i = 0; i < targetSelect.options.length; i++) {
        if (targetSelect.options[i].value === d.id) { exists = true; break; }
      }
      if (!exists) {
        var o = document.createElement('option');
        o.value = d.id; o.textContent = d.label || d.id;
        targetSelect.appendChild(o);
      }
      targetSelect.value = d.id;
      // Триггерим change на случай зависимых полей (recalcTpRow и т.п.).
      try { targetSelect.dispatchEvent(new Event('change', {bubbles:true})); } catch(e) {}
      cleanup();
    } else if (d.source === 'obRefCancel') {
      cleanup();
    }
  }
  function cleanup() {
    window.removeEventListener('message', handler);
    modal.remove();
  }
  window.addEventListener('message', handler);
  modal.addEventListener('click', function(e) { if (e.target === modal) cleanup(); });
}

// openItemPicker — модальный диалог мультивыбора (подбор, план 46). Вызывается
// клиентом, когда ответ form-event содержит pickerData. payload:
//   { columns:[{name,title,type,editable}], rows:[{id,data:{col:val}}],
//     config:{title,searchField,qtyField,checkAll} }
// По «Перенести в документ» собирает отмеченные строки и возвращает их
//
// STOPGAP: авточек и корзина ниже — документ-специфичный UX, временно зашитый
// в платформенный JS. По плану (Фаза B) богатый подбор переедет в форму
// конфигурации через билтин ОткрытьФорму + ОповеститьОВыборе, а здесь останется
// тривиальный диалог. См. crystalline-drifting-tiger.md / picker.go.
function openItemPicker(payload, elementName) {
  if (!payload) return;
  var cols = payload.columns || [];
  var rows = payload.rows || [];
  var cfg = payload.config || {};
  var old = document.getElementById('_item-picker-modal');
  if (old) old.remove();
  var modal = document.createElement('div');
  modal.id = '_item-picker-modal';
  modal.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;display:flex;align-items:center;justify-content:center';
  var box = document.createElement('div');
  box.style.cssText = 'background:#fff;border-radius:10px;padding:20px;width:720px;max-width:96vw;max-height:86vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.18)';

  var head = document.createElement('div');
  head.style.cssText = 'display:flex;align-items:center;justify-content:space-between;margin-bottom:12px';
  var title = document.createElement('div');
  title.style.cssText = 'font-weight:600;font-size:15px;color:#1e293b';
  title.textContent = cfg.title || 'Подбор';
  var counter = document.createElement('div');
  counter.style.cssText = 'font-size:12px;color:#64748b';
  head.appendChild(title); head.appendChild(counter);
  box.appendChild(head);

  var search = document.createElement('input');
  search.type = 'text'; search.placeholder = 'Поиск...'; search.autocomplete = 'off';
  search.style.cssText = 'padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;margin-bottom:10px;outline:none';
  box.appendChild(search);

  var scroll = document.createElement('div');
  scroll.style.cssText = 'overflow:auto;flex:1;min-height:120px;border:1px solid #e2e8f0;border-radius:7px';
  var table = document.createElement('table');
  table.className = 'tp-table'; table.style.cssText = 'width:100%;font-size:13px;margin:0';
  var thead = document.createElement('thead');
  var htr = document.createElement('tr');
  var thCb = document.createElement('th'); thCb.style.width = '34px';
  var cbAll = document.createElement('input'); cbAll.type = 'checkbox';
  thCb.appendChild(cbAll); htr.appendChild(thCb);
  cols.forEach(function(c){ var th = document.createElement('th'); th.textContent = c.title || c.name; htr.appendChild(th); });
  thead.appendChild(htr); table.appendChild(thead);

  var tbody = document.createElement('tbody');
  function rowText(r){
    return cols.map(function(c){ var v = (r.data||{})[c.name]; return v == null ? '' : String(v); }).join(' ').toLowerCase();
  }
  rows.forEach(function(r){
    var tr = document.createElement('tr');
    tr.setAttribute('data-id', r.id || '');
    tr.setAttribute('data-search', rowText(r));
    var tdCb = document.createElement('td'); tdCb.style.textAlign = 'center';
    var cb = document.createElement('input'); cb.type = 'checkbox'; cb.className = '_ip-cb';
    if (cfg.checkAll) cb.checked = true;
    cb.onchange = updateCounter;
    tdCb.appendChild(cb); tr.appendChild(tdCb);
    cols.forEach(function(c){
      var td = document.createElement('td');
      var v = (r.data||{})[c.name];
      if (c.editable) {
        var inp = document.createElement('input');
        inp.type = (c.type === 'number') ? 'number' : 'text';
        if (c.type === 'number') inp.step = 'any';
        inp.value = (v == null ? '' : v);
        inp.className = '_ip-val'; inp.setAttribute('data-col', c.name);
        inp.style.cssText = 'width:90px;padding:3px 6px';
        td.appendChild(inp);
      } else {
        td.textContent = (v == null ? '' : String(v));
        td.setAttribute('data-col', c.name);
      }
      tr.appendChild(td);
    });
    tbody.appendChild(tr);
  });
  // Авточек: при вводе кол-ва > 0 галочка ставится автоматически.
  tbody.addEventListener('input', function(e) {
    var inp = e.target;
    if (!inp.classList.contains('_ip-val')) return;
    if (cfg.qtyField && inp.getAttribute('data-col') !== cfg.qtyField) return;
    var tr = inp.closest('tr');
    if (!tr) return;
    var cb = tr.querySelector('._ip-cb');
    if (!cb) return;
    var val = parseFloat(inp.value);
    cb.checked = (!isNaN(val) && val > 0);
    updateCounter();
    updateBasket();
  });
  table.appendChild(tbody);
  scroll.appendChild(table);
  box.appendChild(scroll);

  // ── Корзина ──────────────────────────────────────────────────────
  var displayCol = null;
  for (var ci = 0; ci < cols.length; ci++) { if (cols[ci].name !== cfg.qtyField) { displayCol = cols[ci]; break; } }
  var qtyCol = null;
  for (var qi = 0; qi < cols.length; qi++) { if (cols[qi].name === cfg.qtyField) { qtyCol = cols[qi]; break; } }

  var basketHead = document.createElement('div');
  basketHead.style.cssText = 'display:flex;align-items:center;justify-content:space-between;margin-top:10px;padding:6px 10px;background:#f1f5f9;border-radius:7px;cursor:pointer;user-select:none;font-weight:600;font-size:13px;color:#334155';
  var basketTitle = document.createElement('span');
  basketTitle.textContent = 'Корзина';
  var basketBadge = document.createElement('span');
  basketBadge.style.cssText = 'font-size:12px;color:#64748b;font-weight:400';
  basketHead.appendChild(basketTitle);
  basketHead.appendChild(basketBadge);
  box.appendChild(basketHead);

  var basketScroll = document.createElement('div');
  basketScroll.style.cssText = 'overflow:auto;max-height:180px;margin-top:4px;border:1px solid #e2e8f0;border-radius:7px;display:none';
  var basketTable = document.createElement('table');
  basketTable.className = 'tp-table';
  basketTable.style.cssText = 'width:100%;font-size:13px;margin:0';
  var bThead = document.createElement('thead');
  var bHtr = document.createElement('tr');
  var bTh1 = document.createElement('th');
  bTh1.textContent = displayCol ? (displayCol.title || displayCol.name) : 'Номенклатура';
  bHtr.appendChild(bTh1);
  var bTh2 = document.createElement('th');
  bTh2.style.cssText = 'width:90px;text-align:right';
  bTh2.textContent = qtyCol ? (qtyCol.title || qtyCol.name) : 'Кол-во';
  bHtr.appendChild(bTh2);
  bThead.appendChild(bHtr);
  basketTable.appendChild(bThead);
  var bTbody = document.createElement('tbody');
  basketTable.appendChild(bTbody);
  basketScroll.appendChild(basketTable);
  box.appendChild(basketScroll);

  basketHead.addEventListener('click', function() {
    basketScroll.style.display = basketScroll.style.display === 'none' ? '' : 'none';
  });

  var foot = document.createElement('div');
  foot.style.cssText = 'margin-top:12px;display:flex;justify-content:flex-end;gap:8px';
  var btnCancel = document.createElement('button');
  btnCancel.type = 'button'; btnCancel.textContent = 'Отмена';
  btnCancel.style.cssText = 'padding:7px 18px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px';
  var btnOk = document.createElement('button');
  btnOk.type = 'button'; btnOk.textContent = 'Перенести в документ';
  btnOk.style.cssText = 'padding:7px 18px;border:1px solid #2563eb;border-radius:7px;background:#2563eb;color:#fff;cursor:pointer;font-size:13px;font-weight:600';
  foot.appendChild(btnCancel); foot.appendChild(btnOk);
  box.appendChild(foot);
  modal.appendChild(box);
  document.body.appendChild(modal);

  function checkedRows(){ return Array.prototype.slice.call(tbody.querySelectorAll('._ip-cb')).filter(function(cb){ return cb.checked && cb.closest('tr').style.display !== 'none'; }); }
  function updateCounter(){ counter.textContent = 'Выбрано: ' + checkedRows().length; }
  function updateBasket(){
    bTbody.innerHTML = '';
    var cnt = 0;
    if (!cfg.qtyField) return;
    Array.prototype.forEach.call(tbody.rows, function(tr){
      if (tr.style.display === 'none') return;
      var inp = tr.querySelector('._ip-val[data-col="' + cfg.qtyField + '"]');
      if (!inp) return;
      var val = parseFloat(inp.value);
      if (isNaN(val) || val <= 0) return;
      cnt++;
      var bTr = document.createElement('tr');
      var tdName = document.createElement('td');
      if (displayCol) {
        var srcTd = tr.querySelector('td[data-col="' + displayCol.name + '"]');
        tdName.textContent = srcTd ? srcTd.textContent : '';
      }
      var tdQty = document.createElement('td');
      tdQty.style.cssText = 'text-align:right;font-weight:600';
      tdQty.textContent = inp.value;
      bTr.appendChild(tdName);
      bTr.appendChild(tdQty);
      bTbody.appendChild(bTr);
    });
    basketBadge.textContent = cnt > 0 ? (cnt + ' поз.') : 'пусто';
    if (cnt > 0 && basketScroll.style.display === 'none') basketScroll.style.display = '';
    if (cnt === 0) basketScroll.style.display = 'none';
  }
  updateCounter();
  updateBasket();
  search.focus();
  search.addEventListener('input', function(){
    var q = this.value.toLowerCase();
    Array.prototype.forEach.call(tbody.rows, function(tr){
      tr.style.display = (tr.getAttribute('data-search') || '').indexOf(q) >= 0 ? '' : 'none';
    });
    updateCounter();
    updateBasket();
  });
  cbAll.addEventListener('change', function(){
    Array.prototype.forEach.call(tbody.rows, function(tr){
      if (tr.style.display === 'none') return;
      var cb = tr.querySelector('._ip-cb'); if (cb) cb.checked = cbAll.checked;
    });
    updateCounter();
    updateBasket();
  });
  btnCancel.addEventListener('click', function(){ modal.remove(); });
  modal.addEventListener('click', function(e){ if (e.target === modal) modal.remove(); });
  btnOk.addEventListener('click', function(){
    var result = checkedRows().map(function(cb){
      var tr = cb.closest('tr');
      var obj = { id: tr.getAttribute('data-id') };
      cols.forEach(function(c){
        if (c.editable) {
          var inp = tr.querySelector('._ip-val[data-col="' + c.name + '"]');
          obj[c.name] = inp ? inp.value : '';
        } else {
          var td = tr.querySelector('td[data-col="' + c.name + '"]');
          obj[c.name] = td ? td.textContent : '';
        }
      });
      return obj;
    });
    modal.remove();
    if (typeof obFire === 'function') {
      obFire(elementName, 'Выбор', { _pick_result: JSON.stringify(result) });
    }
  });
}
</script>
{{end}}
`

const tplReport = `
{{/* Кнопки выгрузки отчёта: Excel + PDF (issue #218). output_format: pdf делает
     PDF основной (первой); по умолчанию первой идёт Excel — поведение не меняется. */}}
{{define "report-export-buttons"}}
{{$q := settingsQuery (presetQuery (variantQuery (reportParamQuery .Report.Params .ParamValues) .ActiveVariant) .ActivePresetID) .ReportSettingsJSON .UserSettings}}
{{$excel := printf "/ui/report/%s/excel%s" (lower .Report.Name) $q}}
{{$pdf := printf "/ui/report/%s/pdf%s" (lower .Report.Name) $q}}
<div style="display:flex;justify-content:flex-end;gap:8px;margin-bottom:8px">
  {{if eq (lower .Report.OutputFormat) "pdf"}}
  <a class="btn btn-sm" href="{{$pdf}}" style="background:#dc2626;color:#fff" title="{{t $.Lang "Скачать PDF"}}">{{t $.Lang "PDF ↓"}}</a>
  <a class="btn btn-sm" href="{{$excel}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
  {{else}}
  <a class="btn btn-sm" href="{{$excel}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
  <a class="btn btn-sm" href="{{$pdf}}" style="background:#dc2626;color:#fff" title="{{t $.Lang "Скачать PDF"}}">{{t $.Lang "PDF ↓"}}</a>
  {{end}}
</div>
{{end}}
{{define "page-report"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{.Report.DisplayName $.Lang}}</h2>
{{if or .ReportParams .Report.Variants .ReportPresets}}
<details class="card report-block" data-block="params" open style="margin-bottom:16px">
<summary>{{t $.Lang "Параметры"}}</summary>
<form method="POST">
  {{if .UserSettings}}<input type="hidden" name="__settings" value="{{if .ReportSettingsJSON}}{{.ReportSettingsJSON}}{{else}}{{.UserSettings.JSON}}{{end}}">{{end}}
  {{if .ReportPresets}}
  <div class="form-group" style="margin-bottom:16px;max-width:360px">
    <label>{{t $.Lang "Вариант пользователя"}}</label>
    <select name="__preset" onchange="var h=this.form.querySelector('input[name=__settings]');if(h)h.remove();this.form.submit()">
      <option value="__standard" {{if eq $.ActivePresetID "__standard"}}selected{{end}}>{{t $.Lang "Стандартные настройки"}}</option>
      {{range .ReportPresets}}<option value="{{.ID}}" {{if eq .ID $.ActivePresetID}}selected{{end}}>{{.Name}}{{if .IsDefault}} *{{end}}</option>{{end}}
    </select>
  </div>
  {{end}}
  {{if .Report.Variants}}
  <div class="form-group" style="margin-bottom:16px;max-width:320px">
    <label>{{t $.Lang "Вариант"}}</label>
    <select name="__variant" onchange="var h=this.form.querySelector('input[name=__settings]');if(h)h.remove();var p=this.form.querySelector('select[name=__preset]');if(p)p.value='__standard';this.form.submit()">
      <option value="" {{if not $.ActiveVariant}}selected{{end}}>{{t $.Lang "Основной"}}</option>
      {{range .Report.Variants}}<option value="{{.Name}}" {{if eq .Name $.ActiveVariant}}selected{{end}}>{{.Name}}</option>{{end}}
    </select>
  </div>
  {{end}}
  {{if .ReportParams}}
  <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px;margin-bottom:16px">
  {{range .ReportParams}}{{$p := .}}{{$pname := .Name}}{{$pval := str (index $.ParamValues .Name)}}
    {{if $p.IsBool}}
    <div class="form-group" style="margin-bottom:0">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
        <input type="checkbox" name="{{$pname}}" value="true" {{if index $.ParamValues $pname}}checked{{end}}>
        <span>{{$p.Label}}</span>
      </label>
    </div>
    {{else}}
    <div class="form-group" style="margin-bottom:0">
      <label>{{$p.Label}}</label>
      {{if $p.IsDate}}
        <input type="date" name="{{$pname}}" value="{{$pval}}">
      {{else if $p.IsNum}}
        <input type="number" name="{{$pname}}" value="{{$pval}}">
      {{else if $p.IsSel}}
        <select name="{{$pname}}">
          {{range $p.Options}}<option value="{{.}}" {{if eq . $pval}}selected{{end}}>{{if .}}{{.}}{{else}}{{t $.Lang "— все —"}}{{end}}</option>{{end}}
        </select>
      {{else if $p.IsRef}}
        <div style="display:flex;gap:4px;align-items:center">
          <select name="{{$pname}}" id="rp-{{$pname}}" style="flex:1;min-width:0" data-ref-entity="{{$p.RefEntity}}">
            <option value="">{{t $.Lang "— все —"}}</option>
            {{range $p.Opts}}
              <option value="{{index . "id"}}" {{if eq $pval (str (index . "id"))}}selected{{end}}>{{index . "_label"}}</option>
            {{end}}
          </select>
          <button type="button" onclick="openRefPicker('rp-{{$pname}}')" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
          <button type="button" onclick="openRefCurrent('rp-{{$pname}}')" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
        </div>
      {{else}}
        <input type="text" name="{{$pname}}" value="{{$pval}}">
      {{end}}
    </div>
    {{end}}
  {{end}}
  </div>
  {{end}}
  <button class="btn btn-primary" type="submit">{{t $.Lang "Сформировать"}}</button>
</form>
</details>
{{end}}
{{if and .ReportCols .ReportSettingsJSON}}
<details class="card report-block" data-block="settings" style="margin-bottom:16px">
<summary>{{t $.Lang "Настройка отчёта"}}{{if .UserSettings}} <span style="background:#fef3c7;color:#92400e;border-radius:6px;padding:1px 8px;font-size:12px;font-weight:600">{{t $.Lang "изменено"}}</span>{{end}}</summary>
<form method="POST" onsubmit="return rsBeforeSubmit(event)">
  {{range .ReportParams}}<input type="hidden" name="{{.Name}}" value="{{str (index $.ParamValues .Name)}}">{{end}}
  <input type="hidden" name="__variant" value="{{.ActiveVariant}}">
  <div style="display:flex;gap:10px;align-items:end;flex-wrap:wrap;margin-bottom:12px">
    <div class="form-group" style="margin-bottom:0;min-width:220px">
      <label>{{t $.Lang "Вариант пользователя"}}</label>
      <select name="__preset" onchange="rsChoosePreset(this)">
        <option value="__standard" {{if eq $.ActivePresetID "__standard"}}selected{{end}}>{{t $.Lang "Стандартные настройки"}}</option>
        {{range .ReportPresets}}<option value="{{.ID}}" {{if eq .ID $.ActivePresetID}}selected{{end}}>{{.Name}}{{if .IsDefault}} *{{end}}</option>{{end}}
      </select>
    </div>
    <div class="form-group" style="margin-bottom:0;min-width:220px">
      <label>{{t $.Lang "Название варианта"}}</label>
      <input type="text" name="__preset_name" value="{{if .ActivePreset}}{{.ActivePreset.Name}}{{end}}" placeholder="{{t $.Lang "Мой вариант"}}">
    </div>
    <label style="display:flex;gap:6px;align-items:center;padding-bottom:8px"><input type="checkbox" name="__preset_default" value="1" {{if .ActivePreset}}{{if .ActivePreset.IsDefault}}checked{{end}}{{end}}> {{t $.Lang "по умолчанию"}}</label>
  </div>
  <input type="hidden" name="__settings" id="rs-json"{{if .ReportSettingsJSON}} value="{{.ReportSettingsJSON}}" data-base="{{.ReportSettingsJSON}}"{{else if .UserSettings}} value="{{.UserSettings.JSON}}" data-base="{{.UserSettings.JSON}}"{{end}}>
  <table style="width:auto;margin-bottom:12px">
    <thead><tr>
      <th style="text-align:left">{{t $.Lang "Поле"}}</th>
      <th style="padding:0 10px">{{t $.Lang "Группировка"}}</th>
      <th style="padding:0 10px">{{t $.Lang "Показатель"}}</th>
    </tr></thead>
    <tbody>
    {{range .ReportCols}}<tr>
      <td>{{.}}</td>
      <td style="text-align:center"><input type="checkbox" class="rs-group" value="{{.}}" {{if reportGroupChecked $.Report $.UserSettings .}}checked{{end}}></td>
      <td style="text-align:center"><input type="checkbox" class="rs-measure" value="{{.}}" {{if reportMeasureChecked $.Report $.UserSettings .}}checked{{end}}></td>
    </tr>{{end}}
    </tbody>
  </table>
  <div class="rs-filters" style="margin:12px 0">
    <div style="font-weight:600;margin-bottom:6px">{{t $.Lang "Отбор"}}</div>
    <div id="rs-filter-rows">
      {{if .UserSettings}}{{range .UserSettings.Filters}}{{$f := .}}
      <div class="rs-filter-row" style="display:flex;gap:6px;margin-bottom:6px;align-items:center">
        <select class="rs-f-field">{{range $.ReportCols}}<option value="{{.}}" {{if eq . $f.Field}}selected{{end}}>{{.}}</option>{{end}}</select>
        <select class="rs-f-op">
          <option value="eq" {{if eq $f.Op "eq"}}selected{{end}}>=</option>
          <option value="ne" {{if eq $f.Op "ne"}}selected{{end}}>≠</option>
          <option value="gt" {{if eq $f.Op "gt"}}selected{{end}}>&gt;</option>
          <option value="ge" {{if eq $f.Op "ge"}}selected{{end}}>≥</option>
          <option value="lt" {{if eq $f.Op "lt"}}selected{{end}}>&lt;</option>
          <option value="le" {{if eq $f.Op "le"}}selected{{end}}>≤</option>
          <option value="contains" {{if eq $f.Op "contains"}}selected{{end}}>{{t $.Lang "содержит"}}</option>
        </select>
        <input class="rs-f-value" type="text" value="{{$f.Value}}">
        <button type="button" class="btn btn-sm" onclick="this.parentNode.remove()">×</button>
      </div>
      {{end}}{{end}}
    </div>
    <button type="button" class="btn btn-sm" onclick="rsAddFilter()">+ {{t $.Lang "Отбор"}}</button>
  </div>
  <template id="rs-filter-tpl">
    <div class="rs-filter-row" style="display:flex;gap:6px;margin-bottom:6px;align-items:center">
      <select class="rs-f-field">{{range .ReportCols}}<option value="{{.}}">{{.}}</option>{{end}}</select>
      <select class="rs-f-op">
        <option value="eq">=</option>
        <option value="ne">≠</option>
        <option value="gt">&gt;</option>
        <option value="ge">≥</option>
        <option value="lt">&lt;</option>
        <option value="le">≤</option>
        <option value="contains">{{t $.Lang "содержит"}}</option>
      </select>
      <input class="rs-f-value" type="text" value="">
      <button type="button" class="btn btn-sm" onclick="this.parentNode.remove()">×</button>
    </div>
  </template>
  <div class="rs-appearance" style="margin:12px 0;display:flex;gap:16px;align-items:center;flex-wrap:wrap">
    <label>{{t $.Lang "Линии сетки"}}
      <select id="rs-lines" style="margin-left:6px">
        <option value="">{{t $.Lang "горизонтальные (как сейчас)"}}</option>
        <option value="vertical">{{t $.Lang "вертикальные"}}</option>
        <option value="both">{{t $.Lang "и те и те"}}</option>
        <option value="none">{{t $.Lang "без линий"}}</option>
      </select>
    </label>
    <label><input type="checkbox" id="rs-zebra"> {{t $.Lang "Чередование строк (зебра)"}}</label>
  </div>
  <div style="display:flex;gap:8px;flex-wrap:wrap">
    <button class="btn btn-primary" type="submit">{{t $.Lang "Применить"}}</button>
    <button class="btn" type="submit" name="__preset_action" value="save" formaction="/ui/report/{{lower .Report.Name}}/settings/save">{{t $.Lang "Сохранить вариант"}}</button>
    <button class="btn" type="submit" name="__preset_action" value="save_as" formaction="/ui/report/{{lower .Report.Name}}/settings/save">{{t $.Lang "Сохранить как"}}</button>
    <button class="btn" type="submit" formaction="/ui/report/{{lower .Report.Name}}/settings/delete"{{if not .ActivePreset}} disabled{{end}}>{{t $.Lang "Удалить вариант"}}</button>
    <button class="btn" type="submit" formaction="/ui/report/{{lower .Report.Name}}/settings/reset"{{if not .UserSettings}} disabled{{end}}>{{t $.Lang "Стандартные настройки"}}</button>
  </div>
</form>
<script>
(function(){
  var hidden=document.getElementById('rs-json');
  window.rsBeforeSubmit=function(ev){
    var form=ev&&ev.target;
    if(form&&form.dataset&&form.dataset.skipCollect==="1"){form.dataset.skipCollect="";return true;}
    rsCollect();
    return true;
  };
  window.rsChoosePreset=function(sel){
    if(!sel||!sel.form)return;
    var h=sel.form.querySelector('input[name="__settings"]');if(h)h.remove();
    sel.form.dataset.skipCollect="1";
    sel.form.submit();
  };
  function rsNorm(v){return String(v||"").toLowerCase();}
  function rsFieldMap(values){
    var out={};
    (values||[]).forEach(function(v){if(v)out[rsNorm(v)]=v;});
    return out;
  }
  function preset(){
    if(!hidden) return;
    var raw=hidden.value||hidden.dataset.base||"";
    if(!raw) return;
    if(!hidden.value) hidden.value=raw;
    try{
      var s=JSON.parse(raw);
      var comp=(s&&s.composition)||{};
      var groups=comp.Groupings||comp.groupings||[];
      var meas=comp.Measures||comp.measures||[];
      var mf=meas.map(function(m){return m.Field||m.field;});
      var groupMap=rsFieldMap(groups), measureMap=rsFieldMap(mf);
      document.querySelectorAll('.rs-group,.rs-measure').forEach(function(el){el.checked=false;});
      document.querySelectorAll('.rs-group').forEach(function(el){ if(groupMap[rsNorm(el.value)]) el.checked=true; });
      document.querySelectorAll('.rs-measure').forEach(function(el){ if(measureMap[rsNorm(el.value)]) el.checked=true; });
      var ap=comp.Appearance||comp.appearance||{};
      var lines=ap.lines||ap.Lines||"";if(lines==="horizontal")lines="";
      var le=document.getElementById('rs-lines');if(le)le.value=lines;
      var ze=document.getElementById('rs-zebra');if(ze)ze.checked=!!(ap.zebra||ap.Zebra);
    }catch(e){}
  }
  window.rsCollect=function(){
    var prev={};
    var raw=hidden?(hidden.value||hidden.dataset.base||""):"";
    if(hidden&&!hidden.value&&raw)hidden.value=raw;
    if(raw){try{prev=JSON.parse(raw)||{};}catch(e){prev={};}}
    var prevComp=(prev&&prev.composition)||{};
    var prevGroups=prevComp.Groupings||prevComp.groupings||[];
    var prevGroupByField=rsFieldMap(prevGroups);
    var prevMeasures=prevComp.Measures||prevComp.measures||[];
    var prevByField={};
    var prevMeasureField={};
    prevMeasures.forEach(function(m){var f=m&&(m.Field||m.field);if(f){prevByField[rsNorm(f)]=m;prevMeasureField[rsNorm(f)]=f;}});
    var groupings=[];document.querySelectorAll('.rs-group:checked').forEach(function(c){
      groupings.push(prevGroupByField[rsNorm(c.value)]||c.value);
    });
    var measures=[];document.querySelectorAll('.rs-measure:checked').forEach(function(c){
      var key=rsNorm(c.value);
      var src=prevByField[key]||{};
      var m={Field:prevMeasureField[key]||c.value,Agg:src.Agg||src.agg||"sum"};
      var title=src.Title||src.title;if(title)m.Title=title;
      var align=src.Align||src.align;if(align)m.Align=align;
      var format=src.Format||src.format;if(format)m.Format=format;
      measures.push(m);
    });
    var filters=[];document.querySelectorAll('.rs-filter-row').forEach(function(row){
      var f=row.querySelector('.rs-f-field'),op=row.querySelector('.rs-f-op'),v=row.querySelector('.rs-f-value');
      if(f&&op&&f.value){ filters.push({field:f.value,op:op.value,value:v?v.value:""}); }
    });
    var variantEl=document.querySelector('input[name="__variant"]');
    var lines=(document.getElementById('rs-lines')||{}).value||"";
    var zebra=!!(document.getElementById('rs-zebra')||{}).checked;
    var columns=prevComp.Columns||prevComp.columns||[];
    var sort=prevComp.Sort||prevComp.sort||[];
    var totals=prevComp.Totals||prevComp.totals;
    var detail=(typeof prevComp.Detail!=="undefined")?prevComp.Detail:prevComp.detail;
    var nextComp={Groupings:groupings,Measures:measures,Appearance:{lines:lines,zebra:zebra}};
    if(columns&&columns.length)nextComp.Columns=columns;
    if(sort&&sort.length)nextComp.Sort=sort;
    if(totals)nextComp.Totals=totals;
    if(typeof detail!=="undefined")nextComp.Detail=!!detail;
    var s={variant:variantEl?variantEl.value:"",composition:nextComp,filters:filters};
    if(hidden)hidden.value=JSON.stringify(s);
  };
  window.rsAddFilter=function(){
    var tpl=document.getElementById('rs-filter-tpl');
    if(!tpl||!tpl.content) return;
    document.getElementById('rs-filter-rows').appendChild(tpl.content.cloneNode(true));
  };
  preset();
})();
</script>
</details>
{{end}}
{{if .QueryError}}<div class="error">{{t $.Lang "Ошибка запроса:"}} {{.QueryError}}</div>{{end}}
{{if .ChartOption}}
<details class="card report-block" data-block="chart" open style="margin-bottom:16px">
<summary>{{t $.Lang "Диаграмма"}}</summary>
  <div id="ob-chart" style="width:100%;min-height:400px"></div>
</details>
<script src="/vendor/echarts/echarts.min.js"></script>
<script>
(function(){
  var c=echarts.init(document.getElementById('ob-chart'));
  var _o={{jsJSON .ChartOption}};_o.animation=false;if(_o.yAxis&&_o.yAxis.type==="value"){_o.yAxis.axisLabel={formatter:function(v){if(Math.abs(v)>=1e6)return(v/1e6).toFixed(1)+"M";if(Math.abs(v)>=1e3)return(v/1e3).toFixed(1)+"k";return v%1===0?v:v.toFixed(2)}};}
  c.setOption(_o);
  window.addEventListener('resize',function(){c.resize()});
})();
</script>
{{end}}
{{if .ComposedHTML}}
{{if .Capped}}<div class="card" style="background:#fffbeb;border-color:#fde68a;margin-bottom:8px;padding:8px 12px">{{t $.Lang "Показаны первые строки — данных больше потолка."}}</div>{{end}}
{{if .ComposeWarnings}}<div class="card" style="background:#fef2f2;border-color:#fecaca;margin-bottom:8px;padding:8px 12px"><strong>{{t $.Lang "Предупреждения компоновки:"}}</strong><ul style="margin:4px 0 0;padding-left:20px">{{range .ComposeWarnings}}<li>{{.}}</li>{{end}}</ul></div>{{end}}
{{template "report-export-buttons" .}}
<details class="card report-block" data-block="data" open>
<summary>{{t $.Lang "Данные"}}</summary>
<div class="rc-toolbar" style="margin-bottom:8px;display:flex;gap:8px"><button type="button" id="rc-expand" class="btn btn-sm">{{t $.Lang "Развернуть всё"}}</button><button type="button" id="rc-collapse" class="btn btn-sm">{{t $.Lang "Свернуть всё"}}</button></div>
{{.ComposedHTML}}
</details>
<script>
(function(){
  function rcEscape(key){
    return (window.CSS&&CSS.escape)?CSS.escape(key):key.replace(/["\\\]]/g,'\\$&');
  }
  function rcSetOpen(tr, open){
    var key=tr.getAttribute('data-group');
    var ek=rcEscape(key);
    var cell=tr.querySelector('td');
    var sel='[data-parent="'+ek+'"],[data-parent^="'+ek+'/"],[data-group^="'+ek+'/"]';
    document.querySelectorAll(sel).forEach(function(el){ el.style.display = open ? '' : 'none'; });
    if(cell){ cell.textContent=(open?'▼':'▶')+cell.textContent.slice(1); }
  }
  document.querySelectorAll('tr.grp').forEach(function(tr){
    tr.style.cursor='pointer';
    tr.addEventListener('click', function(){
      var cell=tr.querySelector('td');
      var open=cell.textContent.trim().charAt(0)==='▼';
      rcSetOpen(tr, !open);
    });
  });
  var expandBtn=document.getElementById('rc-expand');
  var collapseBtn=document.getElementById('rc-collapse');
  if(expandBtn){
    expandBtn.addEventListener('click', function(){
      var tbody=document.querySelector('table.report-composed tbody');
      if(!tbody) return;
      tbody.querySelectorAll('tr').forEach(function(tr){ tr.style.display=''; });
      tbody.querySelectorAll('tr.grp').forEach(function(tr){
        var cell=tr.querySelector('td');
        if(cell&&cell.textContent.trim().charAt(0)==='▶'){
          cell.textContent='▼'+cell.textContent.slice(1);
        }
      });
    });
  }
  if(collapseBtn){
    collapseBtn.addEventListener('click', function(){
      var tbody=document.querySelector('table.report-composed tbody');
      if(!tbody) return;
      tbody.querySelectorAll('tr.det,tr.subtotal').forEach(function(tr){ tr.style.display='none'; });
      tbody.querySelectorAll('tr.grp').forEach(function(tr){
        var level=parseInt(tr.getAttribute('data-level')||'0',10);
        if(level>0){ tr.style.display='none'; } else {
          var cell=tr.querySelector('td');
          if(cell&&cell.textContent.trim().charAt(0)==='▼'){
            cell.textContent='▶'+cell.textContent.slice(1);
          }
        }
      });
    });
  }
})();
</script>
{{end}}
{{if .Cols}}
{{template "report-export-buttons" .}}
<div class="card">
{{if .Rows}}
<table><thead><tr>{{range .Cols}}<th>{{.}}</th>{{end}}</tr></thead>
<tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{range $.Cols}}<td>{{fmtCell (index $row .)}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">{{t $.Lang "Нет данных"}}</p>{{end}}
</div>
{{end}}
{{template "form-shared-js" .}}
<script>
(function(){
  try{
    document.querySelectorAll('details.report-block').forEach(function(el){
      var key='rb-'+location.pathname+'-'+el.dataset.block;
      var saved=localStorage.getItem(key);
      if(saved==='1')el.open=true; else if(saved==='0')el.open=false;
      el.addEventListener('toggle',function(){localStorage.setItem(key,el.open?'1':'0');});
    });
  }catch(e){}
})();
</script>
</main></div></body></html>
{{end}}
`

const tplRegister = `
{{define "reg-filter-form"}}
{{- $flt := .Filter}}{{$refOpts := .RefOpts}}
<form method="get" style="display:flex;flex-wrap:wrap;gap:8px;align-items:flex-end;margin-bottom:12px">
  {{range .Fields}}
  <div style="display:flex;flex-direction:column;gap:2px">
    <label style="font-size:11px;color:#64748b">{{.DisplayName $.Lang}}</label>
    {{if .RefEntity}}
    <select name="flt_{{.Name}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:6px;font-size:13px">
      <option value="">— {{t $.Lang "все"}} —</option>
      {{$cur := index $flt .Name}}{{range index $refOpts .Name}}<option value="{{index . "id"}}" {{if eq (str (index . "id")) $cur}}selected{{end}}>{{index . "_label"}}</option>{{end}}
    </select>
    {{else}}
    <input type="text" name="flt_{{.Name}}" value="{{index $flt .Name}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:6px;font-size:13px">
    {{end}}
  </div>
  {{end}}
  {{if .ShowFromTo}}
  <div style="display:flex;flex-direction:column;gap:2px">
    <label style="font-size:11px;color:#64748b">{{t $.Lang "с"}}</label>
    <input type="date" name="from" value="{{index $flt "from"}}" style="padding:5px 10px;border:1px solid #e2e8f0;border-radius:6px;font-size:13px">
  </div>
  <div style="display:flex;flex-direction:column;gap:2px">
    <label style="font-size:11px;color:#64748b">{{t $.Lang "по"}}</label>
    <input type="date" name="to" value="{{index $flt "to"}}" style="padding:5px 10px;border:1px solid #e2e8f0;border-radius:6px;font-size:13px">
  </div>
  {{else if .ShowToOnly}}
  <div style="display:flex;flex-direction:column;gap:2px">
    <label style="font-size:11px;color:#64748b">{{t $.Lang "на дату"}}</label>
    <input type="date" name="to" value="{{index $flt "to"}}" style="padding:5px 10px;border:1px solid #e2e8f0;border-radius:6px;font-size:13px">
  </div>
  {{end}}
  <button class="btn btn-sm btn-primary" type="submit">{{t $.Lang "Отобрать"}}</button>
  {{if .HasFilters}}<a class="btn btn-sm" href="{{.ResetURL}}" style="background:#e2e8f0;color:#475569">{{t $.Lang "Сбросить"}}</a>{{end}}
</form>
{{end}}

{{define "page-register-movements"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Register.DisplayName $.Lang}} — {{t $.Lang "движения"}}</h2>
  <a class="btn btn-sm" href="/ui/register/{{lower .Register.Name}}/balances" style="background:#e2e8f0;color:#475569">{{t $.Lang "Остатки →"}}</a>
</div>
{{template "reg-filter-form" (dict "Fields" .Register.Dimensions "Filter" .Filter "RefOpts" .RefOpts "ShowFromTo" true "ShowToOnly" false "HasFilters" .HasFilters "ResetURL" (printf "/ui/register/%s" (lower .Register.Name)) "Lang" $.Lang)}}
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th>{{t $.Lang "Вид движения"}}</th>
  <th>{{t $.Lang "Регистратор"}}</th>
  {{range .Register.Dimensions}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .Register.Resources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .Register.Attributes}}<th>{{.DisplayName $.Lang}}</th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  <td>{{$v := index $row "вид_движения"}}{{if eq (str $v) "Приход"}}<span style="color:#16a34a;font-weight:600">▲ {{$v}}</span>{{else}}<span style="color:#dc2626;font-weight:600">▼ {{$v}}</span>{{end}}</td>
  <td style="font-size:12px;color:#475569">{{if index $row "recorder_label"}}{{index $row "recorder_label"}}{{else}}{{index $row "recorder_type"}}{{end}}</td>
  {{range $.Register.Dimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Resources}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Attributes}}<td>{{index $row .Name}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">{{t $.Lang "Движений нет"}}</p>{{end}}
</div></main></div></body></html>
{{end}}

{{define "page-register-balances"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Register.DisplayName $.Lang}} — {{t $.Lang "остатки"}}</h2>
  <a class="btn btn-sm" href="/ui/register/{{lower .Register.Name}}" style="background:#e2e8f0;color:#475569">{{t $.Lang "← Движения"}}</a>
</div>
{{template "reg-filter-form" (dict "Fields" .Register.Dimensions "Filter" .Filter "RefOpts" .RefOpts "ShowFromTo" false "ShowToOnly" true "HasFilters" .HasFilters "ResetURL" (printf "/ui/register/%s/balances" (lower .Register.Name)) "Lang" $.Lang)}}
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{range .Register.Dimensions}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .Register.Resources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{range $.Register.Dimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.Register.Resources}}<td style="font-weight:600">{{index $row .Name}}</td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">{{t $.Lang "Остатков нет"}}</p>{{end}}
</div></main></div></body></html>
{{end}}
`

const tplDeleteMarked = `
{{define "page-delete-marked"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{t $.Lang "Удалить помеченные"}}</h2>
{{if .Deleted}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">
  {{t $.Lang "Удалено:"}} {{.Deleted}}{{if .Skipped}} &nbsp;·&nbsp; {{t $.Lang "Пропущено (есть ссылки):"}} {{.Skipped}}{{end}}
</div>{{end}}
{{if .Entries}}
<div class="card" style="max-width:1400px;margin-bottom:16px">
<table><thead><tr>
  <th>{{t $.Lang "Объект"}}</th><th>{{t $.Lang "Наименование"}}</th><th>{{t $.Lang "Статус"}}</th>
</tr></thead><tbody>
{{range .Entries}}<tr>
  <td><a href="/ui/{{lower .Kind}}/{{lower .EntityName}}/{{.ID}}">{{.EntityName}}</a></td>
  <td>{{.Label}}</td>
  <td>{{if .HasRefs}}<span style="color:#ef4444">{{t $.Lang "Есть ссылки — не будет удалён"}}</span>{{else}}<span style="color:#16a34a">{{t $.Lang "Будет удалён"}}</span>{{end}}</td>
</tr>{{end}}
</tbody></table>
</div>
<form method="POST" action="/ui/delete-marked"
      onsubmit="return confirm('{{t $.Lang "Удалить все помеченные записи без ссылок?"}}')">
  <button class="btn btn-danger" type="submit">{{t $.Lang "Удалить помеченные без ссылок"}}</button>
  <a class="btn btn-secondary" href="/ui" style="margin-left:8px">{{t $.Lang "Отмена"}}</a>
</form>
{{else}}
<div class="card" style="max-width:600px">
  <p class="empty">{{t $.Lang "Помеченных на удаление записей нет."}}</p>
</div>
{{end}}
</main></div></body></html>
{{end}}
`

const tplProcessor = `
{{define "page-processor"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{.Processor.DisplayName $.Lang}}</h2>
{{if .Processor.Params}}
<div class="card" style="margin-bottom:16px">
<form method="POST" enctype="multipart/form-data">
  <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px;margin-bottom:16px">
  {{range .Processor.Params}}{{$pname := .Name}}
    {{if eq .Type "bool"}}
    <div class="form-group" style="margin-bottom:0">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
        <input type="checkbox" name="{{$pname}}" value="true" {{if index $.ParamValues $pname}}checked{{end}}>
        <span>{{.DisplayLabel $.Lang}}</span>
      </label>
    </div>
    {{else if eq .Type "file"}}
    <div class="form-group" style="margin-bottom:0;grid-column:1/-1">
      <label>{{.DisplayLabel $.Lang}}</label>
      <input type="file" name="{{$pname}}">
    </div>
    {{else if eq .Type "text"}}
    <div class="form-group" style="margin-bottom:0;grid-column:1/-1">
      <label>{{.DisplayLabel $.Lang}}</label>
      <textarea name="{{$pname}}" rows="12" style="width:100%;font-family:monospace;font-size:13px;resize:vertical">{{index $.ParamValues $pname}}</textarea>
    </div>
    {{else}}
    <div class="form-group" style="margin-bottom:0">
      <label>{{.DisplayLabel $.Lang}}</label>
      {{if eq .Type "date"}}
        <input type="date" name="{{$pname}}" value="{{index $.ParamValues $pname}}">
      {{else if eq .Type "number"}}
        <input type="number" name="{{$pname}}" value="{{index $.ParamValues $pname}}">
      {{else if eq .Type "choice"}}
        <select name="{{$pname}}">
          {{range .Options}}<option value="{{.}}" {{if eq . (index $.ParamValues $pname)}}selected{{end}}>{{.}}</option>{{end}}
        </select>
      {{else if isRef (str .Type)}}
        <div style="display:flex;gap:6px;align-items:center">
          <select name="{{$pname}}" style="flex:1">
            <option value="">{{t $.Lang "— выбрать —"}}</option>
            {{with index $.RefOptions $pname}}{{range .}}<option value="{{index . "id"}}" {{if eq (index . "id") (index $.ParamValues $pname)}}selected{{end}}>{{index . "_label"}}</option>{{end}}{{end}}
          </select>
        </div>
      {{else}}
        <input type="text" name="{{$pname}}" value="{{index $.ParamValues $pname}}">
      {{end}}
    </div>
    {{end}}
  {{end}}
  </div>
  <button class="btn btn-primary" type="submit">{{t $.Lang "Выполнить"}}</button>
</form>
</div>
{{else}}
<div class="card" style="margin-bottom:16px">
<form method="POST">
  <button class="btn btn-primary" type="submit">{{t $.Lang "Выполнить"}}</button>
</form>
</div>
{{end}}
{{if .Ran}}
<div class="card">
{{if .RunError}}
  <div class="error">{{.RunError}}</div>
{{else if .Messages}}
  <table class="tbl-plain"><tbody>
  {{range .Messages}}<tr><td style="font-family:monospace;font-size:13px;padding:6px 12px;border-bottom:1px solid #f1f5f9">{{.}}</td></tr>{{end}}
  </tbody></table>
{{else}}
  <p class="empty">{{t $.Lang "Выполнено без сообщений"}}</p>
{{end}}
</div>
{{end}}
</main></div></body></html>
{{end}}
`

const tplAgentSettings = `
{{define "page-agent-settings"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>Настройки агента оборудования</h2>
<div class="card" style="max-width:560px">
  <p style="color:#64748b;font-size:14px;margin-bottom:16px">Адрес и токен локального <b>device-agent</b> на этой машине кассира (<code>onebase device-agent</code>). Хранятся в браузере (localStorage) — у каждого рабочего места свои, на сервер не отправляются.</p>
  <div class="form-group">
    <label>Адрес агента</label>
    <input type="text" id="ag-url" placeholder="http://127.0.0.1:8765">
  </div>
  <div class="form-group">
    <label>Токен (X-Agent-Token)</label>
    <input type="text" id="ag-token" placeholder="оставьте пустым, если агент запущен без токена">
  </div>
  <div style="display:flex;gap:10px;align-items:center">
    <button class="btn btn-primary" type="button" id="ag-save">Сохранить</button>
    <button class="btn btn-secondary" type="button" id="ag-check">Проверить связь</button>
    <span id="ag-status" style="font-size:13px"></span>
  </div>
</div>
<script>
(function(){
  var url=document.getElementById('ag-url'),tok=document.getElementById('ag-token'),st=document.getElementById('ag-status');
  url.value=localStorage.getItem('obAgentURL')||'';
  tok.value=localStorage.getItem('obAgentToken')||'';
  function apply(){localStorage.setItem('obAgentURL',url.value.trim());localStorage.setItem('obAgentToken',tok.value.trim());}
  document.getElementById('ag-save').addEventListener('click',function(){apply();st.style.color='#16a34a';st.textContent='Сохранено';});
  document.getElementById('ag-check').addEventListener('click',function(){
    apply();st.style.color='#64748b';st.textContent='Проверяю…';
    onebaseDevice.health().then(function(d){
      st.style.color='#16a34a';st.textContent='Связь есть. Драйверы: '+((d.drivers||[]).join(', ')||'—');
    }).catch(function(e){st.style.color='#dc2626';st.textContent='Нет связи: '+e.message+' (агент запущен?)';});
  });
})();
</script>
</main></div></body></html>
{{end}}
`

const tplPOS = `
{{define "page-pos"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>Рабочее место кассира</h2>
<p style="color:#64748b;font-size:13px;margin:-12px 0 16px">Команды идут из браузера в <b>локальный агент</b> этой машины. Адрес/токен — в <a href="/ui/settings/agent">настройках агента</a>. Если связи нет, проверьте, что запущен <code>onebase device-agent</code>.</p>

<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:16px;max-width:1000px">

  <div class="card">
    <h3 style="margin-top:0">Чек</h3>
    <div class="form-group"><label>Товар</label><input type="text" id="pos-good" value="Товар"></div>
    <div style="display:flex;gap:10px">
      <div class="form-group" style="flex:1"><label>Кол-во</label><input type="number" id="pos-qty" value="1"></div>
      <div class="form-group" style="flex:1"><label>Цена</label><input type="number" id="pos-price" value="100"></div>
    </div>
    <div class="form-group"><label>Оплата</label><input type="text" id="pos-pay" value="Наличные"></div>
    <div class="form-group"><label>Касса: драйвер / порт</label>
      <div style="display:flex;gap:8px"><input type="text" id="pos-prn-drv" value="escpos_tcp" style="flex:1"><input type="text" id="pos-prn-port" placeholder="127.0.0.1:9100" style="flex:1"></div>
    </div>
    <div style="display:flex;gap:8px;flex-wrap:wrap">
      <button class="btn btn-primary btn-sm" type="button" id="pos-print">Напечатать чек</button>
      <button class="btn btn-secondary btn-sm" type="button" id="pos-drawer">Открыть ящик</button>
    </div>
  </div>

  <div class="card">
    <h3 style="margin-top:0">Фискальный чек (ККТ)</h3>
    <p style="color:#64748b;font-size:12px;margin-bottom:12px">Использует данные блока «Чек». Ставка НДС — 20%, признак — товар (демо).</p>
    <div class="form-group"><label>ККТ: драйвер / адрес сервиса</label>
      <div style="display:flex;gap:8px"><input type="text" id="pos-kkt-drv" value="atol_kkt" style="flex:1"><input type="text" id="pos-kkt-port" placeholder="127.0.0.1:16732" style="flex:1"></div>
    </div>
    <button class="btn btn-post btn-sm" type="button" id="pos-fiscal">Пробить фискальный чек</button>
    <div id="pos-fiscal-res" style="font-size:12px;color:#475569;margin-top:10px;font-family:monospace"></div>
  </div>

  <div class="card">
    <h3 style="margin-top:0">Весы</h3>
    <div class="form-group"><label>Драйвер / порт</label>
      <div style="display:flex;gap:8px"><input type="text" id="pos-scl-drv" value="scale_tcp" style="flex:1"><input type="text" id="pos-scl-port" placeholder="127.0.0.1:5001" style="flex:1"></div>
    </div>
    <button class="btn btn-primary btn-sm" type="button" id="pos-weight">Получить вес</button>
    <span id="pos-weight-res" style="font-size:14px;font-weight:600;margin-left:10px"></span>
  </div>

  <div class="card">
    <h3 style="margin-top:0">Эквайринг</h3>
    <div class="form-group"><label>Драйвер / порт</label>
      <div style="display:flex;gap:8px"><input type="text" id="pos-pay-drv" value="acquiring_tcp" style="flex:1"><input type="text" id="pos-pay-port" placeholder="127.0.0.1:5002" style="flex:1"></div>
    </div>
    <div class="form-group"><label>Сумма</label><input type="number" id="pos-pay-sum" value="100"></div>
    <button class="btn btn-primary btn-sm" type="button" id="pos-pay-go">Оплатить картой</button>
    <div id="pos-pay-res" style="font-size:12px;color:#475569;margin-top:10px;font-family:monospace"></div>
  </div>

  <div class="card">
    <h3 style="margin-top:0">Сканер ШК</h3>
    <div class="form-group"><label>Драйвер / порт</label>
      <div style="display:flex;gap:8px"><input type="text" id="pos-scan-drv" value="scanner_tcp" style="flex:1"><input type="text" id="pos-scan-port" placeholder="127.0.0.1:5003" style="flex:1"></div>
    </div>
    <div class="form-group"><label>Штрихкод (последний)</label><input type="text" id="pos-barcode" placeholder="код появится при сканировании"></div>
    <div style="display:flex;gap:8px">
      <button class="btn btn-primary btn-sm" type="button" id="pos-scan-on">Подключить сканер</button>
      <button class="btn btn-cancel btn-sm" type="button" id="pos-scan-off" disabled>Отключить</button>
    </div>
  </div>

</div>

<div class="card" style="max-width:1000px;margin-top:16px">
  <h3 style="margin-top:0">Журнал операций</h3>
  <div id="pos-log" style="font-family:Consolas,monospace;font-size:12px;color:#334155;max-height:180px;overflow-y:auto"><span style="color:#94a3b8">пусто</span></div>
</div>

<script>
(function(){
  function el(id){return document.getElementById(id);}
  function v(id){var e=el(id);return e?e.value.trim():'';}
  // Сохраняем введённые значения в localStorage, чтобы пережили перезагрузку.
  ['pos-good','pos-qty','pos-price','pos-pay','pos-prn-drv','pos-prn-port','pos-kkt-drv','pos-kkt-port','pos-scl-drv','pos-scl-port','pos-pay-drv','pos-pay-port','pos-pay-sum','pos-scan-drv','pos-scan-port'].forEach(function(id){
    var saved=localStorage.getItem('ob:'+id);if(saved!==null&&el(id))el(id).value=saved;
    if(el(id))el(id).addEventListener('change',function(){localStorage.setItem('ob:'+id,el(id).value);});
  });
  var logBox=el('pos-log'),logEmpty=true;
  function log(msg,ok){
    if(logEmpty){logBox.innerHTML='';logEmpty=false;}
    var t=new Date().toLocaleTimeString();
    var line=document.createElement('div');
    line.style.cssText='padding:3px 0;border-bottom:1px solid #f1f5f9;color:'+(ok===false?'#dc2626':(ok?'#16a34a':'#334155'));
    line.textContent='['+t+'] '+msg;
    logBox.appendChild(line);logBox.scrollTop=logBox.scrollHeight;
  }
  function port(id){var p=v(id);return p?{'порт':p}:{};}
  function num(id){return parseFloat(v(id))||0;}
  function receipt(){
    var qty=num('pos-qty')||1,price=num('pos-price'),sum=qty*price;
    return {header:['РМК onebase'],items:[{name:v('pos-good'),qty:qty,price:price,sum:sum}],total:sum,payment:v('pos-pay'),footer:['Спасибо за покупку!']};
  }
  el('pos-print').addEventListener('click',function(){
    onebaseDevice.printReceipt(v('pos-prn-drv'),port('pos-prn-port'),receipt())
      .then(function(){log('Чек напечатан: '+v('pos-good'),true);})
      .catch(function(e){log('Печать: '+e.message,false);});
  });
  el('pos-drawer').addEventListener('click',function(){
    onebaseDevice.drawer(v('pos-prn-drv'),port('pos-prn-port'))
      .then(function(){log('Денежный ящик открыт',true);})
      .catch(function(e){log('Ящик: '+e.message,false);});
  });
  el('pos-fiscal').addEventListener('click',function(){
    var qty=num('pos-qty')||1,price=num('pos-price'),sum=qty*price;
    var pay=v('pos-pay').toLowerCase().indexOf('карт')>=0?'безналичные':'наличные';
    var rec={type:'приход',taxation:'осн',items:[{name:v('pos-good'),qty:qty,price:price,sum:sum,vat:'ндс20',itemType:'товар'}],payments:[{type:pay,sum:sum}]};
    onebaseDevice.fiscal(v('pos-kkt-drv'),port('pos-kkt-port'),rec)
      .then(function(d){el('pos-fiscal-res').textContent='ФД '+d.fd+', ФП '+d.fp+', ФН '+d.fn;log('Чек пробит: ФД '+d.fd,true);})
      .catch(function(e){el('pos-fiscal-res').textContent='';log('Фискализация: '+e.message,false);});
  });
  el('pos-weight').addEventListener('click',function(){
    onebaseDevice.weight(v('pos-scl-drv'),port('pos-scl-port'))
      .then(function(d){el('pos-weight-res').textContent=d.weight+' кг';log('Вес: '+d.weight+' кг',true);})
      .catch(function(e){el('pos-weight-res').textContent='';log('Весы: '+e.message,false);});
  });
  el('pos-pay-go').addEventListener('click',function(){
    onebaseDevice.pay(v('pos-pay-drv'),port('pos-pay-port'),num('pos-pay-sum'))
      .then(function(d){el('pos-pay-res').textContent=(d.approved?'Одобрено':'Отказ')+(d.rrn?' RRN '+d.rrn:'')+(d.card?' '+d.card:'');log('Оплата: '+(d.approved?'одобрено':'отказ'),d.approved);})
      .catch(function(e){el('pos-pay-res').textContent='';log('Эквайринг: '+e.message,false);});
  });
  var scannerES=null;
  el('pos-scan-on').addEventListener('click',function(){
    if(scannerES)return;
    try{
      scannerES=onebaseDevice.events(v('pos-scan-drv'),port('pos-scan-port'),function(code){el('pos-barcode').value=code;log('Скан: '+code,true);});
      scannerES.onerror=function(){log('Сканер: соединение потеряно',false);};
      el('pos-scan-on').disabled=true;el('pos-scan-off').disabled=false;log('Сканер подключён',true);
    }catch(e){log('Сканер: '+e.message,false);}
  });
  el('pos-scan-off').addEventListener('click',function(){
    if(scannerES){scannerES.close();scannerES=null;}
    el('pos-scan-on').disabled=false;el('pos-scan-off').disabled=true;log('Сканер отключён');
  });
})();
</script>
</main></div></body></html>
{{end}}
`

const tplAbout = `
{{define "page-about"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{t $.Lang "О программе"}}</h2>
<div class="card" style="max-width:560px">
  {{if .Cfg.Logo}}<div style="text-align:center;margin-bottom:20px"><img src="/ui/logo" alt="Logo" style="max-height:160px;max-width:360px"></div>{{end}}
  <table class="tbl-plain" style="width:100%;border-collapse:collapse">
    {{if .User}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;width:180px;font-size:14px">{{t $.Lang "Пользователь"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">
        <span style="font-weight:600">{{.User.Login}}</span>
        {{if .User.FullName}}<span style="color:#64748b;margin-left:8px">{{.User.FullName}}</span>{{end}}
        {{if .User.IsAdmin}}<span style="margin-left:8px;background:#dbeafe;color:#1d4ed8;font-size:11px;padding:2px 7px;border-radius:10px;font-weight:600">{{t $.Lang "Администратор"}}</span>{{end}}
      </td>
    </tr>
    {{end}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;width:180px;font-size:14px">{{t $.Lang "Версия платформы"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-weight:600;font-size:14px">onebase {{if .Cfg.PlatVersion}}{{.Cfg.PlatVersion}}{{else}}dev{{end}}{{if .Cfg.PlatDate}}<span style="color:#94a3b8;font-weight:400"> · {{.Cfg.PlatDate}}{{if .Cfg.PlatCommit}} · {{.Cfg.PlatCommit}}{{end}}</span>{{end}}</td>
    </tr>
    {{if .Cfg.PlatAuthor}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Правообладатель платформы"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">{{.Cfg.PlatAuthor}}{{if .Cfg.PlatLicense}} · {{.Cfg.PlatLicense}}{{end}}</td>
    </tr>
    {{end}}
    {{if .Cfg.AppName}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Конфигурация"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px;font-weight:600">{{.Cfg.AppName}}</td>
    </tr>
    {{end}}
    {{if .Cfg.AppVersion}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Версия конфигурации"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">{{.Cfg.AppVersion}}</td>
    </tr>
    {{end}}
    {{if .Cfg.AppAuthor}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Автор конфигурации"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">{{.Cfg.AppAuthor}}</td>
    </tr>
    {{end}}
    {{if .Cfg.AppCopyright}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Правообладатель"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">{{.Cfg.AppCopyright}}</td>
    </tr>
    {{end}}
    {{if .Cfg.AppLicense}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Лицензия конфигурации"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">{{.Cfg.AppLicense}}</td>
    </tr>
    {{end}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "База данных"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:13px;color:#475569;word-break:break-all">{{.Cfg.DSN}}</td>
    </tr>
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Метаданные"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px">
        {{t $.Lang "Справочники:"}} {{.Catalogs}} &nbsp;·&nbsp;
        {{t $.Lang "Документы:"}} {{.Documents}} &nbsp;·&nbsp;
        {{t $.Lang "Регистры:"}} {{.Registers}} &nbsp;·&nbsp;
        {{t $.Lang "Отчёты:"}} {{.Reports}}
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
  <h2>{{.InfoReg.DisplayName $.Lang}}{{if .InfoReg.Periodic}} <span style="font-size:13px;color:#64748b;font-weight:400">({{t $.Lang "периодический"}})</span>{{end}}</h2>
  {{if .CanWrite}}<a class="btn" href="/ui/inforeg/{{lower .InfoReg.Name}}/new">+ {{t $.Lang "Добавить запись"}}</a>{{end}}
</div>
{{template "reg-filter-form" (dict "Fields" .InfoReg.Dimensions "Filter" .Filter "RefOpts" .RefOpts "ShowFromTo" .InfoReg.Periodic "ShowToOnly" false "HasFilters" .HasFilters "ResetURL" (printf "/ui/inforeg/%s" (lower .InfoReg.Name)) "Lang" $.Lang)}}
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{if .InfoReg.Periodic}}<th>{{t $.Lang "Период"}}</th>{{end}}
  {{range .InfoReg.Dimensions}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .InfoReg.Resources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{if .CanDelete}}<th></th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{if $.InfoReg.Periodic}}<td>{{index $row "period"}}</td>{{end}}
  {{range $.InfoReg.Dimensions}}<td>{{$lbl := index $row (printf "%s_label" .Name)}}{{if $lbl}}{{$lbl}}{{else}}{{index $row .Name}}{{end}}</td>{{end}}
  {{range $.InfoReg.Resources}}<td style="font-weight:600">{{$lbl := index $row (printf "%s_label" .Name)}}{{if $lbl}}{{$lbl}}{{else}}{{index $row .Name}}{{end}}</td>{{end}}
  {{if $.CanDelete}}<td>
    <form method="POST" action="/ui/inforeg/{{lower $.InfoReg.Name}}/delete" style="display:inline"
          onsubmit="return confirm('{{t $.Lang "Удалить запись?"}}')">
      {{if $.InfoReg.Periodic}}<input type="hidden" name="period" value="{{index $row "period_key"}}">{{end}}
      {{range $.InfoReg.Dimensions}}<input type="hidden" name="{{.Name}}" value="{{index $row .Name}}">{{end}}
      <button class="btn btn-danger btn-sm" type="submit">×</button>
    </form>
  </td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">{{t $.Lang "Записей нет"}}</p>{{end}}
</div></main></div></body></html>
{{end}}

{{define "page-inforeg-form"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{.InfoReg.DisplayName $.Lang}} — {{t $.Lang "новая запись"}}</h2>
{{if .Error}}<div style="background:#fef2f2;border:1px solid #fecaca;color:#dc2626;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">{{.Error}}</div>{{end}}
<div class="card" style="max-width:560px">
<form method="POST">
  {{if .InfoReg.Periodic}}
  <div class="form-row">
    <label>{{t $.Lang "Период"}}</label>
    <input type="date" name="period" value="{{index .Values "period"}}" required>
  </div>
  {{end}}
  {{range .InfoReg.Dimensions}}
  {{$dn := .Name}}
  <div class="form-row">
    <label>{{.DisplayName $.Lang}} <span style="color:#94a3b8;font-size:11px">[{{t $.Lang "измерение"}}]</span></label>
    {{if .RefEntity}}
    <div style="display:flex;gap:4px;align-items:center">
      <select name="{{$dn}}" id="ird-{{$dn}}" style="flex:1;min-width:0" data-ref-entity="{{.RefEntity}}">
        <option value="">{{t $.Lang "— выбрать —"}}</option>
        {{range index $.RefOpts $dn}}<option value="{{index . "id"}}" {{if eq (index $.Values $dn) (index . "id")}}selected{{end}}>{{index . "_label"}}</option>{{end}}
      </select>
      <button type="button" onclick="openRefPicker('ird-{{$dn}}')" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      <button type="button" onclick="openRefCurrent('ird-{{$dn}}')" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
    </div>
    {{else}}
    <input type="text" name="{{$dn}}" value="{{index $.Values $dn}}">
    {{end}}
  </div>
  {{end}}
  {{range .InfoReg.Resources}}
  <div class="form-row">
    <label>{{.DisplayName $.Lang}}</label>
    <input type="text" name="{{.Name}}" value="{{index $.Values .Name}}">
  </div>
  {{end}}
  <div style="margin-top:20px;display:flex;gap:8px">
    <button class="btn" type="submit">{{t $.Lang "Записать"}}</button>
    <a class="btn btn-secondary" href="/ui/inforeg/{{lower .InfoReg.Name}}">{{t $.Lang "Отмена"}}</a>
  </div>
</form>
</div>
<script>
	function openRefPicker(selOrId){
	  var sel=(typeof selOrId==='string')?document.getElementById(selOrId):selOrId;
	  if(!sel)return;
	  var opts=[];
	  for(var i=0;i<sel.options.length;i++){
	    var o=sel.options[i];
	    if(o.value)opts.push({id:o.value,label:o.text});
	  }
	  var old=document.getElementById('_ref-picker-modal');
	  if(old)old.remove();
	  var modal=document.createElement('div');
	  modal.id='_ref-picker-modal';
	  modal.style.cssText='position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:9999;display:flex;align-items:center;justify-content:center';
	  var inner='<div style="background:#fff;border-radius:10px;padding:20px;width:480px;max-width:95vw;max-height:80vh;display:flex;flex-direction:column;box-shadow:0 8px 32px rgba(0,0,0,.18)">';
	  inner+='<div style="font-weight:600;font-size:15px;margin-bottom:12px;color:#1e293b">Выбор из списка</div>';
	  inner+='<input id="_rp-search" type="text" placeholder="Поиск..." autocomplete="off" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;margin-bottom:10px;outline:none">';
	  inner+='<div id="_rp-list" style="overflow-y:auto;flex:1;border:1px solid #e2e8f0;border-radius:7px">';
	  if(opts.length===0){inner+='<div style="padding:16px;color:#94a3b8;font-size:13px;text-align:center">Список пуст</div>';}
	  inner+='</div>';
	  inner+='<div style="margin-top:12px;text-align:right"><button type="button" id="_rp-cancel" style="padding:6px 18px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px">Отмена</button></div>';
	  inner+='</div>';
	  modal.innerHTML=inner;
	  document.body.appendChild(modal);
	  var list=document.getElementById('_rp-list');
	  if(opts.length>0&&list){
	    for(var j=0;j<opts.length;j++){
	      var item=document.createElement('div');
	      item.className='_rp-item';
	      item.setAttribute('data-id',opts[j].id);
	      item.style.cssText='padding:9px 14px;cursor:pointer;border-bottom:1px solid #f1f5f9;font-size:14px;color:#1e293b';
	      item.textContent=opts[j].label;
	      list.appendChild(item);
	    }
	  }
	  window._rpTarget=sel;
	  var search=document.getElementById('_rp-search');
	  search.focus();
	  search.addEventListener('input',function(){
	    var q=this.value.toLowerCase();
	    document.querySelectorAll('._rp-item').forEach(function(el){el.style.display=el.textContent.toLowerCase().indexOf(q)>=0?'':'none';});
	  });
	  document.getElementById('_rp-list').addEventListener('click',function(e){
	    var item=e.target.closest('._rp-item');
	    if(!item)return;
	    if(window._rpTarget)window._rpTarget.value=item.getAttribute('data-id');
	    modal.remove();
	  });
	  document.getElementById('_rp-cancel').addEventListener('click',function(){modal.remove();});
	  modal.addEventListener('click',function(e){if(e.target===modal)modal.remove();});
	}
</script>
</main></div></body></html>
{{end}}
`

const tplConstants = `
{{define "page-constants"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{t $.Lang "Константы"}}</h2>
{{if .Saved}}<div style="background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">✓ {{t $.Lang "Константы сохранены"}}</div>{{end}}
<div class="card" style="max-width:700px">
<form method="POST" action="/ui/constants">
{{range .Constants}}{{$c := .}}
<div class="form-group">
  <label>{{.DisplayLabel $.Lang}}</label>
  {{if .RefEntity}}
    <select name="{{.Name}}">
      <option value="">{{t $.Lang "— не выбрано —"}}</option>
      {{range index $.RefOpts .Name}}
      <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values $c.Name)}}selected{{end}}>{{index . "_label"}}</option>
      {{end}}
    </select>
  {{else if eq (str .Type) "date"}}
    <input type="date" name="{{.Name}}" value="{{index $.Values .Name}}">
  {{else if eq (str .Type) "bool"}}
    <select name="{{.Name}}">
      <option value="false" {{if eq (index $.Values .Name) "false"}}selected{{end}}>{{t $.Lang "Нет"}}</option>
      <option value="true"  {{if eq (index $.Values .Name) "true"}}selected{{end}}>{{t $.Lang "Да"}}</option>
    </select>
  {{else}}
    <input type="text" name="{{.Name}}" value="{{index $.Values .Name}}" placeholder="{{.Name}}">
  {{end}}
</div>
{{end}}
{{if not .Constants}}
<p class="empty">{{t $.Lang "Нет констант в конфигурации"}}</p>
{{else}}
<div style="margin-top:20px">
  <button class="btn btn-primary" type="submit">{{t $.Lang "Сохранить"}}</button>
</div>
{{end}}
</form>
</div></main></div></body></html>
{{end}}
`

const tplJournal = `
{{define "page-journal"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Journal.DisplayName $.Lang}}</h2>
  <div style="display:flex;align-items:center;gap:12px">
    <span style="color:#94a3b8;font-size:13px">{{t $.Lang "Всего:"}} {{.Total}}</span>
    <a class="btn btn-sm" href="/ui/journal/{{lower .Journal.Name}}/excel{{filterQuery .Params}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
  </div>
</div>
{{$j := .Journal}}{{$params := .Params}}{{$fmts := .ColFormats}}
<details{{if hasFilter $params}} open{{end}}>
  <summary>{{t $.Lang "Отбор"}}</summary>
  <form method="GET" action="">
  <div class="filter-body">
  {{range $j.Filters}}
    {{if eq .Type "date_range"}}
    <div>
      <label>{{.Field}} {{t $.Lang "с"}}</label>
      <input type="date" name="f.{{.Field}}.from" value="{{(filterVal $params .Field).From}}">
    </div>
    <div>
      <label>{{.Field}} {{t $.Lang "по"}}</label>
      <input type="date" name="f.{{.Field}}.to" value="{{(filterVal $params .Field).To}}">
    </div>
    {{else}}
    <div>
      <label>{{.Field}}</label>
      {{if index $.FilterOptions .Field}}
      {{$f := .Field}}
      <select name="f.{{$f}}">
        <option value="">{{t $.Lang "— все —"}}</option>
        {{range index $.FilterOptions $f}}
        <option value="{{index . "id"}}" {{if eq (index . "id") (filterVal $params $f).Value}}selected{{end}}>{{index . "_label"}}</option>
        {{end}}
      </select>
      {{else}}
      <input type="text" name="f.{{.Field}}" value="{{(filterVal $params .Field).Value}}">
      {{end}}
    </div>
    {{end}}
  {{end}}
  </div>
  <div class="filter-actions">
    <button class="btn btn-primary btn-sm" type="submit">{{t $.Lang "Применить"}}</button>
    <a class="btn btn-sm" href="?" style="background:#e2e8f0;color:#475569">{{t $.Lang "Сбросить"}}</a>
  </div>
  {{if $.CurrentSubsystem}}<input type="hidden" name="subsystem" value="{{$.CurrentSubsystem}}">{{end}}
  </form>
</details>

<details class="card report-block" data-block="journal-settings" style="margin-bottom:16px">
  <summary>{{t $.Lang "Настройка списка"}}{{if .JournalSettingsActive}} <span style="background:#fef3c7;color:#92400e;border-radius:6px;padding:1px 8px;font-size:12px;font-weight:600">{{t $.Lang "изменено"}}</span>{{end}}</summary>
  <form method="POST" action="/ui/journal/{{lower .Journal.Name}}/settings/save" onsubmit="return jlBeforeSubmit(event)">
    <input type="hidden" name="__return" value="{{.RequestURI}}">
    <input type="hidden" name="__journal_settings" id="jl-settings-json" value="{{.JournalSettingsJSON}}">
    <table style="width:auto;margin-bottom:12px">
      <thead><tr>
        <th style="text-align:left">{{t $.Lang "Поле"}}</th>
        <th style="padding:0 10px">{{t $.Lang "Показывать"}}</th>
        <th style="width:120px"></th>
      </tr></thead>
      <tbody id="jl-columns">
      {{range .JournalSettingsColumns}}
        <tr class="jl-col-row" data-field="{{.Column.Field}}">
          <td>{{.Column.DisplayLabel $.Lang}}</td>
          <td style="text-align:center"><input type="checkbox" class="jl-visible" {{if .Visible}}checked{{end}}></td>
          <td style="white-space:nowrap;text-align:right">
            <button type="button" class="btn btn-sm" onclick="jlMove(this,-1)" title="{{t $.Lang "Вверх"}}">↑</button>
            <button type="button" class="btn btn-sm" onclick="jlMove(this,1)" title="{{t $.Lang "Вниз"}}">↓</button>
          </td>
        </tr>
      {{end}}
      </tbody>
    </table>
    <div style="display:flex;gap:8px;flex-wrap:wrap">
      <button class="btn btn-primary" type="submit">{{t $.Lang "Сохранить"}}</button>
      <button class="btn" type="submit" formaction="/ui/journal/{{lower .Journal.Name}}/settings/reset"{{if not .JournalSettingsActive}} disabled{{end}}>{{t $.Lang "Стандартные настройки"}}</button>
    </div>
  </form>
  <script>
  (function(){
    window.jlMove=function(btn,dir){
      var tr=btn&&btn.closest?btn.closest('tr'):null;if(!tr||!tr.parentNode)return;
      if(dir<0&&tr.previousElementSibling)tr.parentNode.insertBefore(tr,tr.previousElementSibling);
      if(dir>0&&tr.nextElementSibling)tr.parentNode.insertBefore(tr.nextElementSibling,tr);
    };
    window.jlCollect=function(){
      var rows=document.querySelectorAll('#jl-columns .jl-col-row');
      var cols=[];
      rows.forEach(function(row){
        var cb=row.querySelector('.jl-visible');
        cols.push({field:row.getAttribute('data-field')||'',visible:!!(cb&&cb.checked)});
      });
      var hidden=document.getElementById('jl-settings-json');
      if(hidden)hidden.value=JSON.stringify({columns:cols});
    };
    window.jlBeforeSubmit=function(){jlCollect();return true;};
  })();
  </script>
</details>

{{if .JournalWarnings}}<div class="card" style="background:#fef2f2;border-color:#fecaca;margin-bottom:8px;padding:8px 12px"><strong>{{t $.Lang "Предупреждения журнала:"}}</strong><ul style="margin:4px 0 0;padding-left:20px">{{range .JournalWarnings}}<li>{{.}}</li>{{end}}</ul></div>{{end}}
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th>{{t $.Lang "Документ"}}</th>
  {{range .JournalColumns}}<th>{{.DisplayLabel $.Lang}}</th>{{end}}
  <th style="width:90px"></th>
</tr></thead>
<tbody>
{{range .Rows}}{{$row := .}}
<tr style="cursor:pointer;{{journalRowStyle .}}"
  onclick="if(event.target.tagName!=='A'&&event.target.tagName!=='BUTTON'){window.location='/ui/document/'+encodeURIComponent('{{lower (str (index . "_doc_kind"))}}')+'/'+'{{str (index . "id")}}'}"
>
  <td style="{{journalCellStyle $row "_doc_kind"}}">{{index . "_doc_kind"}}</td>
  {{range $.JournalColumns}}
    {{$v := index $row .Field}}
    {{if eq (index $fmts .Field) "date"}}<td style="{{journalCellStyle $row .Field}}">{{fmtDate $v}}</td>
    {{else}}<td style="{{journalCellStyle $row .Field}}">{{$v}}</td>{{end}}
  {{end}}
  <td><a class="btn btn-sm btn-primary" href="/ui/document/{{lower (str (index . "_doc_kind"))}}/{{str (index . "id")}}">{{t $.Lang "Открыть"}}</a></td>
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Документов нет"}}</p>
{{end}}
</div>

{{if or .HasPrev .HasNext}}
<div style="display:flex;gap:8px;margin-top:12px">
  {{if .HasPrev}}<a class="btn btn-secondary" href="?offset={{.PrevOffset}}{{filterQuery $params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "← Назад"}}</a>{{end}}
  {{if .HasNext}}<a class="btn btn-secondary" href="?offset={{.NextOffset}}{{filterQuery $params}}{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Вперёд →"}}</a>{{end}}
</div>
{{end}}

</main></div></body></html>
{{end}}
`

const tplHistory = `
{{define "page-history"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;max-width:900px">
  <h2 style="margin-bottom:0">{{t $.Lang "История изменений"}} — {{.EntityName}}</h2>
  <a href="{{.BackURL}}" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
</div>
<div class="card" style="max-width:900px">
{{if .Entries}}
<table style="font-size:13px">
<thead><tr>
  <th>{{t $.Lang "Время"}}</th><th>{{t $.Lang "Пользователь"}}</th><th>{{t $.Lang "Действие"}}</th><th>{{t $.Lang "Поле"}}</th><th>{{t $.Lang "Было"}}</th><th>{{t $.Lang "Стало"}}</th>
</tr></thead>
<tbody>
{{range .Entries}}<tr>
  <td style="white-space:nowrap;color:#94a3b8">{{.At.Format "02.01.2006 15:04:05"}}</td>
  <td>{{.UserLogin}}</td>
  <td><span style="font-family:monospace;font-size:11px;background:#f1f5f9;padding:2px 6px;border-radius:4px">{{.Action}}</span></td>
  <td style="font-family:monospace;font-size:12px">{{.Field}}</td>
  <td style="color:#dc2626;font-size:12px">{{.OldValue}}</td>
  <td style="color:#16a34a;font-size:12px">{{.NewValue}}</td>
</tr>{{end}}
</tbody>
</table>
{{else}}
<p class="empty">{{t $.Lang "История изменений пуста."}}</p>
{{end}}
</div>
</main></div></body></html>
{{end}}
`

const tplScheduled = `
{{define "page-scheduled-list"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{t $.Lang "Регламентные задания"}}</h2>
  <span style="color:#94a3b8;font-size:13px">{{t $.Lang "Всего:"}} {{len .JobRows}}</span>
</div>
<div class="card">
{{if .JobRows}}
<table><thead><tr>
  <th>{{t $.Lang "Название"}}</th>
  <th>{{t $.Lang "Расписание"}}</th>
  <th>{{t $.Lang "Обработка"}}</th>
  <th>{{t $.Lang "Статус"}}</th>
  <th>{{t $.Lang "Последний запуск"}}</th>
  <th>{{t $.Lang "Длительность"}}</th>
  <th style="width:90px"></th>
</tr></thead>
<tbody>
{{range .JobRows}}
{{$job := .Job}}
<tr>
  <td><strong>{{$job.Title}}</strong><br><small style="color:#94a3b8">{{$job.Name}}</small></td>
  <td><code>{{$job.Schedule}}</code></td>
  <td>{{$job.Processor}}</td>
  <td>{{if $job.Enabled}}<span style="color:#22c55e">{{t $.Lang "✓ активно"}}</span>{{else}}<span style="color:#94a3b8">{{t $.Lang "— отключено"}}</span>{{end}}</td>
  <td>
    {{if .LastRun}}
      {{$r := .LastRun}}
      <span style="color:{{if eq $r.Status "success"}}#22c55e{{else if eq $r.Status "error"}}#ef4444{{else if eq $r.Status "timeout"}}#f97316{{else}}#94a3b8{{end}}">{{$r.Status}}</span>
      <br><small style="color:#94a3b8">{{fmtDate $r.StartedAt}}</small>
    {{else}}
      <span style="color:#94a3b8">—</span>
    {{end}}
  </td>
  <td>
    {{if .LastRun}}{{.LastRun.DurationMs}} {{t $.Lang "мс"}}{{else}}—{{end}}
  </td>
  <td>
    <a class="btn btn-sm btn-primary" href="/ui/admin/scheduled/{{$job.Name}}">{{t $.Lang "Подробнее"}}</a>
  </td>
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Регламентных заданий нет. Создайте файлы в папке <code>scheduled/</code> вашей конфигурации."}}</p>
{{end}}
</div>
</main></div></body></html>
{{end}}

{{define "page-scheduled-detail"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px">
  <div>
    <h2 style="margin-bottom:4px">{{.Job.DisplayName $.Lang}}</h2>
    <small style="color:#94a3b8">{{.Job.Name}}</small>
  </div>
  <a href="/ui/admin/scheduled" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9">×</a>
</div>

<div class="card" style="margin-bottom:20px">
<table class="tbl-plain" style="width:100%;border-collapse:collapse">
  <tr><td style="padding:6px 12px;color:#64748b;width:160px">{{t $.Lang "Расписание"}}</td><td><code>{{.Job.Schedule}}</code></td></tr>
  <tr><td style="padding:6px 12px;color:#64748b">{{t $.Lang "Обработка"}}</td><td>{{.Job.Processor}}</td></tr>
  <tr><td style="padding:6px 12px;color:#64748b">{{t $.Lang "При ошибке"}}</td><td>{{.Job.OnError}}</td></tr>
  <tr><td style="padding:6px 12px;color:#64748b">{{t $.Lang "Таймаут"}}</td><td>{{.Job.Timeout}} {{t $.Lang "сек."}}</td></tr>
  <tr><td style="padding:6px 12px;color:#64748b">{{t $.Lang "Состояние"}}</td><td>
    {{if .Job.Enabled}}<span style="color:#22c55e">{{t $.Lang "✓ активно"}}</span>{{else}}<span style="color:#94a3b8">{{t $.Lang "— отключено"}}</span>{{end}}
  </td></tr>
</table>
</div>

<form method="POST" action="/ui/admin/scheduled/{{.Job.Name}}/run-now" style="margin-bottom:20px">
  <button class="btn btn-primary" type="submit">{{t $.Lang "▶ Запустить сейчас"}}</button>
</form>

<h3>{{t $.Lang "История запусков (последние 50)"}}</h3>
<div class="card">
{{if .Runs}}
<table><thead><tr>
  <th>{{t $.Lang "Начало"}}</th>
  <th>{{t $.Lang "Статус"}}</th>
  <th>{{t $.Lang "Длительность"}}</th>
  <th>{{t $.Lang "Вывод"}}</th>
  <th>{{t $.Lang "Ошибка"}}</th>
</tr></thead>
<tbody>
{{range .Runs}}
<tr>
  <td style="white-space:nowrap">{{fmtDate .StartedAt}}</td>
  <td>
    <span style="color:{{if eq .Status "success"}}#22c55e{{else if eq .Status "error"}}#ef4444{{else if eq .Status "timeout"}}#f97316{{else}}#94a3b8{{end}}">
      {{.Status}}
    </span>
  </td>
  <td>{{.DurationMs}} {{t $.Lang "мс"}}</td>
  <td style="max-width:400px;white-space:pre-wrap;font-size:12px;color:#475569">{{.Output}}</td>
  <td style="max-width:300px;white-space:pre-wrap;font-size:12px;color:#ef4444">{{.Error}}</td>
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Запусков ещё не было"}}</p>
{{end}}
</div>
</main></div></body></html>
{{end}}
`

const tplAccountReg = `
{{define "page-accounts"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Chart.DisplayName $.Lang}}</h2>
  <span style="color:#94a3b8;font-size:13px">{{len .Rows}} {{t $.Lang "счетов"}}</span>
</div>
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th style="width:120px">{{t $.Lang "Код"}}</th>
  <th>{{t $.Lang "Наименование"}}</th>
  <th style="width:140px">{{t $.Lang "Вид"}}</th>
  <th style="width:80px">{{t $.Lang "Родитель"}}</th>
</tr></thead>
<tbody>
{{range .Rows}}
<tr>
  <td><code>{{index . "code"}}</code></td>
  <td>{{index . "name"}}</td>
  <td style="color:#64748b;font-size:13px">
    {{if eq (str (index . "kind")) "active"}}{{t $.Lang "активный"}}
    {{else if eq (str (index . "kind")) "passive"}}{{t $.Lang "пассивный"}}
    {{else}}{{t $.Lang "активно-пассивный"}}{{end}}
  </td>
  <td style="color:#94a3b8;font-size:12px">{{index . "parent"}}</td>
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Счетов нет"}}</p>
{{end}}
</div>
</main></div></body></html>
{{end}}

{{define "page-accountreg-movements"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Register.DisplayName $.Lang}} — {{t $.Lang "Проводки"}}</h2>
  <a class="btn btn-secondary" href="/ui/accountreg/{{lower .Register.Name}}/balances">{{t $.Lang "Остатки"}}</a>
</div>
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th>{{t $.Lang "Период"}}</th>
  <th>{{t $.Lang "Дт"}}</th>
  <th>{{t $.Lang "Кт"}}</th>
  {{range .Register.Resources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .Register.Subconto}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  <th>{{t $.Lang "Регистратор"}}</th>
</tr></thead>
<tbody>
{{range .Rows}}
{{$row := .}}
<tr>
  <td style="white-space:nowrap">{{fmtDate (index . "period")}}</td>
  <td><code>{{index . "счётдт"}}</code></td>
  <td><code>{{index . "счёткт"}}</code></td>
  {{range $.Register.Resources}}<td>{{str (index $row (lower .Name))}}</td>{{end}}
  {{range $i, $s := $.Register.Subconto}}<td>{{str (index $row (print "субконто" (add $i 1)))}}</td>{{end}}
  <td style="color:#94a3b8;font-size:12px">{{index . "регистратор"}}</td>
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Проводок нет"}}</p>
{{end}}
</div>
</main></div></body></html>
{{end}}

{{define "page-accountreg-balances"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{.Register.DisplayName $.Lang}} — {{t $.Lang "Остатки по счетам"}}</h2>
  <div style="display:flex;gap:8px;align-items:center">
    <form method="GET" style="display:flex;gap:8px;align-items:center">
      <label style="color:#64748b;font-size:13px">{{t $.Lang "На дату:"}}</label>
      <input type="date" name="date" value="{{.AsOf}}" style="padding:4px 8px;border:1px solid #e2e8f0;border-radius:4px">
      <button class="btn btn-primary btn-sm" type="submit">{{t $.Lang "Применить"}}</button>
    </form>
    <a class="btn btn-secondary" href="/ui/accountreg/{{lower .Register.Name}}">{{t $.Lang "Проводки"}}</a>
  </div>
</div>
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th style="width:100px">{{t $.Lang "Счёт"}}</th>
  <th>{{t $.Lang "Наименование"}}</th>
  {{range .Register.Subconto}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .Register.Resources}}
  <th style="text-align:right">{{.DisplayName $.Lang}} {{t $.Lang "Дт"}}</th>
  <th style="text-align:right">{{.DisplayName $.Lang}} {{t $.Lang "Кт"}}</th>
  <th style="text-align:right">{{t $.Lang "Сальдо"}}</th>
  {{end}}
</tr></thead>
<tbody>
{{range .Rows}}
{{$row := .}}
<tr>
  <td><code>{{index . "code"}}</code></td>
  <td>{{index . "name"}}</td>
  {{range $i, $s := $.Register.Subconto}}<td>{{str (index $row (print "субконто" (add $i 1)))}}</td>{{end}}
  {{range $.Register.Resources}}
  {{$col := lower .Name}}
  <td style="text-align:right;font-family:monospace">{{str (index $row (print $col "_дт"))}}</td>
  <td style="text-align:right;font-family:monospace">{{str (index $row (print $col "_кт"))}}</td>
  <td style="text-align:right;font-family:monospace;font-weight:600">{{str (index $row $col)}}</td>
  {{end}}
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Нет движений на выбранную дату"}}</p>
{{end}}
</div>
</main></div></body></html>
{{end}}
`

// tplPageCustom — оболочка произвольной страницы на DSL (план 66). Блоки
// собирает обработчик через построитель «Страница»; здесь они рендерятся в
// общий шейл (head+nav) с автоэкранированием. Сырой HTML (pageRaw) допускается
// только из ДобавитьСыройHTML после санитизации.
const tplPageCustom = `
{{define "page-custom"}}
{{template "head" .}}{{template "nav" .}}
<main class="main-list">
  <h2>{{.PageTitle}}</h2>
  {{if .PageError}}<div class="error">{{.PageError}}</div>{{end}}
  {{range $i, $b := .PageBlocks}}
    {{if eq $b.Kind "heading"}}<h3>{{$b.Text}}</h3>
    {{else if eq $b.Kind "paragraph"}}<p style="margin-bottom:14px;color:#334155;font-size:14px;line-height:1.55;max-width:900px">{{$b.Text}}</p>
    {{else if eq $b.Kind "kpi"}}<div class="card" style="display:inline-block;min-width:200px;margin:0 12px 14px 0;padding:16px 20px;vertical-align:top">{{if $b.Label}}<div style="font-size:12px;text-transform:uppercase;letter-spacing:.05em;color:#64748b;font-weight:600;margin-bottom:6px">{{$b.Label}}</div>{{end}}<div style="font-size:28px;font-weight:700;color:#0f172a;line-height:1.1">{{$b.Value}}</div></div>
    {{else if eq $b.Kind "button"}}{{if $b.Action}}<form method="post" action="{{$.PageActionBase}}{{$b.Action}}{{$.PageQuery}}" style="display:inline-block;margin:0 8px 14px 0"><button type="submit" class="btn btn-primary">{{$b.Text}}</button></form>{{else}}<a href="{{$b.URL}}" class="btn btn-primary" style="margin:0 8px 14px 0">{{$b.Text}}</a>{{end}}
    {{else if eq $b.Kind "divider"}}<hr style="border:none;border-top:1px solid #e2e8f0;margin:18px 0;max-width:1400px">
    {{else if eq $b.Kind "raw"}}<div class="card" style="margin-bottom:14px">{{pageRaw $b.HTML}}</div>
    {{else if eq $b.Kind "list"}}
    <div class="card" style="margin-bottom:14px;max-width:900px">
      {{if $b.Title}}<h3 style="margin-top:0">{{$b.Title}}</h3>{{end}}
      <ul style="margin:0;padding-left:20px;color:#334155;font-size:14px;line-height:1.7">
        {{range $b.Items}}<li>{{if .URL}}<a href="{{.URL}}" style="color:#3b82f6;text-decoration:none">{{.Text}}</a>{{else}}{{.Text}}{{end}}</li>{{end}}
      </ul>
    </div>
    {{else if eq $b.Kind "chart"}}
    <div class="card" style="margin-bottom:14px;max-width:900px">
      {{if $b.Title}}<h3 style="margin-top:0">{{$b.Title}}</h3>{{end}}
      <div class="w-chart-canvas" data-pagechart="{{$i}}" style="width:100%;height:260px"></div>
    </div>
    {{else if eq $b.Kind "table"}}
    <div class="card" style="margin-bottom:14px;overflow-x:auto">
      {{if $b.Title}}<h3 style="margin-top:0">{{$b.Title}}</h3>{{end}}
      {{$cols := $b.Columns}}
      <table>
        <thead><tr>{{range $b.ColumnLabels}}<th>{{.}}</th>{{end}}</tr></thead>
        <tbody>
        {{range $b.Rows}}{{$row := .}}<tr>{{range $cols}}{{$c := index $row.Cells .}}<td>{{if $c.URL}}<a href="{{$c.URL}}" style="color:#3b82f6;text-decoration:none">{{$c.Text}}</a>{{else}}{{$c.Text}}{{end}}</td>{{end}}</tr>
        {{end}}
        </tbody>
      </table>
    </div>
    {{end}}
  {{end}}
</main></div>
{{if .PageHasChart}}
<script>
window.__obPageCharts = window.__obPageCharts || {};
{{range $i, $b := .PageBlocks}}{{if eq $b.Kind "chart"}}window.__obPageCharts["{{$i}}"] = {{echartsJSON (pageChart $b.Chart)}};
{{end}}{{end}}
</script>
<script src="/vendor/echarts/echarts.min.js"></script>
<script>
(function(){
  function init(){
    if(!window.echarts)return;
    var nodes=document.querySelectorAll('.w-chart-canvas[data-pagechart]');
    for(var i=0;i<nodes.length;i++){
      var node=nodes[i];
      if(node.getAttribute('data-ob-init'))continue;
      var opt=window.__obPageCharts[node.getAttribute('data-pagechart')];
      if(!opt)continue;
      node.setAttribute('data-ob-init','1');
      try{var c=echarts.init(node);opt.animation=false;c.setOption(opt);(function(c){window.addEventListener('resize',function(){c.resize();});})(c);}catch(e){console.error('page chart init failed',e);}
    }
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',init);else init();
})();
</script>
{{end}}
</body></html>
{{end}}
`
