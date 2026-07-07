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
		// Инлайн-CSS условного оформления журнала обязан уходить в атрибут style
		// типом template.CSS: строку html/template прогоняет через cssValueFilter,
		// который запрещает ';'/'(' и подменяет стиль из двух и более свойств (или
		// rgb()-цвет) на "ZgotmplZ". Значения безопасны по построению — их собирает
		// только cssStyle() из цветов, прошедших csssafe.Color, и фиксированных
		// font-weight/font-style.
		"journalRowStyle": func(row map[string]any) template.CSS {
			return template.CSS(journalRowStyle(row))
		},
		"journalCellStyle": func(row map[string]any, field string) template.CSS {
			return template.CSS(journalCellStyle(row, field))
		},
		"formRowClass":  formRowClass,
		"formCellClass": formCellClass,
		"add":           func(a, b int) int { return a + b },
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
		"fieldNamesCSV": func(fields []metadata.Field) string {
			names := make([]string, 0, len(fields))
			for _, f := range fields {
				names = append(names, f.Name)
			}
			return strings.Join(names, ",")
		},
		"numberFieldNamesCSV": func(fields []metadata.Field) string {
			names := make([]string, 0, len(fields))
			for _, f := range fields {
				if fmt.Sprintf("%v", f.Type) == string(metadata.FieldTypeNumber) {
					names = append(names, f.Name)
				}
			}
			return strings.Join(names, ",")
		},
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
		"wcell":            widgetCell,
		"echartsJSON":      echartsJSON,
		"splitCamel":       splitCamel,
		"fmtCell":          fmtReportCell,
		"widgetChartsJSON": widgetChartsJSON,
		"pageChartsJSON":   pageChartsJSON,
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
<script src="/static/ui.js"></script>
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
</head><body>
{{end}}
`

const tplNav = `
{{define "nav"}}
<header class="topbar">
  <button class="nav-toggle" type="button" aria-label="{{t $.Lang "Меню"}}" aria-controls="ob-nav" aria-expanded="false" data-ob-nav-toggle>&#9776;</button>
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
    <button class="sys-btn" type="button" data-ob-toggle-target="sysd">&#9881; {{t $.Lang "Система"}} &#9660;</button>
    <div class="sys-drop" id="sysd">
      <a href="/ui/about">{{t $.Lang "О программе"}}</a>
      {{if .IsAdmin}}
      <a href="/ui/admin/users">{{t $.Lang "Пользователи"}}</a>
      <a href="/ui/admin/roles">{{t $.Lang "Роли и права"}}</a>
      <a href="/ui/admin/sessions">{{t $.Lang "Активные пользователи"}}</a>
      <a href="/ui/admin/api-tokens">{{t $.Lang "API-токены"}}</a>
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
      {{if .IsAdmin}}<div class="sys-sub"><a href="#" data-ob-prevent>{{t $.Lang "Инструменты разработчика"}} &#9654;</a>
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
<script type="application/json" id="ob-widget-charts">{{widgetChartsJSON .WidgetResults}}</script>
<script src="/vendor/echarts/echarts.min.js"></script>
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
    <button type="button" id="list-actions-btn" class="btn btn-secondary" data-ob-list-actions title="{{t $.Lang "Команды для выбранной строки"}}">⚙ {{t $.Lang "Действия"}} ▾</button>
    <a class="btn btn-sm" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/excel{{listQuerySuffix .Params}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
  </div>
</div>
<form method="GET" style="display:flex;gap:8px;margin-bottom:12px;max-width:460px">
  <input type="text" name="q" value="{{.Params.Search}}" placeholder="{{t $.Lang "Поиск..."}}" style="flex:1;padding:7px 12px;border:1px solid #e2e8f0;border-radius:6px;font-size:14px" data-ob-auto-submit="320">
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
        <div style="display:flex;gap:4px;align-items:center">
          <select id="flt-{{.Name}}" name="f.{{.Name}}" data-ref-entity="{{.RefEntity}}" style="flex:1;min-width:0">
            <option value="">{{t $.Lang "— все —"}}</option>
            {{range index $refOpts .Name}}
            <option value="{{index . "id"}}" {{if eq (index . "id") (filterVal $params $f.Name).Value}}selected{{end}}>{{index . "_label"}}</option>
            {{end}}
          </select>
          <button type="button" data-ob-ref-picker="flt-{{$f.Name}}" style="padding:7px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
        </div>
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
  data-ob-list-row
  data-tree-id="{{index $row "id"}}"
  data-tree-depth="{{$depth}}"
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
          <button type="button" class="tree-toggle" data-folder-id="{{index $row "id"}}" data-expanded="0" data-loaded="0" title="{{t $.Lang "Свернуть/Развернуть"}}"
            style="background:none;border:none;cursor:pointer;padding:0 2px;font-size:13px">▶</button>
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
  data-ob-list-row
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
  data-ob-list-row
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
<script type="application/json" id="ob-list-config">{{jsJSON (dict
  "isAdmin" .IsAdmin
  "canWrite" .CanWrite
  "canDelete" .CanDelete
  "canUnpost" .CanUnpost
  "treeEntity" .Entity.Name
  "subsystem" (str $.CurrentSubsystem)
  "labels" (dict
    "enterGroup" (t $.Lang "▶ Войти в группу")
    "edit" (t $.Lang "Редактировать")
    "open" (t $.Lang "Открыть")
    "enter" (t $.Lang "▶ Войти")
    "activityShow" (t $.Lang "Вернуть в выбор")
    "activityShowConfirm" (t $.Lang "Вернуть в выбор?")
    "activityHide" (t $.Lang "Скрыть из выбора")
    "activityHideConfirm" (t $.Lang "Скрыть из выбора?")
    "markDelete" (t $.Lang "Пометить на удаление")
    "markDeleteConfirm" "Пометить на удаление?"
    "predefinedNoDelete" (t $.Lang "Предопределённый — нельзя удалить")
    "unpost" (t $.Lang "Отменить проведение")
    "unpostConfirm" (t $.Lang "Отменить проведение?")
    "unmarkDelete" (t $.Lang "Снять пометку на удаление")
    "unmarkDeleteConfirm" (t $.Lang "Снять пометку на удаление?")
    "deleteForever" (t $.Lang "Удалить навсегда")
    "deleteForeverConfirm" "Удалить запись навсегда?"
    "selectRowFirst" (t $.Lang "Сначала выберите строку списка")
    "collapseExpand" "Свернуть/Развернуть"
    "predefined" "Предопределённый"
  )
)}}</script>
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
  <a href="#" data-ob-popup-cancel title="{{t $.Lang "Закрыть"}}" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
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
      <button type="button" class="btn btn-sm btn-secondary" data-ob-toggle-next>{{t $.Lang "Печать"}} ▾</button>
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
      <button type="button" class="btn btn-sm btn-secondary" data-ob-toggle-next>{{t $.Lang "Ввести на основании"}} ▾</button>
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
          data-ob-confirm="{{if .IsAdmin}}{{t $.Lang "Удалить запись навсегда?"}}{{else}}{{t $.Lang "Пометить запись на удаление?"}}{{end}}" style="margin-left:auto">
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
<form id="main-form" method="POST" data-ob-dirty-watch="1">
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
      <button type="button" data-ob-ref-picker="ref-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      <button type="button" data-ob-ref-current="ref-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
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
        <label class="btn btn-sm btn-secondary">{{t $.Lang "Загрузить…"}}<input type="file" accept="image/*" style="display:none" data-ob-image-upload="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/_image"></label>
        <button type="button" class="btn btn-sm img-clear-btn" data-ob-image-clear{{if not $iv}} style="display:none"{{end}}>{{t $.Lang "Очистить"}}</button>
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
            <button type="button" data-ob-ref-picker-self style="padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
            <button type="button" data-ob-ref-current-self style="padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
          </div>
        {{else if eq (str .Type) "number"}}
          <input type="number" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="{{index $row $fn}}"
            data-tp-num="{{$fn}}" data-ob-tp-recalc>
        {{else}}
          <input type="text" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="{{index $row $fn}}"
            data-ob-tp-recalc>
        {{end}}
        </td>
      {{end}}
      <td><button type="button" class="del-btn" data-ob-remove-row="tr">×</button></td>
    </tr>
  {{end}}
  </tbody>
  <tfoot id="tp-foot-{{$tpName}}" class="tp-footer" style="display:none"><tr>
    {{range .Fields}}{{if eq (str .Type) "number"}}<td class="tp-total" data-tp-total="{{$tpName}}.{{.Name}}" style="text-align:right;font-variant-numeric:tabular-nums">0</td>{{else}}<td></td>{{end}}{{end}}<td></td>
  </tr></tfoot>
</table>
<button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin-bottom:8px"
  data-ob-add-tp-row data-tp-name="{{$tpName}}" data-tp-fields="{{fieldNamesCSV .Fields}}" data-tp-num-fields="{{numberFieldNamesCSV .Fields}}">
  + {{t $.Lang "Добавить строку"}}
</button>
{{end}}

<div style="margin-top:16px">
  {{if .IsPopup}}
  {{if .CanWrite}}<button class="btn btn-primary" type="submit" name="_action" value="" form="main-form">{{t $.Lang "Записать и выбрать"}}</button>{{end}}
  <a href="#" data-ob-popup-cancel class="btn btn-cancel">{{t $.Lang "Отмена"}}</a>
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
<div class="card" style="margin-top:16px" data-ob-attachments data-attachments-url="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/attachments">
  <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:12px">
    <h3 style="margin:0;font-size:14px;font-weight:600;color:#374151">{{t $.Lang "Вложения"}}</h3>
    <span id="att-count" style="color:#94a3b8;font-size:12px"></span>
  </div>
  <div id="att-list" style="margin-bottom:12px"></div>
  <form id="att-upload-form" method="POST" enctype="multipart/form-data"
        action="/ui/{{lower (str .Entity.Kind)}}/{{.Entity.Name}}/{{.ID}}/attachments">
    <input type="file" name="file" id="att-file-input" style="display:none" data-ob-submit-form="att-upload-form">
    <button type="button" class="btn btn-sm btn-secondary" data-ob-file-click="att-file-input">
      + {{t $.Lang "Прикрепить файл"}}
    </button>
  </form>
</div>
{{end}}
{{end}}

{{/* form-shared-js — общий <script> блок, используется page-form и
     page-managed-form. Внутри: глобалы window._tpRefOpts/_tpRefMeta,
     функции addTpRow / recalcTpRow / openRefPicker / openRefCreate. */}}
{{define "form-shared-js"}}
{{if entityHasRichText .Entity}}
{{/* Quill (WYSIWYG для richtext-полей, план 65 этап 2). Вендор-ассеты грузятся
     ТОЛЬКО когда у сущности есть richtext-реквизит. Инициализация живёт в
     /static/ui.js, чтобы шаблон не держал inline-JS. */}}
<link rel="stylesheet" href="/vendor/quill/quill.snow.css">
<script src="/vendor/quill/quill.js"></script>
{{end}}
<script type="application/json" id="ob-tp-ref-opts">{{jsJSON .TPRefOptions}}</script>
<script type="application/json" id="ob-tp-ref-meta">{{jsJSON .TPRefMeta}}</script>
{{end}}
`

const tplReport = `
{{/* Кнопки фоновой выгрузки отчёта: Excel + PDF. Старые прямые маршруты /excel и
     /pdf остаются для совместимости; UI ведёт через страницу статуса задачи. */}}
{{define "report-export-buttons"}}
{{$q := settingsQuery (presetQuery (variantQuery (reportParamQuery .Report.Params .ParamValues) .ActiveVariant) .ActivePresetID) .ReportSettingsJSON .UserSettings}}
{{$excel := printf "/ui/report/%s/export/excel%s" (lower .Report.Name) $q}}
{{$pdf := printf "/ui/report/%s/export/pdf%s" (lower .Report.Name) $q}}
<div style="display:flex;justify-content:flex-end;gap:8px;margin-bottom:8px">
  {{if eq (lower .Report.OutputFormat) "pdf"}}
  <a class="btn btn-sm" href="{{$pdf}}" style="background:#dc2626;color:#fff" title="{{t $.Lang "Запустить выгрузку PDF"}}">{{t $.Lang "PDF"}}</a>
  <a class="btn btn-sm" href="{{$excel}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Запустить выгрузку Excel"}}">{{t $.Lang "Excel"}}</a>
  {{else}}
  <a class="btn btn-sm" href="{{$excel}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Запустить выгрузку Excel"}}">{{t $.Lang "Excel"}}</a>
  <a class="btn btn-sm" href="{{$pdf}}" style="background:#dc2626;color:#fff" title="{{t $.Lang "Запустить выгрузку PDF"}}">{{t $.Lang "PDF"}}</a>
  {{end}}
