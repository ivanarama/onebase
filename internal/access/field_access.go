package access

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

// Field masking strategies (план 88). A field not listed in any policy is
// returned in full.
const (
	FieldFull     = "full"      // полное значение (по умолчанию)
	FieldMaskTail = "mask_tail" // оставить последние Keep символов: •••••1122
	FieldMaskCity = "mask_city" // адрес: оставить город, скрыть улицу/дом
	FieldMaskAll  = "mask_all"  // всё значение → фиксированная маска
	FieldHide     = "hide"      // поле отсутствует в выдаче целиком
)

// fixedMask is the constant replacement used by mask_all and by any unknown
// (fail-closed) strategy — it deliberately leaks neither value nor length.
const fixedMask = "••••••"

const maskBullet = "•"

// FieldDecision is the effective read decision for one field, after merging all
// roles that grant object-level read (least-restrictive wins).
type FieldDecision struct {
	Strategy string // full|mask_tail|mask_city|mask_all|hide
	Keep     int
}

// Masked reports whether the value must be replaced (mask_* or hide).
func (d FieldDecision) Masked() bool { return d.Strategy != "" && d.Strategy != FieldFull }

// Hidden reports whether the field must be removed from the output entirely.
func (d FieldDecision) Hidden() bool { return strings.EqualFold(d.Strategy, FieldHide) }

// maskAdmin — deployment flag mask_admin (CC-SEC-006). Off by default: admins
// read full values but disclosure is still audited. When on, admins are subject
// to the field masking implied by their assigned roles, like a normal user.
var maskAdmin atomic.Bool

// SetMaskAdmin toggles whether administrators are also subject to field masking.
func SetMaskAdmin(v bool) { maskAdmin.Store(v) }

// MaskAdmin reports the current mask_admin flag.
func MaskAdmin() bool { return maskAdmin.Load() }

// addressCityFn is an optional application hook that extracts the disclosable
// part (city) of an address for the mask_city strategy. Без хука платформа
// оставляет первый сегмент до запятой.
var addressCityFn atomic.Value // func(string) string

// SetAddressCityFunc installs the mask_city address normaliser (прикладной хук).
func SetAddressCityFunc(fn func(string) string) {
	if fn != nil {
		addressCityFn.Store(fn)
	}
}

