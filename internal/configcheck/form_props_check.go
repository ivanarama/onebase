package configcheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormProps предупреждает о ключах `props`, которых не читает ни один
// потребитель.
//
// `FormElement.Props` — свободный map[string]any, и до этой проверки любое
// написанное туда имя принималось молча (#1492). Рендерер управляемой формы к
// Props не обращается вовсе; смысл ключам придаёт только конвертер 1С. Изнутри
// конфигурации отличить «свойство есть, но не применилось» от «такого свойства
// не существует» было нельзя — ровно там, где автор ставит эксперимент:
//
//	props: {РастягиватьПоГоризонтали: true}   принято, ничего не делает
//	props: {HorizontalStretch: true}          принято, уходит в Form.xml
//
// Перечень известных ключей живёт в metadata.FormPropRegistry рядом с флагом
// потребителя, поэтому предупреждение не может разойтись с кодом конвертера
// (сторож — TestFormPropsRegistryCoversConsumers).
func CheckFormProps(proj *project.Project) []Issue {
	var warns []Issue

	report := func(label, object string, form *metadata.FormModule) {
		if form == nil {
			return
		}
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			for _, key := range sortedPropKeys(el.Props) {
				if _, known := metadata.LookupFormProp(key); known {
					continue
				}
				warns = append(warns, Issue{
					File:   label,
					Object: object,
					Kind:   "Управляемая форма",
					Code:   "form.prop-unused",
					Message: fmt.Sprintf(
						"элемент %q: ключ props %q не используется управляемой формой и не читается конвертером 1С — платформа молча его игнорирует",
						formElementName(el), key),
					SuggestedFix: propSuggestedFix(key),
				})
			}
		})
	}

	for _, ent := range proj.Entities {
		if ent == nil {
			continue
		}
		for _, form := range ent.Forms {
			report(formFileLabel(ent, form), ent.Name, form)
		}
	}
	for _, proc := range proj.Processors {
		if proc == nil {
			continue
		}
		for _, form := range proc.Forms {
			if form == nil {
				continue
			}
			name := form.Name
			if name == "" {
				name = "объекта"
			}
			report("forms/"+strings.ToLower(proc.Name)+"/"+name+".form.yaml", proc.Name, form)
		}
	}
	return warns
}

// sortedPropKeys — порядок обхода map фиксируем, иначе вывод check прыгал бы
// от запуска к запуску.
func sortedPropKeys(props map[string]any) []string {
	if len(props) == 0 {
		return nil
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// propSuggestedFix подсказывает либо написание известного ключа (регистр для
// 1С-имён значим), либо настоящие ключи элемента, которыми задаётся раскладка.
func propSuggestedFix(key string) string {
	if spec, ok := metadata.LookupFormPropFold(key); ok {
		return fmt.Sprintf("Ключ props чувствителен к регистру: конвертер 1С читает %q (%s). Проверьте написание.",
			spec.Key, spec.Note)
	}
	return "Управляемая форма не читает props вообще: размер и растягивание задаются собственными ключами элемента — " +
		"width, height, halign (" + strings.Join(metadata.FormHAlignValues, ", ") + "), valign (" +
		strings.Join(metadata.FormVAlignValues, ", ") + "). " +
		"В props имеет смысл только то, что читает конвертер 1С: " +
		strings.Join(metadata.FormPropOneCXMLKeys(), ", ") + "."
}
