package metadata

import (
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
)

// FormImportanceError — значение ключа importance, при котором поле или
// надпись показываются красным текстом ошибки.
const FormImportanceError = "error"

// formErrorTextColor — красный текст ошибки платформы: тот же оттенок, что у
// блока .error и обязательной звёздочки, читается и на белом, и на сером фоне
// нередактируемого поля.
const formErrorTextColor = "#b91c1c"

// FormElementSupportsTextColor — виды элементов, для которых применяются
// text_color и importance: поле ввода и надпись.
func FormElementSupportsTextColor(kind FormElementType) bool {
	return kind == FormElementField || kind == FormElementLabel
}

// FormImportanceKnown сообщает, что значение importance рантайм понимает.
func FormImportanceKnown(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), FormImportanceError)
}

// FormElementTextColor возвращает цвет текста элемента или пустую строку:
// text_color, прошедший csssafe.Color, иначе красный при importance: error.
// Недопустимый text_color не применяется (его называет check,
// form.text-color), но и не гасит importance.
func FormElementTextColor(el *FormElement) string {
	if el == nil || !FormElementSupportsTextColor(el.Kind) {
		return ""
	}
	if c := csssafe.Color(el.TextColor); c != "" {
		return c
	}
	if FormImportanceKnown(el.Importance) {
		return formErrorTextColor
	}
	return ""
}

// FormElementTextColorCSS — CSS-объявления цвета текста для внешнего блока
// элемента: color для подписи и надписи и переменная --ob-text-color, которую
// правило .ob-text-colored передаёт самим input/select/textarea (они цвет
// родителя не наследуют).
func FormElementTextColorCSS(el *FormElement) string {
	c := FormElementTextColor(el)
	if c == "" {
		return ""
	}
	return "--ob-text-color:" + c + ";color:" + c + ";"
}
