package launcher

import (
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
