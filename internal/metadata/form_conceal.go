package metadata

import (
	"fmt"
	"strings"
)

// FormConcealRule is the deliberately small, validated conceal contract.
// Controller is a form-only boolean, never an authority supplied by a browser.
type FormConcealRule struct {
	Field      string
	Controller string
	Negated    bool
}

func (r FormConcealRule) Allows(value bool) bool { return value != r.Negated }

// FormConcealRules is shared by check and the runtime; unsupported forms cannot
// acquire disclosure authority even when configuration validation was skipped.
func FormConcealRules(entity *Entity, form *FormModule) ([]FormConcealRule, error) {
	if form == nil {
		return nil, nil
	}
	var rules []FormConcealRule
	var problem error
	counts := map[string]int{}
	form.Walk(func(el *FormElement) bool {
		if el != nil && el.DataPath != "" {
			parts := strings.Split(el.DataPath, ".")
			name := el.DataPath
			if len(parts) == 2 && (strings.EqualFold(parts[0], "Объект") || strings.EqualFold(parts[0], "Object")) {
				name = parts[1]
			}
			counts[strings.ToLower(name)]++
		}
		return true
	})
	form.Walk(func(el *FormElement) bool {
		if el == nil || (!el.Conceal && el.RevealWhen == "") {
			return true
		}
		fail := func(message string) {
			if problem == nil {
				problem = fmt.Errorf("conceal/reveal_when: %s: %s", el.Name, message)
			}
		}
		if !el.Conceal {
			fail("reveal_when требует conceal: true")
			return true
		}
		if entity == nil || (form.Kind != "" && !strings.EqualFold(form.Kind, "object")) || el.Kind != FormElementField {
			fail("поддерживается только ПолеВвода строкового поля объекта")
			return true
		}
		path := strings.Split(el.DataPath, ".")
		name := el.DataPath
		if len(path) == 2 && (strings.EqualFold(path[0], "Объект") || strings.EqualFold(path[0], "Object")) {
			name = path[1]
		} else if len(path) != 1 {
			fail("data_path должен указывать строковое поле объекта")
			return true
		}
		var field *Field
		for i := range entity.Fields {
			if strings.EqualFold(entity.Fields[i].Name, name) {
				field = &entity.Fields[i]
			}
		}
		if field == nil || field.Type != FieldTypeString || (el.Type != "" && el.Type != "string") {
			fail("допустим только string, включая multiline; number/date/bool/reference/enum/richtext/image/file не поддерживаются")
			return true
		}
		if counts[strings.ToLower(name)] != 1 {
			fail("повторные размещения скрытого поля не поддерживаются")
			return true
		}
		for _, a := range form.Attributes {
			if a != nil && strings.EqualFold(a.Name, name) {
				fail("поле объекта не должно совпадать с реквизитом формы")
				return true
			}
		}
		rule := FormConcealRule{Field: field.Name}
		expr := strings.Fields(strings.TrimSpace(el.RevealWhen))
		if len(expr) > 0 {
			if len(expr) == 2 && (strings.EqualFold(expr[0], "НЕ") || strings.EqualFold(expr[0], "NOT")) {
				rule.Negated = true
				expr = expr[1:]
			}
			if len(expr) != 1 {
				fail("reveal_when: нужен один булев реквизит формы либо НЕ/NOT перед ним")
				return true
			}
			for _, a := range form.Attributes {
				if a != nil && strings.EqualFold(a.Name, expr[0]) && (strings.EqualFold(a.TypeRef, "bool") || strings.EqualFold(a.TypeRef, "boolean")) {
					rule.Controller = a.Name
				}
			}
			if rule.Controller == "" {
				fail("reveal_when: нужен объявленный булев реквизит формы")
				return true
			}
			for _, f := range entity.Fields {
				if strings.EqualFold(f.Name, rule.Controller) {
					fail("управляющий реквизит должен быть отдельным реквизитом формы")
					return true
				}
			}
		}
		rules = append(rules, rule)
		return true
	})
	controllers := map[string]bool{}
	for _, rule := range rules {
		if rule.Controller != "" {
			key := strings.ToLower(rule.Controller)
			if old, ok := controllers[key]; ok && old != rule.Negated {
				problem = fmt.Errorf("conceal/reveal_when: один управляющий реквизит не может иметь противоположные условия")
			}
			controllers[key] = rule.Negated
		}
	}
	if problem != nil {
		return nil, problem
	}
	return rules, nil
}
