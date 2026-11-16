package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
	"gopkg.in/yaml.v3"
)

// ParamContext selects exactly one server-owned scalar. user_card uses the
// unique, non-deleted catalog card linked by reference:_users to the actor ID.
type ParamContext struct {
	User     string               `yaml:"user,omitempty"`
	UserCard *auth.UserCardSource `yaml:"user_card,omitempty"`
}

func (p *Param) UnmarshalYAML(n *yaml.Node) error {
	type plain Param
	if err := n.Decode((*plain)(p)); err != nil {
		return err
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "context" && p.Context == nil {
			return fmt.Errorf("report context cannot be null")
		}
	}
	return nil
}

func (c *ParamContext) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return fmt.Errorf("report context requires exactly one user or user_card source")
	}
	if key := n.Content[0].Value; key != "user" && key != "user_card" {
		return fmt.Errorf("unknown report context source %q", key)
	}
	type plain ParamContext
	if err := n.Decode((*plain)(c)); err != nil {
		return err
	}
	if c.User == "" && c.UserCard == nil {
		return fmt.Errorf("empty report context source")
	}
	return nil
}

var ErrContextUnavailable = errors.New("report context is unavailable for the current user")

func contextDescriptor(p Param, lookup access.EntityLookup) (storage.UserCardDescriptor, error) {
	c := p.Context
	if c == nil {
		return storage.UserCardDescriptor{}, nil
	}
	if p.Name == "" || p.Default != "" || p.PeriodEnd || len(p.Options) != 0 {
		return storage.UserCardDescriptor{}, fmt.Errorf("context parameter requires a name and cannot use default, period_end or options")
	}
	if (c.User != "") == (c.UserCard != nil) {
		return storage.UserCardDescriptor{}, fmt.Errorf("choose exactly one context source")
	}
	if c.User != "" {
		if c.User != "id" && c.User != "login" {
			return storage.UserCardDescriptor{}, fmt.Errorf("context user must be id or login")
		}
		if p.Type != "string" && (c.User != "id" || p.Type != "reference:_users") {
			return storage.UserCardDescriptor{}, fmt.Errorf("context user source has incompatible parameter type %q", p.Type)
		}
		return storage.UserCardDescriptor{}, nil
	}
	desc, err := access.ScalarUserCardDescriptor(c.UserCard, lookup)
	if err != nil {
		return desc, err
	}
	if !strings.EqualFold(p.Type, string(desc.Field.Type)) {
		return desc, fmt.Errorf("context parameter type must match user_card field type")
	}
	return desc, nil
}

// ValidateContextParams is also used by configcheck, before any report runs.
func ValidateContextParams(rep *Report, lookup access.EntityLookup) error {
	if rep == nil {
		return fmt.Errorf("report is required")
	}
	seen := map[string]bool{}
	for _, p := range rep.Params {
		key := strings.ToLower(p.Name)
		if seen[key] {
			return fmt.Errorf("duplicate report parameter %q", p.Name)
		}
		seen[key] = true
		if p.Context == nil {
			continue
		}
		if rep.External {
			return fmt.Errorf("context is only available to configuration reports")
		}
		if _, err := contextDescriptor(p, lookup); err != nil {
			return fmt.Errorf("parameter %s: %w", p.Name, err)
		}
	}
	return nil
}

// ResolveContextParams returns a detached binding map. Client/preset values,
// including case variants, never supply or override a trusted parameter. An
// absent/ambiguous/empty user card fails closed instead of removing the filter.
func ResolveContextParams(ctx context.Context, rep *Report, input map[string]any, lookup access.EntityLookup, reader access.UserCardResolver) (map[string]any, error) {
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	hasContext := false
	for _, p := range rep.Params {
		hasContext = hasContext || p.Context != nil
	}
	if !hasContext {
		return out, nil
	}
	if err := ValidateContextParams(rep, lookup); err != nil {
		return nil, err
	}
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, ErrContextUnavailable
	}
	for _, p := range rep.Params {
		if p.Context == nil {
			continue
		}
		for k := range out {
			if strings.EqualFold(k, p.Name) {
				delete(out, k)
			}
		}
		var value any
		switch p.Context.User {
		case "id":
			id, err := uuid.Parse(u.ID)
			if err != nil || id == uuid.Nil {
				return nil, ErrContextUnavailable
			}
			value = id.String()
		case "login":
			if strings.TrimSpace(u.Login) == "" {
				return nil, ErrContextUnavailable
			}
			value = u.Login
		default:
			if reader == nil {
				return nil, ErrContextUnavailable
			}
			desc, err := contextDescriptor(p, lookup)
			if err != nil {
				return nil, err
			}
			card, err := reader.ReadUserCardSource(ctx, desc, u.ID)
			if err != nil || !card.Available || card.Value == nil {
				return nil, ErrContextUnavailable
			}
			value = card.Value
		}
		out[p.Name] = value
	}
	return out, nil
}

// InputParams excludes server bindings from forms and their URL snapshots.
func (r *Report) InputParams() []Param {
	out := make([]Param, 0, len(r.Params))
	for _, p := range r.Params {
		if p.Context == nil {
			out = append(out, p)
		}
	}
	return out
}
