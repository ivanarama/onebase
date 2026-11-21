package rowsource

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"github.com/ivantit66/onebase/internal/runtime"
)

func cloneInputSubset(names []string) []string {
	if names == nil {
		return nil
	}
	return append([]string{}, names...)
}

// Names select only fields already proven by the unchanged all-column Schema.
// Neither a name nor membership in trusted metadata is a lineage proof itself.
func selectDSLFields(fields []resultfilter.Field, names []string) ([]resultfilter.Field, error) {
	if names == nil {
		return fields, nil
	}
	if len(names) == 0 || len(names) > 128 {
		return nil, fmt.Errorf("row source: invalid DSL input subset")
	}
	known := map[string]resultfilter.Field{}
	for _, field := range fields {
		key := strings.ToLower(field.Name)
		if _, exists := known[key]; exists {
			return nil, fmt.Errorf("row source: ambiguous proven column")
		}
		known[key] = field
	}
	selected := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(name)
		if _, ok := known[key]; !ok || selected[key] {
			return nil, fmt.Errorf("row source: input is not a unique proven column")
		}
		selected[key] = true
	}
	// Preserve compiler order rather than introducing config-dependent row shapes.
	out := make([]resultfilter.Field, 0, len(names))
	for _, field := range fields {
		if selected[strings.ToLower(field.Name)] {
			out = append(out, field)
		}
	}
	return out, nil
}
func minimalInputRows(originals []map[string]any, fields []resultfilter.Field, reg *runtime.Registry, aliases map[string]string) (*interpreter.Array, []originalRow, error) {
	rows := make([]map[string]any, 0, len(originals))
	selected := map[string]bool{}
	for _, field := range fields {
		selected[strings.ToLower(field.Name)] = true
	}
	for _, original := range originals {
		row := map[string]any{}
		for key, value := range original {
			if selected[strings.ToLower(key)] {
				row[key] = value
			}
		}
		rows = append(rows, row)
	}
	return inputRows(rows, fields, reg, aliases)
}
