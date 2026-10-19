package access

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// QueryRowFilters returns read row-level predicates keyed by query source.
// Sources without read permission are left to the caller's object-RBAC checks;
// this helper only supplies predicates for granted-but-restricted sources.
func QueryRowFilters(
	u *auth.User,
	entities []*metadata.Entity,
	registers []*metadata.Register,
	infoRegs []*metadata.InfoRegister,
	accountRegs []*metadata.AccountRegister,
) (map[query.SourceRef]*storage.Predicate, error) {
	return QueryRowFiltersWithLookup(u, entities, registers, infoRegs, accountRegs, entitySliceLookup(entities))
}

func QueryRowFiltersWithLookup(
	u *auth.User,
	entities []*metadata.Entity,
	registers []*metadata.Register,
	infoRegs []*metadata.InfoRegister,
	accountRegs []*metadata.AccountRegister,
	lookup EntityLookup,
) (map[query.SourceRef]*storage.Predicate, error) {
	return QueryRowFiltersWithContext(context.Background(), u, entities, registers, infoRegs, accountRegs, DecisionContext{Lookup: lookup})
}

func QueryRowFiltersWithContext(ctx context.Context, u *auth.User, entities []*metadata.Entity, registers []*metadata.Register, infoRegs []*metadata.InfoRegister, accountRegs []*metadata.AccountRegister, dc DecisionContext) (map[query.SourceRef]*storage.Predicate, error) {
	if u == nil || u.IsAdmin {
		return nil, nil
	}
	out := map[query.SourceRef]*storage.Predicate{}
	add := func(kind, name string, meta *metadata.Entity) error {
		dec, err := DecideWithContext(ctx, u, kind, name, "read", meta, dc)
		if err != nil {
			return err
		}
		if dec.Allowed && !dec.Unrestricted {
			out[query.SourceRef{Kind: kind, Name: name}] = dec.Predicate
		}
		return nil
	}
	for _, e := range entities {
		if e != nil {
			if err := add(string(e.Kind), e.Name, e); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range registers {
		if r != nil {
			if err := add("register", r.Name, storage.RegisterPredicateEntity(r)); err != nil {
				return nil, err
			}
		}
	}
	for _, ir := range infoRegs {
		if ir != nil {
			if err := add("inforeg", ir.Name, storage.InfoRegisterPredicateEntity(ir)); err != nil {
				return nil, err
			}
		}
	}
	for _, ar := range accountRegs {
		if ar != nil {
			if err := add("register", ar.Name, storage.AccountRegisterPredicateEntity(ar)); err != nil {
				return nil, err
			}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

type entitySliceLookup []*metadata.Entity

func (l entitySliceLookup) GetEntity(name string) *metadata.Entity {
	for _, e := range l {
		if e != nil && strings.EqualFold(e.Name, name) {
			return e
		}
	}
	return nil
}

// DeniedReadSource returns the first query source the user cannot read.
// A nil user means auth is disabled/open deployment; admins pass via User.Has.
func DeniedReadSource(u *auth.User, sources []query.SourceRef) string {
	if u == nil || u.IsAdmin {
		return ""
	}
	for _, src := range sources {
		if !u.Has(src.Kind, src.Name, "read") {
			return src.Name
		}
	}
	return ""
}
