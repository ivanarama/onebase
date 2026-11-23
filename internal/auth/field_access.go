package auth

import "strings"

// FieldAccess stores field-level masking policies grouped by the same object
// sections as object RBAC (план 88). A policy hides or masks a field's value on
// read, after the object-level permission and any row-level policy (план 79)
// already allowed the row. Roles without field_access behave exactly as before.
type FieldAccess struct {
	Catalogs  map[string]FieldPolicies `yaml:"catalogs" json:"catalogs,omitempty"`
	Documents map[string]FieldPolicies `yaml:"documents" json:"documents,omitempty"`
	Registers map[string]FieldPolicies `yaml:"registers" json:"registers,omitempty"`
	InfoRegs  map[string]FieldPolicies `yaml:"inforegs" json:"inforegs,omitempty"`
}

func (fa FieldAccess) IsZero() bool {
	return len(fa.Catalogs) == 0 && len(fa.Documents) == 0 &&
		len(fa.Registers) == 0 && len(fa.InfoRegs) == 0
}

// FieldPolicies maps a field name to its read policy.
type FieldPolicies map[string]FieldPolicy

// FieldPolicy is one field's read strategy.
//
//	Read: full | mask_tail | mask_city | mask_all | hide (default full when empty)
//	Keep: number of trailing characters left visible by mask_tail (•••••1122)
type FieldPolicy struct {
	Read string `yaml:"read,omitempty" json:"read,omitempty"`
	Keep int    `yaml:"keep,omitempty" json:"keep,omitempty"`
	// Disclose=false forbids revealing this field even with object-level
	// disclose. nil preserves the existing disclosure contract.
	Disclose *bool `yaml:"disclose,omitempty" json:"disclose,omitempty"`
}

// Policies returns the field policies declared for (kind, entity), or nil.
func (fa FieldAccess) Policies(kind, entity string) FieldPolicies {
	section := fa.section(kind)
	if section == nil {
		return nil
	}
	return section[entity]
}

func (fa FieldAccess) section(kind string) map[string]FieldPolicies {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "catalog", "catalogs":
		return fa.Catalogs
	case "document", "documents":
		return fa.Documents
	case "register", "registers":
		return fa.Registers
	case "inforeg", "inforegs":
		return fa.InfoRegs
	default:
		return nil
	}
}

func normalizeFieldAccess(in FieldAccess) FieldAccess {
	return FieldAccess{
		Catalogs:  normalizeFieldPolicyMap(in.Catalogs),
		Documents: normalizeFieldPolicyMap(in.Documents),
		Registers: normalizeFieldPolicyMap(in.Registers),
		InfoRegs:  normalizeFieldPolicyMap(in.InfoRegs),
	}
}

func normalizeFieldPolicyMap(in map[string]FieldPolicies) map[string]FieldPolicies {
	if in == nil {
		return nil
	}
	out := make(map[string]FieldPolicies, len(in))
	for entity, policies := range in {
		if policies == nil {
			continue
		}
		dst := make(FieldPolicies, len(policies))
		for field, policy := range policies {
			dst[strings.TrimSpace(field)] = normalizeFieldPolicy(policy)
		}
		out[entity] = dst
	}
	return out
}

func normalizeFieldPolicy(p FieldPolicy) FieldPolicy {
	p.Read = strings.ToLower(strings.TrimSpace(p.Read))
	return p
}

func mergeFieldAccess(dst, src FieldAccess) FieldAccess {
	dst.Catalogs = mergeFieldAccessSection(dst.Catalogs, src.Catalogs)
	dst.Documents = mergeFieldAccessSection(dst.Documents, src.Documents)
	dst.Registers = mergeFieldAccessSection(dst.Registers, src.Registers)
	dst.InfoRegs = mergeFieldAccessSection(dst.InfoRegs, src.InfoRegs)
	return normalizeFieldAccess(dst)
}

func mergeFieldAccessSection(dst, src map[string]FieldPolicies) map[string]FieldPolicies {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = make(map[string]FieldPolicies, len(src))
	}
	for entity, policies := range src {
		if dst[entity] == nil {
			dst[entity] = FieldPolicies{}
		}
		for field, policy := range policies {
			dst[entity][strings.TrimSpace(field)] = policy
		}
	}
	return dst
}

func fieldAccessKey(key string) bool {
	switch normalizePermissionKey(key) {
	case "fieldaccess", "fieldmasking", "полевойдоступ", "маскирование", "маскированиеполей":
		return true
	default:
		return false
	}
}
