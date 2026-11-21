package widget

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CellFilter compares a visible output column to its original scalar value.
// Comparison follows the query's authorization/masking and precedes widget.limit.
// It never modifies SQL or the configured query's own TOP/LIMIT semantics.
type CellFilter struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

const CellValuesKey = "_ob_widget_cell_values"
const maskedCellsKey = "_ob_widget_masked_cells"

var ErrCellFilter = errors.New("unsupported widget cell filter")

// CellValue is a typed, lossless token, rather than a formatted caption.
func CellValue(value any) (string, bool) {
	switch value.(type) {
	case nil, string, bool, int, int32, int64, uint, uint32, uint64,
		float32, float64, decimal.Decimal, uuid.UUID, time.Time:
	default:
		return "", false
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err == nil && len(encoded) <= 8192
}

func captureCellValues(rows []map[string]any, columns []ColumnSpec) {
	for _, row := range rows {
		values := map[string]string{}
		masked, _ := row[maskedCellsKey].(map[string]struct{})
		for _, column := range columns {
			if _, protected := masked[column.Field]; protected {
				continue
			}
			if value, exists := row[column.Field]; exists {
				if token, ok := CellValue(value); ok {
					values[column.Field] = token
				}
			}
		}
		row[CellValuesKey] = values
		delete(row, maskedCellsKey)
	}
}

func filterCells(rows []map[string]any, columns []ColumnSpec, filter *CellFilter) ([]map[string]any, error) {
	if filter == nil {
		return rows, nil
	}
	field := ""
	matches := 0
	for _, column := range columns {
		if strings.EqualFold(column.Field, filter.Field) {
			field = column.Field
			matches++
		}
	}
	if matches != 1 {
		return nil, ErrCellFilter
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		values, _ := row[CellValuesKey].(map[string]string)
		value, allowed := values[field]
		if !allowed {
			return nil, fmt.Errorf("%w: unavailable column", ErrCellFilter)
		}
		if value == filter.Value {
			out = append(out, row)
		}
	}
	return out, nil
}
