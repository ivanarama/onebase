package launcher

import (
	"fmt"
	"html"

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

// pictureSizeStyleAttr — ограничения настоящего изображения ПолеКартинки.
// Внешняя обёртка остаётся shrink-to-fit и получает только выравнивание:
// width/height у этого вида исторически относятся к самой картинке, а не к
// блоку элемента. Как и runtime, designer не растягивает маленький asset до
// заданного размера, а применяет max-width/max-height с прежним default 100px.
func pictureSizeStyleAttr(el *metadata.FormElement) string {
	if el == nil {
		return ""
	}
	width := metadata.NormalizeFormLayoutSize(el.Width)
	if width == 0 {
		width = 100
	}
	height := metadata.NormalizeFormLayoutSize(el.Height)
	if height == 0 {
		height = 100
	}
	return styleAttr(fmt.Sprintf("max-width:%dpx;max-height:%dpx;", width, height))
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
