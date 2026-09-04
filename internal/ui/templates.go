package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	processorpkg "github.com/ivantit66/onebase/internal/processor"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/richtext"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/shopspring/decimal"
)

var tmpl = template.Must(newTemplate(nil))

type managedTPColumnJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Ref         string `json:"ref,omitempty"`
	AllowCreate bool   `json:"allowCreate,omitempty"`
	Enum        bool   `json:"enum,omitempty"`
	// Virtual — колонка показывается, но не хранится (#845). Флаг нужен клиенту:
	// такая колонка не редактируется и НЕ попадает в tp_json при отправке формы.
	Virtual bool `json:"virtual,omitempty"`
	Width   int  `json:"width,omitempty"`
	// Hidden — колонка не показывается, но остаётся в columnsMeta и потому
	// по-прежнему сериализуется в tp_json (план 154). Ровно наоборот к Virtual:
	// та видима и не отправляется, эта отправляется и не видима.
	Hidden bool `json:"hidden,omitempty"`
	// Index — позиция реквизита в метаданных табличной части. Клиент кладёт её
	// в _tp_col_index: сервер сверяет индекс с порядком метаданных, а порядок
	// показа с ним больше не совпадает. Для виртуальной колонки — -1.
	Index int `json:"index"`
}

// infoRegKeyValue serialises an information-register dimension for the hidden
// delete form. Display formatting is not a primary-key format: SQLite booleans
// arrive as 0/1 and dates as time.Time, while the delete parser requires a
// lossless value that has the same meaning on PostgreSQL and SQLite.
func infoRegKeyValue(field metadata.Field, row map[string]any) string {
	if keyValues, ok := row[storage.InfoRegKeyValuesField].(map[string]string); ok {
		if value, exists := keyValues[field.Name]; exists {
			return value
		}
	}
	value := row[field.Name]
	if value == nil {
		return ""
	}
	if field.RefEntity != "" {
		if id, _, ok := uuidFromValue(value); ok {
			return id
		}
	}
	switch field.Type {
	case metadata.FieldTypeDate:
		switch typed := value.(type) {
		case time.Time:
			return typed.Format(time.RFC3339Nano)
		case *time.Time:
			if typed == nil {
				return ""
			}
			return typed.Format(time.RFC3339Nano)
		}
	case metadata.FieldTypeBool:
		switch typed := value.(type) {
		case bool:
			return fmt.Sprintf("%t", typed)
		case int64:
			if typed == 0 {
				return "false"
			}
			if typed == 1 {
				return "true"
			}
		case int:
			if typed == 0 {
				return "false"
			}
			if typed == 1 {
				return "true"
			}
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true", "1":
				return "true"
			case "false", "0":
				return "false"
			}
		}
	}
	return fmt.Sprintf("%v", value)
}

func newTemplate(bundle *i18n.Bundle) (*template.Template, error) {
	return template.New("root").Funcs(templateFuncs(bundle)).Parse(templateSource())
}

// fmtDateValue — единственное в проекте место, где значение даты превращается в
// текст для показа ПРАВИЛЬНО: с приведением к местной зоне (`t.In(time.Local)`).
// Без него момент печатается в той зоне, в которой его отдал драйвер, а она у
// диалектов разная — SQLite всегда UTC, pgx берёт зону Go-процесса. На хосте со
// смещением от UTC это меняет не только время, но и КАЛЕНДАРНЫЙ ДЕНЬ: дата,
// записанная как 14.03.1985, показывалась на SQLite как 13-е (#1071).
//
// Функция пакетного уровня, а не замыкание в templateFuncs, именно поэтому:
// показ даты нужен не только шаблонам, и каждая новая копия этой логики — новый
// шанс забыть про `In(time.Local)`.
func fmtDateValue(v any) string {
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
		// Форматы С зоной разбираются как есть: зона в самой строке.
		for _, layout := range []string{
			time.RFC3339, time.RFC3339Nano,
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05 -0700 MST",
			"2006-01-02 15:04:05.999999999 -0700 MST",
		} {
			if t, err := time.Parse(layout, s); err == nil {
				return fmtT(t)
			}
		}
		// Форматы БЕЗ зоны — ParseInLocation, а не Parse. time.Parse считает
		// такую строку UTC, и следующий за ним In(time.Local) сдвигал бы её на
		// смещение хоста: «2026-05-22» превращалось в «22.05.2026 03:00:00», а
		// дата без времени переставала быть датой без времени. Строка без зоны —
		// это стенные часы, и трогать их нельзя (#1076).
		for _, layout := range []string{
			"2006-01-02T15:04:05", "2006-01-02 15:04:05",
			"2006-01-02T15:04", "2006-01-02",
		} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
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
}