</div>
{{end}}
{{define "page-export-job"}}
{{template "head" .}}{{template "nav" .}}
<main>
<h2>{{t $.Lang "Выгрузка отчёта"}}</h2>
<div class="card" style="max-width:720px">
  <div style="display:grid;grid-template-columns:140px 1fr;gap:8px 16px;margin-bottom:16px">
    <div style="color:#64748b">{{t $.Lang "Отчёт"}}</div><div>{{.Job.Name}}</div>
    <div style="color:#64748b">{{t $.Lang "Формат"}}</div><div>{{.JobFormatLabel}}</div>
    <div style="color:#64748b">{{t $.Lang "Статус"}}</div><div>{{.JobStatusLabel}}</div>
    <div style="color:#64748b">{{t $.Lang "Создано"}}</div><div>{{.CreatedAtText}}</div>
    {{if .JobDone}}<div style="color:#64748b">{{t $.Lang "Доступно до"}}</div><div>{{.ExpiresAtText}}</div>{{end}}
  </div>
  {{if .JobDone}}
    <a class="btn btn-primary" href="{{.DownloadURL}}">{{t $.Lang "Скачать файл"}}</a>
    <a class="btn btn-sm" href="{{.BackURL}}" style="margin-left:8px">{{t $.Lang "К отчёту"}}</a>
  {{else if .JobFailed}}
    <div class="alert alert-error" style="margin-bottom:12px">{{.Job.Error}}</div>
    <a class="btn btn-sm" href="{{.BackURL}}">{{t $.Lang "К отчёту"}}</a>
  {{else}}
    <div style="height:6px;background:#e2e8f0;border-radius:3px;overflow:hidden;margin-bottom:12px">
      <div style="height:100%;width:45%;background:#2563eb"></div>
    </div>
    <p style="margin:0;color:#475569">{{t $.Lang "Файл готовится в фоне. Страница обновится автоматически."}}</p>
    <script>setTimeout(function(){ window.location.reload(); }, 2000);</script>
  {{end}}
