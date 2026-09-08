package launcher

import (
	"fmt"
	"html"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Раскладка элемента (width/height/halign/valign) в конструкторе форм: и
// read-only предпросмотр, и интерактивный холст показывают ту же геометрию, что
// нарисует рантайм. Контракт один на всех — metadata.FormElementLayoutCSS
// (#1185); здесь только обёртки, отдающие готовый HTML-атрибут.

// layoutStyleAttr — атрибут style= с размерами и выравниванием, либо пустая
// строка: элемент без этих ключей рисуется прежней разметкой, без style.
func layoutStyleAttr(el *metadata.FormElement) string {
	return styleAttr(metadata.FormElementLayoutCSS(el))
}

// alignStyleAttr — только выравнивание. Для ПолеКартинки, где width/height
// ограничивают саму картинку.
func alignStyleAttr(el *metadata.FormElement) string {
	return styleAttr(metadata.FormElementAlignCSS(el))
}

// pictureSizeStyleAttr — размер визуального прямоугольника ПолеКартинки.
// Внешняя обёртка остаётся shrink-to-fit и получает только выравнивание:
// width/height у этого вида исторически относятся к самой картинке, а не к
// блоку элемента. Preview и canvas рисуют заглушку вместо настоящего img, но
// обязаны занимать те же заданные пиксели.
func pictureSizeStyleAttr(el *metadata.FormElement) string {
	if el == nil {
		return ""
	}
	var css strings.Builder
	if width := metadata.NormalizeFormLayoutSize(el.Width); width > 0 {
		fmt.Fprintf(&css, "width:%dpx;max-width:100%%;", width)
	}
	if height := metadata.NormalizeFormLayoutSize(el.Height); height > 0 {
		fmt.Fprintf(&css, "height:%dpx;", height)
	}
	return styleAttr(css.String())
}

func styleAttr(css string) string {
	if css == "" {
		return ""
	}
	return ` style="` + html.EscapeString(css) + `"`
}

// layoutFillClass — класс внешнего блока с заданной высотой (пробел впереди,
// чтобы дописываться к уже собранному списку классов).
func layoutFillClass(el *metadata.FormElement) string {
	if !metadata.FormElementFillsHeight(el) {
		return ""
	}
	return " " + metadata.FormLayoutFillClass
}
