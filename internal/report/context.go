package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"gopkg.in/yaml.v3"
)

// ParamContext selects exactly one server-owned scalar. user_card uses the
// unique, non-deleted catalog card linked by reference:_users to the actor ID.
type ParamContext struct {
	User        string               `yaml:"user,omitempty"`
	UserCard    *auth.UserCardSource `yaml:"user_card,omitempty"`
	CatalogName *CatalogNameSource   `yaml:"catalog_name,omitempty"`
}

// CatalogNameSource identifies an existing catalog element by its configured name.
type CatalogNameSource struct {
	Catalog string `yaml:"catalog"`
	Name    string `yaml:"name"`
}

type catalogNameReader interface {
	ReadCatalogNameReference(context.Context, *metadata.Entity, string) (uuid.UUID, int, error)
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
		return fmt.Errorf("report context requires exactly one user, user_card or catalog_name source")
	}
	key := n.Content[0].Value
	switch key {
	case "user", "user_card":
	case "catalog_name":
		source := n.Content[1]
		if source.Kind != yaml.MappingNode || len(source.Content) != 4 {
			return fmt.Errorf("catalog_name requires catalog and name")
		}
		for i := 0; i < len(source.Content); i += 2 {
			if field := source.Content[i].Value; field != "catalog" && field != "name" {
				return fmt.Errorf("unknown catalog_name field %q", field)
			}
		}
	default:
		return fmt.Errorf("unknown report context source %q", key)
	}
	type plain ParamContext
	if err := n.Decode((*plain)(c)); err != nil {
		return err
	}
	if c.User == "" && c.UserCard == nil && c.CatalogName == nil {
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
	if c.CatalogName != nil {
		if c.User != "" || c.UserCard != nil {
			return storage.UserCardDescriptor{}, fmt.Errorf("catalog_name cannot be combined with another context source")
		}
		_, err := catalogNameDescriptor(p, lookup)
		return storage.UserCardDescriptor{}, err
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

func catalogNameDescriptor(p Param, lookup access.EntityLookup) (*metadata.Entity, error) {
	source := p.Context.CatalogName
	if lookup == nil || strings.TrimSpace(source.Catalog) == "" || strings.TrimSpace(source.Name) == "" {
		return nil, fmt.Errorf("catalog_name requires catalog and name")
	}
	catalog := lookup.GetEntity(source.Catalog)
	if catalog == nil || catalog.Kind != metadata.KindCatalog || !strings.EqualFold(p.Type, "reference:"+catalog.Name) {
		return nil, fmt.Errorf("catalog_name parameter type must reference the configured catalog")
	}
	for _, field := range catalog.Fields {
		if strings.EqualFold(field.Name, "Наименование") && field.Type == metadata.FieldTypeString && field.RefEntity == "" && !field.PII {
			return catalog, nil
		}
	}
	return nil, fmt.Errorf("catalog_name requires a plain Наименование field in the configured catalog")
}

var errCatalogNameUnavailable = errors.New("серверная настройка отчёта недоступна")

// ContextErrorText exposes only the controlled validation failure for a fixed
// catalog name, without leaking storage errors or user-card diagnostics.
func ContextErrorText(err error) string {
	if errors.Is(err, errCatalogNameUnavailable) {
		return err.Error()
	}
	return "report context unavailable"
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
		switch {
		case p.Context.CatalogName != nil:
			catalog, err := catalogNameDescriptor(p, lookup)
			catalogReader, ok := reader.(catalogNameReader)
			if err != nil || !ok {
				return nil, ErrContextUnavailable
			}
			id, count, err := catalogReader.ReadCatalogNameReference(ctx, catalog, p.Context.CatalogName.Name)
			if err != nil {
				return nil, ErrContextUnavailable
			}
			if count == 0 {
				return nil, fmt.Errorf("%w: запись для параметра %s не найдена", errCatalogNameUnavailable, p.Name)
			}
			if count != 1 {
				return nil, fmt.Errorf("%w: для параметра %s найдено несколько записей", errCatalogNameUnavailable, p.Name)
			}
			if id == uuid.Nil {
				return nil, ErrContextUnavailable
			}
			value = id.String()
		case p.Context.User == "id":
			id, err := uuid.Parse(u.ID)
			if err != nil || id == uuid.Nil {
				return nil, ErrContextUnavailable
			}
			value = id.String()
		case p.Context.User == "login":
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
