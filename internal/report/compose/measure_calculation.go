package compose

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/report"
)

func validateCalculations(spec report.Composition) error {
	for _, m := range spec.Measures {
		if errs := m.CalculationErrors(); len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
	}
	return nil
}

func aggMeasureAtLevel(rows []Row, m report.Measure, level string, wc *warnCollector) any {
	c := m.Calculation(level)
	m.Agg = c.Agg
	if c.GroupBy == "" {
		return aggMeasure(rows, m)
	}
	buckets := map[any][]Row{}
	var keys []any
	for _, row := range rows {
		value, found := row[c.GroupBy]
		if !found {
			for k, v := range row {
				if strings.EqualFold(k, c.GroupBy) {
					value, found = v, true
					break
				}
			}
		}
		if !found {
			wc.add(fmt.Sprintf("показатель «%s»: исходное поле group_by «%s» отсутствует", m.Field, c.GroupBy))
			return nil
		}
		key := normalizeGroupKey(value)
		if _, ok := buckets[key]; !ok {
			keys = append(keys, key)
		}
		buckets[key] = append(buckets[key], row)
	}
	values := make([]Row, 0, len(keys))
	for _, key := range keys {
		values = append(values, Row{m.Field: aggMeasure(buckets[key], report.Measure{Field: m.Field, Agg: c.InnerAgg})})
	}
	return aggMeasure(values, m)
}
