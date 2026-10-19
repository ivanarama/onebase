package auth

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// UserCardSource selects one ordinary field of a catalog linked to the login.
// Table-part selectors are recognized so that this release can reject them.
type UserCardSource struct {
	Catalog      string `yaml:"catalog" json:"catalog"`
	AccountField string `yaml:"account_field" json:"account_field"`
	Field        string `yaml:"field,omitempty" json:"field,omitempty"`
	TablePart    string `yaml:"table_part,omitempty" json:"table_part,omitempty"`
	Column       string `yaml:"column,omitempty" json:"column,omitempty"`
	Shorthand    bool   `yaml:"-" json:"-"`
}

func (s *UserCardSource) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		parts := strings.Split(n.Value, ".")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("user_card shorthand must be Catalog.Field")
		}
		*s = UserCardSource{Catalog: parts[0], Field: parts[1], Shorthand: true}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("user_card requires a mapping or Catalog.Field")
	}
	seen := map[string]bool{}
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if seen[k] {
			return fmt.Errorf("duplicate user_card key %q", k)
		}
		seen[k] = true
		switch k {
		case "catalog", "account_field", "field", "table_part", "column":
		default:
			return fmt.Errorf("unknown user_card key %q", k)
		}
	}
	type plain UserCardSource
	return n.Decode((*plain)(s))
}

func (v *RowValue) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("row value requires a mapping")
	}
	seen := map[string]bool{}
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i].Value
		switch k {
		case "user", "user_attr", "literal", "list", "user_card":
		default:
			return fmt.Errorf("unknown row value key %q", k)
		}
		if seen[k] {
			return fmt.Errorf("duplicate row value key %q", k)
		}
		seen[k] = true
	}
	if len(seen) > 1 {
		return fmt.Errorf("row value must have exactly one source")
	}
	type plain RowValue
	if err := n.Decode((*plain)(v)); err != nil {
		return err
	}
	// yaml.v3 skips pointer unmarshalling for null (including aliases), so
	// the key must survive independently of the decoded descriptor.
	if seen["user_card"] && v.UserCard == nil {
		return fmt.Errorf("user_card requires a mapping or Catalog.Field; null is not a source")
	}
	v.LiteralSet = seen["literal"]
	return nil
}

func (v *RowValue) UnmarshalJSON(b []byte) error { return yaml.Unmarshal(b, v) }
func (v RowValue) MarshalYAML() (any, error) {
	m := map[string]any{}
	switch {
	case v.UserCard != nil:
		m["user_card"] = v.UserCard
	case v.User != "":
		m["user"] = v.User
	case v.UserAttr != "":
		m["user_attr"] = v.UserAttr
	case v.List != nil:
		m["list"] = v.List
	case v.LiteralSet || v.Literal != nil:
		m["literal"] = v.Literal
	}
	return m, nil
}
func (v RowValue) MarshalJSON() ([]byte, error) { m, _ := v.MarshalYAML(); return json.Marshal(m) }

// Validate aliases and nested permission spellings before the compatibility
// parser can discard a decode error and accidentally create unrestricted RBAC.

func validateRowValuesYAML(node *yaml.Node) error {
	active := map[*yaml.Node]bool{}
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if node == nil {
			return nil
		}
		if active[node] {
			return fmt.Errorf("cyclic permissions alias")
		}
		active[node] = true
		defer delete(active, node)
		if node.Kind == yaml.AliasNode {
			return visit(node.Alias)
		}
		if node.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			value := node.Content[i+1]
			if rowAccessKey(key) {
				var rows RowAccess
				if err := value.Decode(&rows); err != nil {
					return err
				}
			}
			if permissionWrapperKey(key) {
				if err := visit(value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(node)
}

// HasUserCardSource detects database-dependent authorization. Response caches
// keyed only by login/role text must not reuse results for these users.
func HasUserCardSource(u *User) bool {
	if u == nil || u.IsAdmin {
		return false
	}
	for _, role := range u.Roles {
		if role == nil {
			continue
		}
		for _, section := range []map[string]RowPolicies{role.Permissions.RowAccess.Catalogs, role.Permissions.RowAccess.Documents, role.Permissions.RowAccess.Registers, role.Permissions.RowAccess.InfoRegs} {
			for _, policies := range section {
				for _, p := range policies {
					if RowPolicyHasUserCard(p) {
						return true
					}
				}
			}
		}
	}
	return false
}

func (s UserCardSource) MarshalYAML() (any, error) {
	if s.Shorthand {
		return s.Catalog + "." + s.Field, nil
	}
	type plain UserCardSource
	return plain(s), nil
}
func (s UserCardSource) MarshalJSON() ([]byte, error) {
	v, _ := s.MarshalYAML()
	return json.Marshal(v)
}

func RowPolicyHasUserCard(p RowPolicy) bool {
	if p.Value.UserCard != nil {
		return true
	}
	for _, child := range p.All {
		if RowPolicyHasUserCard(child) {
			return true
		}
	}
	for _, child := range p.Any {
		if RowPolicyHasUserCard(child) {
			return true
		}
	}
	return p.Not != nil && RowPolicyHasUserCard(*p.Not)
}
