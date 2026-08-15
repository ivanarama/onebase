package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

var (
	errSubmitRowAccessDenied = errors.New("submit row access denied")
	errSubmitFormHook        = errors.New("submit form hook failed")
	errCreateRowAccessDenied = errors.New("create row access denied")
)

func (s *Server) rowDecision(ctx context.Context, entity *metadata.Entity, op string) (access.Decision, error) {
	if entity == nil {
		return access.Decision{}, nil
	}
	return s.rowDecisionFor(ctx, string(entity.Kind), entity.Name, op, entity)
}

func (s *Server) rowDecisionFor(ctx context.Context, kind, name, op string, meta *metadata.Entity) (access.Decision, error) {
	return access.DecideWithLookup(auth.UserFromContext(ctx), kind, name, op, meta, s.reg)
}

func (s *Server) applyRowFilter(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, op string, params storage.ListParams) (storage.ListParams, bool) {
	dec, err := s.rowDecision(r.Context(), entity, op)
	if err != nil || !dec.Allowed {
		s.renderForbidden(w, r)
		return params, false
	}
	if !dec.Unrestricted {
		params.RowFilter = dec.Predicate
	}
	params.RowFilterEvaluated = true // план 79F: строковый доступ вычислен
	return params, true
}

func (s *Server) rowAllowed(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, op string, row map[string]any) bool {
	dec, err := s.rowDecision(r.Context(), entity, op)
	return s.renderRowDecision(w, r, dec, err, row)
}

func (s *Server) rowAllowedContext(ctx context.Context, entity *metadata.Entity, op string, row map[string]any) bool {
	dec, err := s.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		return false
	}
	return dec.Unrestricted || s.matchRowPredicate(ctx, row, dec.Predicate)
}

func (s *Server) rowAllowedFor(w http.ResponseWriter, r *http.Request, kind, name, op string, meta *metadata.Entity, row map[string]any) bool {
	dec, err := s.rowDecisionFor(r.Context(), kind, name, op, meta)
	return s.renderRowDecision(w, r, dec, err, row)
}

func (s *Server) renderRowDecision(w http.ResponseWriter, r *http.Request, dec access.Decision, err error, row map[string]any) bool {
	if err != nil || !dec.Allowed {
		s.renderForbidden(w, r)
		return false
	}
	if dec.Unrestricted || s.matchRowPredicate(r.Context(), row, dec.Predicate) {
		return true
	}
	s.renderForbidden(w, r)
	return false
}

func (s *Server) applyRegRowFilter(w http.ResponseWriter, r *http.Request, kind, name, op string, meta *metadata.Entity, params storage.RegFilter) (storage.RegFilter, bool) {
	dec, err := s.rowDecisionFor(r.Context(), kind, name, op, meta)
	if err != nil || !dec.Allowed {
		s.renderForbidden(w, r)
		return params, false
	}
	if !dec.Unrestricted {
		params.RowFilter = dec.Predicate
	}
	return params, true
}

func (s *Server) rowAllowedID(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, op string, id uuid.UUID) bool {
	dec, err := s.rowDecision(r.Context(), entity, op)
	if err != nil || !dec.Allowed {
		s.renderForbidden(w, r)
		return false
	}
	if dec.Unrestricted {
		return true
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil || !s.matchRowPredicate(r.Context(), row, dec.Predicate) {
		s.renderForbidden(w, r)
		return false
	}
	return true
}

func (s *Server) rowAllowedUpdate(w http.ResponseWriter, r *http.Request, entity *metadata.Entity, op string, id uuid.UUID, fields map[string]any) bool {
	dec, err := s.rowDecision(r.Context(), entity, op)
	if err != nil || !dec.Allowed {
		s.renderForbidden(w, r)
		return false
	}
	if dec.Unrestricted {
		return true
	}
	row, err := s.store.GetByID(r.Context(), entity.Name, id, entity)
	if err != nil || !s.matchRowPredicate(r.Context(), row, dec.Predicate) {
		s.renderForbidden(w, r)
		return false
	}
	if !s.matchRowPredicate(r.Context(), storage.MergeRowFields(row, fields), dec.Predicate) {
		s.renderForbidden(w, r)
		return false
	}
	return true
}

func (s *Server) rowAllowsOwnerID(r *http.Request, ownerKind, ownerName, op string, id uuid.UUID) bool {
	entity := s.ownerEntity(ownerKind, ownerName)
	if entity == nil {
		return s.can(r, ownerKind, ownerName, op)
	}
	return s.rowAllowsID(r.Context(), entity, op, id)
}

func (s *Server) requireOwnerRow(w http.ResponseWriter, r *http.Request, ownerKind, ownerName, op string, id uuid.UUID) bool {
	if s.rowAllowsOwnerID(r, ownerKind, ownerName, op, id) {
		return true
	}
	s.renderForbidden(w, r)
	return false
}

func (s *Server) ownerEntity(ownerKind, ownerName string) *metadata.Entity {
	if s == nil || s.reg == nil {
		return nil
	}
	entity := s.reg.GetEntity(ownerName)
	if entity == nil {
		return nil
	}
	if ownerKind != "" && !strings.EqualFold(string(entity.Kind), ownerKind) {
		return nil
	}
	return entity
}

func (s *Server) rowAllowsID(ctx context.Context, entity *metadata.Entity, op string, id uuid.UUID) bool {
	dec, err := s.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		return false
	}
	if dec.Unrestricted {
		return true
	}
	row, err := s.store.GetByID(ctx, entity.Name, id, entity)
	return err == nil && s.matchRowPredicate(ctx, row, dec.Predicate)
}

