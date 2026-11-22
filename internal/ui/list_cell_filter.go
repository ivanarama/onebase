package ui

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

type listCellFilterValue struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func listCellFilters(row map[string]any, cols []metadata.Field) []listCellFilterValue {
	values, _ := row["_ob_cell_values"].(map[string]string)
	out := make([]listCellFilterValue, len(cols))
	for i, f := range cols {
		if v, ok := values[f.Name]; ok {
			out[i] = listCellFilterValue{Field: f.Name, Value: v}
		}
	}
	return out
}

// Exact cell filters select rows by the stored value. Apply the same field
// policy as the cell attributes before the list or export reaches storage.
func (s *Server) allowExactListFilters(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, filters map[string]storage.FilterValue) bool {
	if len(filters) == 0 {
		return true
	}
	decisions := s.fieldDecisions(r.Context(), entity)
	for field, filter := range filters {
		if decision, ok := fieldDecisionByName(decisions, field); filter.Exact && ok && decision.Masked() {
			s.renderForbidden(w, r)
			return false
		}
	}
	return true
}

// Save visible, unmasked scalar values before references become labels.
func (s *Server) captureListCellFilters(ctx context.Context, entity *metadata.Entity, rows []map[string]any) {
	decisions := s.fieldDecisions(ctx, entity)
	columns := resolveListColumns(entity)
	for _, row := range rows {
		values := map[string]string{}
		for _, field := range columns {
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
			formatted := formatFieldValueForInput(field, value)
			if field.Type == metadata.FieldTypeDate {
				formatted, _, _ = strings.Cut(formatted, "T")
				if _, err := time.Parse(time.DateOnly, formatted); err != nil {
					continue
				}
			}
			values[field.Name] = formatted
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
	return template.HTMLAttr(fmt.Sprintf(`data-ob-cell="%s" data-ob-cell-value="%s"`, html.EscapeString(field.Name), html.EscapeString(value))) //nolint:gosec // G203: fixed attribute names and HTML-escaped values.
}
