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

// detailPanelJSONFor — общая сборка: раскладывает реквизиты по закладкам
// «Основное» / «Изображения» / «Описание». Автокомпоновка сознательно
// показывает ВСЕ реквизиты шапки, а не только вынесенные в колонки: ради этого
// панель и просили — «смотреть все колонки неудобно». Маскирование ПДн
// применено к строке ДО панели, поэтому скрытое остаётся скрытым.
func detailPanelJSON(fields []metadata.Field, row map[string]any, title string,
	enumLabels map[string]map[string]string, lang string) string {
	var main, images, rich []detailPanelField
	for _, f := range fields {
		v, ok := detailPanelRowValue(row, f.Name)
		if !ok {
			// A field removed by field_access.hide must not be resurrected as
			// an empty labelled entry in the client payload. Stored NULL values
			// still have a key and remain visible as an empty value.
			continue
		}
		// Information-register rows retain a reference UUID for round-trips
		// and put its safe presentation in <field>_label. Use that label only
		// while the source still is an unmasked UUID: a masked value must not
		// accidentally pair with a stale/full label.
		if f.RefEntity != "" {
			if _, _, isUUID := uuidFromValue(v); isUUID {
				if label, found := detailPanelRowValue(row, f.Name+"_label"); found && fmtReportCell(label) != "" {
					v = label
				}
			}
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
