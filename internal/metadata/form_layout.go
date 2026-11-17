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

// FormElementAlignCSS — только выравнивание, без объявленных размеров. Для
// ПолеКартинки, где width/height заняты размером самой картинки. Stretch всё
// равно задаёт ширину/flex внешней обёртки: это и есть его контракт
// выравнивания, а не размер из width.
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

	// Stretch старше явной ширины: «растянуть на контейнер» и «ровно N
	// пикселей» — взаимоисключающие требования, и молча склеить их в
	// width:100%;width:200px значило бы отдать выбор порядку объявлений.
	// Он нужен и align-only обёртке ПолеКартинки: без width/flex допустимый
	// halign принимался, но оставлял картинку shrink-to-fit.
	if h == "stretch" {
		// В горизонтальной группе родитель задаёт дочерним блокам flex-basis и
		// min-width. Полная flex-запись делает stretch отдельной строкой группы
		// и одинаково работает в рантайме, preview и на холсте.
		b.WriteString("width:100%;flex:1 1 100%;min-width:0;")
	} else if withSize {
		switch {
		case formLayoutSize(el.Width) > 0:
			fmt.Fprintf(&b, "width:%dpx;max-width:100%%;", formLayoutSize(el.Width))
			// flex-basis элемента в горизонтальной группе перебивает width
			// (.managed-group-horizontal задаёт flex:0 1 260px), поэтому
			// ширина без этого объявления действовала бы только в
			// вертикальной раскладке — то есть через раз.
			// min-width родительской горизонтальной группы не должен
			// переписывать явно заданную меньшую ширину.
			b.WriteString("flex:0 0 auto;min-width:0;")
		}
	}
	if withSize {
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
	return NormalizeFormLayoutSize(n)
}

// NormalizeFormLayoutSize применяет общий допустимый диапазон размеров формы.
// Публичный helper нужен рендерам с особой семантикой размера (ПолеКартинки):
// смысл width/height у них свой, но мусор они обязаны отбрасывать одинаково.
func NormalizeFormLayoutSize(n int) int {
	if n <= 0 || n > FormLayoutMaxSize {
		return 0
	}
	return n
}

// HorizontalGrouping — вариант группировки детей группы: true только при явном
// orientation: horizontal (регистр и лишние пробелы не важны).
//
// Незаданный ключ остаётся ВЕРТИКАЛЬНЫМ. Это не мелочь: формы, написанные до
// появления вложенных групп, не должны поехать оттого, что конструктор теперь
// создаёт новые группы горизонтальными. Поэтому умолчание новой группы пишется
// в YAML явно (см. applyEditOp), а рендер читает только то, что написано.
func (el *FormElement) HorizontalGrouping() bool {
	return el != nil && strings.EqualFold(strings.TrimSpace(el.Orientation), "horizontal")
}

// FormGroupOrientationValues — словарь вариантов группировки группы. Порядок
// фиксирован: он же порядок вариантов в панели свойств и в сообщении check.
var FormGroupOrientationValues = []string{"horizontal", "vertical"}

// NormalizeFormGroupOrientation приводит orientation к словарю. Пустая строка —
// «ключ не задан», это допустимо (вертикальная раскладка по умолчанию).
// ok=false — значение не из словаря: рендер его игнорирует, а check предупреждает.
func NormalizeFormGroupOrientation(s string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "":
		return "", true
	case "horizontal", "vertical":
		return v, true
	default:
		return "", false
	}
}

// FormGroupPlainClass — класс группы без рамки (bordered: false). Держим его
// здесь, а не россыпью строк: класс пишет шаблон рантайма, повторяют холст и
// предпросмотр конструктора, а правила для него есть и в обычном виде, и в новом.
// Разойдись они — «группа без рамки» молча вернула бы рамку на одной из страниц.
const FormGroupPlainClass = "ob-group-plain"

// FormElementSupportsGroupFrame — виды элементов, у которых ключи show_title и
// bordered что-то меняют: заголовок (<legend>) и рамку рисует только группа.
func FormElementSupportsGroupFrame(kind FormElementType) bool {
	return kind == FormElementGroupBox
}

// GroupTitleVisible — показывать заголовок группы. Умолчание — да: у всех форм,
// написанных до появления ключа, show_title отсутствует, и они обязаны остаться
// с заголовком.
func (el *FormElement) GroupTitleVisible() bool {
	return el == nil || el.ShowTitle == nil || *el.ShowTitle
}

// GroupBordered — рисовать у группы рамку и внутренние отступы. Умолчание — да.
// bordered: false оставляет от группы только раскладку детей: полоса кнопок или
// строка реквизитов перестаёт читаться отдельным блоком.
func (el *FormElement) GroupBordered() bool {
	return el == nil || el.Bordered == nil || *el.Bordered
}

// UsesEqualColumns включает режим только у горизонтальной группы без scroll_x.
// В остальных случаях check сообщает, почему настройка не применяется.
func (el *FormElement) UsesEqualColumns() bool {
	return el != nil && el.Kind == FormElementGroupBox && el.HorizontalGrouping() && el.EqualColumns && !el.ScrollX
}

// FormEqualColumnsCSS — единый режим для runtime, preview и холста. Группа
// управляет шириной всех непосредственных детей, включая кнопки и группы;
// !important намеренно перекрывает их явную width/flex/halign. Высота и
// вертикальное выравнивание остаются свойствами самого элемента.
const FormEqualColumnsCSS = `
.ob-equal-columns>:is(.managed-group-body,.group-body,.fc-children){display:flex;flex-wrap:wrap;gap:12px;align-items:flex-start}
.ob-equal-columns>:is(.managed-group-body,.group-body,.fc-children)>*{box-sizing:border-box;flex:1 1 180px!important;min-width:min(180px,100%)!important;width:auto!important;max-width:100%;margin-left:0!important;margin-right:0!important}
.ob-equal-columns :is(input,select,textarea){box-sizing:border-box;max-width:100%}
/* Пустые drop-зоны холста не занимают колонку или gap в обычном режиме. */
.ob-equal-columns>.fc-children>.fc-drop{display:none}
body.ob-dragging .ob-equal-columns>.fc-children>.fc-drop{display:block;flex:0 0 8px!important;min-width:0!important;width:8px!important}
`
