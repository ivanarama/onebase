package metadata

import "github.com/ivantit66/onebase/internal/csssafe"

// FormElementBackgroundCSS возвращает CSS-объявление фона элемента формы
// или пустую строку. Фон читают ГруппаФормы (#1547) и Кнопка — кнопки разных
// действий различают цветом, как в формах 1С. Значение обязано пройти
// csssafe.Color (hex, rgb, именованный цвет), иначе объявление не выдаётся —
// рантайм не применяет произвольные строки, а недопустимое значение называет
// check (form.background-color).
func FormElementBackgroundCSS(el *FormElement) string {
	if el == nil || !FormElementSupportsBackground(el.Kind) || el.Background == "" {
		return ""
	}
	if c := csssafe.Color(el.Background); c != "" {
		return "background:" + c + ";"
	}
	return ""
}

// FormElementSupportsBackground — виды элементов, для которых ключ background
// применяется: контейнер и кнопка.
func FormElementSupportsBackground(kind FormElementType) bool {
	return kind == FormElementGroupBox || kind == FormElementButton
}

// FormTableHeaderColors — классы и CSS-переменные контейнера таблицы
// ТабличнаяЧасть для фона и границы шапки (header_background /
// header_border_color): SlickGrid, простая таблица и таблица значений.
// Каждый цвет проходит csssafe.Color; нераспознанный не выдаётся, его
// называет check.
func FormTableHeaderColors(el *FormElement) (classes, vars string) {
	if el == nil || el.Kind != FormElementTablePart {
		return "", ""
	}
	if c := csssafe.Color(el.HeaderBackground); el.HeaderBackground != "" && c != "" {
		classes += " ob-th-bg"
		vars += "--ob-th-bg:" + c + ";"
	}
	if c := csssafe.Color(el.HeaderBorderColor); el.HeaderBorderColor != "" && c != "" {
		classes += " ob-th-border"
		vars += "--ob-th-border:" + c + ";"
	}
	return classes, vars
}
