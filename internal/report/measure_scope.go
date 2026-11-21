package report

import (
	"fmt"
	"strings"
)

const (
	MeasureScopeGrand  = "$grand"
	MeasureScopeDetail = "$detail"
)

// InScope selects where a resource is evaluated and emitted. Group names use
// the same case-insensitive comparison as DSL fields. It never changes input rows.
func (m Measure) InScope(level string) bool {
	if len(m.Scope) == 0 {
		return true
	}
	for _, allowed := range m.Scope {
		if strings.EqualFold(allowed, level) {
			return true
		}
	}
	return false
}

// MeasureScopeErrors validates trusted composition metadata. Runtime regrouping
// may omit a named level; that level simply emits no resource in that view.
func (c Composition) MeasureScopeErrors() []string {
	levels := map[string]bool{MeasureScopeGrand: true, MeasureScopeDetail: true}
	for _, field := range c.Groupings {
		levels[strings.ToLower(field)] = true
	}
	var out []string
	for _, m := range c.Measures {
		seen := map[string]bool{}
		for _, level := range m.Scope {
			key := strings.ToLower(level)
			if !levels[key] {
				out = append(out, fmt.Sprintf("показатель %q: неизвестная область ресурса %q", m.Field, level))
			} else if key == MeasureScopeDetail && len(c.Columns) > 0 {
				out = append(out, fmt.Sprintf("показатель %q: область $detail несовместима с кросс-таблицей", m.Field))
			}
			if seen[key] {
				out = append(out, fmt.Sprintf("показатель %q: повторная область ресурса %q", m.Field, level))
			}
			seen[key] = true
		}
	}
	return out
}
