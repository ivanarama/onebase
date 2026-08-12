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
	Title  string             `json:"title"`
	Fields []detailPanelField `json:"fields"`
}

// detailPanelData — payload одной строки.
type detailPanelData struct {
	Title string           `json:"title"`
	Tabs  []detailPanelTab `json:"tabs"`
}

// detailPanelForEntity собирает payload с учётом блока `detail_panel:`.
// Приоритет источников зеркалит resolveListColumns: явный блок → автокомпоновка.
//
// Явный состав НЕ расширяет права: значения берутся из той же строки, что уже
// прошла маску ПДн, поэтому перечисленный, но скрытый реквизит остаётся
// скрытым — маскированным его и покажет.
func detailPanelForEntity(e *metadata.Entity, row map[string]any,
	enumLabels map[string]map[string]string, lang string) string {
	if e == nil {
		return ""
	}
	dp := e.DetailPanel
	if dp == nil {
		return detailPanelJSON(e.Fields, row, detailPanelTitle(e.Fields, row), enumLabels, lang)
	}
	title := detailPanelTitle(e.Fields, row)
	if dp.Title != "" {
		if f := findFieldFold(e.Fields, dp.Title); f != nil {
			if v := fmtReportCell(rowValueFold(row, f.Name)); v != "" {
				title = v
			}
		}
	}
	// Короткая форма: перечислен состав, закладки собираются по типам — как в
	// автокомпоновке, но из выбранных реквизитов.
	if dp.FieldsSet {
		return detailPanelJSON(fieldsByNames(e.Fields, dp.Fields), row, title, enumLabels, lang)
	}
	if len(dp.Tabs) == 0 {
		return detailPanelJSON(e.Fields, row, title, enumLabels, lang)
	}
	data := detailPanelData{Title: title}
	for _, tab := range dp.Tabs {
		fields := fieldsByNames(e.Fields, tab.Fields)
		if len(fields) == 0 {
			continue
		}
		raw := detailPanelJSON(fields, row, "", enumLabels, lang)
		var part detailPanelData
		if raw == "" || json.Unmarshal([]byte(raw), &part) != nil {
			continue
		}
		// Внутри явной закладки типы не разносим: автор уже решил, что вместе.
		var flat []detailPanelField
		for _, t := range part.Tabs {
			flat = append(flat, t.Fields...)
		}
		data.Tabs = append(data.Tabs, detailPanelTab{Title: tab.DisplayName(lang), Fields: flat})
	}
	if len(data.Tabs) == 0 {
		return ""
	}
	out, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(out)
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

// detailPanelJSONFor — общая сборка: раскладывает реквизиты по закладкам
// «Основное» / «Изображения» / «Описание». Автокомпоновка сознательно
// показывает ВСЕ реквизиты шапки, а не только вынесенные в колонки: ради этого
// панель и просили — «смотреть все колонки неудобно». Маскирование ПДн
// применено к строке ДО панели, поэтому скрытое остаётся скрытым.
func detailPanelJSON(fields []metadata.Field, row map[string]any, title string,
	enumLabels map[string]map[string]string, lang string) string {
	var main, images, rich []detailPanelField
	for _, f := range fields {
		v, ok := row[f.Name]
		if !ok {
			v = row[strings.ToLower(f.Name)]
		}
		label := f.DisplayName(lang)
		switch {
		case metadata.IsImage(f.Type):
			if s := fmtReportCell(v); s != "" {
				images = append(images, detailPanelField{Label: label, Value: s, Kind: "image"})
			}
		case metadata.IsRichText(f.Type):
			// В панель уезжает только текстовый срез: полный richtext раздул бы
			// разметку страницы на каждую строку списка.
			rich = append(rich, detailPanelField{Label: label, Value: richPlainExcerpt(v), Kind: "rich"})
		case f.EnumName != "":
			main = append(main, detailPanelField{
				Label: label,
				Value: enumLabelFor(enumLabels, f.Name, fmtReportCell(v)),
			})
		default:
			main = append(main, detailPanelField{Label: label, Value: fmtReportCell(v)})
		}
	}

	data := detailPanelData{Title: title}
	if len(main) > 0 {
		data.Tabs = append(data.Tabs, detailPanelTab{Title: "Основное", Fields: main})
	}
	if len(images) > 0 {
		data.Tabs = append(data.Tabs, detailPanelTab{Title: "Изображения", Fields: images})
	}
	if len(rich) > 0 {
		data.Tabs = append(data.Tabs, detailPanelTab{Title: "Описание", Fields: rich})
	}
	if len(data.Tabs) == 0 {
		return ""
	}
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
		if s := fmtReportCell(row[f.Name]); s != "" {
			return s
		}
	}
	return ""
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
