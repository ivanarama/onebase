package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

func (s *Server) reportOutputMaskPlan(ctx context.Context, compiled query.Result) access.QueryMaskPlan {
	return access.ReportOutputQueryMaskPlan(auth.UserFromContext(ctx), compiled, s.sourceMeta)
}

// Stored labels are arbitrary application strings. A protected reference uses
// a technical title built without reading any personal field or presentation.
func (s *Server) reportOutputRecordLabel(ctx context.Context, entity *metadata.Entity, row map[string]any) string {
	if len(access.ReportOutputDecisions(entity)) > 0 {
		if id := row["id"]; id != nil {
			return fmt.Sprint(id)
		}
		return ""
	}
	return s.maskedRecordLabel(ctx, entity, row)
}

// A UUID string is not evidence of its entity. Resolve only compiler-proven
// reference columns, never an arbitrary matching row in another catalog.
func (s *Server) resolveTypedReportReferences(ctx context.Context, rows []map[string]any, skipCol string, refs map[string]string) {
	for column, target := range refs {
		if strings.EqualFold(column, skipCol) {
			continue
		}
		entity := s.reg.GetEntity(target)
		if entity == nil || !s.canCtx(ctx, string(entity.Kind), entity.Name, "read") {
			continue
		}
		for _, row := range rows {
			for key, value := range row {
				if !strings.EqualFold(key, column) {
					continue
				}
				id, err := uuid.Parse(fmt.Sprint(value))
				if err != nil || id == uuid.Nil {
					continue
				}
				record, err := s.store.GetByID(ctx, entity.Name, id, entity)
				if err == nil && s.rowAllowedContext(ctx, entity, "read", record) {
					row[key] = s.reportOutputRecordLabel(ctx, entity, record)
				}
			}
		}
	}
}