func templateFuncs(bundle *i18n.Bundle) template.FuncMap {
	translate := func(lang, key string) string {
		if bundle != nil {
			return bundle.T(lang, key)
		}
		return key
	}
	return template.FuncMap{
		"lower":           strings.ToLower,
		"infoRegKeyValue": infoRegKeyValue,
		"processorParamPresenceName": func(proc *processorpkg.Processor, name string) string {
			if proc == nil {
				return processorParamPresencePrefix + name
			}
			return processorParamPresenceName(proc.Params, name)
		},
		"processorFileContentName": func(proc *processorpkg.Processor, name string) string {
			if proc == nil {
				return processorFileContentPrefix + name
			}
			return processorFileContentName(proc.Params, name)
		},
		"processorServiceFieldNames": func(proc *processorpkg.Processor) map[string]string {
			if proc == nil {
				return nil
			}
			return processorServiceFieldNames(proc.Params)
		},
		"processorExecuteFallbackButton": isProcessorExecuteFallbackButton,
		"effectiveFormElementReadOnly":   effectiveFormElementReadOnly,
		"effectiveFormElementRequired":   effectiveFormElementRequired,
		"managedCommandBarElement":       managedCommandBarElement,
		"nativeFormElementRequired":      nativeFormElementRequired,
		"normalizedFormHotkey":           normalizedFormHotkey,
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
			return template.CSS(journalRowStyle(row)) //nolint:gosec // G203: стиль собран cssStyle → csssafe.Color, произвольная строка в CSS не попадает
		},
		"journalCellStyle": func(row map[string]any, field string) template.CSS {
			return template.CSS(journalCellStyle(row, field)) //nolint:gosec // G203: стиль собран cssStyle → csssafe.Color, произвольная строка в CSS не попадает
		},
		"formRowClass":  formRowClass,
		"formCellClass": formCellClass,
		"add":           func(a, b int) int { return a + b },
		// lucideIcon рендерит иконку навигации по имени Lucide ссылкой на общий
		// спрайт /vendor/lucide/sprite.svg (планы 72/73).
		"lucideIcon": LucideIcon,
		"t":          translate,
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
		// liveListRefreshOn (план 87, ступень A): имена событий живого списка через
		// пробел для атрибута data-ob-refresh-on. Пусто — список статичный.
		"liveListRefreshOn": func(e *metadata.Entity) string {
			if e == nil {
				return ""
			}
			return strings.Join(e.ListRefreshOn, " ")
		},
		"treeColumn": isTreeListColumn,
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
		// detailPanel — payload боковой панели для строки списка (план 118B).
		// Собирается на сервере из УЖЕ отрисованной строки: второй путь чтения
		// пришлось бы заново проводить через права, строковые политики и маску.
		"detailPanel": func(fields []metadata.Field, row map[string]any,
			enumLabels map[string]map[string]string, lang string) string {
			return detailPanelJSON(fields, row, detailPanelTitle(fields, row), enumLabels, lang,
				func(key string) string { return translate(lang, key) })
		},
		// detailPanelEntity — payload с учётом блока detail_panel: сущности.
		"detailPanelEntity": func(e *metadata.Entity, row map[string]any,
			enumLabels map[string]map[string]string, lang string) string {
			return detailPanelForEntity(e, row, enumLabels, lang,
				func(key string) string { return translate(lang, key) })
		},
		"detailPanelDefaultWidth": func(value any) int {
			entity, ok := value.(*metadata.Entity)
			if !ok || entity == nil || entity.DetailPanel == nil {
				return 0
			}
			return entity.DetailPanel.Width
		},
		"entityFields": func(e *metadata.Entity) []metadata.Field { return e.Fields },
		// journalFields — колонки журнала как поля панели. Журнал сводит разные
		// виды документов, поэтому состав задан им самим, а не сущностью.
		"journalFields": func(j *metadata.Journal) []metadata.Field {
			if j == nil {
				return nil
			}
			out := make([]metadata.Field, 0, len(j.Columns))
			for _, c := range j.Columns {
				out = append(out, metadata.Field{Name: c.Field, Title: c.Label, Titles: c.Labels, Type: metadata.FieldTypeString})
			}
			return out
		},
		"infoRegDetailPanel": func(ir *metadata.InfoRegister, row map[string]any, lang string) string {
			periodTitle := "Период"
			if bundle != nil {
				periodTitle = bundle.T(lang, periodTitle)
			}
			return infoRegisterDetailPanelJSONTranslated(ir, row, lang, periodTitle,
				func(key string) string { return translate(lang, key) })
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
		// boolFieldNamesCSV — булевы колонки табличной части для addTpRow:
		// новая строка должна получить флажок, а не поле ввода (#1010). Имена
		// перечислений JS достаёт из ob-tp-enum-labels, а тип bool в разметке
		// автоформы больше ниоткуда не виден.
		"boolFieldNamesCSV": func(fields []metadata.Field) string {
			names := make([]string, 0, len(fields))
			for _, f := range fields {
				if fmt.Sprintf("%v", f.Type) == string(metadata.FieldTypeBool) {
					names = append(names, f.Name)
				}
			}
			return strings.Join(names, ",")
		},
		// tpEnumOptions — варианты для <select> enum-колонки табличной части
		// в порядке объявления values: (#1010). Данные берём из уже готовых
		// TPEnumLabels/TPEnumOrder контекста, чтобы не заводить третью карту.
		"tpEnumOptions": tpEnumOptions,
		// checked — «взведён ли флажок» для булевой колонки ТЧ. Значение
		// приезжает разнотипным: bool из PostgreSQL, int64 1/0 из SQLite,
		// строка "true" после неудачного сохранения формы.
		"checked": truthyCell,
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
		// itemFormVisible/itemFormHidden делят реквизиты по блоку `item_form:`
		// (план 117, Д12). До этого ключ парсился, хранился, отдавался в
		// describe, круглился конфигуратором и проходил линт, но НИ ОДИН
		// рендерер его не читал: пользователь снимал в конфигураторе галочку
		// видимости реквизита, сохранял — и на форме ничего не менялось.
		//
		// Скрытые реквизиты продолжают ездить в форме как hidden-поля. Без
		// этого «скрыть» означало бы «стереть»: сборка значений формы
		// (formToFields) кладёт nil для каждого реквизита, которого нет в
		// запросе, и первое же сохранение обнулило бы всё скрытое. Отсюда же
		// граница смысла: item_form — это ВИДИМОСТЬ, а не защита; ограничение
		// доступа к реквизиту делается политикой поля.
		"itemFormVisible": func(entity *metadata.Entity) []itemFormField {
			vis, _ := splitItemFormFields(entity)
			return vis
		},
		"itemFormHidden": func(entity *metadata.Entity) []metadata.Field {
			_, hidden := splitItemFormFields(entity)
			return hidden
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
		// attrByName ищет реквизит формы (FormAttribute) по имени — нужен managed-
		// шаблону, чтобы дать ссылочному реквизиту формы пикер (фикс B).
		"attrByName": func(form *metadata.FormModule, name string) *metadata.FormAttribute {
			if form == nil {
				return nil
			}
			for _, a := range form.Attributes {
				if a != nil && a.Name == name {
					return a
				}
			}
			return nil
		},
		// attrRefEntity — имя сущности из ссылочного типа реквизита формы
		// ("CatalogRef.X" → "X"), пусто если тип не ссылочный.
		"attrRefEntity": func(typeRef string) string { return attrRefEntityName(typeRef) },
		// formAttrNames — имена скалярных реквизитов формы (save:false), которых
		// нет среди полей сущности. Клиент по ним восстанавливает введённое после
		// полной перезагрузки страницы: «Записать» уходит POST'ом с редиректом, и
		// иначе выбор в реквизите формы просто пропадал (в 1С форма живёт в
		// памяти клиента и реквизиты переживают запись).
		"formAttrNames": func(form *metadata.FormModule, entity *metadata.Entity) []string {
			if form == nil {
				return nil
			}
			var names []string
			for _, a := range form.Attributes {
				if a == nil || a.Name == "" || a.MainAttribute || a.TypeRef == "ValueTable" {
					continue
				}
				if _, isEntityField := entityFieldByName(entity, a.Name); isEntityField {
					continue
				}
				names = append(names, a.Name)
			}
			return names
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
		// Inline event attributes вызывают /ui/{kind}/{entity}/form-event через obFire.
		"hasHandler": func(el *metadata.FormElement, eventName string) bool {
			if el == nil || el.Handlers == nil {
				return false
			}
			handler, ok := el.Handlers[metadata.FormEventType(eventName)]
			return ok && strings.TrimSpace(handler) != ""
		},
		// elReadOnly / elHidden — итоговое состояние элемента управляемой формы
		// с учётом условий readonly_when/hidden_when по полям записи. Условия
		// вычисляются на сервере при отрисовке (и заново после каждого события
		// формы), а шаблон только читает результат по имени элемента.
		"elReadOnly": func(ctx map[string]any, el *metadata.FormElement) bool {
			if el == nil {
				return false
			}
			if el.ReadOnly {
				return true
			}
			set, _ := ctx["ElReadOnly"].(map[string]bool)
			return set[el.Name]
		},
		"elHidden": func(ctx map[string]any, el *metadata.FormElement) bool {
			if el == nil {
				return false
			}
			set, _ := ctx["ElHidden"].(map[string]bool)
			return set[el.Name]
		},
		// visibleFormPages — страницы набора СтраницыФормы, которые надо
		// отрисовать. Отбор вынесен сюда, а не сделан внутри range, потому что
		// заголовок вкладки и её содержимое связаны одним индексом
		// (data-tab-idx ↔ data-tab-content), а активна всегда нулевая: пропуск
		// внутри range разошёл бы нумерацию, а скрытая первая страница оставила
		// бы форму вовсе без активной вкладки.
		//
		// Ветка СтраницыФормы обходит детей сама и шаблон managed-element для
		// самой страницы не зовёт — значит и hidden_when для неё никто не
		// спросит. Без этого условие на вкладке молча не работало: страница
		// показывалась вместе с кнопкой и всем содержимым.
		"visibleFormPages": func(ctx map[string]any, el *metadata.FormElement) []*metadata.FormElement {
			if el == nil {
				return nil
			}
			hidden, _ := ctx["ElHidden"].(map[string]bool)
			var out []*metadata.FormElement
			for _, page := range el.Children {
				if page == nil || string(page.Kind) != "Страница" || hidden[page.Name] {
					continue
				}
				out = append(out, page)
			}
			return out
		},
		// hasFormHandler — есть ли у формы (а не элемента) обработчик события.
		// Используется в managed-шаблоне для авто-вызова ПриОткрытииФормы при
		// загрузке страницы.
		"hasFormHandler": func(form *metadata.FormModule, eventName string) bool {
			if form == nil || form.Handlers == nil {
				return false
			}
			handler, ok := form.Handlers[metadata.FormEventType(eventName)]
			return ok && strings.TrimSpace(handler) != ""
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
				if strings.EqualFold(entity.TableParts[i].Name, name) {
					return &entity.TableParts[i]
				}
			}
			return nil
		},
		"tablePartTitle": func(tablePart *metadata.TablePart) string {
			if tablePart == nil {
				return ""
			}
			return tablePart.Title
		},
		// canonicalFormTableName maps the DataPath spelling to the unique
		// canonical metadata name shared by entity/processor TP and ValueTable.
		// Browser field names must use it: the parsers intentionally consume the
		// canonical namespace, while DataPath matching is case-insensitive.
		"canonicalFormTableName": func(form *metadata.FormModule, entity *metadata.Entity, name string) string {
			var tableParts []metadata.TablePart
			if entity != nil {
				tableParts = entity.TableParts
			}
			definitions, err := metadata.FormTableDefinitions(form, tableParts)
			if err != nil {
				return name
			}
			for _, definition := range definitions {
				if strings.EqualFold(definition.Name, name) {
					return definition.Name
				}
			}
			return name
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
		// hasCodeField — есть ли на форме kind: ПолеКода. Monaco (vendor-ассеты +
		// init) грузится только при true: редактор весит заметно, и тянуть его
		// на каждую форму ради поля, которого там нет, незачем. Тот же приём,
		// что с Quill для richtext.
		"hasCodeField": func(form *metadata.FormModule) bool {
			if form == nil {
				return false
			}
			found := false
			form.Walk(func(el *metadata.FormElement) bool {
				if el != nil && el.Kind == metadata.FormElementCodeField {
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
				if attr != nil && strings.EqualFold(attr.Name, name) && strings.EqualFold(attr.TypeRef, "ValueTable") {
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
		"fmtDate": fmtDateValue,
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
			return template.URL("?" + strings.Join(parts, "&")) //nolint:gosec // G203: части строки запроса пропущены через url.QueryEscape
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
			return template.URL("&" + strings.Join(parts, "&")) //nolint:gosec // G203: части строки запроса пропущены через url.QueryEscape
		},
		// listURL — адрес того же списка с изменённой частью состояния.
		// current — параметры текущего запроса, дальше пары «ключ значение»:
		// пустое значение убирает ключ, ключ вида "f.*" — все параметры с этим
		// префиксом. Остальное состояние (поиск q, отбор f.*, сортировка,
		// папка parent, вид view, лента lm, раздел) переносится как есть:
		// одно действие меняет ровно одно, поэтому переключение вида не
		// сбрасывает поиск, а сортировка не выкидывает из папки. Номер
		// страницы сбрасывается всегда — после такого перехода набор строк
		// другой, и старый offset указывает не туда.
		"listURL": func(current any, kv ...string) template.URL {
			vals := cloneQuery(current)
			vals.Del("page")
			for i := 0; i+1 < len(kv); i += 2 {
				setQueryValue(vals, kv[i], kv[i+1])
			}
			if len(vals) == 0 {
				return "?"
			}
			return template.URL("?" + vals.Encode()) //nolint:gosec // G203: url.Values.Encode экранирует и ключи, и значения
		},
		// listHidden переносит то же состояние в GET-форму скрытыми полями:
		// own — параметры, которыми владеет сама форма (для них в разметке уже
		// есть свои поля), "f.*" исключает весь отбор.
		"listHidden": func(current any, own ...string) template.HTML {
			vals := cloneQuery(current)
			vals.Del("page")
			for _, name := range own {
				setQueryValue(vals, name, "")
			}
			keys := make([]string, 0, len(vals))
			for k := range vals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var b strings.Builder
			for _, k := range keys {
				for _, v := range vals[k] {
					b.WriteString(`<input type="hidden" name="`)
					b.WriteString(template.HTMLEscapeString(k))
					b.WriteString(`" value="`)
					b.WriteString(template.HTMLEscapeString(v))
					b.WriteString(`">`)
				}
			}
			return template.HTML(b.String()) //nolint:gosec // G203: имена и значения пропущены через HTMLEscapeString
		},
		"reportParamQuery": func(params any, values map[string]any) string {
			parts := []string{}
			if declared, ok := params.([]reportpkg.Param); ok {
				// Ссылка выгрузки — снимок формы, поэтому в неё попадают ВСЕ
				// объявленные параметры в порядке объявления, включая пустые.
				// Пропустить пустой нельзя: «параметра в ссылке нет» означает
				// «пользователь его не задавал», и выгрузка подставила бы туда
				// значение по умолчанию — там, где на экране пусто.
				for _, p := range declared {
					val := ""
					if v := values[p.Name]; v != nil {
						val = fmt.Sprintf("%v", v)
					}
					parts = append(parts, url.QueryEscape(p.Name)+"="+url.QueryEscape(val))
				}
			} else {
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
			return template.JS(b) //nolint:gosec // G203: значение получено json.Marshal — он экранирует < > & в \u-последовательности, поэтому «</script>» из данных не разорвёт тег
		},
		// managedTPRowsJSON отдаёт гриду строки табличной части, приводя значения
		// ДАТ к одному виду.
		//
		// Раньше здесь стоял jsJSON, то есть голый json.Marshal, а он печатает
		// time.Time в той зоне, в которой его отдал драйвер. Зоны у диалектов
		// разные — SQLite всегда UTC, pgx берёт зону Go-процесса, — поэтому одна
		// и та же дата приезжала в браузер как «1985-03-13T21:00:00Z» на SQLite
		// и «1985-03-14T00:00:00+03:00» на PostgreSQL. На хосте со смещением от
		// UTC у SQLite при этом съезжал КАЛЕНДАРНЫЙ ДЕНЬ (#1077).
		//
		// Формат — тот же, что у даты в шапке формы (formatDateValueForInput):
		// «2006-01-02T15:04» в местной зоне. Значит браузеру не нужно ни знать
		// про зоны, ни разбирать две разные метки: он получает готовые стенные
		// часы и работает с ними как с текстом.
		"managedTPRowsJSON": func(fields []metadata.Field, rows []map[string]any) template.JS {
			dateFields := make(map[string]bool, len(fields))
			for _, f := range fields {
				if f.Type == metadata.FieldTypeDate {
					dateFields[strings.ToLower(f.Name)] = true
				}
			}
			out := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				copied := make(map[string]any, len(row))
				for k, v := range row {
					if dateFields[strings.ToLower(k)] {
						copied[k] = formatDateValueForInput(v)
						continue
					}
					copied[k] = v
				}
				out = append(out, copied)
			}
			b, err := json.Marshal(out)
			if err != nil {
				return template.JS("[]")
			}
			return template.JS(b) //nolint:gosec // G203: JSON сформирован encoding/json
		},
		"managedTPColumnsJSON": func(plan []managedTPColumn, virtual []metadata.FormVirtualColumn, lang string) template.JS {
			fields := make([]metadata.Field, 0, len(plan))
			for _, column := range plan {
				fields = append(fields, column.Field)
			}
			virtual = filterVirtualTPColumns(fields, virtual)
			cols := make([]managedTPColumnJSON, 0, len(plan)+len(virtual))
			for _, column := range plan {
				field := column.Field
				cols = append(cols, managedTPColumnJSON{
					ID:          field.Name,
					Name:        field.DisplayName(lang),
					Type:        string(field.Type),
					Ref:         field.RefEntity,
					AllowCreate: field.RefEntity != "" && field.InlineCreateEnabled(true),
					Enum:        strings.HasPrefix(string(field.Type), "enum:"),
					Hidden:      column.Hidden,
					Index:       column.Index,
				})
			}
			// Виртуальные колонки идут ПОСЛЕ реквизитов: порядок хранимых колонок
			// менять нельзя, по нему пользователи ориентируются и его же ожидает
			// перенос данных из буфера.
			for _, vc := range virtual {
				if strings.TrimSpace(vc.Name) == "" || metadata.IsReservedFormVirtualColumnName(vc.Name) {
					continue
				}
				cols = append(cols, managedTPColumnJSON{
					ID:      vc.Name,
					Name:    vc.ColumnTitle(lang),
					Type:    string(metadata.FieldTypeString),
					Virtual: true,
					Width:   vc.Width,
					Index:   -1, // не реквизит ТЧ: индекса в метаданных нет
				})
			}
			b, err := json.Marshal(cols)
			if err != nil {
				return template.JS("[]")
			}
			return template.JS(b) //nolint:gosec // G203: JSON сформирован encoding/json
		},
		"managedTPColumnPlan": managedTPColumnPlan,
		// managedTPFieldsAttr — значение data-tp-fields в порядке отрисовки
		// ячеек: applyTableParts перестраивает строку по этому списку, и любое
		// расхождение порядка развалило бы соответствие ячеек колонкам.
		"managedTPFieldsAttr": func(plan []managedTPColumn) string {
			parts := make([]string, 0, len(plan))
			for _, column := range plan {
				field := column.Field
				part := field.Name + "|" + string(field.Type)
				if field.RefEntity != "" {
					part += ":" + field.RefEntity
				}
				parts = append(parts, part)
			}
			return strings.Join(parts, ",")
		},
		// managedTPHiddenColsJSON — имена скрытых колонок для перерисовки
		// таблицы после события формы: ячейка остаётся в DOM (иначе значение
		// потерялось бы при записи), но не показывается.
		"managedTPHiddenColsJSON": func(plan []managedTPColumn) string {
			names := make([]string, 0, len(plan))
			for _, column := range plan {
				if column.Hidden {
					names = append(names, column.Field.Name)
				}
			}
			b, err := json.Marshal(names)
			if err != nil {
				return "[]"
			}
			return string(b)
		},
		// managedTPColumnEventsJSON — «реквизит → имя элемента-колонки» для
		// колонок со своим ПриИзменении (план 154, Р4).
		"managedTPColumnEventsJSON": func(plan []managedTPColumn, readOnly bool) string {
			events := managedTPColumnEvents(plan, readOnly)
			if len(events) == 0 {
				return ""
			}
			b, err := json.Marshal(events)
			if err != nil {
				return ""
			}
			return string(b)
		},
		"managedTPVirtualColumns": filterVirtualTPColumns,
		"managedTPVirtualNamesJSON": func(virtual []metadata.FormVirtualColumn) string {
			filtered := filterVirtualTPColumns(nil, virtual)
			names := make([]string, 0, len(filtered))
			for _, vc := range filtered {
				names = append(names, vc.Name)
			}
			b, err := json.Marshal(names)
			if err != nil {
				return "[]"
			}
			return string(b)
		},
		// inputMaskDigitsOnly — просить ли у мобильной клавиатуры цифровой режим.
		// Решается по составу маски, а не отдельным ключом: «00.00.00» — это
		// заведомо цифры, и заставлять автора объявлять это второй раз незачем.
		"inputMaskDigitsOnly": metadata.InputMaskDigitsOnly,
		"wcell":               widgetCell,
		"echartsJSON":         echartsJSON,
		"stageChartJSON":      stageChartJSON,
		"stageSourceLabel":    stageSourceLabel,
		"splitCamel":          splitCamel,
		"fmtCell":             fmtReportCell,
		// fmtBool нужен там, где шаблон РАЗБИРАЕТ тип колонки: без типа поля
		// int64(1)-булево неотличимо от числа 1, и bool доезжал до fmtCell,
		// показываясь как «1» на SQLite против «true» на PostgreSQL (#1076).
		"fmtBool": func(v any) string {
			if v == nil {
				return ""
			}
			if asBool(v) {
				return "✓"
			}
			return "—"
		},
		"widgetChartsJSON": widgetChartsJSON,
		"pageChartsJSON":   pageChartsJSON,
		// pageRaw помечает уже санитизированный HTML страницы (план 66) как
		// безопасный. Источник — только ДобавитьСыройHTML, прошедший sanitizePageHTML.
		"pageRaw": func(s string) template.HTML { return template.HTML(s) }, //nolint:gosec // G203: источник — только ДобавитьСыройHTML, прошедший allowlist sanitizePageHTML
		// pageChart конвертирует чарт-блок страницы в widget.ChartData для echartsJSON.
		"pageChart": pageChartData,
	}
}

// cloneQuery — копия только поддерживаемого состояния списка, пригодная для
// правки. Произвольные query-параметры переносить в GET-формы нельзя: имя вроде
// "submit" создаёт form.submit-control и перекрывает нативный form.submit().
// Тип any, а не url.Values: в шаблон данные приходят через map[string]any, и
// там, где запрос не прокинут (юнит-тесты рендера), значение окажется nil.
func cloneQuery(current any) url.Values {
	vals := url.Values{}
	src, _ := current.(url.Values)
	for k, v := range src {
		if !isListStateQueryKey(k) {
			continue
		}
		vals[k] = append([]string(nil), v...)
	}
	return vals
}

func isListStateQueryKey(key string) bool {
	switch key {
	case "q", "sort", "dir", "activity", "view", "lm", "parent", "subsystem", "limit", "page":
		return true
	default:
		return strings.HasPrefix(key, "f.")
	}
}

// setQueryValue ставит значение параметра; пустое значение убирает параметр, а
// ключ с «*» на конце — все параметры с таким префиксом (например "f.*" — весь
// отбор списка).
func setQueryValue(vals url.Values, key, value string) {
	if prefix, ok := strings.CutSuffix(key, "*"); ok {
		for k := range vals {
			if strings.HasPrefix(k, prefix) {
				vals.Del(k)
			}
		}
		return
	}
	if value == "" {
		vals.Del(key)
		return
	}
	vals.Set(key, value)
}

func normalizedFormHotkey(value string) string {
	key := strings.ToUpper(strings.TrimSpace(value))
	switch key {
	case "F2", "F4", "F7", "F8", "F9", "F10":
		return key
	default:
		return ""
	}
}

func templateSource() string {
	return tplHead + tplNav + tplIndex + tplList + tplForm + tplManagedForm + tplRegister + tplReport + tplProcessor + tplAgentSettings + tplPOS + tplAbout + tplDeleteMarked + tplInfoReg + tplConstants + tplHistory + tplStages + tplJournal + tplScheduled + tplAccountReg + tplQueryBuilder + tplAllFunctions + tplSearch + tplQueryConsole + tplCodeConsole + tplGengen + tplForbidden + tplReportProblem + tplPageCustom + tplAppShell
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
.sys-drop{display:none;position:absolute;right:0;top:calc(100% + 4px);background:#fff;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.18);width:300px;max-width:calc(100vw - 16px);max-height:calc(100vh - 50px);max-height:calc(100dvh - 50px);overflow-y:auto;overscroll-behavior:contain;padding:4px 0;z-index:200}
.sys-drop.open{display:block}
.sys-drop>a,.sys-drop>button{display:block;padding:10px 16px;color:#334155;text-decoration:none;font-size:14px;width:100%;text-align:left;background:none;border:none;cursor:pointer;border-bottom:1px solid #f1f5f9}
.sys-drop>:last-child>a,.sys-drop>a:last-child,.sys-drop>button:last-child{border-bottom:none}
.sys-drop>a:hover,.sys-drop>button:hover{background:#f1f5f9}
.sys-drop details.sys-group{margin:0;max-width:none;background:#fff;border-radius:0;box-shadow:none;border-bottom:1px solid #f1f5f9}
.sys-drop .sys-group>summary{display:flex;align-items:center;gap:8px;padding:10px 16px;color:#334155;font-size:14px;font-weight:600;cursor:pointer;list-style:none;user-select:none}
.sys-drop .sys-group>summary::-webkit-details-marker{display:none}
.sys-drop .sys-group>summary::before{content:""}
.sys-drop .sys-group>summary::after{content:"▸";margin-left:auto;color:#94a3b8;font-size:12px;transition:transform .12s ease}
.sys-drop .sys-group[open]>summary::after{content:"▸";transform:rotate(90deg)}
.sys-drop .sys-group>summary:hover{background:#f8fafc;color:#1d4ed8}
.sys-group-body{padding:2px 0 5px;background:#f8fafc}
.sys-group-body>a{display:block;padding:8px 16px 8px 34px;color:#475569;text-decoration:none;font-size:13px;border-top:1px solid #f1f5f9}
.sys-group-body>a:hover{background:#eef2ff;color:#1d4ed8}
.sys-mode-block{padding:9px 16px 10px 34px;border-top:1px solid #f1f5f9}
.sys-mode-title{font-size:12px;color:#64748b;margin-bottom:6px;font-weight:600}
.sys-mode-block label{display:block;font-size:13px;padding:2px 0;cursor:pointer;color:#475569}
.sys-mode-block .sys-btn{margin-top:6px;background:#e2e8f0;color:#334155;font-size:12px;padding:6px 10px}
.sys-logout{position:sticky;bottom:-4px;background:#fff;z-index:1;margin:0;padding:0;box-shadow:0 -3px 8px rgba(15,23,42,.06)}
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
/* Кнопка, которой сейчас нечего делать (нет выбранной строки списка): гасим вид,
   но не ставим disabled — клик по ней объясняет причину. */
.btn[aria-disabled="true"]{opacity:.5}
.btn-danger{background:#ef4444;color:#fff}.btn-danger:hover{background:#dc2626}
.form-group{margin-bottom:16px}
label{display:block;font-size:13px;font-weight:500;margin-bottom:5px;color:#475569}
input[type=text],input[type=datetime-local],input[type=date],input[type=number],select{width:100%;padding:9px 12px;border:1px solid #e2e8f0;border-radius:7px;font-size:14px;outline:none;background:#fff}
input:focus,select:focus{border-color:#3b82f6;box-shadow:0 0 0 3px rgba(59,130,246,.15)}
/* Реквизит «только просмотр» (item_form: readonly, #1011): видно, что поле не
   для ввода. Без этого readonly-поле неотличимо от обычного, и пользователь
   решает, что форма сломалась, когда в него не печатается. */
input[readonly],textarea[readonly],select:disabled,input:disabled{background:#f1f5f9;color:#475569;cursor:default}
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
/* Стили плавающих виджетов (ИИ-помощник, панель сообщений) инжектирует сам
   /static/ui.js вместе с их разметкой — они работают и на страницах с
   собственным <head> (админские). Здесь их не дублировать. */
/* ===== Мобильная адаптация (этап 45). Правила в @media применяются только на
   узких экранах — десктопная вёрстка выше остаётся неизменной. ===== */
/* Гамбургер виден на любой ширине (план 157): на узком экране он выдвигает
   шторку, на широком — схлопывает панель объектов, отдавая её 210px форме. */
.nav-toggle{display:inline-flex;align-items:center;justify-content:center;background:none;border:none;color:#cbd5e1;font-size:20px;cursor:pointer;padding:4px 12px 4px 0;line-height:1}
.nav-toggle:hover{color:#fff}
/* Схлопывание — только широкий экран: на узком ширину панели забирает сама
   шторка, и nav-collapsed не должен вмешиваться в nav-open. Разведение по
   брейкпоинту делает классы независимыми — пересечься при ресайзе им негде.
   Класс висит на <html>, а не на body: ставится синхронно в <head> (#1122). */
@media (min-width:821px){
  html.nav-collapsed #ob-nav{display:none}
}
@media (max-width:820px){
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
.view-switch .view-btn{padding:6px 11px;color:#64748b;text-decoration:none;font:inherit;font-size:15px;line-height:1;background:#fff;border:0;border-right:1px solid #e2e8f0;cursor:pointer}
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
/* Глобальный поиск (план 82): строка в шапке и страница результатов */
.topbar-search{display:flex;align-items:center;margin:0 6px}
.topbar-search input{width:210px;max-width:34vw;padding:5px 10px;border:1px solid #475569;border-radius:5px;font-size:13px;background:#334155;color:#f1f5f9}
.topbar-search input::placeholder{color:#94a3b8}
.topbar-search input:focus{outline:none;border-color:#7dd3fc;background:#1e293b}
.search-results{display:flex;flex-direction:column;gap:8px}
.search-hit{display:block;padding:11px 14px;border:1px solid #e2e8f0;border-radius:8px;background:#fff;text-decoration:none;transition:box-shadow .15s,border-color .15s}
.search-hit:hover{box-shadow:0 4px 14px rgba(0,0,0,.08);border-color:#cbd5e1}
.search-hit-title{font-size:15px;font-weight:600;color:#1e293b;word-break:break-word}
.search-hit-deleted{text-decoration:line-through;opacity:.6}
.search-hit-meta{margin-top:4px;display:flex;gap:10px;flex-wrap:wrap;font-size:12.5px;color:#94a3b8}
.search-hit-entity{color:#64748b}
.search-hit-flag{color:#dc2626}
.search-hit-posted{color:#16a34a;font-weight:600}
/* Поле-картинка на форме */
.img-field{display:flex;flex-direction:column;gap:8px;align-items:flex-start}
.img-preview img{max-width:240px;max-height:240px;border-radius:8px;border:1px solid #e2e8f0;display:block;background:#f8fafc}
.img-actions{display:flex;gap:8px;align-items:center}
.img-field label.btn{cursor:pointer;margin:0}
</style>
</head><body data-ob-dev="{{if .Cfg.Dev}}1{{else}}0{{end}}">
{{end}}
`

const tplNav = `
{{define "nav"}}
<header class="topbar">
  <button class="nav-toggle" type="button" aria-label="{{t $.Lang "Меню"}}" title="{{t $.Lang "Меню"}}" aria-controls="ob-nav" aria-expanded="false" data-ob-nav-toggle>&#9776;</button>
  <a href="/ui/" class="topbar-title" style="text-decoration:none;color:inherit" title="{{t $.Lang "Главная"}}">{{if .Cfg.Logo}}<img src="/ui/logo" alt="" style="height:22px;max-width:90px;vertical-align:middle;margin-right:6px;border-radius:2px">{{end}}⚡ {{if .Cfg.AppName}}{{.Cfg.AppName}}{{else}}onebase{{end}}</a>
  <form class="topbar-search" method="get" action="/ui/search" role="search">
    <input type="search" name="q" value="{{.SearchQuery}}" placeholder="{{t $.Lang "Поиск по базе"}}" aria-label="{{t $.Lang "Поиск по базе"}}">
  </form>
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
      <a href="/ui/report-problem">{{t $.Lang "Сообщить об ошибке"}}</a>
      <details class="sys-group">
        <summary>{{t $.Lang "Профиль и интерфейс"}}</summary>
        <div class="sys-group-body">
          <div class="sys-mode-block">
            <div class="sys-mode-title">{{t $.Lang "Режим открытия форм"}}</div>
            <form method="post" action="/ui/form-mode" style="margin:0;padding:0">
              <label><input type="radio" name="mode" value="pages" {{if eq (printf "%v" .FormOpenModePersonal) "pages"}}checked{{end}}> {{t $.Lang "Отдельные страницы"}}</label>
              <label><input type="radio" name="mode" value="tabs" {{if eq (printf "%v" .FormOpenModePersonal) "tabs"}}checked{{end}}> {{t $.Lang "Вкладки"}}</label>
              <label><input type="radio" name="mode" value="default" {{if eq (printf "%v" .FormOpenModePersonal) ""}}checked{{end}}> {{t $.Lang "По умолчанию (глобально)"}}</label>
              <button type="submit" class="sys-btn">{{t $.Lang "Применить"}}</button>
            </form>
          </div>
          {{if .HasAuth}}{{if not .DenyPasswdChange}}<a href="/ui/profile/passwd">{{t $.Lang "Сменить пароль"}}</a>{{end}}
          <a href="/ui/profile/2fa">{{t $.Lang "Второй фактор"}}</a>{{end}}
        </div>
      </details>
      {{if not .IsAdmin}}
      <details class="sys-group">
        <summary>{{t $.Lang "Платформенные возможности"}}</summary>
        <div class="sys-group-body">
          <a href="/ui/pos">{{t $.Lang "Рабочее место кассира (РМК)"}}</a>
          {{if .HasStages}}<a href="/ui/stages">{{t $.Lang "Этапы — где застряло"}}</a>{{end}}
        </div>
      </details>
      {{end}}
      {{if .IsAdmin}}
      <details class="sys-group">
        <summary>{{t $.Lang "Администрирование"}}</summary>
        <div class="sys-group-body">
          <a href="/ui/admin/users">{{t $.Lang "Пользователи"}}</a>
          <a href="/ui/admin/roles">{{t $.Lang "Роли и права"}}</a>
          <a href="/ui/admin/sessions">{{t $.Lang "Активные пользователи"}}</a>
          <a href="/ui/admin/auth">{{t $.Lang "Аутентификация"}}</a>
          <a href="/ui/admin/api-tokens">{{t $.Lang "API-токены"}}</a>
          <a href="/ui/admin/audit">{{t $.Lang "Журнал изменений"}}</a>
          {{if .HasStages}}<a href="/ui/stages">{{t $.Lang "Этапы — где застряло"}}</a>{{end}}
          <a href="/ui/admin/rls">{{t $.Lang "Диагностика RLS"}}</a>
        </div>
      </details>
      <details class="sys-group">
        <summary>{{t $.Lang "Интеграции и задания"}}</summary>
        <div class="sys-group-body">
          <a href="/ui/admin/exchange">{{t $.Lang "Обмен данными"}}</a>
          <a href="/ui/admin/intake">{{t $.Lang "Входная приёмка"}}</a>
          <a href="/ui/admin/scheduled">{{t $.Lang "Регламентные задания"}}</a>
          <a href="/ui/admin/queue">{{t $.Lang "Очередь фоновых заданий"}}</a>
          <a href="/ui/admin/webhooks">{{t $.Lang "Журнал веб-хуков"}}</a>
        </div>
      </details>
      <details class="sys-group">
        <summary>{{t $.Lang "Обслуживание базы"}}</summary>
        <div class="sys-group-body">
          <a href="/ui/delete-marked">{{t $.Lang "Удалить помеченные"}}</a>
          <a href="/ui/admin/cleanup">{{t $.Lang "Диагностика базы"}}</a>
        </div>
      </details>
      <details class="sys-group">
        <summary>{{t $.Lang "Расширения и оборудование"}}</summary>
        <div class="sys-group-body">
          <a href="/ui/settings/agent">{{t $.Lang "Настройки агента оборудования"}}</a>
          <a href="/ui/admin/extforms">{{t $.Lang "Внешние печатные формы"}}</a>
          <a href="/ui/admin/extreports">{{t $.Lang "Внешние отчёты"}}</a>
          <a href="/ui/admin/extprocessors">{{t $.Lang "Внешние обработки"}}</a>
        </div>
      </details>
      <details class="sys-group">
        <summary>{{t $.Lang "Инструменты разработчика"}}</summary>
        <div class="sys-group-body">
          <a href="/ui/all-functions">{{t $.Lang "Все функции"}}</a>
          <a href="/ui/dev/query-console">{{t $.Lang "Консоль запросов"}}</a>
          <a href="/ui/dev/code-console">{{t $.Lang "Консоль кода"}}</a>
          <a href="/ui/dev/gengen">{{t $.Lang "Gengen"}}</a>
        </div>
      </details>
      {{end}}
      <form class="sys-logout" method="POST" action="/logout"><button type="submit" style="display:block;width:100%;padding:10px 16px;color:#dc2626;text-decoration:none;font-size:14px;text-align:left;background:none;border:none;border-top:1px solid #f1f5f9;cursor:pointer">{{t $.Lang "Выйти"}}</button></form>
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
  {{/* #304: home_page.hidden прячет ведущую «Главную» — навигация только по разделам. */}}
  {{if not .HideHome}}<a href="/ui/" class="{{if not .CurrentSubsystem}}active{{end}}">{{$home}}</a>{{end}}
  {{/* #215.2: не дублируем ведущую «Главная», если в базе есть одноимённая
       подсистема (её представление совпадает с меткой домашней ссылки). Когда
       ведущей ссылки нет (HideHome), одноимённый раздел, наоборот, показываем. */}}
  {{range .Subsystems}}{{if or $.HideHome (ne (.DisplayName $.Lang) $home)}}<a href="/ui/?subsystem={{.Name}}" class="{{if eq .Name $.CurrentSubsystem}}active{{end}}">{{lucideIcon .Icon}}{{.DisplayName $.Lang}}</a>{{end}}{{end}}
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
{{define "dashboard-style"}}
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
/* Кликабельный счётчик: остаётся числом (тот же кегль и цвет), но ведёт себя
   как ссылка — подчёркивание только при наведении, чтобы карточка не выглядела
   пестрее соседних. */
a.w-kpi-link{display:block;text-decoration:none;color:#0f172a}
a.w-kpi-link:hover{color:#1a4a80;text-decoration:underline}
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
{{end}}

{{define "dashboard-body"}}
  <h2 style="margin-bottom:14px">{{.HomeTitle}}</h2>
  {{if .DefaultedHome}}<div class="w-default-hint">Стартовая страница не настроена — показаны последние документы из аудита. Создайте <code>config/home_page.yaml</code> и виджеты в <code>widgets/</code>, чтобы оформить дашборд.</div>{{end}}
  <div class="dash">
    {{range .WidgetRows}}
    <div class="dash-row">
      {{range .}}{{template "widget-card" .}}{{end}}
    </div>
    {{end}}
  </div>
{{end}}

{{define "dashboard-scripts"}}
<script type="application/json" id="ob-widget-charts">{{widgetChartsJSON .WidgetResults}}</script>
<script src="/vendor/echarts/echarts.min.js"></script>
{{end}}

{{define "page-index"}}
{{template "head" .}}{{template "nav" .}}
{{template "dashboard-style" .}}
<main>
  {{template "dashboard-body" .}}
</main></div>
{{template "dashboard-scripts" .}}
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
  {{/* Со ссылкой (link:) значение становится переходом к списку/отчёту:
       счётчик «в карантине: 3» должен открывать эту очередь, а не заставлять
       искать её руками. Без link — прежняя некликабельная карточка. */}}
  {{if .KPI}}
    {{if .Link}}<a class="w-kpi-value w-kpi-link" href="{{.Link}}">{{.KPI.Display}}</a>
    {{else}}<div class="w-kpi-value">{{.KPI.Display}}</div>{{end}}
  {{else}}<div class="w-empty">нет данных</div>{{end}}
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
  {{range .Actions}}<a href="{{.URL}}"{{if .NewTab}} target="_blank" rel="noopener"{{end}}>{{.Label}}</a>{{else}}<div class="w-empty">нет действий</div>{{end}}
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
{{define "detail-panel"}}
<aside class="ob-detail" id="ob-detail" hidden data-ob-default-width="{{detailPanelDefaultWidth .Entity}}" aria-label="{{t $.Lang "Детали записи"}}">
  <div class="ob-detail-grip" data-ob-detail-grip title="{{t $.Lang "Потяните, чтобы изменить ширину"}}"></div>
  <div class="ob-detail-body">
    <div class="ob-detail-head">
      <strong data-ob-detail-title></strong>
      <button type="button" class="ob-detail-close" data-ob-detail-close title="{{t $.Lang "Скрыть панель"}}">✕</button>
    </div>
    <div class="ob-detail-tabs" data-ob-detail-tabs></div>
    <div class="ob-detail-fields" data-ob-detail-fields></div>
    <p class="ob-detail-empty" data-ob-detail-empty>{{t $.Lang "Выберите строку"}}</p>
  </div>
</aside>
{{end}}

{{define "detail-panel-toggle"}}
<div class="view-switch">
  <button class="view-btn" type="button" data-ob-detail-toggle aria-controls="ob-detail" aria-expanded="false" title="{{t $.Lang "Детали записи"}}">▤</button>
</div>
{{end}}

{{define "page-list"}}
{{template "head" .}}{{template "nav" .}}
<main class="main-list">
<div class="row-top">
  <h2>{{.Entity.DisplayName $.Lang}}</h2>
  <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">
    <div class="view-switch">
      {{/* Переключение вида меняет только вид: поиск, отбор и сортировка
           остаются — их сбрасывает лишь явная очистка. */}}
      <a class="view-btn{{if and (not .TreeView) (not .TilesView)}} active{{end}}" href="{{listURL $.Query "view" ""}}" title="{{t $.Lang "Список"}}">☰</a>
      <a class="view-btn{{if .TilesView}} active{{end}}" href="{{listURL $.Query "view" "tiles"}}" title="{{t $.Lang "Плитка"}}">▦</a>
      {{if .Entity.Hierarchical}}<a class="view-btn{{if .TreeView}} active{{end}}" href="?view=tree{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}" title="{{t $.Lang "Дерево"}}">📂</a>{{end}}
    </div>
    {{template "detail-panel-toggle" .}}
    {{if and .Entity.Activity (not .TreeView)}}
    <div class="view-switch" title="{{t $.Lang "Активность"}}">
      <a class="view-btn{{if eq .Params.ActivityScope "active"}} active{{end}}" href="{{listURL $.Query "activity" "active"}}">{{t $.Lang "Активные"}}</a>
      <a class="view-btn{{if eq .Params.ActivityScope "inactive"}} active{{end}}" href="{{listURL $.Query "activity" "inactive"}}">{{t $.Lang "Скрытые"}}</a>
      <a class="view-btn{{if eq .Params.ActivityScope "all"}} active{{end}}" href="{{listURL $.Query "activity" "all"}}">{{t $.Lang "Все"}}</a>
    </div>
    {{end}}
    {{if not .TreeView}}
      {{if .Feed}}<a class="btn btn-secondary btn-sm" href="{{listURL $.Query "lm" "pages"}}" title="{{t $.Lang "Показывать постранично"}}">▤ {{t $.Lang "Страницы"}}</a>
      {{else}}<a class="btn btn-secondary btn-sm" href="{{listURL $.Query "lm" "feed"}}" title="{{t $.Lang "Лента с догрузкой по скроллу"}}">≣ {{t $.Lang "Лента"}}</a>{{end}}
    {{end}}
    {{if .Entity.Hierarchical}}
      {{if .UpURL}}<a class="btn btn-secondary btn-sm" href="{{.UpURL}}">{{t $.Lang "↑ Наверх"}}</a>{{end}}
      {{if .CanWrite}}
      <a class="btn btn-primary" data-ob-list-create title="Insert" aria-keyshortcuts="Insert" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new?{{if .ParentStr}}parent={{.ParentStr}}&{{end}}subsystem={{$.CurrentSubsystem}}">{{t $.Lang "+ Элемент"}}</a>
      <a class="btn btn-secondary" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new?is_folder=true{{if .ParentStr}}&parent={{.ParentStr}}{{end}}">{{t $.Lang "📁 Группа"}}</a>
      {{end}}
    {{else}}
      {{if .CanWrite}}<a class="btn btn-primary" data-ob-list-create title="Insert" aria-keyshortcuts="Insert" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "+ Создать"}}</a>{{end}}
    {{end}}
    {{/* На свежем списке строка не выбрана — кнопка приходит с сервера уже
         приглушённой, дальше её состояние ведёт ui.js по выделению. */}}
    <button type="button" id="list-actions-btn" class="btn btn-secondary" data-ob-list-actions aria-disabled="true" title="{{t $.Lang "Сначала выберите строку списка"}}">⚙ {{t $.Lang "Действия"}} ▾</button>
    <a class="btn btn-sm" href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/excel{{listQuerySuffix .Params}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
  </div>
</div>
<form method="GET" style="display:flex;gap:8px;margin-bottom:12px;max-width:460px">
  <input type="text" id="ob-list-search" name="q" value="{{.Params.Search}}" placeholder="{{t $.Lang "Поиск..."}}" style="flex:1;padding:7px 12px;border:1px solid #e2e8f0;border-radius:6px;font-size:14px" data-ob-list-search data-ob-auto-submit="320" title="Ctrl+F" aria-keyshortcuts="Control+F">
  {{if .Params.Search}}<a class="btn btn-sm" href="{{listURL $.Query "q" ""}}" style="background:#e2e8f0;color:#475569;align-self:center">✕</a>{{end}}
  {{/* Форма владеет только q — остальное состояние списка (вид, папка,
       отбор, сортировка, раздел) уезжает скрытыми полями, иначе поиск
       выкидывал бы из плитки и из папки. */}}
  {{if .TreeView}}{{listHidden $.Query "q" "view"}}{{else}}{{listHidden $.Query "q"}}{{end}}
</form>
{{if .Breadcrumbs}}
<nav class="breadcrumb">
  <a href="{{listURL $.Query "parent" ""}}">{{t $.Lang "Корень"}}</a>
  {{range .Breadcrumbs}}<span>›</span><a href="{{listURL $.Query "parent" .ID}}">{{.Label}}</a>{{end}}
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
    <a class="btn btn-sm" href="{{listURL $.Query "f.*" ""}}" style="background:#e2e8f0;color:#475569">{{t $.Lang "Сбросить"}}</a>
  </div>
  {{/* Форма владеет отбором f.* — поиск, вид, папку и сортировку переносим. */}}
  {{if .TreeView}}{{listHidden $.Query "f.*" "view"}}{{else}}{{listHidden $.Query "f.*"}}{{end}}
  </form>
</details>

{{$obRefresh := liveListRefreshOn .Entity}}
<div class="ob-list-wrap">
<div class="card" data-ob-live="{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}"{{if $obRefresh}} data-ob-refresh-on="{{$obRefresh}}"{{end}}>
{{if .TreeView}}
{{/* ===== TREE VIEW ===== */}}
{{if .TreeRows}}
{{$treeCols := listColumns .Entity}}
<div style="overflow-x:auto">
<table
  data-ob-row-base="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}"
  data-ob-row-open-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}"
  data-ob-row-mark-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete?mark=1"
  data-ob-row-unmark-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete?mark=0"
  data-ob-row-del-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete"
  data-ob-row-unpost-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/unpost"
  data-ob-row-activity-show-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/activity?active=1"
  data-ob-row-activity-hide-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/activity?active=0"
  data-ob-row-detail-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/detail-panel"
  data-ob-row-copy-tpl="{{if $.CanWrite}}/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/new?copy=__ID__{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}{{end}}"
  data-ob-row-activity-enabled="{{if $.Entity.Activity}}1{{end}}"
  data-ob-row-folder-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}?parent=__ID__{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}"><thead><tr>
  {{range $treeCols}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  <th style="width:90px"></th>
</tr></thead><tbody>
{{range .TreeRows}}{{$row := .}}{{$isFolder := index $row "is_folder"}}{{$depth := index $row "_depth"}}
<tr {{if index $row "deletion_mark"}}style="opacity:0.45;text-decoration:line-through;cursor:pointer"{{else}}style="cursor:pointer"{{end}}
  data-ob-list-row tabindex="-1" aria-selected="false" aria-keyshortcuts="ArrowUp ArrowDown Enter F2{{if $.CanWrite}} F9{{end}}{{if and $.CanDelete (not (index $row "_is_predefined"))}} Delete{{end}}"
  data-tree-id="{{index $row "id"}}"
  data-tree-depth="{{$depth}}"
  data-tree-parent="{{index $row "parent_id"}}"
  data-ob-id="{{index $row "id"}}"
  data-predefined="{{if index $row "_is_predefined"}}1{{end}}"
  data-is-folder="{{if $isFolder}}1{{end}}"
  data-posted="{{if index $row "posted"}}1{{end}}"
  data-marked="{{if index $row "deletion_mark"}}1{{end}}"
  data-activity-inactive="{{if index $row "_activity_inactive"}}1{{end}}"
>
  {{range $i, $col := $treeCols}}
    {{if treeColumn $treeCols $i}}
      <td>
        <span style="display:inline-block;width:{{mul (int $depth) 20}}px"></span>
        {{if $isFolder}}
          <button type="button" class="tree-toggle" data-folder-id="{{index $row "id"}}" data-expanded="0" data-loaded="0" title="{{t $.Lang "Свернуть/Развернуть"}}"
            style="background:none;border:none;cursor:pointer;padding:0 2px;font-size:13px">▶</button>
          📁
        {{else}}📄{{end}}
        {{if eq (str $col.Type) "date"}}{{fmtDate (index $row $col.Name)}}{{else if eq (str $col.Type) "bool"}}{{fmtBool (index $row $col.Name)}}{{else if isRichText (str $col.Type)}}{{richPlain (index $row $col.Name)}}{{else if isEnum (str $col.Type)}}{{enumLabel $.EnumLabels $col.Name (str (index $row $col.Name))}}{{else}}{{fmtCell (index $row $col.Name)}}{{end}}{{if index $row "_is_predefined"}} <span title="{{t $.Lang "Предопределённый"}}" style="color:#f59e0b;font-size:11px">★</span>{{end}}
      </td>
    {{else if eq (str $col.Type) "date"}}<td>{{fmtDate (index $row $col.Name)}}</td>
    {{else if eq (str $col.Type) "bool"}}<td>{{fmtBool (index $row $col.Name)}}</td>
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
<div class="tile-grid" role="listbox"
  data-ob-row-base="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}"
  data-ob-row-open-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}"
  data-ob-row-mark-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete?mark=1"
  data-ob-row-unmark-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete?mark=0"
  data-ob-row-del-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete"
  data-ob-row-unpost-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/unpost"
  data-ob-row-activity-show-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/activity?active=1"
  data-ob-row-activity-hide-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/activity?active=0"
  data-ob-row-detail-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/detail-panel"
  data-ob-row-copy-tpl="{{if $.CanWrite}}/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/new?copy=__ID__{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}{{end}}"
  data-ob-row-activity-enabled="{{if $.Entity.Activity}}1{{end}}"
  data-ob-row-folder-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}{{listURL $.Query "parent" "__ID__"}}">
{{range .Rows}}{{$row := .}}{{$isFolder := index $row "is_folder"}}
<div class="tile-card{{if index $row "deletion_mark"}} tile-deleted{{end}}"
  data-ob-list-row tabindex="-1" aria-selected="false" aria-keyshortcuts="ArrowUp ArrowDown Enter F2{{if $.CanWrite}} F9{{end}}{{if and $.CanDelete (not (index $row "_is_predefined"))}} Delete{{end}}" role="option"
  data-ob-id="{{index $row "id"}}"
  data-predefined="{{if index $row "_is_predefined"}}1{{end}}"
  data-is-folder="{{if $isFolder}}1{{end}}"
  data-posted="{{if index $row "posted"}}1{{end}}"
  data-marked="{{if index $row "deletion_mark"}}1{{end}}"
  data-activity-inactive="{{if index $row "_activity_inactive"}}1{{end}}"
>
  {{range $f := $tile.ImageFields}}{{$iv := index $row $f.Name}}
  <div class="tile-img"{{if $iv}} style="background-image:url('/ui/_image/{{$iv}}')"{{end}}>{{if not $iv}}🖼{{end}}</div>
  {{end}}
  {{with $tile.TitleField}}
    <div class="tile-title">{{if $.Entity.Hierarchical}}{{if $isFolder}}📁 {{else}}📄 {{end}}{{end}}{{fmtCell (index $row .Name)}}{{if index $row "_is_predefined"}} <span title="{{t $.Lang "Предопределённый элемент"}}" style="color:#f59e0b;font-size:11px">★</span>{{end}}{{if eq (str $.Entity.Kind) "document"}}{{if index $row "posted"}} <span class="tile-posted" title="{{t $.Lang "Проведён"}}">✓</span>{{end}}{{end}}</div>
  {{end}}
  {{with $tile.SubtitleField}}{{$v := index $row .Name}}{{if hasValue $v}}
    <div class="tile-subtitle">{{if eq (str .Type) "date"}}{{fmtDate $v}}{{else if eq (str .Type) "bool"}}{{fmtBool $v}}{{else if isRichText (str .Type)}}{{richPlain $v}}{{else if isEnum (str .Type)}}{{enumLabel $.EnumLabels .Name (str $v)}}{{else}}{{fmtCell $v}}{{end}}</div>
  {{end}}{{end}}
  {{range $f := $tile.Fields}}{{$v := index $row $f.Name}}{{if hasValue $v}}
    <div class="tile-field"><span class="tile-label">{{$f.DisplayName $.Lang}}:</span> {{if eq (str $f.Type) "date"}}<span class="tile-val">{{fmtDate $v}}</span>{{else if eq (str $f.Type) "bool"}}<span class="tile-val">{{fmtBool $v}}</span>{{else if isRichText (str $f.Type)}}<span class="tile-val">{{richPlain $v}}</span>{{else if isEnum (str $f.Type)}}<span class="tile-val">{{enumLabel $.EnumLabels $f.Name (str $v)}}</span>{{else if isImage (str $f.Type)}}<span class="tile-val">{{if $v}}<img src="/ui/_image/{{$v}}" style="height:28px;width:28px;object-fit:cover;border-radius:5px;vertical-align:middle" alt="">{{else}}—{{end}}</span>{{else}}<span class="tile-val">{{fmtCell $v}}</span>{{end}}</div>
  {{end}}{{end}}
  <div class="tile-foot">
    {{if and $isFolder $.Entity.Hierarchical}}
      <a class="btn btn-sm btn-secondary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}{{listURL $.Query "parent" (str (index $row "id"))}}">{{t $.Lang "▶ Войти"}}</a>
    {{else}}
      <a class="btn btn-sm btn-primary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Открыть"}}</a>
    {{end}}
  </div>
</div>{{end}}
</div>
{{else}}
<p class="empty">{{if .Params.Search}}{{t $.Lang "Ничего не найдено по запросу"}} «{{.Params.Search}}» — <a href="{{listURL $.Query "q" ""}}">{{t $.Lang "сбросить поиск"}}</a>{{else}}{{t $.Lang "Записей нет"}} — <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">{{t $.Lang "создать первую"}}</a>{{end}}</p>
{{end}}
{{else}}
{{/* ===== LIST VIEW (default) ===== */}}
{{if .Rows}}
<div style="overflow-x:auto">
<table><thead><tr>
  {{if eq (str .Entity.Kind) "document"}}<th style="width:36px">✓</th>{{end}}
  {{range listColumns .Entity}}
  <th>
    <a href="{{listURL $.Query "sort" .Name "dir" (nextDir $params .Name)}}">
      {{.DisplayName $.Lang}} {{sortIcon $params .Name}}
    </a>
  </th>
  {{end}}
  <th style="width:90px"></th>
</tr></thead><tbody id="list-body"
  data-ob-row-base="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}"
  data-ob-row-open-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}"
  data-ob-row-mark-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete?mark=1"
  data-ob-row-unmark-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete?mark=0"
  data-ob-row-del-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/delete"
  data-ob-row-unpost-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/unpost"
  data-ob-row-activity-show-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/activity?active=1"
  data-ob-row-activity-hide-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/activity?active=0"
  data-ob-row-detail-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/__ID__/detail-panel"
  data-ob-row-copy-tpl="{{if $.CanWrite}}/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/new?copy=__ID__{{if $.CurrentSubsystem}}&subsystem={{$.CurrentSubsystem}}{{end}}{{end}}"
  data-ob-row-activity-enabled="{{if $.Entity.Activity}}1{{end}}"
  data-ob-row-folder-tpl="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}{{listURL $.Query "parent" "__ID__"}}">
{{range .Rows}}{{$row := .}}{{$isFolder := index $row "is_folder"}}
<tr {{if index $row "deletion_mark"}}style="opacity:0.45;text-decoration:line-through;cursor:pointer"{{else}}style="cursor:pointer"{{end}}
  data-ob-list-row tabindex="-1" aria-selected="false" aria-keyshortcuts="ArrowUp ArrowDown Enter F2{{if $.CanWrite}} F9{{end}}{{if and $.CanDelete (not (index $row "_is_predefined"))}} Delete{{end}}"
  data-ob-id="{{index $row "id"}}"
  data-predefined="{{if index $row "_is_predefined"}}1{{end}}"
  data-is-folder="{{if $isFolder}}1{{end}}"
  data-posted="{{if index $row "posted"}}1{{end}}"
  data-marked="{{if index $row "deletion_mark"}}1{{end}}"
  data-activity-inactive="{{if index $row "_activity_inactive"}}1{{end}}"
>
  {{if eq (str $.Entity.Kind) "document"}}
    <td style="text-align:center">
      {{if index $row "posted"}}<span style="color:#16a34a;font-weight:700" title="{{t $.Lang "Проведён"}}">✓</span>{{else}}<span style="color:#94a3b8" title="{{t $.Lang "Не проведён"}}">—</span>{{end}}
    </td>
  {{end}}
  {{range listColumns $.Entity}}
    {{if eq (str .Type) "date"}}<td style="white-space:nowrap">{{fmtDate (index $row .Name)}}</td>
    {{else if eq (str .Type) "bool"}}<td style="white-space:nowrap">{{fmtBool (index $row .Name)}}</td>
    {{else if isImage (str .Type)}}<td>{{$iv := index $row .Name}}{{if $iv}}<img src="/ui/_image/{{$iv}}" style="height:34px;width:34px;object-fit:cover;border-radius:5px;vertical-align:middle" alt="">{{else}}<span style="color:#cbd5e1">—</span>{{end}}</td>
    {{else if isRichText (str .Type)}}<td style="white-space:nowrap;color:#64748b">{{richPlain (index $row .Name)}}</td>
    {{else if isEnum (str .Type)}}<td style="white-space:nowrap">{{enumLabel $.EnumLabels .Name (str (index $row .Name))}}</td>
    {{else}}<td style="white-space:nowrap">{{if and (eq .Name "Наименование") $.Entity.Hierarchical}}{{if $isFolder}}📁 {{else}}📄 {{end}}{{end}}{{fmtCell (index $row .Name)}}{{if and (eq .Name "Наименование") (index $row "_is_predefined")}} <span title="{{t $.Lang "Предопределённый элемент"}}" style="color:#f59e0b;font-size:11px">★</span>{{end}}</td>{{end}}
  {{end}}
  <td>
    {{if and $isFolder $.Entity.Hierarchical}}
      <a class="btn btn-sm btn-secondary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}{{listURL $.Query "parent" (str (index $row "id"))}}">{{t $.Lang "▶ Войти"}}</a>
    {{else}}
      <a class="btn btn-sm btn-primary" href="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/{{index $row "id"}}{{if $.CurrentSubsystem}}?subsystem={{$.CurrentSubsystem}}{{end}}">{{t $.Lang "Открыть"}}</a>
    {{end}}
  </td>
</tr>{{end}}
</tbody></table>
</div>
{{else}}
<p class="empty">{{if .Params.Search}}{{t $.Lang "Ничего не найдено по запросу"}} «{{.Params.Search}}» — <a href="{{listURL $.Query "q" ""}}">{{t $.Lang "сбросить поиск"}}</a>{{else}}{{t $.Lang "Записей нет"}} — <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new">{{t $.Lang "создать первую"}}</a>{{end}}</p>
{{end}}
{{end}}
</div>
{{template "detail-panel" .}}
</div>
{{if and .Feed (not .TreeView)}}
{{/* Лента: догрузка по скроллу. Без JS «Показать ещё» = переход на след. страницу. */}}
{{if .HasNext}}
<div id="feed-more" data-next="{{.NextPage}}" data-pages="{{.TotalPages}}" data-container="{{if .TilesView}}.tile-grid{{else}}#list-body{{end}}" data-item="{{if .TilesView}}.tile-card{{else}}tr{{end}}" style="margin-top:14px;text-align:center">
  <a class="btn btn-secondary btn-sm" href="{{listURL $.Query "page" (str .NextPage) "lm" "feed"}}">{{t $.Lang "Показать ещё"}}</a>
</div>
{{end}}
{{if gt .Total 0}}<div style="color:#94a3b8;font-size:12px;margin-top:8px;text-align:center">{{t $.Lang "Загружено:"}} <span id="feed-loaded">{{len .Rows}}</span> {{t $.Lang "из"}} {{.Total}}</div>{{end}}
{{else if gt .TotalPages 1}}
<div style="display:flex;align-items:center;gap:8px;margin-top:12px;flex-wrap:wrap">
  {{if .HasPrev}}<a class="btn btn-secondary btn-sm" href="{{listURL $.Query "page" (str .PrevPage)}}">{{t $.Lang "← Назад"}}</a>{{end}}
  <span style="color:#64748b;font-size:13px">{{t $.Lang "Стр."}} {{.Page}} {{t $.Lang "из"}} {{.TotalPages}} ({{.Total}} {{t $.Lang "записей"}})</span>
  {{if .HasNext}}<a class="btn btn-secondary btn-sm" href="{{listURL $.Query "page" (str .NextPage)}}">{{t $.Lang "Вперёд →"}}</a>{{end}}
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
    "copy" (t $.Lang "Скопировать")
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
    "actionsReady" (t $.Lang "Команды для выбранной строки")
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
{{if .TabTitle}}<meta name="ob-tab-title" content="{{.TabTitle}}">{{end}}
<main>
<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:20px;max-width:1400px">
  <h2 style="margin-bottom:0">{{if .IsNew}}{{t $.Lang "Создать"}} — {{.Entity.DisplayName $.Lang}}{{else}}{{if .RecordTitle}}{{.RecordTitle}}{{else}}{{t $.Lang "Редактировать"}} — {{.Entity.DisplayName $.Lang}}{{end}}{{end}}</h2>
  {{if .IsPopup}}
  <a href="#" data-ob-popup-cancel title="{{t $.Lang "Закрыть"}}" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
  {{else}}
  <a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}" data-ob-close-tab title="{{t $.Lang "Закрыть"}}" style="font-size:22px;line-height:1;color:#94a3b8;text-decoration:none;padding:2px 8px;border-radius:5px;background:#f1f5f9;font-weight:300">×</a>
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
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form" title="Ctrl+S" aria-keyshortcuts="Control+S">{{t $.Lang "Записать"}}</button>{{end}}
  {{if .Entity.Posting}}
    {{if ne (index .Values "deletion_mark") "true"}}
      {{if $.CanPost}}<button class="btn btn-primary" type="submit" name="_action" value="post" form="main-form">{{if $.Entity.PostCaption}}{{$.Entity.PostCaption}}{{else}}{{t $.Lang "Провести"}}{{end}}</button>{{end}}
      {{if and $.CanPost (not $.Entity.PostAndCloseHidden)}}<button class="btn btn-post" type="submit" name="_action" value="post_and_close" form="main-form" title="Ctrl+Enter" aria-keyshortcuts="Control+Enter">{{if $.Entity.PostCaption}}{{$.Entity.PostCaption}} и закрыть{{else}}{{t $.Lang "Провести и закрыть"}}{{end}}</button>{{end}}
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
    {{/* Копия открывается формой создания: пока её не записали, в базе ничего
         нет. Горячей клавиши здесь нет намеренно — F9 на карточке принадлежит
         копированию строки табличной части. */}}
    {{if .CanWrite}}<a href="/ui/{{lower (str .Entity.Kind)}}/{{lower .Entity.Name}}/new?copy={{.ID}}" class="btn btn-sm btn-secondary">{{t $.Lang "Скопировать"}}</a>{{end}}
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

{{template "stage-route" .}}

{{/* Movement links (collapsed) */}}
{{if and (not .IsNew) .DocMovements}}
<div style="margin-bottom:12px;display:flex;gap:6px;flex-wrap:wrap">
  {{range $regName, $rows := .DocMovements}}
  {{$columns := index $.DocMovementColumns $regName}}
  <details style="display:inline">
    <summary style="display:inline;cursor:pointer;font-size:12px;padding:4px 10px;background:#f0f4ff;color:#1a4a80;border-radius:4px;font-weight:600;list-style:none">
      {{$regName}} ({{len $rows}}) ▾
    </summary>
    <div style="position:absolute;z-index:100;background:#fff;border:1px solid #e2e8f0;border-radius:8px;box-shadow:0 4px 16px rgba(0,0,0,.12);margin-top:4px;min-width:300px;max-height:300px;overflow:auto">
      <table class="list-tbl" style="font-size:12px;margin:0">
        <tr>{{if $columns.ShowLineNumber}}<th>{{t $.Lang "№"}}</th>{{end}}{{if $columns.ShowKind}}<th>{{t $.Lang "Вид"}}</th>{{end}}{{$first := index $rows 0}}{{range $k, $v := $first}}{{if and (ne $k "line_number") (ne $k "вид_движения")}}<th>{{$k}}</th>{{end}}{{end}}</tr>
        {{range $i, $row := $rows}}
        <tr>
          {{if $columns.ShowLineNumber}}<td>{{index $row "line_number"}}</td>{{end}}
          {{if $columns.ShowKind}}<td>{{$kind := index $row "вид_движения"}}{{if eq $kind "Приход"}}<span style="color:#16a34a">▲</span>{{else if eq $kind "Расход"}}<span style="color:#dc2626">▼</span>{{else}}{{$kind}}{{end}}</td>{{end}}
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
{{range itemFormHidden .Entity}}<input type="hidden" name="{{.Name}}" value="{{index $.Values .Name}}">
{{end}}{{range itemFormVisible .Entity}}{{$fn := .Name}}{{$flabel := .DisplayName $.Lang}}{{$ro := .ReadOnly}}
{{/* $ro — реквизит помечен «только просмотр» в item_form (#1011). Поля ввода
     получают readonly (значение всё равно уходит в запрос), а списки — disabled
     плюс скрытый input-двойник: отключённый select браузер не отправляет вовсе,
     и без двойника сборка полей формы обнулила бы реквизит при записи. */}}
<div class="form-group">
  <label>{{$flabel}}{{if $ro}} <span style="color:#94a3b8;font-size:11px;font-weight:400">({{t $.Lang "только просмотр"}})</span>{{end}}</label>
  {{if isRef (str .Type)}}
    <div style="display:flex;gap:6px;align-items:center">
      {{if $ro}}<input type="hidden" name="{{$fn}}" value="{{index $.Values $fn}}">{{end}}
      <select id="ref-{{$fn}}"{{if not $ro}} name="{{$fn}}"{{end}} style="flex:1" data-ref-entity="{{.RefEntity}}"{{if and (not $ro) (.InlineCreateEnabled false)}} data-ref-allow-create="1"{{end}}{{if $ro}} disabled{{end}}>
        <option value="">{{t $.Lang "— выбрать —"}}</option>
        {{range index $.RefOptions $fn}}
        <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values $fn)}}selected{{end}}>{{index . "_label"}}</option>
        {{end}}
      </select>
      {{if not $ro}}<button type="button" data-ob-ref-picker="ref-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;white-space:nowrap;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>{{end}}
      {{/* «Открыть карточку» остаётся и в режиме просмотра: посмотреть, на что
           ссылается служебный реквизит, — ровно то, зачем его показывают. */}}
      <button type="button" data-ob-ref-current="ref-{{$fn}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
    </div>
  {{else if isEnum (str .Type)}}
    {{if $ro}}<input type="hidden" name="{{$fn}}" value="{{index $.Values $fn}}">{{end}}
    <select{{if not $ro}} name="{{$fn}}"{{end}}{{if $ro}} disabled{{end}}>
      <option value="">{{t $.Lang "— выбрать —"}}</option>
      {{range index $.EnumOptions $fn}}
      <option value="{{.Value}}" {{if eq .Value (index $.Values $fn)}}selected{{end}}>{{.Label}}</option>
      {{end}}
    </select>
  {{else if eq (str .Type) "date"}}
    <input type="datetime-local" name="{{$fn}}" value="{{index $.Values $fn}}"{{if $ro}} readonly{{end}}>
  {{else if eq (str .Type) "bool"}}
    {{if $ro}}<input type="hidden" name="{{$fn}}" value="{{index $.Values $fn}}">{{end}}
    <select{{if not $ro}} name="{{$fn}}"{{end}}{{if $ro}} disabled{{end}}>
      <option value="false" {{if eq (index $.Values $fn) "false"}}selected{{end}}>{{t $.Lang "Нет"}}</option>
      <option value="true"  {{if eq (index $.Values $fn) "true"}}selected{{end}}>{{t $.Lang "Да"}}</option>
    </select>
  {{else if eq (str .Type) "number"}}
    <input type="text" autocomplete="off" inputmode="decimal" pattern="[+-]?([0-9]+([.,][0-9]+)?|[.,][0-9]+)" name="{{$fn}}" value="{{index $.Values $fn}}" placeholder="{{$flabel}}" title="{{t $.Lang "Введите число; десятичный разделитель — запятая или точка"}}"{{if $ro}} readonly{{end}}>
  {{else if isRichText (str .Type)}}
    {{/* textarea — скрытое form-backing поле (хранит санитизированный HTML).
         Без JS остаётся видимым и рабочим (прогрессивное улучшение). С JS
         Quill (этап 2) инициализируется над .richtext-editor и синхронизирует
         содержимое обратно в textarea перед submit — серверный санитайзер
         обрабатывает результат. В режиме просмотра редактор не поднимаем:
         Quill не знает про readonly и снова дал бы править текст. */}}
    <textarea name="{{$fn}}" autocomplete="off" class="richtext-field" rows="8" style="width:100%"{{if $ro}} readonly{{end}}>{{index $.Values $fn}}</textarea>
    {{if not $ro}}<div class="richtext-editor"></div>{{end}}
  {{else if isImage (str .Type)}}
    {{/* Поле-картинка: скрытый input хранит ссылку (UUID), превью + загрузка/очистка.
         Без JS остаётся скрытый input — значение не теряется при записи. */}}
    {{$iv := index $.Values $fn}}
    <div class="img-field">
      <input type="hidden" name="{{$fn}}" value="{{$iv}}">
      <div class="img-preview"{{if not $iv}} style="display:none"{{end}}><img src="{{if $iv}}/ui/_image/{{$iv}}{{end}}" alt=""></div>
      {{if not $ro}}
      <div class="img-actions">
        <label class="btn btn-sm btn-secondary">{{t $.Lang "Загрузить…"}}<input type="file" accept="image/*" style="display:none" data-ob-image-upload="/ui/{{lower (str $.Entity.Kind)}}/{{lower $.Entity.Name}}/_image"></label>
        <button type="button" class="btn btn-sm img-clear-btn" data-ob-image-clear{{if not $iv}} style="display:none"{{end}}>{{t $.Lang "Очистить"}}</button>
      </div>
      {{end}}
    </div>
  {{else}}
    <input type="text" autocomplete="off" name="{{$fn}}" value="{{index $.Values $fn}}" placeholder="{{$flabel}}"{{if $ro}} readonly{{end}}>
  {{end}}
</div>
{{end}}

{{range .Entity.TableParts}}{{$tp := .}}{{$tpName := .Name}}{{$tpRef := index $.TPRefOptions $tpName}}{{$tpReadOnly := not $.CanWrite}}
<h3>{{$tp.DisplayName $.Lang}}</h3>
<table class="tp-table" data-ob-dom-table="{{$tpName}}" data-ob-readonly="{{if $tpReadOnly}}1{{else}}0{{end}}"
  {{if not $tpReadOnly}}title="Insert; F9; Delete; Ctrl+↑/↓" aria-keyshortcuts="Insert F9 Delete Control+ArrowUp Control+ArrowDown"{{end}}>
  <thead><tr>
    {{range .Fields}}<th>{{.DisplayName $.Lang}}</th>{{end}}
    <th style="width:40px"></th>
  </tr></thead>
  <tbody id="tp-body-{{$tpName}}">
  {{$existingRows := index $.TablePartRows $tpName}}
  {{range $i, $row := $existingRows}}
    <tr tabindex="-1" aria-selected="false">
      {{range $tp.Fields}}{{$fn := .Name}}
        <td>
        {{if isRef (str .Type)}}
          <div style="display:flex;gap:4px;align-items:center">
            <select name="tp.{{$tpName}}.{{$i}}.{{$fn}}" style="flex:1" data-ref-entity="{{.RefEntity}}"{{if .InlineCreateEnabled true}} data-ref-allow-create="1"{{end}}{{if $tpReadOnly}} disabled{{end}}>
              <option value="">{{t $.Lang "— выбрать —"}}</option>
              {{range index $tpRef $fn}}
              <option value="{{index . "id"}}" {{if eq (str (index . "id")) (refID (index $row $fn))}}selected{{end}}>{{index . "_label"}}</option>
              {{end}}
            </select>
            <button type="button" data-ob-ref-picker-self style="padding:4px 8px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}"{{if $tpReadOnly}} disabled{{end}}>...</button>
            <button type="button" data-ob-ref-current-self style="padding:4px 7px;border:1px solid #e2e8f0;border-radius:5px;background:#f8fafc;cursor:pointer;font-size:12px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
          </div>
        {{else if eq (str .Type) "number"}}
          <input type="number" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="{{index $row $fn}}"
            data-tp-num="{{$fn}}" data-ob-tp-recalc{{if $tpReadOnly}} disabled{{end}}>
        {{else if isEnum (str .Type)}}
          {{/* Перечисление — список, а не свободный текст (#1010): руками в
               ячейке набирали строку, и опечатка молча ломала прикладные
               сравнения. Значение вне перечисления показываем отдельным
               пунктом с ⚠, иначе браузер выбрал бы первый вариант и открытие
               формы подменило бы данные. */}}
          <select name="tp.{{$tpName}}.{{$i}}.{{$fn}}"{{if $tpReadOnly}} disabled{{end}}>
            <option value="">{{t $.Lang "— выбрать —"}}</option>
            {{range tpEnumOptions $.TPEnumLabels $.TPEnumOrder $tpName $fn (index $row $fn)}}
            <option value="{{.Value}}"{{if .Selected}} selected{{end}}{{if .Unknown}} style="color:#dc2626"{{end}}>{{if .Unknown}}⚠ {{end}}{{.Label}}</option>
            {{end}}
          </select>
        {{else if eq (str .Type) "bool"}}
          {{/* Флажок, а не текст (#1010): в текстовом поле стояло сырое
               значение из БД («1» на SQLite), а разбор сохранения признаёт
               истиной только «true» — пересохранение формы снимало флажок. */}}
          <input type="checkbox" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="true"{{if checked (index $row $fn)}} checked{{end}}{{if $tpReadOnly}} disabled{{end}}>
        {{else}}
          <input type="text" name="tp.{{$tpName}}.{{$i}}.{{$fn}}" value="{{index $row $fn}}"
            data-ob-tp-recalc{{if $tpReadOnly}} disabled{{end}}>
        {{end}}
        </td>
      {{end}}
      <td><button type="button" class="del-btn" data-ob-remove-row="tr"{{if $tpReadOnly}} disabled{{else}} title="Delete" aria-keyshortcuts="Delete"{{end}}>×</button></td>
    </tr>
  {{end}}
  </tbody>
  <tfoot id="tp-foot-{{$tpName}}" class="tp-footer" style="display:none"><tr>
    {{range .Fields}}{{if eq (str .Type) "number"}}<td class="tp-total" data-tp-total="{{$tpName}}.{{.Name}}" style="text-align:right;font-variant-numeric:tabular-nums">0</td>{{else}}<td></td>{{end}}{{end}}<td></td>
  </tr></tfoot>
</table>
<button type="button" class="btn btn-sm" style="background:#e2e8f0;color:#475569;margin-bottom:8px"
  data-ob-add-tp-row data-tp-name="{{$tpName}}" data-tp-fields="{{fieldNamesCSV .Fields}}" data-tp-num-fields="{{numberFieldNamesCSV .Fields}}" data-tp-bool-fields="{{boolFieldNamesCSV .Fields}}"
  {{if $tpReadOnly}}disabled{{else}}title="Insert" aria-keyshortcuts="Insert"{{end}}>
  + {{t $.Lang "Добавить строку"}}
</button>
{{end}}

<div style="margin-top:16px">
  {{if .IsPopup}}
  {{if .CanWrite}}<button class="btn btn-primary" type="submit" name="_action" value="" form="main-form" title="Ctrl+S" aria-keyshortcuts="Control+S">{{t $.Lang "Записать и выбрать"}}</button>{{end}}
  <a href="#" data-ob-popup-cancel class="btn btn-cancel">{{t $.Lang "Отмена"}}</a>
  {{else}}
  {{if .CanWrite}}<button class="btn btn-secondary" type="submit" name="_action" value="" form="main-form" title="Ctrl+S" aria-keyshortcuts="Control+S">{{t $.Lang "Записать"}}</button>{{end}}
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
{{/* Перечисления колонок ТЧ — для строк, которые добавляет JS (#1010): сервер
     их не рендерит, а без вариантов и порядка addTpRow снова поставил бы
     текстовое поле. Порядок отдельно от подписей: JSON-карта приезжает с
     алфавитным порядком ключей, а в списке нужен порядок объявления values:. */}}
<script type="application/json" id="ob-tp-enum-labels">{{jsJSON .TPEnumLabels}}</script>
<script type="application/json" id="ob-tp-enum-order">{{jsJSON .TPEnumOrder}}</script>
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
      {{/* Снятый checkbox браузер не отправляет вовсе — маркер __has. говорит
           серверу, что флажок на форме был, и снятую галку не заменят
           значением по умолчанию. */}}
      <input type="hidden" name="__has.{{$pname}}" value="1">
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
    <label>{{t $.Lang "Свернуто до уровня"}}
      <input type="number" id="rs-collapse-to" min="0" step="1" placeholder="—" title="{{t $.Lang "Пусто — отчёт открывается развёрнутым; 0 — видны только группы верхнего уровня"}}" style="margin-left:6px;width:64px">
    </label>
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
  {{if .CanViewBalances}}<a class="btn btn-sm" href="/ui/register/{{lower .Register.Name}}/balances" style="background:#e2e8f0;color:#475569">{{t $.Lang "Остатки →"}}</a>{{end}}
</div>
{{template "reg-filter-form" (dict "Fields" .FilterFields "Filter" .Filter "RefOpts" .RefOpts "ShowFromTo" .ShowPeriodFilter "ShowToOnly" false "HasFilters" .HasFilters "ResetURL" (printf "/ui/register/%s" (lower .Register.Name)) "Lang" $.Lang)}}
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{if .ShowMovementKind}}<th>{{t $.Lang "Вид движения"}}</th>{{end}}
  {{if .ShowRecorder}}<th>{{t $.Lang "Регистратор"}}</th>{{end}}
  {{range .VisibleDimensions}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .VisibleResources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .VisibleAttributes}}<th>{{.DisplayName $.Lang}}</th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{if $.ShowMovementKind}}<td>{{$v := index $row "вид_движения"}}{{if eq (str $v) "Приход"}}<span style="color:#16a34a;font-weight:600">▲ {{$v}}</span>{{else if eq (str $v) "Расход"}}<span style="color:#dc2626;font-weight:600">▼ {{$v}}</span>{{else}}{{$v}}{{end}}</td>{{end}}
  {{if $.ShowRecorder}}<td style="font-size:12px;color:#475569">{{if index $row "recorder_label"}}{{index $row "recorder_label"}}{{else}}{{index $row "recorder_type"}}{{end}}</td>{{end}}
  {{range $.VisibleDimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.VisibleResources}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.VisibleAttributes}}<td>{{index $row .Name}}</td>{{end}}
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
{{template "reg-filter-form" (dict "Fields" .FilterFields "Filter" .Filter "RefOpts" .RefOpts "ShowFromTo" false "ShowToOnly" .ShowPeriodFilter "HasFilters" .HasFilters "ResetURL" (printf "/ui/register/%s/balances" (lower .Register.Name)) "Lang" $.Lang)}}
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{range .VisibleDimensions}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .VisibleResources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr>
  {{range $.VisibleDimensions}}<td>{{index $row .Name}}</td>{{end}}
  {{range $.VisibleResources}}<td style="font-weight:600">{{index $row .Name}}</td>{{end}}
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
{{/* data-ob-busy: пока идёт запуск, кнопка выключается и говорит, что работа
     идёт. Обработка может качать каталог минуту и дольше, а страница при
     обычной отправке формы не менялась вовсе — человек решал, что нажатие не
     сработало, и жал ещё раз. Обработчик — в /static/ui.js. */}}
<form method="POST" enctype="multipart/form-data" data-ob-busy="{{t $.Lang "Выполняется…"}}">
  <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px;margin-bottom:16px">
  {{range .Processor.Params}}{{$pname := .Name}}
    {{if eq .Type "bool"}}
    <div class="form-group" style="margin-bottom:0">
      <label style="display:flex;align-items:center;gap:8px;cursor:pointer">
        <input type="hidden" name="{{processorParamPresenceName $.Processor $pname}}" value="1">
        <input type="checkbox" name="{{$pname}}" value="true" {{if index $.ParamValues $pname}}checked{{end}}>
        <span>{{.DisplayLabel $.Lang}}</span>
      </label>
    </div>
    {{else if eq .Type "file"}}
    <div class="form-group" style="margin-bottom:0;grid-column:1/-1">
      <label>{{.DisplayLabel $.Lang}}{{if .Required}} <span style="color:#dc2626">*</span>{{end}}</label>
      {{/* Файл нельзя вернуть в поле после перезагрузки — это запрет браузера, а
           не недоделка. Поэтому после запуска поле пустое, и повторное
           «Выполнить» без файла обязано останавливаться здесь, а не улетать на
           сервер за ошибкой прикладного модуля. */}}
      <input type="file" name="{{$pname}}" {{if .Required}}required{{end}}>
    </div>
    {{else if eq .Type "text"}}
    <div class="form-group" style="margin-bottom:0;grid-column:1/-1">
      <label>{{.DisplayLabel $.Lang}}</label>
      <textarea name="{{$pname}}" rows="12" style="width:100%;font-family:monospace;font-size:13px;resize:vertical">{{index $.ParamValues $pname}}</textarea>
    </div>
    {{else}}
    <div class="form-group" style="margin-bottom:0">
      <label>{{.DisplayLabel $.Lang}}{{if .Required}} <span style="color:#dc2626">*</span>{{end}}</label>
      {{if eq .Type "date"}}
        <input type="date" name="{{$pname}}" value="{{index $.ParamValues $pname}}" {{if .Required}}required{{end}}>
      {{else if eq .Type "number"}}
        <input type="number" name="{{$pname}}" value="{{index $.ParamValues $pname}}" {{if .Required}}required{{end}}>
      {{else if eq .Type "choice"}}
        <select name="{{$pname}}" {{if .Required}}required{{end}}>
          {{range .Options}}<option value="{{.}}" {{if eq . (index $.ParamValues $pname)}}selected{{end}}>{{.}}</option>{{end}}
        </select>
      {{else if isRef (str .Type)}}
        <div style="display:flex;gap:6px;align-items:center">
          <select id="pp-{{$pname}}" name="{{$pname}}" style="flex:1" data-ref-entity="{{index $.ProcessorRefEntity $pname}}" {{if .Required}}required{{end}}>
            <option value="">{{t $.Lang "— выбрать —"}}</option>
            {{with index $.RefOptions $pname}}{{range .}}<option value="{{index . "id"}}" {{if eq (index . "id") (index $.ParamValues $pname)}}selected{{end}}>{{index . "_label"}}</option>{{end}}{{end}}
          </select>
          <button type="button" data-ob-ref-picker="pp-{{$pname}}" style="padding:6px 10px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
          <button type="button" data-ob-ref-current="pp-{{$pname}}" style="padding:6px 9px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
        </div>
      {{else}}
        <input type="text" name="{{$pname}}" value="{{index $.ParamValues $pname}}" {{if .Required}}required{{end}}>
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
{{/* Карточка результата показывается и когда запуска НЕ БЫЛО, но есть что
     сказать: отказ по незаполненным обязательным полям, ненайденная процедура
     Выполнить(). Под «{{if .Ran}}» такой отказ не рисовался вовсе — человек
     получал ту же форму без единого слова, то есть ровно то «нажал, и ничего
     не произошло», ради которого всё и затевалось. */}}
{{if or .Ran .RunError}}
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
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-weight:600;font-size:14px">onebase {{if .Cfg.PlatVersion}}{{.Cfg.PlatVersion}}{{else}}dev{{end}}{{if .Cfg.PlatDate}}<span style="color:#94a3b8;font-weight:400"> · {{.Cfg.PlatDate}}{{if .Cfg.PlatCommit}} · {{.Cfg.PlatCommit}}{{end}}</span>{{end}}
        {{/* Только уведомление: процесс базы своим бинарём не распоряжается —
             он может быть службой. Обновляют платформу из лаунчера. */}}
        {{with .UpdateAvailable}}<div style="margin-top:6px;font-weight:400;font-size:13px;color:#166534">{{t $.Lang "Доступна новая версия платформы"}}: {{.}} <span style="color:#94a3b8">— {{t $.Lang "обновление выполняется из лаунчера"}}</span></div>{{end}}
      </td>
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
    {{if .Cfg.ConfigSource}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Хранение конфигурации"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:14px;color:#475569">{{if eq .Cfg.ConfigSource "database"}}{{t $.Lang "В базе данных"}}{{else}}{{t $.Lang "Файлы"}}{{end}}</td>
    </tr>
    {{end}}
    {{if and .IsAdmin .Cfg.ConfigLocation}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "Расположение конфигурации"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:13px;color:#475569;word-break:break-all">{{.Cfg.ConfigLocation}}</td>
    </tr>
    {{end}}
    {{if .Cfg.DatabaseLocation}}
    <tr>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;color:#64748b;font-size:14px">{{t $.Lang "База данных"}}</td>
      <td style="padding:14px 0;border-bottom:1px solid #f1f5f9;font-size:13px;color:#475569;word-break:break-all">{{if eq .Cfg.DatabaseType "sqlite"}}SQLite{{else}}PostgreSQL{{end}}{{if .IsAdmin}} · {{.Cfg.DatabaseLocation}}{{end}}</td>
    </tr>
    {{end}}
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
  <div style="margin-top:18px;text-align:right">
    <a class="btn" href="/ui/report-problem">{{t $.Lang "Сообщить об ошибке"}}</a>
  </div>
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
<div style="margin-bottom:8px">{{template "detail-panel-toggle" .}}</div>
<div class="ob-list-wrap">
<div class="card">
{{if .Rows}}
<table><thead><tr>
  {{if .InfoReg.Periodic}}<th>{{t $.Lang "Период"}}</th>{{end}}
  {{range .InfoReg.Dimensions}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{range .InfoReg.Resources}}<th>{{.DisplayName $.Lang}}</th>{{end}}
  {{if .CanDelete}}<th></th>{{end}}
</tr></thead><tbody>
{{range .Rows}}{{$row := .}}<tr data-ob-list-row tabindex="-1" aria-selected="false"
  data-ob-detail='{{infoRegDetailPanel $.InfoReg $row $.Lang}}'>
  {{if $.InfoReg.Periodic}}<td>{{index $row "period"}}</td>{{end}}
  {{range $.InfoReg.Dimensions}}<td>{{$lbl := index $row (printf "%s_label" .Name)}}{{if $lbl}}{{$lbl}}{{else}}{{index $row .Name}}{{end}}</td>{{end}}
  {{range $.InfoReg.Resources}}<td style="font-weight:600">{{$lbl := index $row (printf "%s_label" .Name)}}{{if $lbl}}{{$lbl}}{{else}}{{index $row .Name}}{{end}}</td>{{end}}
  {{if $.CanDelete}}<td>
    <form method="POST" action="/ui/inforeg/{{lower $.InfoReg.Name}}/delete" style="display:inline"
          data-ob-confirm="{{t $.Lang "Удалить запись?"}}">
      {{if $.InfoReg.Periodic}}<input type="hidden" name="period" value="{{index $row "period_key"}}">{{end}}
      {{range $.InfoReg.Dimensions}}<input type="hidden" name="{{.Name}}" value="{{infoRegKeyValue . $row}}">{{end}}
      <button class="btn btn-danger btn-sm" type="submit">×</button>
    </form>
  </td>{{end}}
</tr>{{end}}
</tbody></table>
{{else}}<p class="empty">{{t $.Lang "Записей нет"}}</p>{{end}}
</div>
{{template "detail-panel" .}}
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
{{if .Error}}<div style="background:#fef2f2;border:1px solid #fca5a5;color:#b91c1c;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px">⚠ {{.Error}}</div>{{end}}
<div class="card" style="max-width:700px">
<form method="POST" action="/ui/constants">
{{range .Constants}}{{$c := .}}
<div class="form-group">
  <label>{{.DisplayLabel $.Lang}}{{if .Required}} <span style="color:#dc2626" title="{{t $.Lang "Обязательное поле"}}">*</span>{{end}}</label>
  {{if .RefEntity}}
    <div style="display:flex;gap:6px;align-items:center">
      <select id="const-{{.Name}}" name="{{.Name}}" data-ref-entity="{{.RefEntity}}" style="flex:1;min-width:0"{{if .Required}} required{{end}}>
        <option value="">{{t $.Lang "— не выбрано —"}}</option>
        {{range index $.RefOpts .Name}}
        <option value="{{index . "id"}}" {{if eq (index . "id") (index $.Values $c.Name)}}selected{{end}}>{{index . "_label"}}</option>
        {{end}}
      </select>
      <button type="button" data-ob-ref-picker="const-{{$c.Name}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Выбрать из списка"}}">...</button>
      <button type="button" data-ob-ref-current="const-{{$c.Name}}" style="padding:8px 12px;border:1px solid #e2e8f0;border-radius:7px;background:#f8fafc;cursor:pointer;font-size:13px;flex-shrink:0" title="{{t $.Lang "Открыть карточку"}}">🔍</button>
    </div>
  {{else if .EnumName}}
    <select name="{{.Name}}"{{if .Required}} required{{end}}>
      <option value="">{{t $.Lang "— не выбрано —"}}</option>
      {{range index $.EnumOpts $c.Name}}
      <option value="{{.Value}}" {{if eq .Value (index $.Values $c.Name)}}selected{{end}}>{{.Label}}</option>
      {{end}}
    </select>
  {{else if eq (str .Type) "date"}}
    <input type="date" name="{{.Name}}" value="{{index $.Values .Name}}"{{if .Required}} required{{end}}>
  {{else if eq (str .Type) "bool"}}
    <select name="{{.Name}}">
      <option value="false" {{if eq (index $.Values .Name) "false"}}selected{{end}}>{{t $.Lang "Нет"}}</option>
      <option value="true"  {{if eq (index $.Values .Name) "true"}}selected{{end}}>{{t $.Lang "Да"}}</option>
    </select>
  {{else}}
    <input type="text" name="{{.Name}}" value="{{index $.Values .Name}}" placeholder="{{.Name}}"{{if .Required}} required{{end}}>
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
    {{/* listQuerySuffix, а не filterQuery: тут строка запроса начинается, а не
         продолжается — с «&» отбор уезжал в путь и ссылка давала 404. */}}
    <a class="btn btn-sm" href="/ui/journal/{{lower .Journal.Name}}/excel{{listQuerySuffix .Params}}" style="background:#16a34a;color:#fff" title="{{t $.Lang "Скачать Excel"}}">{{t $.Lang "Excel ↓"}}</a>
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
    <a class="btn btn-sm" href="{{listURL $.Query "f.*" ""}}" style="background:#e2e8f0;color:#475569">{{t $.Lang "Сбросить"}}</a>
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
<div style="margin-bottom:8px">{{template "detail-panel-toggle" .}}</div>
<div class="ob-list-wrap">
<div class="card">
{{if .Rows}}
<table><thead><tr>
  <th>{{t $.Lang "Документ"}}</th>
  {{range .JournalColumns}}<th>{{.DisplayLabel $.Lang}}</th>{{end}}
  <th style="width:90px"></th>
</tr></thead>
<tbody>
{{range .Rows}}{{$row := .}}
<tr style="cursor:pointer;{{journalRowStyle .}}" data-ob-list-row tabindex="-1" aria-selected="false"
  data-open-url="/ui/document/{{lower (str (index . "_doc_kind"))}}/{{str (index . "id")}}"
  data-ob-detail='{{detailPanel (journalFields $.Journal) $row nil $.Lang}}'>
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
{{template "detail-panel" .}}
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
{{/* Этапы (план 121): переходы пишутся всегда, даже когда журнал регистрации
     выключен, поэтому идут отдельной таблицей выше него. */}}
{{if .StageHistory}}
<div class="card" style="max-width:900px;margin-bottom:16px">
<h3 style="margin:0 0 10px;font-size:14px">{{t $.Lang "Этапы"}}</h3>
<table style="font-size:13px">
<thead><tr>
  <th>{{t $.Lang "Время"}}</th><th>{{t $.Lang "Пользователь"}}</th><th>{{t $.Lang "Из этапа"}}</th><th>{{t $.Lang "В этап"}}</th><th>{{t $.Lang "Источник"}}</th>
</tr></thead>
<tbody>
{{range .StageHistory}}<tr>
  <td style="white-space:nowrap;color:#94a3b8">{{.At.Format "02.01.2006 15:04:05"}}</td>
  <td>{{.UserLogin}}</td>
  <td style="color:#94a3b8">{{if .From}}{{.From}}{{else}}— {{t $.Lang "создание"}} —{{end}}</td>
  <td style="color:#1d4ed8;font-weight:600">{{.To}}{{if .Violation}} <span title="{{t $.Lang "Переход не предусмотрен маршрутом и прошёл только потому, что маршрут объявлен в режиме warn"}}" style="color:#b45309;font-weight:600">⚠</span>{{end}}</td>
  <td style="color:#94a3b8;font-size:12px">{{stageSourceLabel .Source}}</td>
</tr>{{end}}
</tbody>
</table>
</div>
{{end}}
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

// tplStages — отчёт «где застряло» (план 121): сколько объектов стоит на каждом
// этапе, сколько из них просрочено и по скольким время на этапе неизвестно
// (объекты, накопленные до объявления блока `stages`, истории не имеют — и
// показать «неизвестно» честнее, чем считать их срок от нуля).
const tplStages = `
{{/* Общий блок «маршрут объекта»: подключается и автоформой, и управляемой
     формой. Инициализатор идемпотентен — блок может появиться после замены
     части DOM, и второй график поверх первого рисовать нельзя. */}}
{{define "stage-route"}}
{{if and (not .IsNew) .StageRoute}}
<div class="card" style="max-width:1400px;margin-bottom:12px;padding:12px 16px">
  <div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-bottom:6px">
    <span style="font-size:12px;color:#94a3b8;text-transform:uppercase;letter-spacing:.04em">{{t $.Lang "Этап"}}</span>
    {{if .StageRoute.OffRoute}}
      <span style="font-size:13px;font-weight:600;color:#b45309">{{t $.Lang "вне маршрута"}}</span>
    {{else if .StageRoute.Label}}
      <span style="font-size:13px;font-weight:600;color:#1d4ed8">{{.StageRoute.Label}}</span>
    {{else}}
      <span style="font-size:13px;color:#94a3b8">{{t $.Lang "не задан"}}</span>
    {{end}}
    <a href="/ui/stages/{{lower .Entity.Name}}" style="font-size:12px;color:#64748b;margin-left:auto">{{t $.Lang "Где застряло"}} →</a>
  </div>
  <div class="ob-stage-route" data-ob-stage-option="{{stageChartJSON .StageRoute}}" style="height:90px"></div>
</div>
<script src="/vendor/echarts/echarts.min.js"></script>
<script>
(function(){
  function draw(){
    if (typeof echarts === 'undefined') { return; }
    var nodes = document.querySelectorAll('.ob-stage-route');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (el.dataset.obStageDrawn === '1') { continue; }
      var opt;
      try { opt = JSON.parse(el.dataset.obStageOption || 'null'); } catch (e) { opt = null; }
      if (!opt) { continue; }
      el.dataset.obStageDrawn = '1';
      var chart = echarts.init(el);
      chart.setOption(opt);
      window.addEventListener('resize', function(){ chart.resize(); });
    }
  }
  draw();
  if (document.readyState === 'loading') { document.addEventListener('DOMContentLoaded', draw); }
})();
</script>
{{end}}
{{end}}

{{define "page-stages"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{t $.Lang "Этапы — где застряло"}}</h2>
</div>
{{if not .Reports}}
<p class="empty">{{t $.Lang "Ни у одной сущности не объявлен блок stages."}}</p>
{{end}}
{{range $ri, $rep := .Reports}}
<div class="card" style="margin-bottom:20px">
  <div style="display:flex;align-items:center;gap:12px;margin-bottom:10px;flex-wrap:wrap">
    <h3 style="margin:0;font-size:15px">{{$rep.Entity.DisplayName $.Lang}}</h3>
    <span style="color:#94a3b8;font-size:12px">{{t $.Lang "реквизит"}}: {{$rep.Field}}</span>
    <a href="{{$rep.ListURL}}" style="font-size:12px;color:#64748b;margin-left:auto">{{t $.Lang "К списку"}} →</a>
  </div>
  <div id="ob-stage-report-{{$ri}}" style="height:110px;margin-bottom:8px"></div>
  <table style="font-size:13px">
  <thead><tr>
    <th>{{t $.Lang "Этап"}}</th>
    <th style="text-align:right">{{t $.Lang "Объектов"}}</th>
    <th style="text-align:right">{{t $.Lang "Срок, дней"}}</th>
    <th style="text-align:right">{{t $.Lang "Просрочено"}}</th>
    <th style="text-align:right">{{t $.Lang "Время неизвестно"}}</th>
    <th>{{t $.Lang "Ждёт с"}}</th>
  </tr></thead>
  <tbody>
  {{range $rep.Rows}}<tr>
    <td>{{.Label}}</td>
    <td style="text-align:right">{{.Count}}</td>
    <td style="text-align:right;color:#94a3b8">{{if .DeadlineDays}}{{.DeadlineDays}}{{else}}—{{end}}</td>
    <td style="text-align:right">{{if .Overdue}}<span style="color:#dc2626;font-weight:600">{{.Overdue}}</span>{{else}}—{{end}}</td>
    <td style="text-align:right;color:#94a3b8">{{if .Unknown}}{{.Unknown}}{{else}}—{{end}}</td>
    <td style="color:#94a3b8">{{if .Since.IsZero}}—{{else}}{{.Since.Format "02.01.2006"}}{{end}}</td>
  </tr>{{end}}
  </tbody>
  </table>
  {{if $rep.OffRoute}}
  <p style="margin-top:10px;font-size:13px;color:#b45309">
    {{t $.Lang "Вне маршрута"}}: {{$rep.OffRoute}} —
    {{t $.Lang "состояние этих объектов не объявлено в order. Включение enforce: strict отвергнет их первую же правку."}}
  </p>
  {{end}}
</div>
<script src="/vendor/echarts/echarts.min.js"></script>
<script>
(function(){
  var el = document.getElementById('ob-stage-report-{{$ri}}');
  var opt = {{stageChartJSON $rep.Route}};
  if (!el || !opt || typeof echarts === 'undefined') { return; }
  var chart = echarts.init(el);
  chart.setOption(opt);
  window.addEventListener('resize', function(){ chart.resize(); });
})();
</script>
{{end}}
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
  <th style="width:170px"></th>
</tr></thead>
<tbody>
{{range .JobRows}}
{{$job := .Job}}
<tr>
  <td><strong>{{$job.Title}}</strong><br><small style="color:#94a3b8">{{$job.Name}}</small></td>
  <td><code>{{$job.Schedule}}</code></td>
  <td>{{$job.Processor}}</td>
  <td>{{if .State.OverrideSet}}{{if .State.EffectiveOn}}<span style="color:#22c55e">{{t $.Lang "✓ включено администратором"}}</span>{{else}}<span style="color:#94a3b8">{{t $.Lang "— выключено администратором"}}</span>{{end}}{{else}}{{if $job.Enabled}}<span style="color:#22c55e">{{t $.Lang "✓ активно"}}</span>{{else}}<span style="color:#94a3b8">{{t $.Lang "— отключено"}}</span>{{end}}{{end}}</td>
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
    <form method="POST" action="/ui/admin/scheduled/{{$job.Name}}/toggle" style="display:inline;margin-left:6px">
      <button class="btn btn-sm btn-secondary" type="submit">{{if .State.EffectiveOn}}{{t $.Lang "Выключить"}}{{else}}{{t $.Lang "Включить"}}{{end}}</button>
    </form>
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

{{define "page-job-queue"}}
{{template "head" .}}{{template "nav" .}}
<main>
<div class="row-top">
  <h2>{{t $.Lang "Очередь фоновых заданий"}}</h2>
  <span style="color:#94a3b8;font-size:13px">
    {{t $.Lang "Исполнителей:"}} {{.Workers}}{{if .InFlight}} · {{t $.Lang "в работе:"}} {{.InFlight}}{{end}}
  </span>
</div>
{{if not .Available}}
<div class="card"><p class="empty">{{t $.Lang "Очередь недоступна в этом режиме работы сервера."}}</p></div>
{{else}}
{{if not .Enabled}}
<div class="card" style="margin-bottom:16px;border-left:3px solid #f97316">
  {{t $.Lang "Очередь выключена настройкой queue.workers: 0 — задачи не принимаются и не исполняются."}}
</div>
{{end}}
{{if .Degraded}}
<div class="card" style="margin-bottom:16px;border-left:3px solid #f97316">
  {{t $.Lang "База на SQLite: параллелизм недоступен, работает один исполнитель. Пул на 16–32 исполнителя требует PostgreSQL."}}
</div>
{{end}}
<div style="display:flex;gap:10px;flex-wrap:wrap;margin-bottom:20px">
{{range .Cards}}
  <a href="/ui/admin/queue{{if not .Active}}?status={{.Status}}{{end}}" class="card"
     style="flex:1 1 150px;text-decoration:none;color:inherit;padding:12px 16px;{{if .Active}}outline:2px solid #2563eb{{end}}">
    <div style="font-size:24px;font-weight:600">{{.Count}}</div>
    <div style="color:#64748b;font-size:13px">{{t $.Lang .Title}}</div>
  </a>
{{end}}
</div>
<div class="card">
{{if .Tasks}}
<table><thead><tr>
  <th>{{t $.Lang "Задание"}}</th>
  <th>{{t $.Lang "Параметры"}}</th>
  <th>{{t $.Lang "Статус"}}</th>
  <th>{{t $.Lang "Попытки"}}</th>
  <th>{{t $.Lang "Поставлена"}}</th>
  <th>{{t $.Lang "Длительность"}}</th>
  <th style="width:170px"></th>
</tr></thead>
<tbody>
{{range .Tasks}}
<tr>
  <td>
    <strong>{{.JobName}}</strong>
    <br><small style="color:#94a3b8" title="{{.ID}}">{{.ShortID}}</small>
    {{if .Key}}<br><small style="color:#94a3b8">{{t $.Lang "ключ:"}} {{.Key}}</small>{{end}}
  </td>
  <td><small>{{.Params}}</small></td>
  <td>
    <span style="color:{{if eq .Status "done"}}#22c55e{{else if eq .Status "dead"}}#ef4444{{else if eq .Status "running"}}#2563eb{{else}}#94a3b8{{end}}">{{.Status}}</span>
    {{if .RetryAt}}<br><small style="color:#94a3b8">{{t $.Lang "повтор в"}} {{fmtDate .RetryAt}}</small>{{end}}
    {{if .Error}}<br><small style="color:#ef4444">{{.Error}}</small>{{end}}
  </td>
  <td>{{.Attempts}} / {{.MaxAttempts}}</td>
  <td>{{if .CreatedAt}}<small>{{fmtDate .CreatedAt}}</small>{{else}}—{{end}}</td>
  <td>{{if .DurationMs}}{{.DurationMs}} {{t $.Lang "мс"}}{{else}}—{{end}}</td>
  <td>
    {{if .Quarantined}}
      <form method="POST" action="/ui/admin/queue/{{.ID}}/replay{{if $.Status}}?status={{$.Status}}{{end}}" style="display:inline">
        <button class="btn btn-sm btn-primary" type="submit">{{t $.Lang "Повторить"}}</button>
      </form>
    {{else if not .Terminal}}
      <form method="POST" action="/ui/admin/queue/{{.ID}}/cancel{{if $.Status}}?status={{$.Status}}{{end}}" style="display:inline">
        <button class="btn btn-sm" type="submit">{{t $.Lang "Отменить"}}</button>
      </form>
    {{end}}
  </td>
</tr>
{{end}}
</tbody></table>
{{else}}
<p class="empty">{{t $.Lang "Задач нет. Ставит их прикладной код: ФоновыеЗадания.Поставить(«ИмяЗадания», Параметры)."}}</p>
{{end}}
</div>
{{end}}
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
    {{if .State.OverrideSet}}{{if .State.EffectiveOn}}<span style="color:#22c55e">{{t $.Lang "✓ включено администратором"}}</span>{{else}}<span style="color:#94a3b8">{{t $.Lang "— выключено администратором"}}</span>{{end}}{{else}}{{if .Job.Enabled}}<span style="color:#22c55e">{{t $.Lang "✓ активно"}}</span>{{else}}<span style="color:#94a3b8">{{t $.Lang "— отключено"}}</span>{{end}}{{end}}
  </td></tr>
</table>
</div>

{{if .Msg}}<div style="background:#f0fdf4;border:1px solid #bbf7d0;color:#16a34a;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:1000px">✓ {{.Msg}}</div>{{end}}
{{if .Err}}<div class="error" style="max-width:1000px;margin-bottom:16px">{{.Err}}</div>{{end}}

<div style="display:flex;gap:8px;margin-bottom:20px;flex-wrap:wrap">
  <form method="POST" action="/ui/admin/scheduled/{{.Job.Name}}/toggle">
    <button class="btn {{if .State.EffectiveOn}}btn-secondary{{else}}btn-primary{{end}}" type="submit">{{if .State.EffectiveOn}}{{t $.Lang "Выключить"}}{{else}}{{t $.Lang "Включить"}}{{end}}</button>
  </form>
  {{if .State.OverrideSet}}
  <form method="POST" action="/ui/admin/scheduled/{{.Job.Name}}/reset">
    <button class="btn btn-secondary" type="submit">{{t $.Lang "Вернуть как в конфигурации"}}</button>
  </form>
  {{end}}
  <form method="POST" action="/ui/admin/scheduled/{{.Job.Name}}/run-now">
    <button class="btn btn-primary" type="submit">{{t $.Lang "▶ Запустить сейчас"}}</button>
  </form>
</div>

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