// FieldDecisions returns effective masking decisions per field for reading
// entity. Only fields that must be masked or hidden appear in the result; a nil
// or empty map means every field is returned in full. A nil user (no auth) or
// an unmasked admin also returns nil.
//
// Semantics mirror row_access (план 79): among the roles that grant object-level
// read, the least-restrictive vote wins. A reading role that does not restrict a
// field votes "full" for it, so a single unrestricted reading role exposes the
// field. Only when every reading role restricts the field is it masked/hidden.
func FieldDecisions(u *auth.User, kind, entity string, meta *metadata.Entity) map[string]FieldDecision {
	if u == nil {
		return nil
	}
	if u.IsAdmin && !maskAdmin.Load() {
		return nil
	}
	var readingRoles []*auth.Role
	for _, role := range u.Roles {
		if role != nil && auth.PermissionHas(role.Permissions, kind, entity, "read") {
			readingRoles = append(readingRoles, role)
		}
	}
	if len(readingRoles) == 0 {
		// Object-level read is denied elsewhere; nothing to mask here.
		return nil
	}
	// Candidate fields = union of fields any reading role restricts.
	candidates := map[string]string{} // fieldKey → first-seen display name
	for _, role := range readingRoles {
		for field := range role.Permissions.FieldAccess.Policies(kind, entity) {
			k := fieldKey(field)
			if _, ok := candidates[k]; !ok {
				candidates[k] = field
			}
		}
	}
	out := map[string]FieldDecision{}
	for k, name := range candidates {
		var best *FieldDecision
		full := false
		for _, role := range readingRoles {
			pol, ok := lookupFieldPolicy(role.Permissions.FieldAccess.Policies(kind, entity), k)
			if !ok {
				full = true
				break
			}
			dec := decisionFromPolicy(pol)
			if !dec.Masked() {
				full = true
				break
			}
			if best == nil || lessRestrictive(dec, *best) {
				d := dec
				best = &d
			}
		}
		if full || best == nil {
			continue
		}
		out[canonicalFieldName(meta, name)] = *best
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HasFieldPolicy reports whether reading entity would mask or hide any field for
// this user. Used by the fail-closed report/AI gate and the storage chokepoint.
func HasFieldPolicy(u *auth.User, kind, entity string, meta *metadata.Entity) bool {
	return len(FieldDecisions(u, kind, entity, meta)) > 0
}

// DeniedMaskedColumn is the fail-closed gate for reports/AI (план 88D). cols are
// logical source fields from query.Result.ProjectionFields, not SQL result column
// names: aliases and expressions must not erase the protected-field provenance.
// A wildcard is denied whenever at least one source field is masked or hidden.
// lookup resolves a source object's metadata and may return nil.
func DeniedMaskedColumn(u *auth.User, sources []query.SourceRef, cols []string, lookup func(kind, name string) *metadata.Entity) string {
	if u == nil || (u.IsAdmin && !MaskAdmin()) {
		return ""
	}
	masked := map[string]bool{}
	for _, src := range sources {
		var meta *metadata.Entity
		if lookup != nil {
			meta = lookup(src.Kind, src.Name)
		}
		for field := range FieldDecisions(u, src.Kind, src.Name, meta) {
			masked[strings.ToLower(strings.TrimSpace(field))] = true
		}
	}
	if len(masked) == 0 {
		return ""
	}
	for _, c := range cols {
		if strings.TrimSpace(c) == "*" {
			return c
		}
		if masked[strings.ToLower(strings.TrimSpace(c))] {
			return c
		}
	}
	return ""
}

// MaskRecord applies field decisions to one row in place: hidden fields are
// removed, masked fields have their value replaced. Rows are keyed by field name
// (matched case-insensitively). It is the single chokepoint shared by every read
// path (list/get/picker/print/export).
func MaskRecord(dec map[string]FieldDecision, row map[string]any) {
	if len(dec) == 0 || row == nil {
		return
	}
	for field, d := range dec {
		key, ok := matchRowKey(row, field)
		if !ok {
			continue
		}
		switch {
		case d.Hidden():
			delete(row, key)
		case d.Masked():
			row[key] = MaskValue(d.Strategy, d.Keep, row[key])
		}
	}
}

// MaskRecords applies MaskRecord to every row.
func MaskRecords(dec map[string]FieldDecision, rows []map[string]any) {
	if len(dec) == 0 {
		return
	}
	for _, row := range rows {
		MaskRecord(dec, row)
	}
}

// MaskValue applies one masking strategy to a value. Unknown strategies fail
// closed (fixed mask). full returns the value unchanged; hide returns nil.
func MaskValue(strategy string, keep int, v any) any {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", FieldFull:
		return v
	case FieldHide:
		return nil
	case FieldMaskTail:
		return maskTail(toDisplayString(v), keep)
	case FieldMaskCity:
		return maskCity(toDisplayString(v))
	case FieldMaskAll:
		return maskAll(toDisplayString(v))
	default:
		return maskAll(toDisplayString(v))
	}
}

// ValidateFieldPolicy checks one field policy against entity metadata: the field
// must be a concrete requisit and the strategy must be known and applicable to
// the field type. Used by onebase check --lint (config lint field_access.*).
func ValidateFieldPolicy(field string, p auth.FieldPolicy, meta *metadata.Entity) error {
	if strings.TrimSpace(field) == "" {
		return fmt.Errorf("field_access has empty field name")
	}
	f, ok := concreteMetaField(meta, field)
	if !ok {
		return fmt.Errorf("references unknown field %q", field)
	}
	switch strings.ToLower(strings.TrimSpace(p.Read)) {
	case "", FieldFull, FieldMaskAll, FieldHide:
		return nil
	case FieldMaskTail:
		if p.Keep < 0 {
			return fmt.Errorf("mask_tail keep must be >= 0, got %d", p.Keep)
		}
		return nil
	case FieldMaskCity:
		if f.Type != metadata.FieldTypeString {
			return fmt.Errorf("mask_city applies to string fields, field %q is %s", field, f.Type)
		}
		return nil
	default:
		return fmt.Errorf("unknown mask strategy %q", p.Read)
	}
}

func decisionFromPolicy(p auth.FieldPolicy) FieldDecision {
	strat := strings.ToLower(strings.TrimSpace(p.Read))
	if strat == "" {
		strat = FieldFull
	}
	return FieldDecision{Strategy: strat, Keep: p.Keep}
}

// restrictRank orders strategies from least (0) to most (4) restrictive. Unknown
// strategies rank as mask_all so a typo never resolves to "reveal more".
func restrictRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case FieldFull, "":
		return 0
	case FieldMaskTail:
		return 1
	case FieldMaskCity:
		return 2
	case FieldMaskAll:
		return 3
	case FieldHide:
		return 4
	default:
		return 3
	}
}

