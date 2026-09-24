package metadata

import "github.com/ivantit66/onebase/internal/csssafe"

// FormElementBackgroundCSS возвращает CSS-объявление фона контейнера формы
// или пустую строку. Фон читает только ГруппаФормы (#1547): значение обязано
// пройти csssafe.Color (hex, rgb, именованный цвет), иначе объявление не
// выдаётся — рантайм не применяет произвольные строки, а недопустимое значение
// называет check (form.background-color).
func FormElementBackgroundCSS(el *FormElement) string {
	if el == nil || el.Kind != FormElementGroupBox || el.Background == "" {
		return ""
	}
	if c := csssafe.Color(el.Background); c != "" {
		return "background:" + c + ";"
	}
	return ""
}
