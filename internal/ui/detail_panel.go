package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
)

// Боковая панель деталей активной записи списка (план 118B, issue #670).
//
// Смотреть широкую таблицу неудобно, особенно когда среди колонок картинки:
// каждая строка тянет свой <img>, а прочитать всё равно нельзя. Панель
// показывает полную карточку записи, на которой стоит курсор, — список при
// этом можно оставить узким.
//
// Данные берутся из УЖЕ отрисованных строк, а не отдельным запросом. Это не
// оптимизация, а требование безопасности: те же строки прошли права, строковые
// политики и маскирование ПДн, а второй путь чтения пришлось бы проводить через
// все три гейта заново — именно такие «вторые пути» давали утечки раньше.
// Поэтому payload собирается здесь, на сервере, из готовой строки, и уезжает в
// data-атрибут вместе с ней.

// detailPanelField — одна строка карточки.
type detailPanelField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Kind: "" — текст, "image" — идентификатор картинки, "rich" — размеченный
	// текст (в панели показываем как обычный текст, без разметки).
	Kind string `json:"kind,omitempty"`
}

// detailPanelTab — закладка панели.
type detailPanelTab struct {
	// Key is a stable, non-localized identity. Titles are presentation and may
	// collide after translation or change when the user switches language.
	Key    string             `json:"key"`
	Title  string             `json:"title"`
	Fields []detailPanelField `json:"fields"`
}

// detailPanelData — payload одной строки.
type detailPanelData struct {
	Title string           `json:"title"`
	Tabs  []detailPanelTab `json:"tabs"`
}

// detailPanelForEntity собирает payload с учётом управляемой формы списка и
// блока `detail_panel:`. Приоритет источников зеркалит resolveListColumns:
// управляемая форма со СтраницыФормы → явный блок → автокомпоновка.
//
// Явный состав НЕ расширяет права: значения берутся из той же строки, что уже
// прошла маску ПДн, поэтому перечисленный, но скрытый реквизит остаётся
// скрытым — маскированным его и покажет.
func detailPanelForEntity(e *metadata.Entity, row map[string]any,
	enumLabels map[string]map[string]string, lang string, translate ...func(string) string) string {
	if e == nil {
		return ""
	}
	dp := e.DetailPanel
	title := detailPanelTitle(e.Fields, row)
	if dp != nil && dp.Title != "" {
		if f := findFieldFold(e.Fields, dp.Title); f != nil {
			if rendered, ok := detailPanelFieldForRow(*f, row, enumLabels, lang); ok &&
				rendered.Kind != "image" && rendered.Value != "" {
				title = rendered.Value
			}
		}
	}
	if tabs := managedDetailPanelTabs(e, row, enumLabels, lang); len(tabs) > 0 {
		return marshalDetailPanel(detailPanelData{Title: title, Tabs: tabs})
	}
	if dp == nil {
		return detailPanelJSON(e.Fields, row, title, enumLabels, lang, translate...)
	}
	// Короткая форма: перечислен состав, закладки собираются по типам — как в
	// автокомпоновке, но из выбранных реквизитов.
	if dp.FieldsSet {
		return detailPanelJSON(fieldsByNames(e.Fields, dp.Fields), row, title, enumLabels, lang, translate...)
	}
	tabsConfigured := dp.TabsSet || len(dp.Tabs) > 0
	if !tabsConfigured {
		return detailPanelJSON(e.Fields, row, title, enumLabels, lang, translate...)
	}
	data := detailPanelData{Title: title}
	for _, tab := range dp.Tabs {
		fields := fieldsByNames(e.Fields, tab.Fields)
		rendered := detailPanelFieldsInOrder(fields, row, enumLabels, lang)
		if len(rendered) == 0 {
			continue
		}
		data.Tabs = append(data.Tabs, detailPanelTab{Key: "explicit:" + tab.Name, Title: tab.DisplayName(lang), Fields: rendered})
	}
	if len(data.Tabs) == 0 {
		return ""
	}
	return marshalDetailPanel(data)
}

// managedDetailPanelTabs projects СтраницыФормы from the managed list form
// into the list detail panel. Only already-loaded entity fields are included;
// commands, labels and table parts cannot introduce a second data path.
func managedDetailPanelTabs(e *metadata.Entity, row map[string]any,
	enumLabels map[string]map[string]string, lang string) []detailPanelTab {
	form := pickManagedForm(e, "list")
	if form == nil {
		return nil
	}
	var tabs []detailPanelTab
	pagesOrdinal := 0
	var walkPages func([]*metadata.FormElement)
	walkPages = func(elements []*metadata.FormElement) {
		for _, element := range elements {
			if element == nil {
				continue
			}
			if element.Kind == metadata.FormElementPages {
				pagesOrdinal++
				for pageOrdinal, page := range element.Children {
					if page == nil || page.Kind != metadata.FormElementPage {
						continue
					}
					fields := managedDetailPageFields(e, page)
					rendered := detailPanelFieldsInOrder(fields, row, enumLabels, lang)
					if len(rendered) == 0 {
						continue
					}
					tabs = append(tabs, detailPanelTab{
						Key:    fmt.Sprintf("managed:%s:%d:%s:%d", form.Name, pagesOrdinal, page.Name, pageOrdinal),
						Title:  managedElementTitle(page, lang),
						Fields: rendered,
					})
				}
				// Nested page sets are owned by this container's pages and are
				// discovered while collecting each page's fields, not as peers.
				continue
			}
			walkPages(element.Children)
		}
	}
	walkPages(form.Elements)
	return tabs
}

