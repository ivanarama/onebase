package ui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/richtext"
	"golang.org/x/net/html"
)

// effectiveFormElementRequired combines the form-local requirement with the
// stronger entity invariant. The latter is reflected in a managed form even
// when its element does not repeat required: true.
func effectiveFormElementRequired(entity *metadata.Entity, element *metadata.FormElement) bool {
	if element == nil {
		return false
	}
	if element.Required {
		return true
	}
	field, ok := managedFormElementEntityField(entity, element)
	return ok && field.Required
}

// nativeFormElementRequired reports whether the browser may require input
// before submit. An explicit form rule always may. A metadata rule is also
// reflected in ordinary editable controls, except when the platform itself
// fills an auto-numbered Code/Number during Save.
func nativeFormElementRequired(entity *metadata.Entity, element *metadata.FormElement) bool {
	if element == nil {
		return false
	}
	if element.Required {
		return true
	}
	field, ok := managedFormElementEntityField(entity, element)
	return ok && field.Required && !isAutoNumberedField(entity, field)
}

// managedFormElementEntityField resolves only a header field of Объект. A
// form attribute such as Форма.Комментарий may deliberately have the same
// final name as an entity field and must not inherit its global requirement.
// A path without a root remains a supported legacy spelling.
func managedFormElementEntityField(entity *metadata.Entity, element *metadata.FormElement) (metadata.Field, bool) {
	if entity == nil || element == nil {
		return metadata.Field{}, false
	}
	path := strings.TrimSpace(element.DataPath)
	if path == "" || strings.Count(path, ".") > 1 {
		return metadata.Field{}, false
	}
	if dot := strings.Index(path, "."); dot >= 0 && !strings.EqualFold(strings.TrimSpace(path[:dot]), "Объект") {
		return metadata.Field{}, false
	}
	return entityFieldByName(entity, strings.TrimSpace(dpFieldName(path)))
}

// validateManagedFormRequired enforces required fields at the managed-form
// boundary. It intentionally remains in the UI layer: FormElement.required
// protects this form's submit, while metadata.Field.required remains the
// entity-wide invariant enforced by entityservice on every write path.
func (s *Server) validateManagedFormRequired(
	r *http.Request,
	entity *metadata.Entity,
	form *metadata.FormModule,
	values map[string]any,
) error {
	if form == nil {
		return nil
	}

	lang := s.resolveLang(r)
	seen := make(map[string]bool)
	var missing []string
	walkBrowserFormElements(form, func(visit browserFormElementVisit) {
		element := visit.element
		if element == nil || visit.parentTablePart != nil || visit.effectiveReadOnly ||
			!managedElementPostsScalar(element) {
			return
		}
		path := strings.TrimSpace(element.DataPath)
		if path == "" || strings.Count(path, ".") > 1 {
			return
		}
		name := strings.TrimSpace(dpFieldName(path))
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if seen[key] {
			return
		}

		field, entityField := managedFormElementEntityField(entity, element)
		if entityField && element.Kind == metadata.FormElementCheckbox && field.Type == metadata.FieldTypeBool {
			value, given := maskCIKeyValue(values, field.Name)
			// An editable checkbox omitted by the browser is the meaningful
			// value false, not an absent value. Materialise it independently of
			// required validation so every managed-form submit keeps the normal
			// two-valued checkbox contract.
			if !given || value == nil {
				// The browser encodes an unchecked checkbox by omitting its key.
				if storedKey, exists := maskCIKey(values, field.Name); exists {
					values[storedKey] = false
				} else {
					values[field.Name] = false
				}
			}
		}
		// Field.required is deliberately not checked here. Entityservice
		// validates it against the final state after auto-numbering, Preflight
		// and write hooks. This early boundary owns only the form-local rule.
		if !element.Required {
			return
		}

		blank := false
		if entityField {
			value, given := maskCIKeyValue(values, field.Name)
			blank = !given || requiredManagedFieldValueBlank(field, value)
		} else {
			raw := ""
			if r != nil {
				raw = r.FormValue(name)
			}
			// Form-local boolean attributes have the same two-valued
			// semantics: an unchecked checkbox is a filled false value.
			blank = element.Kind != metadata.FormElementCheckbox && strings.TrimSpace(raw) == ""
			if !blank {
				if attr := formAttributeByName(form, name); attr != nil {
					if attrRefEntityName(attr.TypeRef) != "" {
						id, err := uuid.Parse(strings.TrimSpace(raw))
						blank = err != nil || id == uuid.Nil
					} else if strings.EqualFold(strings.TrimSpace(attr.TypeRef), string(metadata.FieldTypeRichText)) {
						blank = requiredManagedRichTextBlank(raw)
					}
				}
			}
		}
		if !blank {
			return
		}

		seen[key] = true
		missing = append(missing, "«"+requiredManagedElementTitle(element, name, lang)+"»")
	})
	if len(missing) == 0 {
		return nil
	}
	return errors.New(s.tr(lang, "Заполните обязательные поля:") + " " + strings.Join(missing, ", "))
}

func requiredManagedFieldValueBlank(field metadata.Field, value any) bool {
	if value == nil {
		return true
	}
	if field.RefEntity != "" {
		_, id, ok := uuidFromValue(value)
		return !ok || id == uuid.Nil
	}
	if text, ok := value.(string); ok {
		if field.Type == metadata.FieldTypeRichText {
			return requiredManagedRichTextBlank(text)
		}
		text = strings.TrimSpace(text)
		return text == "" || text == "<nil>"
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value)) == ""
}

// requiredManagedRichTextBlank follows what the user sees in Quill rather
// than treating its empty backing markup (for example <p><br></p>) as content.
// An image is meaningful richtext even when there is no textual projection.
func requiredManagedRichTextBlank(value string) bool {
	if strings.TrimSpace(richtext.Plaintext(value)) != "" {
		return false
	}
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return true
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			if strings.EqualFold(string(name), "img") {
				return false
			}
		}
	}
}

func formAttributeByName(form *metadata.FormModule, name string) *metadata.FormAttribute {
	if form == nil {
		return nil
	}
	for _, attr := range form.Attributes {
		if attr != nil && strings.EqualFold(attr.Name, name) {
			return attr
		}
	}
	return nil
}

func requiredManagedElementTitle(element *metadata.FormElement, fallback, lang string) string {
	if element == nil {
		return fallback
	}
	if lang != "" {
		if title := strings.TrimSpace(element.TitleMap[lang]); title != "" {
			return title
		}
	}
	if title := strings.TrimSpace(element.TitleMap["ru"]); title != "" {
		return title
	}
	if title := strings.TrimSpace(element.Title); title != "" {
		return title
	}
	return fallback
}
