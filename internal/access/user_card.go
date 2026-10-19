package access

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// UserCardResolver is an infrastructure reader, never a user-visible List.
type UserCardResolver interface {
	ReadUserCardSource(context.Context, storage.UserCardDescriptor, string) (storage.UserCardValue, error)
}

type rowSourceError struct{ cause error }

func (e rowSourceError) Error() string { return "row access source is temporarily unavailable" }
func (e rowSourceError) Unwrap() error { return e.cause }

type DecisionContext struct {
	Lookup   EntityLookup
	Resolver UserCardResolver
}

// RuntimeContext also supports storage ports without the optional reader;
// user_card then fails closed, while static policies continue to work.
func RuntimeContext(lookup EntityLookup, store any) DecisionContext {
	resolver, _ := store.(UserCardResolver)
	return DecisionContext{Lookup: lookup, Resolver: resolver}
}

func userCardDescriptor(src *auth.UserCardSource, target *metadata.Entity, field string, lookup EntityLookup, op string) (storage.UserCardDescriptor, error) {
	fail := func(msg string) (storage.UserCardDescriptor, error) {
		return storage.UserCardDescriptor{}, fmt.Errorf("user_card: %s", msg)
	}
	if lookup == nil {
		return fail("requires metadata lookup")
	}
	if src.TablePart != "" || src.Column != "" {
		return fail("table-part sources are not supported in this release")
	}
	if src.Catalog == "" || src.Field == "" || strings.Contains(src.Field, ".") {
		return fail("catalog and ordinary scalar field are required")
	}
	if op != "eq" && op != "ne" && op != "" {
		return fail("scalar sources require eq or ne")
	}
	catalog := lookup.GetEntity(src.Catalog)
	if catalog == nil || catalog.Kind != metadata.KindCatalog {
		return fail("unknown catalog")
	}
	var account metadata.Field
	if src.Shorthand {
		count := 0
		for _, f := range catalog.Fields {
			if strings.EqualFold(f.RefEntity, "_users") {
				account = f
				count++
			}
		}
		if count != 1 {
			return fail("shorthand requires exactly one reference:_users field; use the full structure")
		}
	} else {
		f, ok := concreteField(catalog, src.AccountField)
		if !ok || !strings.EqualFold(f.RefEntity, "_users") {
			return fail("account_field must be reference:_users")
		}
		account = f
	}
	value, ok := concreteField(catalog, src.Field)
	if !ok {
		return fail("unknown ordinary source field")
	}
	// Dot navigation on the target is resolved independently of the source.
	if strings.Contains(field, ".") {
		parts := strings.Split(field, ".")
		if len(parts) != 2 {
			return fail("invalid target field")
		}
		ref, ok := referenceField(target, parts[0])
		if !ok {
			return fail("unknown target reference")
		}
		target = lookup.GetEntity(ref.RefEntity)
		field = parts[1]
	}
	if target == nil {
		return fail("unknown target")
	}
	tf, ok := concreteField(target, field)
	if !ok {
		return fail("target must be an ordinary field")
	}
	if !strings.EqualFold(string(tf.Type), string(value.Type)) || !strings.EqualFold(tf.RefEntity, value.RefEntity) {
		return fail("source and target field types must match")
	}
	switch {
	case value.RefEntity != "", metadata.IsEnum(value.Type):
	case value.Type == metadata.FieldTypeString, value.Type == metadata.FieldTypeNumber, value.Type == metadata.FieldTypeBool, value.Type == metadata.FieldTypeDate:
	default:
		return fail("unsupported scalar type")
	}
	return storage.UserCardDescriptor{Catalog: catalog, AccountField: account, Field: value}, nil
}

