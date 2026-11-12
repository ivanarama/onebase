package widget

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Служебные ключи строки list-виджета с оформлением (row_style). Значения уже
// проверены: цвет — только "#rrggbb", признак — bool.
const (
	RowBackgroundKey = "_row_bg"
	RowTextColorKey  = "_row_fg"
	RowBoldKey       = "_row_bold"
)

// NormalizeRowColor принимает только "#RGB" и "#RRGGBB" и возвращает цвет в
// виде "#rrggbb". Всё прочее — пустая строка: из данных в стиль строки
// попадает только проверенный цвет, никакого произвольного CSS.
func NormalizeRowColor(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) != 4 && len(s) != 7 || s[0] != '#' {
		return ""
	}
	hex := strings.ToLower(s[1:])
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	return "#" + hex
}

// RowTextColor — цвет текста, читаемый на фоне bg ("#rrggbb"): тёмный на
// светлом, белый на тёмном (яркость по YIQ). Цвет текста задаётся вместе с
// фоном, поэтому строка читается и в светлой, и в тёмной теме.
func RowTextColor(bg string) string {
	if len(bg) != 7 {
		return ""
	}
	r, _ := strconv.ParseUint(bg[1:3], 16, 8)
	g, _ := strconv.ParseUint(bg[3:5], 16, 8)
	b, _ := strconv.ParseUint(bg[5:7], 16, 8)
	if (r*299+g*587+b*114)/1000 >= 150 {
		return "#111827"
	}
	return "#ffffff"
}

// rowStyleTruthy — признак «жирный» из колонки выборки: булево, число ≠ 0 или
// строка "true"/"1"/"истина"/"да".
func rowStyleTruthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int, int32, int64, float32, float64:
		return fmt.Sprint(x) != "0"
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "истина", "да":
			return true
		}
	}
	return false
}

// applyRowStyle переносит оформление строк из колонок выборки в служебные
// ключи и возвращает имена колонок оформления (их таблица не показывает).
func applyRowStyle(w *metadata.Widget, cols []string, rows []map[string]any) []string {
	if w.RowStyle == nil {
		return nil
	}
	bgCol := resolveFieldName(cols, w.RowStyle.Background)
	boldCol := resolveFieldName(cols, w.RowStyle.Bold)
	colorCol := resolveFieldName(cols, w.RowStyle.Color)
	var styleCols []string
	if bgCol != "" {
		styleCols = append(styleCols, bgCol)
	}
	if boldCol != "" {
		styleCols = append(styleCols, boldCol)
	}
	if colorCol != "" {
		styleCols = append(styleCols, colorCol)
	}
	for _, row := range rows {
		if bgCol != "" {
			if bg := NormalizeRowColor(row[bgCol]); bg != "" {
				row[RowBackgroundKey] = bg
				row[RowTextColorKey] = RowTextColor(bg)
			}
		}
		if boldCol != "" && rowStyleTruthy(row[boldCol]) {
			row[RowBoldKey] = true
		}
		if colorCol != "" {
			if color := NormalizeRowColor(row[colorCol]); color != "" {
				row[RowTextColorKey] = color
			}
		}
	}
	return styleCols
}