func managedDetailPageFields(e *metadata.Entity, page *metadata.FormElement) []metadata.Field {
	var names []string
	var walk func([]*metadata.FormElement)
	walk = func(elements []*metadata.FormElement) {
		for _, element := range elements {
			if element == nil {
				continue
			}
			if isManagedListColumnElement(element.Kind) {
				name := managedDetailFieldName(element)
				if name != "" {
					names = append(names, name)
				}
			}
			walk(element.Children)
		}
	}
	walk(page.Children)
	return namedListColumns(e, names)
}

func managedDetailFieldName(element *metadata.FormElement) string {
	if element == nil {
		return ""
	}
	path := strings.TrimSpace(element.DataPath)
	if path == "" {
		return strings.TrimSpace(element.FieldName)
	}
	prefix, name, found := strings.Cut(path, ".")
	if !found || strings.Contains(name, ".") {
		return ""
	}
	prefix = strings.TrimSpace(prefix)
	if !strings.EqualFold(prefix, "Список") && !strings.EqualFold(prefix, "Объект") {
		return ""
	}
	return strings.TrimSpace(name)
}

func managedElementTitle(element *metadata.FormElement, lang string) string {
	if element == nil {
		return ""
	}
	if lang != "" {
		if title := element.TitleMap[lang]; title != "" {
			return title
		}
	}
	if title := element.TitleMap["ru"]; title != "" {
		return title
	}
	for _, title := range element.TitleMap {
		if title != "" {
			return title
		}
	}
	if element.Title != "" {
		return element.Title
	}
	return element.Name
}

