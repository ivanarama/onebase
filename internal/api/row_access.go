package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

var errCreateRowAccessDenied = errors.New("create row access denied")

func (h *handler) rowDecision(ctx context.Context, entity *metadata.Entity, op string) (access.Decision, error) {
	if entity == nil {
		return access.Decision{}, nil
	}
	return access.DecideWithLookup(auth.UserFromContext(ctx), string(entity.Kind), entity.Name, op, entity, h.reg)
}

func (h *handler) applyRowFilter(ctx context.Context, entity *metadata.Entity, op string, params storage.ListParams) (storage.ListParams, error) {
	dec, err := h.rowDecision(ctx, entity, op)
	if err != nil {
		return params, err
	}
	if !dec.Allowed {
		return params, fmt.Errorf("forbidden")
	}
	if !dec.Unrestricted {
		params.RowFilter = dec.Predicate
	}
	params.RowFilterEvaluated = true // план 79F: строковый доступ вычислен
	return params, nil
}

func (h *handler) rowAllowed(ctx context.Context, entity *metadata.Entity, op string, row map[string]any) bool {
	dec, err := h.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		return false
	}
	return dec.Unrestricted || h.matchRowPredicate(ctx, row, dec.Predicate)
}

func (h *handler) rowAllowedID(ctx context.Context, entity *metadata.Entity, op string, id uuid.UUID) bool {
	dec, err := h.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		return false
	}
	if dec.Unrestricted {
		return true
	}
	row, err := h.store.GetByID(ctx, entity.Name, id, entity)
	return err == nil && h.matchRowPredicate(ctx, row, dec.Predicate)
}

// rowAllowsOwnerID проверяет доступ к строке-владельцу вложения (защита от IDOR
// в REST attachments, issue #315). Если сущность-владелец есть в реестре —
// полная RLS-проверка через rowAllowedID; иначе (метаданных нет) — откат к RBAC
// canREST, как в UI-пути (ui.Server.rowAllowsOwnerID).
func (h *handler) rowAllowsOwnerID(ctx context.Context, ownerKind, ownerName, op string, id uuid.UUID) bool {
	entity := h.reg.GetEntity(ownerName)
	if entity == nil || !strings.EqualFold(string(entity.Kind), ownerKind) {
		return canREST(ctx, ownerKind, ownerName, op)
	}
	return h.rowAllowedID(ctx, entity, op, id)
}

func (h *handler) rowAllowedUpdate(ctx context.Context, entity *metadata.Entity, op string, id uuid.UUID, fields map[string]any) bool {
	dec, err := h.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		return false
	}
	if dec.Unrestricted {
		return true
	}
	row, err := h.store.GetByID(ctx, entity.Name, id, entity)
	if err != nil || !h.matchRowPredicate(ctx, row, dec.Predicate) {
		return false
	}
	return h.matchRowPredicate(ctx, storage.MergeRowFields(row, fields), dec.Predicate)
}

func (h *handler) compileQueryWithRowAccess(ctx context.Context, text string, params map[string]any) (query.Result, error) {
	rowFilters, err := access.QueryRowFiltersWithLookup(
		auth.UserFromContext(ctx),
		h.reg.Entities(),
		h.reg.Registers(),
		h.reg.InfoRegisters(),
		h.reg.AccountRegisters(),
		h.reg,
	)
	if err != nil {
		return query.Result{}, err
	}
	return query.Compile(text, query.CompileOpts{
		Entities:    h.reg.Entities(),
		Params:      params,
		Registers:   h.reg.Registers(),
		InfoRegs:    h.reg.InfoRegisters(),
		AccountRegs: h.reg.AccountRegisters(),
		RowFilters:  rowFilters,
		Dialect:     h.store.Dialect(),
	})
}

func (h *handler) deniedQuerySource(ctx context.Context, sources []query.SourceRef) string {
	return access.DeniedReadSource(auth.UserFromContext(ctx), sources)
}

func (h *handler) matchRowPredicate(ctx context.Context, row map[string]any, p *storage.Predicate) bool {
	return storage.MatchPredicateWithRefs(row, p, func(entity *metadata.Entity, id uuid.UUID) (map[string]any, bool) {
		if entity == nil {
			return nil, false
		}
		refRow, err := h.store.GetByID(ctx, entity.Name, id, entity)
		return refRow, err == nil
	})
}

func (h *handler) autoFillRowAccessFields(ctx context.Context, entity *metadata.Entity, op string, fields map[string]any) error {
	dec, err := h.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed || dec.Unrestricted {
		return err
	}
	access.AutoFillPredicateFields(dec.Predicate, fields, entity)
	return nil
}

func (h *handler) createRowAccessPreflight(entity *metadata.Entity, action string) func(context.Context, *runtime.Object) error {
	return func(ctx context.Context, obj *runtime.Object) error {
		if err := h.autoFillRowAccessFields(ctx, entity, "write", obj.Fields); err != nil {
			return fmt.Errorf("%w: %v", errCreateRowAccessDenied, err)
		}
		posting := entity.Kind == metadata.KindDocument && isPostAction(action)
		if posting {
			if err := h.autoFillRowAccessFields(ctx, entity, "post", obj.Fields); err != nil {
				return fmt.Errorf("%w: %v", errCreateRowAccessDenied, err)
			}
		}
		if !h.rowAllowed(ctx, entity, "write", obj.Fields) {
			return errCreateRowAccessDenied
		}
		if posting && !h.rowAllowed(ctx, entity, "post", obj.Fields) {
			return errCreateRowAccessDenied
		}
		return nil
	}
}
