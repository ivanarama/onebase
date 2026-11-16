package report

import (
	"fmt"
	"strings"
)

// MeasureCalculation replaces the base Agg/Expr only at this row level.
// GroupBy partitions the original rows; InnerAgg is evaluated independently
// on each partition, then Agg combines those values. It never reuses displayed
// child resources, which may have different level-specific formulas.
// Expr and GroupBy are mutually exclusive. No rule means legacy calculation.
// The rules are trusted report metadata, never user/preset expressions.
type MeasureCalculation struct {
	Level    string `yaml:"level"`
	Agg      string `yaml:"agg,omitempty"`
	Expr     string `yaml:"expr,omitempty"`
	GroupBy  string `yaml:"group_by,omitempty"`
	InnerAgg string `yaml:"inner_agg,omitempty"`
}

func (m Measure) Calculation(level string) MeasureCalculation {
	for _, c := range m.Calculations {
		if strings.EqualFold(c.Level, level) {
			return c
		}
	}
	return MeasureCalculation{Agg: m.Agg, Expr: m.Expr}
}

// AtLevel keeps the resource identity and trusted rules while choosing the
// effective expression. Call only after InScope so excluded Expr never runs.
func (m Measure) AtLevel(level string) Measure {
	c := m.Calculation(level)
	m.Agg, m.Expr = c.Agg, c.Expr
	return m
}

// CalculationErrors validates rule shape. A runtime preset may remove a named
// grouping, so reference validation belongs to trusted configuration checks.
func (m Measure) CalculationErrors() []string {
	if len(m.Calculations) == 0 {
		return nil
	}
	var out []string
	add := func(msg string) { out = append(out, fmt.Sprintf("показатель %q: %s", m.Field, msg)) }
	aggs := map[string]bool{"": true, "sum": true, "avg": true, "count": true, "min": true, "max": true}
	if len(m.Calculations) > 32 {
		add("слишком много формул уровней (не более 32)")
	}
	seen := map[string]bool{}
	for _, c := range m.Calculations {
		key := strings.ToLower(c.Level)
		if key == "" || key == MeasureScopeDetail {
			add("формула требует именованный уровень группировки или $grand")
		}
		if seen[key] {
			add("повторная формула уровня " + c.Level)
		}
		seen[key] = true
		if !aggs[c.Agg] || !aggs[c.InnerAgg] {
			add("неизвестный агрегат формулы уровня " + c.Level)
		}
		if c.GroupBy != "" {
			if c.InnerAgg == "" || c.Expr != "" {
				add("group_by требует inner_agg без expr для уровня " + c.Level)
			}
		} else if c.InnerAgg != "" {
			add("inner_agg требует group_by для уровня " + c.Level)
		}
	}
	return out
}

func (c Composition) MeasureCalculationErrors() []string {
	levels := map[string]bool{MeasureScopeGrand: true}
	groups := map[string]bool{}
	for _, g := range c.Groupings {
		groups[strings.ToLower(g)] = true
		levels[strings.ToLower(g)] = true
	}
	var out []string
	for _, m := range c.Measures {
		out = append(out, m.CalculationErrors()...)
		for _, rule := range m.Calculations {
			if !levels[strings.ToLower(rule.Level)] {
				out = append(out, fmt.Sprintf("показатель %q: неизвестный уровень формулы %q", m.Field, rule.Level))
			}
			if rule.GroupBy != "" && !groups[strings.ToLower(rule.GroupBy)] {
				out = append(out, fmt.Sprintf("показатель %q: group_by не входит в группировки: %s", m.Field, rule.GroupBy))
			}
		}
	}
	return out
}