// fieldsByNames отбирает реквизиты в порядке, заданном автором.
func fieldsByNames(all []metadata.Field, names []string) []metadata.Field {
	out := make([]metadata.Field, 0, len(names))
	for _, name := range names {
		if f := findFieldFold(all, name); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func findFieldFold(all []metadata.Field, name string) *metadata.Field {
	for i := range all {
		if strings.EqualFold(all[i].Name, name) {
			return &all[i]
		}
	}
	return nil
}

// detailPanelFieldsInOrder formats fields without regrouping them. Explicit
// YAML tabs and managed-form pages are authored layouts, so their order must
// survive even when text, image and rich-text fields are interleaved.
func detailPanelFieldsInOrder(fields []metadata.Field, row map[string]any,
	enumLabels map[string]map[string]string, lang string) []detailPanelField {
	out := make([]detailPanelField, 0, len(fields))
	for _, field := range fields {
		if rendered, ok := detailPanelFieldForRow(field, row, enumLabels, lang); ok {
			out = append(out, rendered)
		}
	}
	return out
}

// detailPanelFieldForRow is the single typed formatter for panel fields. It
// also enforces the payload boundary: a key removed by field_access.hide is
// absent, and a masked reference can never regain its stale presentation.
func detailPanelFieldForRow(f metadata.Field, row map[string]any,
	enumLabels map[string]map[string]string, lang string) (detailPanelField, bool) {
	v, ok := detailPanelRowValue(row, f.Name)
	if !ok {
		// Stored NULL values retain a key and are shown as empty. A missing key
		// means field_access.hide removed the field and must stay absent.
		return detailPanelField{}, false
	}
	if f.RefEntity != "" {
		if _, _, isUUID := uuidFromValue(v); isUUID {
			if label, found := detailPanelRowValue(row, f.Name+"_label"); found && fmtReportCell(label) != "" {
				v = label
			}
		}
	}
	field := detailPanelField{Label: f.DisplayName(lang)}
	switch {
	case metadata.IsImage(f.Type):
		field.Value = fmtReportCell(v)
		if field.Value == "" {
			return detailPanelField{}, false
		}
		field.Kind = "image"
	case metadata.IsRichText(f.Type):
		field.Value = richPlainExcerpt(v)
		field.Kind = "rich"
	case f.EnumName != "":
		field.Value = enumLabelFor(enumLabels, f.Name, fmtReportCell(v))
	default:
		field.Value = fmtReportCell(v)
	}
	return field, true
}

// detailPanelJSONFor — общая сборка: раскладывает реквизиты по закладкам
// «Основное» / «Изображения» / «Описание». Автокомпоновка сознательно
// показывает ВСЕ реквизиты шапки, а не только вынесенные в колонки: ради этого
// панель и просили — «смотреть все колонки неудобно». Маскирование ПДн
// применено к строке ДО панели, поэтому скрытое остаётся скрытым.
func detailPanelJSON(fields []metadata.Field, row map[string]any, title string,
	enumLabels map[string]map[string]string, lang string, translate ...func(string) string) string {
	var main, images, rich []detailPanelField
	for _, f := range fields {
		rendered, ok := detailPanelFieldForRow(f, row, enumLabels, lang)
		if !ok {
			continue
		}
		switch rendered.Kind {
		case "image":
			images = append(images, rendered)
		case "rich":
			rich = append(rich, rendered)
		default:
			main = append(main, rendered)
		}
	}

	data := detailPanelData{Title: title}
	if len(main) > 0 {
		data.Tabs = append(data.Tabs, detailPanelTab{Key: "auto:main", Title: detailPanelTranslated("Основное", translate), Fields: main})
	}
	if len(images) > 0 {
		data.Tabs = append(data.Tabs, detailPanelTab{Key: "auto:images", Title: detailPanelTranslated("Изображения", translate), Fields: images})
	}
	if len(rich) > 0 {
		data.Tabs = append(data.Tabs, detailPanelTab{Key: "auto:rich", Title: detailPanelTranslated("Описание", translate), Fields: rich})
	}
	if len(data.Tabs) == 0 {
		return ""
	}
	return marshalDetailPanel(data)
}

func detailPanelTranslated(key string, translate []func(string) string) string {
	if len(translate) > 0 && translate[0] != nil {
		if value := translate[0](key); value != "" {
			return value
		}
	}
	return key
}

func marshalDetailPanel(data detailPanelData) string {
	out, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(out)
}

// detailPanelTitle — заголовок карточки: представление записи. Берём первое
// непустое строковое поле, как это уже делает представление ссылки.
func detailPanelTitle(fields []metadata.Field, row map[string]any) string {
	for _, f := range fields {
		if f.Type != metadata.FieldTypeString || f.RefEntity != "" {
			continue
		}
		if value, ok := detailPanelRowValue(row, f.Name); ok {
			if s := fmtReportCell(value); s != "" {
				return s
			}
		}
	}
	return ""
}

func detailPanelRowValue(row map[string]any, name string) (any, bool) {
	for key, value := range row {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return nil, false
}

// infoRegisterDetailFields includes the period as part of a periodic record's
// identity, then the configured dimensions and resources. The list table has
// always shown period; omitting it from the card made two otherwise identical
// periodic records indistinguishable.
func infoRegisterDetailFields(ir *metadata.InfoRegister, periodTitle string) []metadata.Field {
	if ir == nil {
		return nil
	}
	fields := make([]metadata.Field, 0, len(ir.Dimensions)+len(ir.Resources)+1)
	if ir.Periodic {
		if periodTitle == "" {
			periodTitle = "Период"
		}
		fields = append(fields, metadata.Field{Name: "period", Title: periodTitle, Type: metadata.FieldTypeDate})
	}
	fields = append(fields, ir.Dimensions...)
	fields = append(fields, ir.Resources...)
	return fields
}

func infoRegisterDetailPanelJSON(ir *metadata.InfoRegister, row map[string]any, lang string, periodTitle ...string) string {
	localizedPeriod := "Период"
	if len(periodTitle) > 0 && periodTitle[0] != "" {
		localizedPeriod = periodTitle[0]
	}
	fields := infoRegisterDetailFields(ir, localizedPeriod)
	return detailPanelJSON(fields, row, detailPanelTitle(fields, row), nil, lang)
}

func infoRegisterDetailPanelJSONTranslated(ir *metadata.InfoRegister, row map[string]any, lang, periodTitle string,
	translate func(string) string) string {
	fields := infoRegisterDetailFields(ir, periodTitle)
	return detailPanelJSON(fields, row, detailPanelTitle(fields, row), nil, lang, translate)
}

// enumLabelFor — подпись значения перечисления, та же, что в колонках списка.
func enumLabelFor(labels map[string]map[string]string, field, value string) string {
	if m, ok := labels[field]; ok {
		if lbl, ok := m[value]; ok && lbl != "" {
			return lbl
		}
	}
	return value
}

// richPlainExcerpt — текстовый срез размеченного поля. Тот же приём, что в
// колонке списка: полная разметка в payload раздула бы страницу.
func richPlainExcerpt(v any) string {
	if v == nil {
		return ""
	}
	s := richtext.Plaintext(fmt.Sprintf("%v", v))
	const maxRunes = 400
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}