// lessRestrictive reports whether a reveals more than b (deterministic total
// order so multi-role merges are stable).
func lessRestrictive(a, b FieldDecision) bool {
	ra, rb := restrictRank(a.Strategy), restrictRank(b.Strategy)
	if ra != rb {
		return ra < rb
	}
	// Keep is meaningful only for mask_tail: keeping more trailing characters
	// reveals more. For other equal strategies it is ignored by masking, and
	// this tie-break merely keeps multi-role resolution deterministic.
	return a.Keep > b.Keep
}

func lookupFieldPolicy(policies auth.FieldPolicies, key string) (auth.FieldPolicy, bool) {
	for f, p := range policies {
		if fieldKey(f) == key {
			return p, true
		}
	}
	return auth.FieldPolicy{}, false
}

func fieldKey(f string) string { return strings.ToLower(strings.TrimSpace(f)) }

func matchRowKey(row map[string]any, field string) (string, bool) {
	for k := range row {
		if strings.EqualFold(k, field) {
			return k, true
		}
	}
	return "", false
}

// canonicalFieldName maps a policy field name to the exact metadata field name
// so masked keys align with stored row keys; falls back to the policy name.
func canonicalFieldName(meta *metadata.Entity, name string) string {
	if f, ok := concreteMetaField(meta, name); ok {
		return f.Name
	}
	return strings.TrimSpace(name)
}

func concreteMetaField(entity *metadata.Entity, name string) (metadata.Field, bool) {
	if entity == nil {
		return metadata.Field{}, false
	}
	for _, f := range entity.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return metadata.Field{}, false
}

func toDisplayString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func maskTail(s string, keep int) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if keep < 0 {
		keep = 0
	}
	// keep >= length would reveal the whole value — mask all instead.
	if keep >= len(r) {
		return strings.Repeat(maskBullet, len(r))
	}
	return strings.Repeat(maskBullet, len(r)-keep) + string(r[len(r)-keep:])
}

func maskAll(s string) string {
	if s == "" {
		return ""
	}
	return fixedMask
}

func maskCity(s string) string {
	if s == "" {
		return ""
	}
	if v := addressCityFn.Load(); v != nil {
		if fn, ok := v.(func(string) string); ok && fn != nil {
			return fn(s)
		}
	}
	return defaultCity(s)
}

// defaultCity keeps the first comma-separated segment (usually the city) and
// hides the rest. Applications with structured addresses should register a real
// parser via SetAddressCityFunc.
func defaultCity(s string) string {
	parts := strings.SplitN(s, ",", 2)
	city := strings.TrimSpace(parts[0])
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		return city + ", " + fixedMask
	}
	return city
}
