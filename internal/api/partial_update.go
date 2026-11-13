package api

import (
	"context"
	"errors"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

var errPartialUpdateDenied = errors.New("partial update denied")

// Keep given-key access semantics while using one authoritative snapshot for
// row predicates, protected-field restoration, the hook header and final CAS.
func (h *handler) preparePartialUpdate(entity *metadata.Entity, action string) func(context.Context, map[string]any, map[string]any) error {
	return func(ctx context.Context, stored, fields map[string]any) error {
		ops := []string{"write"}
		if entity.Kind == metadata.KindDocument && isPostAction(action) {
			ops = append(ops, "post")
		}
		for _, op := range ops {
			decision, err := h.rowDecision(ctx, entity, op)
			if err != nil || !decision.Allowed {
				return errPartialUpdateDenied
			}
			if !decision.Unrestricted && (!h.matchRowPredicate(ctx, stored, decision.Predicate) || !h.matchRowPredicate(ctx, storage.MergeRowFields(stored, fields), decision.Predicate)) {
				return errPartialUpdateDenied
			}
		}
		h.protectMaskedFieldsFromRow(ctx, entity, stored, fields)
		return nil
	}
}
