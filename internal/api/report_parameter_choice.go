package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
)

// Validate before executing a report, including the composed JSON response.
// The same catalog activity, deletion and row-access rules apply as in UI choice.
func (h *handler) validateReportChoiceValues(ctx context.Context, rep *reportpkg.Report, values map[string]any) error {
	visible := func(entity *metadata.Entity, value any, predicates []storage.ChoicePredicate) error {
		if value == nil || fmt.Sprint(value) == "" {
			return nil
		}
		id, err := uuid.Parse(fmt.Sprint(value))
		if err != nil || id == uuid.Nil || entity == nil || !canREST(ctx, string(entity.Kind), entity.Name, "read") {
			return fmt.Errorf("report choice is unavailable")
		}
		base := storage.ListParams{ExcludeFolders: true, ChoicePredicates: predicates}
		if entity.Activity != nil && entity.Activity.HideFromChoice {
			base.ActivityScope = metadata.ActivityScopeActive
		}
		params, err := h.applyRowFilter(ctx, entity, "read", base)
		if err != nil {
			return err
		}
		allowed, err := h.store.ListContainsID(ctx, entity.Name, entity, id, params)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("report parameter does not match its choice filter")
		}
		return nil
	}
	for _, p := range rep.Params {
		if len(p.ChoiceFilter) == 0 {
			continue
		}
		target := h.reg.GetEntity(strings.TrimPrefix(p.Type, "reference:"))
		if target == nil {
			return fmt.Errorf("unknown report choice target")
		}
		var predicates []storage.ChoicePredicate
		for _, f := range p.ChoiceFilter {
			var source *metadata.Entity
			for _, field := range target.Fields {
				if strings.EqualFold(field.Name, f.Field) && field.RefEntity != "" {
					source = h.reg.GetEntity(field.RefEntity)
				}
			}
			if source == nil {
				return fmt.Errorf("invalid report choice source")
			}
			value := values[f.Param]
			if value == nil || fmt.Sprint(value) == "" {
				continue
			}
			if err := visible(source, value, nil); err != nil {
				return err
			}
			id, _ := uuid.Parse(fmt.Sprint(value))
			predicates = append(predicates, storage.ChoicePredicate{Field: f.Field, Op: metadata.FormChoiceOpEqual, Value: id})
		}
		if err := visible(target, values[p.Name], predicates); err != nil {
			return err
		}
	}
	return nil
}