</div>
</main></body></html>
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
    <select name="__preset" data-ob-report-preset-submit>
      <option value="__standard" {{if eq $.ActivePresetID "__standard"}}selected{{end}}>{{t $.Lang "Стандартные настройки"}}</option>
      {{range .ReportPresets}}<option value="{{.ID}}" {{if eq .ID $.ActivePresetID}}selected{{end}}>{{.Name}}{{if .IsDefault}} *{{end}}</option>{{end}}
    </select>
  </div>
  {{end}}
  {{if .Report.Variants}}
  <div class="form-group" style="margin-bottom:16px;max-width:320px">
    <label>{{t $.Lang "Вариант"}}</label>
    <select name="__variant" data-ob-report-variant-submit>
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
          <button type="button" data-ob-ref-picker="rp-{{$pname}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
          <button type="button" data-ob-ref-current="rp-{{$pname}}" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
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
<form method="POST" data-ob-rs-before-submit>
  {{range .ReportParams}}<input type="hidden" name="{{.Name}}" value="{{str (index $.ParamValues .Name)}}">{{end}}
  <input type="hidden" name="__variant" value="{{.ActiveVariant}}">
  <div style="display:flex;gap:10px;align-items:end;flex-wrap:wrap;margin-bottom:12px">
    <div class="form-group" style="margin-bottom:0;min-width:220px">
      <label>{{t $.Lang "Вариант пользователя"}}</label>
      <select name="__preset" data-ob-rs-choose-preset>
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
        <button type="button" class="btn btn-sm" data-ob-remove-row=".rs-filter-row">×</button>
      </div>
      {{end}}{{end}}
    </div>
    <button type="button" class="btn btn-sm" data-ob-rs-add-filter>+ {{t $.Lang "Отбор"}}</button>
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
      <button type="button" class="btn btn-sm" data-ob-remove-row=".rs-filter-row">×</button>
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
</details>
{{end}}
{{if .QueryError}}<div class="error">{{t $.Lang "Ошибка запроса:"}} {{.QueryError}}</div>{{end}}
{{if .QueryWarning}}<div class="card" style="background:#fffbeb;border-color:#fde68a;margin-bottom:8px;padding:8px 12px">{{.QueryWarning}}</div>{{end}}
{{if .ChartOption}}
<details class="card report-block" data-block="chart" open style="margin-bottom:16px">
<summary>{{t $.Lang "Диаграмма"}}</summary>
  <div id="ob-chart" style="width:100%;min-height:400px"></div>
