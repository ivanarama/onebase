package ui

import (
	"context"
	"fmt"
	"html"
	"html/template"

	"github.com/ivantit66/onebase/internal/metadata"
)

type listCellFilterValue struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

func listCellFilters(row map[string]any, cols []metadata.Field) []listCellFilterValue {
	values, _ := row["_ob_cell_values"].(map[string]string)
	out := make([]listCellFilterValue, len(cols))
	for i, f := range cols {
		if v, ok := values[f.Name]; ok {
			out[i] = listCellFilterValue{Field: f.Name, Value: v, Type: string(f.Type)}
		}
	}
	return out
}

// Save visible, unmasked scalar values before references become labels.
func (s *Server) captureListCellFilters(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	decisions := s.fieldDecisions(ctx, entity)
	for _, row := range rows {
		values := map[string]string{}
		for _, field := range resolveListColumns(entity) {
			if d, ok := fieldDecisionByName(decisions, field.Name); ok && d.Masked() {
				continue
			}
			switch field.Type {
			case metadata.FieldTypeString, metadata.FieldTypeNumber, metadata.FieldTypeBool, metadata.FieldTypeDate:
			default:
				if field.RefEntity == "" && field.EnumName == "" {
					continue
				}
			}
			value, exists := row[field.Name]
			if !exists {
				continue
			}
			if value == nil && (field.Type == metadata.FieldTypeNumber || field.Type == metadata.FieldTypeDate) {
				continue
			}
			values[field.Name] = formatFieldValueForInput(field, value)
		}
		row["_ob_cell_values"] = values
	}
}

func listCellFilterAttrs(row map[string]any, field metadata.Field) template.HTMLAttr {
	values, _ := row["_ob_cell_values"].(map[string]string)
	value, ok := values[field.Name]
	if !ok {
		return ""
	}
	// Attribute names are fixed; each metadata/value string is HTML-escaped.
	return template.HTMLAttr(fmt.Sprintf(`data-ob-cell="%s" data-ob-cell-value="%s" data-ob-cell-type="%s"`, html.EscapeString(field.Name), html.EscapeString(value), html.EscapeString(string(field.Type)))) //nolint:gosec // G203: fixed attribute names and HTML-escaped values.
}
