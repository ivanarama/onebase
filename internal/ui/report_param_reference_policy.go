package ui

import (
	"context"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// This server-selected boundary also covers the initial GET, before any report
// query runs. Ordinary reference pickers and card contexts are unchanged.
func (s *Server) reportParamReferenceOptions(ctx context.Context, entity *metadata.Entity, selected []string) ([]map[string]any, error) {
	ctx = access.WithReportOutput(ctx)
	params := storage.ListParams{Limit: refPickerDefaultLimit}
	if len(access.ReportOutputDecisions(entity)) != 0 {
		// Hidden labels must not influence membership of a bounded page. A
		// protected target has a technical UUID title, ordered by that same ID.
		params.Sort = "id"
		params.Dir = "asc"
	}
	rows, err := s.referenceOptionsWithParams(ctx, entity, refOptionsChoice, params)
	if err != nil {
		return nil, err
	}
	rows = s.appendSelectedRefOptions(ctx, rows, entity, selected)
	for _, row := range rows {
		row["_label"] = s.reportOutputRecordLabel(ctx, entity, row)
	}
	return rows, nil
}