</details>
<script type="application/json" id="ob-report-chart">{{jsJSON .ChartOption}}</script>
<script src="/vendor/echarts/echarts.min.js"></script>
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
    <div style="display:flex;gap:4px;align-items:center">
      <select id="regflt-{{.Name}}" name="flt_{{.Name}}" data-ref-entity="{{.RefEntity}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:6px;font-size:13px;min-width:0">
        <option value="">— {{t $.Lang "все"}} —</option>
        {{$cur := index $flt .Name}}{{range index $refOpts .Name}}<option value="{{index . "id"}}" {{if eq (str (index . "id")) $cur}}selected{{end}}>{{index . "_label"}}</option>{{end}}
      </select>
      <button type="button" data-ob-ref-picker="regflt-{{.Name}}" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:6px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
    </div>
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
      data-ob-confirm="{{t $.Lang "Удалить все помеченные записи без ссылок?"}}">
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
          <select id="pp-{{$pname}}" name="{{$pname}}" style="flex:1" data-ref-entity="{{index $.ProcessorRefEntity $pname}}">
            <option value="">{{t $.Lang "— выбрать —"}}</option>
            {{with index $.RefOptions $pname}}{{range .}}<option value="{{index . "id"}}" {{if eq (index . "id") (index $.ParamValues $pname)}}selected{{end}}>{{index . "_label"}}</option>{{end}}{{end}}
          </select>
          <button type="button" data-ob-ref-picker="pp-{{$pname}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
          <button type="button" data-ob-ref-current="pp-{{$pname}}" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
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
          data-ob-confirm="{{t $.Lang "Удалить запись?"}}">
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
      <button type="button" data-ob-ref-picker="ird-{{$dn}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      <button type="button" data-ob-ref-current="ird-{{$dn}}" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
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
    <div style="display:flex;gap:6px;align-items:center">
      <select id="const-{{.Name}}" name="{{.Name}}" data-ref-entity="{{.RefEntity}}" style="flex:1;min-width:0">
        <option value="">{{t $.Lang "— не выбрано —"}}</option>
        {{range index $.RefOpts .Name}}
        <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values $c.Name)}}selected{{end}}>{{index . "_label"}}</option>
        {{end}}
      </select>
      <button type="button" data-ob-ref-picker="const-{{$c.Name}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      <button type="button" data-ob-ref-current="const-{{$c.Name}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
    </div>
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
      <label>{{.DisplayLabel $.Lang}} {{t $.Lang "с"}}</label>
      <input type="date" name="f.{{.Field}}.from" value="{{(filterVal $params .Field).From}}">
    </div>
    <div>
      <label>{{.DisplayLabel $.Lang}} {{t $.Lang "по"}}</label>
      <input type="date" name="f.{{.Field}}.to" value="{{(filterVal $params .Field).To}}">
    </div>
    {{else}}
    <div>
      <label>{{.DisplayLabel $.Lang}}</label>
      {{if index $.FilterOptions .Field}}
      {{$f := .Field}}
      <div style="display:flex;gap:4px;align-items:center">
        <select id="jflt-{{$f}}" name="f.{{$f}}" data-ref-entity="{{index $.FilterRefEntities $f}}" style="flex:1;min-width:0">
          <option value="">{{t $.Lang "— все —"}}</option>
          {{range index $.FilterOptions $f}}
          <option value="{{index . "id"}}" {{if eq (index . "id") (filterVal $params $f).Value}}selected{{end}}>{{index . "_label"}}</option>
          {{end}}
        </select>
        <button type="button" data-ob-ref-picker="jflt-{{$f}}" style="padding:7px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      </div>
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
  <form method="POST" action="/ui/journal/{{lower .Journal.Name}}/settings/save" data-ob-jl-before-submit>
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
            <button type="button" class="btn btn-sm" data-ob-jl-move="-1" title="{{t $.Lang "Вверх"}}">↑</button>
            <button type="button" class="btn btn-sm" data-ob-jl-move="1" title="{{t $.Lang "Вниз"}}">↓</button>
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
<tr style="cursor:pointer;{{journalRowStyle .}}" data-ob-journal-open-url="/ui/document/{{lower (str (index . "_doc_kind"))}}/{{str (index . "id")}}">
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
<script type="application/json" id="ob-page-charts">{{pageChartsJSON .PageBlocks}}</script>
<script src="/vendor/echarts/echarts.min.js"></script>
{{end}}
</body></html>
{{end}}
`
