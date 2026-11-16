package report

import (
	"strings"
	"unicode"
)

// RowSourceDefinition selects a bounded typed-row transformer in this report's
// trusted .rep.os module. It is separate from a ready-TabDoc result procedure.
// HTTP/runtime integration is deliberately not implied by this metadata model.
type RowSourceDefinition struct {
	Procedure string `yaml:"procedure"`
	// Inputs is a trusted explicit DSL subset of already proven initial columns.
	// nil preserves the legacy all-proven-columns kernel contract.
	Inputs  []string          `yaml:"inputs,omitempty"`
	Columns []RowSourceColumn `yaml:"columns"`
}
type RowSourceColumn struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
}

func (d *RowSourceDefinition) Errors() []string {
	if d == nil {
		return nil
	}
	var out []string
	if !rowSourceName(d.Procedure) {
		out = append(out, "источник строк: неверное имя процедуры")
	}
	if len(d.Columns) == 0 || len(d.Columns) > 32 {
		out = append(out, "источник строк: требуется от 1 до 32 добавленных колонок")
	}
	if d.Inputs != nil {
		if len(d.Inputs) == 0 || len(d.Inputs) > 128 {
			out = append(out, "источник строк: требуется от 1 до 128 входных колонок")
		}
		inputNames := map[string]bool{}
		for _, name := range d.Inputs {
			key := strings.ToLower(name)
			if !rowSourceName(name) || inputNames[key] {
				out = append(out, "источник строк: неверное или повторное имя входной колонки")
			}
			inputNames[key] = true
		}
	}
	seen := map[string]bool{}
	for _, c := range d.Columns {
		key := strings.ToLower(c.Name)
		if !rowSourceName(c.Name) || seen[key] {
			out = append(out, "источник строк: неверное или повторное имя добавленной колонки")
		}
		seen[key] = true
		switch c.Type {
		case "string", "number", "bool", "date", "datetime":
		default:
			out = append(out, "источник строк: неизвестный тип добавленной колонки")
		}
	}
	return out
}
func rowSourceName(name string) bool {
	if name == "" || len(name) > 128 || strings.HasPrefix(name, "__") {
		return false
	}
	for i, r := range name {
		if r != '_' && !unicode.IsLetter(r) && !(i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
