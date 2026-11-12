package report

import (
	"fmt"
	"strings"
	"unicode"
)

// VariantFilter is a permanent AND predicate on a query output field, before
// server pagination/limits. YAML scalars retain their types (false and 0 included).
// Unlike UserReportSettings.Filters, these predicates belong to trusted YAML.
type VariantFilter struct {
	Field string `yaml:"field" json:"field"`
	Op    string `yaml:"op" json:"op"`
	Value any    `yaml:"value" json:"value"`
}

// QueryForVariant selects trusted query text and binds permanent filters. The
// resulting DSL passes through the ordinary compiler, row access and field mask
// checks on every execution path. It never edits the shared report or params.
func (r *Report) QueryForVariant(name string, params map[string]any) (string, map[string]any, error) {
	text := r.Query
	var filters []VariantFilter
	for _, v := range r.Variants {
		if name != "" && v.Name == name {
			if strings.TrimSpace(v.Query) != "" {
				text = v.Query
			}
			filters = v.Filters
			break
		}
	}
	if len(filters) == 0 {
		return text, params, nil
	}
	values := make(map[string]any, len(params)+len(filters))
	used := make(map[string]bool, len(params)+len(filters))
	for k, v := range params {
		values[k] = v
		used[strings.ToLower(k)] = true
	}
	conditions := make([]string, 0, len(filters))
	for i, f := range filters {
		if !variantFieldIdentifier(f.Field) {
			return "", nil, fmt.Errorf("variant %q filter %d: invalid field %q", name, i+1, f.Field)
		}
		op := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}[f.Op]
		if op == "" {
			return "", nil, fmt.Errorf("variant %q filter %d: invalid operator %q", name, i+1, f.Op)
		}
		column := "_report_variant." + f.Field
		if f.Value == nil {
			switch f.Op {
			case "eq":
				conditions = append(conditions, column+" ЕСТЬ NULL")
			case "ne":
				conditions = append(conditions, column+" ЕСТЬ НЕ NULL")
			default:
				return "", nil, fmt.Errorf("variant %q: null supports only eq/ne", name)
			}
			continue
		}
		switch f.Value.(type) {
		case string, bool, int, int64, uint64, float64:
		default:
			return "", nil, fmt.Errorf("variant %q filter %d: value must be a scalar", name, i+1)
		}
		key := fmt.Sprintf("__report_variant_%d", i)
		for used[strings.ToLower(key)] {
			key += "_"
		}
		used[strings.ToLower(key)] = true
		values[key] = f.Value
		conditions = append(conditions, column+" "+op+" &"+key)
	}
	text = "ВЫБРАТЬ _report_variant.* ИЗ (\n" + strings.TrimSuffix(strings.TrimSpace(text), ";") + "\n) КАК _report_variant ГДЕ " + strings.Join(conditions, " И ")
	return text, values, nil
}

func variantFieldIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r != '_' && !unicode.IsLetter(r) && (i == 0 || !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}