func (s *Server) rowAccessRestricted(ctx context.Context, entity *metadata.Entity, op string) bool {
	if entity == nil {
		return false
	}
	return access.HasRestrictedPolicy(auth.UserFromContext(ctx), string(entity.Kind), entity.Name, op)
}

func (s *Server) rowAllowsSelected(ctx context.Context, entity *metadata.Entity, row map[string]any) bool {
	dec, err := s.rowDecision(ctx, entity, "read")
	if err != nil || !dec.Allowed {
		return false
	}
	return dec.Unrestricted || s.matchRowPredicate(ctx, row, dec.Predicate)
}

func (s *Server) rowFilterFor(ctx context.Context, entity *metadata.Entity, op string, params storage.ListParams) (storage.ListParams, error) {
	dec, err := s.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed {
		if err != nil {
			return params, err
		}
		return params, fmt.Errorf("forbidden")
	}
	if !dec.Unrestricted {
		params.RowFilter = dec.Predicate
	}
	params.RowFilterEvaluated = true // план 79F: строковый доступ вычислен
	return params, nil
}

func (s *Server) queryRowFilters(ctx context.Context) (map[query.SourceRef]*storage.Predicate, error) {
	return access.QueryRowFiltersWithLookup(
		auth.UserFromContext(ctx),
		s.reg.Entities(),
		s.reg.Registers(),
		s.reg.InfoRegisters(),
		s.reg.AccountRegisters(),
		s.reg,
	)
}

func (s *Server) compileQueryWithRowAccess(ctx context.Context, text string, params map[string]any) (query.Result, error) {
	rowFilters, err := s.queryRowFilters(ctx)
	if err != nil {
		return query.Result{}, err
	}
	return query.Compile(text, query.CompileOpts{
		Entities:    s.reg.Entities(),
		Params:      params,
		Registers:   s.reg.Registers(),
		InfoRegs:    s.reg.InfoRegisters(),
		AccountRegs: s.reg.AccountRegisters(),
		RowFilters:  rowFilters,
		Dialect:     s.store.Dialect(),
	})
}

func (s *Server) compileDSLQueryWithRowAccess(ctx context.Context, text string, params map[string]any) (query.Result, error) {
	if isTrustedDSLContext(ctx) {
		return query.Compile(text, query.CompileOpts{
			Entities:    s.reg.Entities(),
			Params:      params,
			Registers:   s.reg.Registers(),
			InfoRegs:    s.reg.InfoRegisters(),
			AccountRegs: s.reg.AccountRegisters(),
			Dialect:     s.store.Dialect(),
		})
	}
	res, err := s.compileQueryWithRowAccess(ctx, text, params)
	if err != nil {
		return query.Result{}, err
	}
	if denied := s.deniedQuerySource(ctx, res.Sources); denied != "" {
		return query.Result{}, fmt.Errorf("нет доступа к объекту: %s", denied)
	}
	return res, nil
}

func (s *Server) deniedQuerySource(ctx context.Context, sources []query.SourceRef) string {
	return access.DeniedReadSource(auth.UserFromContext(ctx), sources)
}

func (s *Server) matchRowPredicate(ctx context.Context, row map[string]any, p *storage.Predicate) bool {
	return storage.MatchPredicateWithRefs(row, p, func(entity *metadata.Entity, id uuid.UUID) (map[string]any, bool) {
		if entity == nil {
			return nil, false
		}
		refRow, err := s.store.GetByID(ctx, entity.Name, id, entity)
		return refRow, err == nil
	})
}

func (s *Server) autoFillRowAccessFields(ctx context.Context, entity *metadata.Entity, op string, fields map[string]any) error {
	dec, err := s.rowDecision(ctx, entity, op)
	if err != nil || !dec.Allowed || dec.Unrestricted {
		return err
	}
	access.AutoFillPredicateFields(dec.Predicate, fields, entity)
	return nil
}
