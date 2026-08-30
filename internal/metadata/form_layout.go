package metadata

import (
	"fmt"
	"strings"
)

// Раскладка элемента управляемой формы: ключи width/height/halign/valign.
//
// До #1185 эти ключи YAML принимал, `onebase check` пропускал, конвертер 1С
// переносил — а рантайм читал только width/height у ПолеКартинки. Всё
// остальное молча не действовало: форму «декларативно отполировать» было
// нечем, и найти это можно было лишь чтением шаблона рендера.
//
// Контракт один для рантайма, предпросмотра конструктора и холста, поэтому он
// живёт здесь, в метаданных, а не в трёх рендерерах:
//
//	width  — ширина ВНЕШНЕГО блока элемента в пикселях, с max-width:100%
//	         (элемент не вылезает за контейнер даже на узком экране);
//	height — высота внешнего блока в пикселях; поле ввода внутри растягивается
//	         на остаток высоты под подписью (класс FormLayoutFillClass);
//	halign — положение блока по горизонтали: left|center|right|stretch;
//	valign — положение по вертикали: top|center|bottom (синоним middle).
//
// Ноль и пустая строка ничего не меняют — форма без этих ключей рисуется ровно
// как раньше.
//
// Исключение — ПолеКартинки: там width/height с самого начала ограничивали саму
// картинку (max-width/max-height), и переезд на «размер блока» перестроил бы уже
// написанные формы. Для него берётся FormElementAlignCSS (только выравнивание).
const (
	// FormLayoutMaxSize — потолок размера в пикселях. Не техническое
	// ограничение, а сторож опечатки: 10000px — это не «широкое поле», а
	// пропущенная запятая или единица измерения не из этой системы.
	FormLayoutMaxSize = 4000

	// FormLayoutFillClass — класс внешнего блока с заданной высотой. CSS-правило
	// (в стиле managed-формы и в предпросмотре) растягивает единственный
	// управляющий элемент внутри на остаток высоты: без него height у поля
	// растянул бы только рамку блока, а сам input остался бы прежним.
	FormLayoutFillClass = "ob-el-fill"
)

// FormHAlignValues / FormVAlignValues — словари значений выравнивания.
// Порядок фиксирован: он же порядок вариантов в панели свойств конструктора и
// в сообщении `onebase check` о неизвестном значении.
var (
	FormHAlignValues = []string{"left", "center", "right", "stretch"}
	FormVAlignValues = []string{"top", "center", "bottom"}
)

// NormalizeFormHAlign приводит значение halign к словарю (регистр не важен).
// ok=false — значение не из словаря; рендер его игнорирует, а check предупреждает.
func NormalizeFormHAlign(s string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "":
		return "", true
	case "left", "center", "right", "stretch":
		return v, true
	default:
		return "", false
	}
}

// NormalizeFormVAlign приводит значение valign к словарю. `middle` принимается
// синонимом `center`: так же называется середина в макетах печатных форм
// (DEVELOPER.md), и разойтись двум словарям в одной конфигурации было бы
// ловушкой.
func NormalizeFormVAlign(s string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "":
		return "", true
	case "middle", "center":
		return "center", true
	case "top", "bottom":
		return v, true
	default:
		return "", false
	}
}

// FormElementLayoutCSS собирает CSS-объявления внешнего блока элемента:
// размеры и выравнивание. Пустая строка — раскладка не задана, разметка
// остаётся прежней (в том числе без атрибута style).
//
// Значения берутся только из словарей и целых чисел, поэтому строка безопасна
// для подстановки в style= (рантайм отдаёт её как template.CSS).
func FormElementLayoutCSS(el *FormElement) string {
	if el == nil {
		return ""
	}
	return formLayoutCSS(el, true)
}

// FormElementAlignCSS — только выравнивание, без размеров. Для ПолеКартинки, где
// width/height заняты размером самой картинки.
func FormElementAlignCSS(el *FormElement) string {
	if el == nil {
		return ""
	}
	return formLayoutCSS(el, false)
}

func formLayoutCSS(el *FormElement, withSize bool) string {
	var b strings.Builder
	h, _ := NormalizeFormHAlign(el.HorizontalAlign)
	v, _ := NormalizeFormVAlign(el.VerticalAlign)

	if withSize {
		// stretch старше явной ширины: «растянуть на контейнер» и «ровно N
		// пикселей» — взаимоисключающие требования, и молча склеить их в
		// width:100%;width:200px значило бы отдать выбор порядку объявлений.
		switch {
		case h == "stretch":
			b.WriteString("width:100%;")
		case formLayoutSize(el.Width) > 0:
			fmt.Fprintf(&b, "width:%dpx;max-width:100%%;", formLayoutSize(el.Width))
			// flex-basis элемента в горизонтальной группе перебивает width
			// (.managed-group-horizontal задаёт flex:0 1 260px), поэтому
			// ширина без этого объявления действовала бы только в
			// вертикальной раскладке — то есть через раз.
			b.WriteString("flex:0 0 auto;")
		}
		if hh := formLayoutSize(el.Height); hh > 0 {
			fmt.Fprintf(&b, "height:%dpx;", hh)
		}
	}

	switch h {
	case "center":
		b.WriteString("margin-left:auto;margin-right:auto;")
	case "right":
		b.WriteString("margin-left:auto;")
	case "left":
		b.WriteString("margin-right:auto;")
	}
	switch v {
	case "top":
		b.WriteString("align-self:flex-start;")
	case "center":
		b.WriteString("align-self:center;")
	case "bottom":
		b.WriteString("align-self:flex-end;")
	}
	return b.String()
}

// FormTablePartGridCSS — стиль контейнера табличной части (SlickGrid). Высота
// по умолчанию считается по числу строк, как и до #1185; ключ height её
// перебивает — «покажи пятнадцать строк, а не восемь» задать было нечем.
// Ширина по умолчанию — во весь контейнер.
func FormTablePartGridCSS(el *FormElement, rows int) string {
	height := 200
	if rows > 8 {
		height = 300
	}
	width := "100%"
	var align string
	if el != nil {
		if h := formLayoutSize(el.Height); h > 0 {
			height = h
		}
		hAlign, _ := NormalizeFormHAlign(el.HorizontalAlign)
		if w := formLayoutSize(el.Width); w > 0 && hAlign != "stretch" {
			width = fmt.Sprintf("%dpx", w)
		}
		// Размеры сетки собраны выше, поэтому от общего контракта здесь берётся
		// только выравнивание блока.
		align = FormElementAlignCSS(el)
	}
	return fmt.Sprintf("height:%dpx;width:%s;max-width:100%%;%s", height, width, align)
}

// FormElementFillsHeight — задана ли элементу высота, ради которой внешнему
// блоку нужен класс FormLayoutFillClass.
func FormElementFillsHeight(el *FormElement) bool {
	return el != nil && formLayoutSize(el.Height) > 0
}

// formLayoutSize отсекает мусор: отрицательный размер даёт невалидный CSS
// (браузер объявление выбросит), запредельный — форму шириной с три экрана.
// В обоих случаях рендер ведёт себя как при незаданном размере, а `onebase
// check` называет причину (CheckFormLayout).
func formLayoutSize(n int) int {
	if n <= 0 || n > FormLayoutMaxSize {
		return 0
	}
	return n
}