// prepareCardPolicy walks every branch before boolean evaluation. Unavailable
// reaches the root of this role, including through NOT and permissive ANY.
func prepareCardPolicy(ctx context.Context, p auth.RowPolicy, u *auth.User, meta *metadata.Entity, dc DecisionContext, validate bool) (auth.RowPolicy, bool, error) {
	unavailable := false
	walk := func(in auth.RowPolicy) (auth.RowPolicy, error) {
		out, missing, err := prepareCardPolicy(ctx, in, u, meta, dc, validate)
		unavailable = unavailable || missing
		return out, err
	}
	if len(p.All) > 0 {
		p.All = append([]auth.RowPolicy(nil), p.All...)
		for i := range p.All {
			var err error
			p.All[i], err = walk(p.All[i])
			if err != nil {
				return p, false, err
			}
		}
	} else if len(p.Any) > 0 {
		p.Any = append([]auth.RowPolicy(nil), p.Any...)
		for i := range p.Any {
			var err error
			p.Any[i], err = walk(p.Any[i])
			if err != nil {
				return p, false, err
			}
		}
	} else if p.Not != nil {
		n, err := walk(*p.Not)
		if err != nil {
			return p, false, err
		}
		p.Not = &n
	} else if p.Value.UserCard != nil {
		if p.Value.User != "" || p.Value.UserAttr != "" || p.Value.List != nil || p.Value.LiteralSet || p.Value.Literal != nil {
			return p, false, fmt.Errorf("user_card conflicts with another value source")
		}
		desc, err := userCardDescriptor(p.Value.UserCard, meta, p.Field, dc.Lookup, p.Op)
		if err != nil {
			return p, false, err
		}
		var result storage.UserCardValue
		if validate {
			result = storage.UserCardValue{Available: true, Value: "_lint_card_value"}
			switch desc.Field.Type {
			case metadata.FieldTypeNumber:
				result.Value = 0
			case metadata.FieldTypeBool:
				result.Value = false
			case metadata.FieldTypeDate:
				result.Value = time.Unix(0, 0)
			}
			if desc.Field.RefEntity != "" {
				result.Value = uuid.MustParse("00000000-0000-0000-0000-000000000001")
			}
		} else {
			if dc.Resolver == nil {
				return p, false, fmt.Errorf("user_card requires a runtime resolver")
			}
			result, err = dc.Resolver.ReadUserCardSource(ctx, desc, u.ID)
			if err != nil {
				slog.Error("row access source read failed", "error", err)
				return p, false, rowSourceError{cause: err}
			}
		}
		unavailable = !result.Available
		p.Value = auth.RowValue{Literal: result.Value, LiteralSet: true}
	}
	return p, unavailable, nil
}

// CanonicalRowAccess returns a detached copy with full, canonically spelled
// source descriptors for documentation/editors. It never reads card data.
func CanonicalRowAccess(in auth.RowAccess, lookup EntityLookup) (auth.RowAccess, error) {
	var canonical func(auth.RowPolicy) (auth.RowPolicy, error)
	canonical = func(p auth.RowPolicy) (auth.RowPolicy, error) {
		if p.Value.UserCard != nil {
			src := p.Value.UserCard
			if lookup == nil {
				return p, fmt.Errorf("user_card requires metadata lookup")
			}
			catalog := lookup.GetEntity(src.Catalog)
			desc, err := userCardDescriptor(src, catalog, src.Field, lookup, "eq")
			if err != nil {
				return p, err
			}
			p.Value.UserCard = &auth.UserCardSource{Catalog: desc.Catalog.Name, AccountField: desc.AccountField.Name, Field: desc.Field.Name}
		}
		p.All = append([]auth.RowPolicy(nil), p.All...)
		p.Any = append([]auth.RowPolicy(nil), p.Any...)
		for i := range p.All {
			var err error
			p.All[i], err = canonical(p.All[i])
			if err != nil {
				return p, err
			}
		}
		for i := range p.Any {
			var err error
			p.Any[i], err = canonical(p.Any[i])
			if err != nil {
				return p, err
			}
		}
		if p.Not != nil {
			n, err := canonical(*p.Not)
			if err != nil {
				return p, err
			}
			p.Not = &n
		}
		return p, nil
	}
	section := func(in map[string]auth.RowPolicies) (map[string]auth.RowPolicies, error) {
		if in == nil {
			return nil, nil
		}
		out := map[string]auth.RowPolicies{}
		for name, policies := range in {
			ops := auth.RowPolicies{}
			for op, p := range policies {
				n, err := canonical(p)
				if err != nil {
					return nil, err
				}
				ops[op] = n
			}
			out[name] = ops
		}
		return out, nil
	}
	var out auth.RowAccess
	var err error
	if out.Catalogs, err = section(in.Catalogs); err != nil {
		return out, err
	}
	if out.Documents, err = section(in.Documents); err != nil {
		return out, err
	}
	if out.Registers, err = section(in.Registers); err != nil {
		return out, err
	}
	out.InfoRegs, err = section(in.InfoRegs)
	return out, err
}
