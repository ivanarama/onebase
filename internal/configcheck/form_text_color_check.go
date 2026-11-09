package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormTextColor предупреждает о цвете текста, который рантайм не
// применит: text_color и importance читают только ПолеВвода и Надпись,
// text_color обязан проходить csssafe.Color, importance понимает одно
// значение — error. Как и у background (#1547), ключ без диагностики
// превращался бы в наблюдение «почему-то не покраснело».
func CheckFormTextColor(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var warns []Issue
	forEachLabeledForm(proj, func(label, object string, form *metadata.FormModule) {
		if form == nil {
			return
		}
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			color := strings.TrimSpace(el.TextColor)
			importance := strings.TrimSpace(el.Importance)
			if color == "" && importance == "" {
				return
			}
			issue := func(code, msg, fix string) {
				warns = append(warns, Issue{File: label, Object: object, Kind: "Управляемая форма", Code: code, Message: msg, SuggestedFix: fix})
			}
			if !metadata.FormElementSupportsTextColor(el.Kind) {
				issue("form.text-color-kind",
					fmt.Sprintf("элемент %q (%s): ключи text_color и importance игнорируются — цвет текста поддерживают только ПолеВвода и Надпись", formElementName(el), el.Kind),
					"Перенесите ключ на ПолеВвода или Надпись либо удалите его.")
				return
			}
			if color != "" && csssafe.Color(color) == "" {
				issue("form.text-color",
					fmt.Sprintf("%s %q: text_color %q — не распознанный цвет, цвет текста применён не будет", el.Kind, formElementName(el), el.TextColor),
					"Допустимы hex (#rrggbb, #rgb), rgb(...) и именованные CSS-цвета; для текста ошибки — importance: error.")
			}
			if importance != "" && !metadata.FormImportanceKnown(importance) {
				issue("form.importance",
					fmt.Sprintf("%s %q: importance %q не поддерживается, выделение применено не будет", el.Kind, formElementName(el), el.Importance),
					"Допустимое значение — error (красный текст ошибки).")
			}
		})
	})
	return warns
}
