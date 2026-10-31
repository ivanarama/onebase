package metadata

import "strings"

// FormChoiceTablePart returns the owning table and its canonical metadata name.
// Only direct columns belong to a row; nested controls cannot name another row.
func FormChoiceTablePart(form *FormModule, column *FormElement) (*FormElement, string) {
	if form == nil || column == nil {
		return nil, ""
	}
	var table *FormElement
	var name string
	form.Walk(func(el *FormElement) bool {
		if el.Kind != FormElementTablePart && el.Kind != FormElementTable {
			return true
		}
		for _, child := range append(append([]*FormElement{}, el.Children...), el.Columns...) {
			if child != column {
				continue
			}
			table = el
			name = strings.TrimSpace(el.TablePart)
			if name == "" {
				parts := strings.Split(el.DataPath, ".")
				name = parts[len(parts)-1]
			}
			if name == "" {
				name = el.Name
			}
		}
		return true
	})
	return table, name
}

// FormChoiceTPField accepts only an explicit column path within its own table.
func FormChoiceTPField(owner *Entity, form *FormModule, column *FormElement) (*Field, string) {
	if owner == nil || column == nil {
		return nil, ""
	}
	_, name := FormChoiceTablePart(form, column)
	parts := strings.Split(column.DataPath, ".")
	if name == "" || len(parts) != 3 || !strings.EqualFold(parts[0], "Объект") || !strings.EqualFold(parts[1], name) {
		return nil, name
	}
	for i := range owner.TableParts {
		tp := &owner.TableParts[i]
		if !strings.EqualFold(tp.Name, name) {
			continue
		}
		for j := range tp.Fields {
			if strings.EqualFold(tp.Fields[j].Name, parts[2]) {
				return &tp.Fields[j], tp.Name
			}
		}
	}
	return nil, name
}
