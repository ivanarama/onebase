package auth

import "strings"

// RowAccess stores row-level policies grouped by the same object sections as
// object RBAC. Policies restrict rows after the object-level permission allows
// the operation.
type RowAccess struct {
	Catalogs  map[string]RowPolicies `yaml:"catalogs" json:"catalogs,omitempty"`
	Documents map[string]RowPolicies `yaml:"documents" json:"documents,omitempty"`
	Registers map[string]RowPolicies `yaml:"registers" json:"registers,omitempty"`
	InfoRegs  map[string]RowPolicies `yaml:"inforegs" json:"inforegs,omitempty"`
}

func (ra RowAccess) IsZero() bool {
	return len(ra.Catalogs) == 0 && len(ra.Documents) == 0 && len(ra.Registers) == 0 && len(ra.InfoRegs) == 0
}

type RowPolicies map[string]RowPolicy

type RowPolicy struct {
	All    []RowPolicy `yaml:"all,omitempty" json:"all,omitempty"`
	Any    []RowPolicy `yaml:"any,omitempty" json:"any,omitempty"`
	Not    *RowPolicy  `yaml:"not,omitempty" json:"not,omitempty"`
	Field  string      `yaml:"field,omitempty" json:"field,omitempty"`
	Op     string      `yaml:"op,omitempty" json:"op,omitempty"`
	Value  RowValue    `yaml:"value,omitempty" json:"value,omitempty"`
	SameAs string      `yaml:"same_as,omitempty" json:"same_as,omitempty"`
}

type RowValue struct {
	UserCard   *UserCardSource `yaml:"user_card,omitempty" json:"user_card,omitempty"`
	LiteralSet bool            `yaml:"-" json:"-"`
	User       string          `yaml:"user,omitempty" json:"user,omitempty"`
	UserAttr   string          `yaml:"user_attr,omitempty" json:"user_attr,omitempty"`
	Literal    any             `yaml:"literal,omitempty" json:"literal,omitempty"`
	List       []any           `yaml:"list,omitempty" json:"list,omitempty"`
}

func (policies RowPolicies) Resolve(op string) (RowPolicy, bool) {
	if policies == nil {
		return RowPolicy{}, false
	}
	return resolveRowPolicy(policies, strings.ToLower(strings.TrimSpace(op)), map[string]bool{})
}

func normalizeRowAccess(in RowAccess) RowAccess {
	return RowAccess{
		Catalogs:  normalizeRowPolicyMap(in.Catalogs),
		Documents: normalizeRowPolicyMap(in.Documents),
		Registers: normalizeRowPolicyMap(in.Registers),
		InfoRegs:  normalizeRowPolicyMap(in.InfoRegs),
	}
}

func normalizeRowPolicyMap(in map[string]RowPolicies) map[string]RowPolicies {
	if in == nil {
		return nil
	}
	out := make(map[string]RowPolicies, len(in))
	for entity, policies := range in {
		if policies == nil {
			continue
		}
		dst := make(RowPolicies, len(policies))
		for op, policy := range policies {
			dst[strings.ToLower(strings.TrimSpace(op))] = normalizeRowPolicy(policy)
		}
		out[entity] = dst
	}
	return out
}

func normalizeRowPolicy(p RowPolicy) RowPolicy {
	p.Field = strings.TrimSpace(p.Field)
	p.Op = strings.ToLower(strings.TrimSpace(p.Op))
	p.SameAs = strings.ToLower(strings.TrimSpace(p.SameAs))
	for i := range p.All {
		p.All[i] = normalizeRowPolicy(p.All[i])
	}
	for i := range p.Any {
		p.Any[i] = normalizeRowPolicy(p.Any[i])
	}
	if p.Not != nil {
		n := normalizeRowPolicy(*p.Not)
		p.Not = &n
	}
	p.Value.User = strings.ToLower(strings.TrimSpace(p.Value.User))
	p.Value.UserAttr = strings.ToLower(strings.TrimSpace(p.Value.UserAttr))
	return p
}

func mergeRowAccess(dst, src RowAccess) RowAccess {
	dst.Catalogs = mergeRowAccessSection(dst.Catalogs, src.Catalogs)
	dst.Documents = mergeRowAccessSection(dst.Documents, src.Documents)
	dst.Registers = mergeRowAccessSection(dst.Registers, src.Registers)
	dst.InfoRegs = mergeRowAccessSection(dst.InfoRegs, src.InfoRegs)
	return normalizeRowAccess(dst)
}

func mergeRowAccessSection(dst, src map[string]RowPolicies) map[string]RowPolicies {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = make(map[string]RowPolicies, len(src))
	}
	for entity, policies := range src {
		if dst[entity] == nil {
			dst[entity] = RowPolicies{}
		}
		for op, policy := range policies {
			dst[entity][strings.ToLower(strings.TrimSpace(op))] = policy
		}
	}
	return dst
}

func (ra RowAccess) Policy(kind, entity, op string) (RowPolicy, bool) {
	section := ra.section(kind)
	if section == nil {
		return RowPolicy{}, false
	}
	policies := section[entity]
	if policies == nil {
		return RowPolicy{}, false
	}
	return policies.Resolve(op)
}

func (ra RowAccess) HasPolicy(kind, entity, op string) bool {
	_, ok := ra.Policy(kind, entity, op)
	return ok
}

func (ra RowAccess) section(kind string) map[string]RowPolicies {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "catalog", "catalogs":
		return ra.Catalogs
	case "document", "documents":
		return ra.Documents
	case "register", "registers":
		return ra.Registers
	case "inforeg", "inforegs":
		return ra.InfoRegs
	default:
		return nil
	}
}

func resolveRowPolicy(policies RowPolicies, op string, seen map[string]bool) (RowPolicy, bool) {
	p, ok := policies[op]
	if !ok {
		return RowPolicy{}, false
	}
	if p.SameAs == "" {
		return p, true
	}
	if seen[op] {
		return RowPolicy{}, true
	}
	seen[op] = true
	resolved, ok := resolveRowPolicy(policies, p.SameAs, seen)
	if !ok {
		return RowPolicy{}, true
	}
	return resolved, true
}

func rowAccessKey(key string) bool {
	switch normalizePermissionKey(key) {
	case "rowaccess", "rowfilters", "rowfilter", "строковыеправа", "строковыйдоступ":
		return true
	default:
		return false
	}
}
