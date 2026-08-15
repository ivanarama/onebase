package configcheck

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormMask предупреждает о `mask`, написанной как шаблон 1С, а не как
// регулярное выражение.
//
// `mask` рендерится в HTML-атрибут pattern, то есть это regexp. В шаблоне 1С
// «00.00.00» цифра — это `0`, а точка — разделитель; в регулярном выражении
// ровно наоборот: `0` — литеральный ноль, а `.` — ЛЮБОЙ символ. Маска отвергает
// тот самый формат, ради которого её написали, и принимает мусор (#763):
//
//	значение   mask: "00.00.00"
//	12.34.56   отвергнуто
//	00X00Y00   принято
//
// Раньше об этом не сообщал никто: check молчал, а браузер при отправке писал
// обезличенное «Используйте требуемый формат».
func CheckFormMask(proj *project.Project) []Issue {
	var warns []Issue
	for _, ent := range proj.Entities {
		for _, form := range ent.Forms {
			label := formFileLabel(ent, form)
			walkFormElements(form.Elements, func(el *metadata.FormElement) {
				msg, fix := maskDiagnosis(el.Mask)
				if msg == "" {
					return
				}
				warns = append(warns, Issue{
					File:         label,
					Object:       ent.Name,
					Kind:         "Управляемая форма",
					Code:         "form.mask-not-regexp",
					Message:      fmt.Sprintf("реквизит %q: %s", formElementName(el), msg),
					SuggestedFix: fix,
				})
			})
		}
	}
	return warns
}

// onecMaskChars — символы-заполнители шаблона 1С. `9` и `X` в регулярном
// выражении сами по себе безобидны, но вместе с `0` и разделителями образуют
// узнаваемый шаблон ввода.
const onecMaskChars = "09X"

// maskDiagnosis возвращает текст предупреждения и подсказку либо пустые строки,
// если маска выглядит осмысленным регулярным выражением.
func maskDiagnosis(mask string) (msg, fix string) {
	mask = strings.TrimSpace(mask)
	if mask == "" {
		return "", ""
	}
	if _, err := regexp.Compile(mask); err != nil {
		return fmt.Sprintf("mask не компилируется как регулярное выражение (%v), а pattern у поля — именно regexp", err),
			"Исправьте выражение или уберите mask: браузер молча не применит нерабочий pattern."
	}
	if !looksLikeOneCMask(mask) {
		return "", ""
	}
	return "mask — это регулярное выражение (атрибут pattern), а не шаблон ввода 1С: " +
			"в нём «0» значит литеральный ноль, а «.» — любой символ, поэтому такая маска " +
			"отвергает 12.34.56 и принимает 00X00Y00",
		`Для формата вида 12.34.56 напишите mask: '\d{2}\.\d{2}\.\d{2}'. ` +
			"Автоподстановки разделителей платформа пока не делает — mask только проверяет значение."
}

// looksLikeOneCMask — маска состоит ТОЛЬКО из заполнителей 1С, цифр-литералов и
// разделителей и содержит НЕ МЕНЕЕ ДВУХ заполнителей.
//
// Узко намеренно: задача — поймать характерную ошибку, а не оценивать чужие
// регулярные выражения. Всё, где есть настоящий синтаксис regexp (\d, [], {},
// *, ?, |), под правило не попадает. Порог в два заполнителя отсекает
// осмысленные короткие выражения вроде «0» (ровно ноль).
func looksLikeOneCMask(mask string) bool {
	placeholders := 0
	for _, r := range mask {
		switch {
		case strings.ContainsRune(onecMaskChars, r):
			placeholders++
		case r >= '1' && r <= '8':
			// Цифра-литерал шаблона: код страны в +7(000)…, век в 20XX.
		case r == '.' || r == '-' || r == '/' || r == ' ' || r == '(' || r == ')' || r == ':' || r == '+':
			// Разделители шаблона. Скобки и плюс — часть телефонных масок
			// вида +7(000)000-00-00; как regexp они означают группу и повтор,
			// что и делает такую маску особенно обманчивой.
		default:
			return false
		}
	}
	return placeholders >= 2
}
