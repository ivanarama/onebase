package storage

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// Predicate is a small structured row filter used by row-level access.
// It is intentionally not SQL text: callers provide field/op/value and storage
// renders placeholders for the active dialect.
type Predicate struct {
	Any          []Predicate
	All          []Predicate
	Not          *Predicate
	Field        string
	Op           string
	Value        any
	Values       []any
	RefEntity    *metadata.Entity
	RefPredicate *Predicate
}

// PredicateSQL compiles p to a SQL WHERE fragment and arguments. nextArg is
// the first 1-based placeholder index available to this fragment.
func PredicateSQL(d Dialect, entity *metadata.Entity, p *Predicate, nextArg int) (string, []any, int, error) {
	if p == nil {
		return "", nil, nextArg, nil
	}
	return predicateSQL(d, entity, *p, nextArg, "")
}

// PredicateSQLQualified is PredicateSQL with every field column prefixed by qualifier.
// It is used by query compilation when a row-level predicate must target a SQL alias.
func PredicateSQLQualified(d Dialect, entity *metadata.Entity, p *Predicate, nextArg int, qualifier string) (string, []any, int, error) {
	if p == nil {
		return "", nil, nextArg, nil
	}
	return predicateSQL(d, entity, *p, nextArg, qualifier)
}

func predicateSQL(d Dialect, entity *metadata.Entity, p Predicate, nextArg int, qualifier string) (string, []any, int, error) {
	if len(p.All) > 0 {
		return predicateGroupSQL(d, entity, p.All, " AND ", nextArg, qualifier)
	}
	if len(p.Any) > 0 {
		return predicateGroupSQL(d, entity, p.Any, " OR ", nextArg, qualifier)
	}
	if p.Not != nil {
		inner, args, next, err := predicateSQL(d, entity, *p.Not, nextArg, qualifier)
		if err != nil || inner == "" {
			return inner, args, next, err
		}
		return "NOT (" + inner + ")", args, next, nil
	}
	if p.RefPredicate != nil {
		return referencePredicateSQL(d, entity, p, nextArg, qualifier)
	}
	col, field, ok := predicateColumn(entity, p.Field)
	if !ok {
		return "", nil, nextArg, fmt.Errorf("unknown row predicate field %q", p.Field)
	}
	col = qualifyPredicateColumn(qualifier, col)
	op := strings.ToLower(strings.TrimSpace(p.Op))
	switch op {
	case "eq", "":
		return predicateCompareSQL(d, field, col, "=", p.Value, nextArg)
	case "ne":
		return predicateCompareSQL(d, field, col, "<>", p.Value, nextArg)
	case "in", "not_in":
		values := p.Values
		if len(values) == 0 {
			if list, ok := p.Value.([]any); ok {
				values = list
			} else {
				return "", nil, nextArg, fmt.Errorf("row predicate op %q requires a list value", p.Op)
			}
		}
		if len(values) == 0 {
			if op == "in" {
				return "1=0", nil, nextArg, nil
			}
			return "1=1", nil, nextArg, nil
		}
		ph := make([]string, 0, len(values))
		args := make([]any, 0, len(values))
		for _, v := range values {
			ph = append(ph, d.Placeholder(nextArg))
			args = append(args, predicateSQLValue(d, field, v))
			nextArg++
		}
		sqlOp := "IN"
		if op == "not_in" {
			sqlOp = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", col, sqlOp, strings.Join(ph, ", ")), args, nextArg, nil
	case "empty":
		if predicateStringLikeField(field) {
			return fmt.Sprintf("(%s IS NULL OR %s = '')", col, col), nil, nextArg, nil
		}
		return fmt.Sprintf("%s IS NULL", col), nil, nextArg, nil
	case "not_empty":
		if predicateStringLikeField(field) {
			return fmt.Sprintf("(%s IS NOT NULL AND %s <> '')", col, col), nil, nextArg, nil
		}
		return fmt.Sprintf("%s IS NOT NULL", col), nil, nextArg, nil
	default:
		return "", nil, nextArg, fmt.Errorf("unknown row predicate op %q", p.Op)
	}
}

func referencePredicateSQL(d Dialect, entity *metadata.Entity, p Predicate, nextArg int, qualifier string) (string, []any, int, error) {
	if p.RefEntity == nil {
		return "", nil, nextArg, fmt.Errorf("row predicate reference field %q has no target entity metadata", p.Field)
	}
	col, field, ok := predicateColumn(entity, p.Field)
	if !ok {
		return "", nil, nextArg, fmt.Errorf("unknown row predicate field %q", p.Field)
	}
	if field == nil || field.RefEntity == "" {
		return "", nil, nextArg, fmt.Errorf("row predicate field %q is not a reference", p.Field)
	}
	col = qualifyPredicateColumn(qualifier, col)
	alias := "rls_ref"
	inner, args, next, err := predicateSQL(d, p.RefEntity, *p.RefPredicate, nextArg, alias)
	if err != nil {
		return "", nil, nextArg, err
	}
	if inner == "" {
		return "", args, next, nil
	}
	sql := fmt.Sprintf("EXISTS (SELECT 1 FROM %s %s WHERE %s.id = %s AND (%s))",
		metadata.TableName(p.RefEntity.Name), alias, alias, col, inner)
	return sql, args, next, nil
}

func predicateGroupSQL(d Dialect, entity *metadata.Entity, items []Predicate, join string, nextArg int, qualifier string) (string, []any, int, error) {
	parts := make([]string, 0, len(items))
	var args []any
	for _, item := range items {
		sql, itemArgs, next, err := predicateSQL(d, entity, item, nextArg, qualifier)
		if err != nil {
			return "", nil, nextArg, err
		}
		nextArg = next
		if sql == "" {
			continue
		}
		parts = append(parts, "("+sql+")")
		args = append(args, itemArgs...)
	}
	if len(parts) == 0 {
		return "", args, nextArg, nil
	}
	joined := strings.Join(parts, join)
	if len(parts) > 1 {
		// Группа целиком — ОДИН операнд для вызывающего. Без внешних скобок
		// потребители, склеивающие WHERE через " AND " (List, GetMovements,
		// GetBalances, InfoRegList), получали `фильтр AND (a) OR (b)`: OR-ветка
		// политики вырывалась из-под остальных условий, и пользователь видел
		// чужие строки (issue #858; в internal/query это же чинил #652 — но
		// оборачивал результат снаружи, не тронув параллельные билдеры).
		joined = "(" + joined + ")"
	}
	return joined, args, nextArg, nil
}

func qualifyPredicateColumn(qualifier, col string) string {
	qualifier = strings.TrimSpace(qualifier)
	if qualifier == "" || strings.Contains(col, ".") || strings.ContainsAny(col, " ()") {
		return col
	}
	return qualifier + "." + col
}

func predicateCompareSQL(d Dialect, field *metadata.Field, col, op string, value any, nextArg int) (string, []any, int, error) {
	if value == nil {
		if op == "<>" {
			return fmt.Sprintf("%s IS NOT NULL", col), nil, nextArg, nil
		}
		return fmt.Sprintf("%s IS NULL", col), nil, nextArg, nil
	}
	return fmt.Sprintf("%s %s %s", col, op, d.Placeholder(nextArg)),
		[]any{predicateSQLValue(d, field, value)}, nextArg + 1, nil
}

func predicateColumn(entity *metadata.Entity, field string) (string, *metadata.Field, bool) {
	name := strings.TrimSpace(field)
	if name == "" {
		return "", nil, false
	}
	switch strings.ToLower(name) {
	case "id", "ссылка":
		return "id", &metadata.Field{Name: "id", Type: metadata.FieldTypeString, RefEntity: "_uuid"}, true
	case "posted", "проведен", "проведён":
		if entity != nil && entity.Kind == metadata.KindDocument {
			return "posted", &metadata.Field{Name: "posted", Type: metadata.FieldTypeBool}, true
		}
	case "deletion_mark", "пометкаудаления", "пометка_удаления":
		return "deletion_mark", &metadata.Field{Name: "deletion_mark", Type: metadata.FieldTypeBool}, true
	case "_version":
		return "_version", &metadata.Field{Name: "_version", Type: metadata.FieldTypeNumber}, true
	case "parent_id":
		if entity != nil && entity.Hierarchical {
			return "parent_id", &metadata.Field{Name: "parent_id", Type: metadata.FieldTypeString, RefEntity: "_uuid"}, true
		}
	case "is_folder":
		if entity != nil && entity.Hierarchical {
			return "is_folder", &metadata.Field{Name: "is_folder", Type: metadata.FieldTypeBool}, true
		}
	case "period", "период":
		if predicateEntityHasField(entity, "period") {
			return "period", &metadata.Field{Name: "period", Type: metadata.FieldTypeDate}, true
		}
	case "recorder", "регистратор":
		if predicateEntityHasField(entity, "recorder") {
			return "recorder", &metadata.Field{Name: "recorder", Type: metadata.FieldTypeString, RefEntity: "_uuid"}, true
		}
		if predicateEntityHasField(entity, "регистратор") {
			return "регистратор", &metadata.Field{Name: "регистратор", Type: metadata.FieldTypeString, RefEntity: "_uuid"}, true
		}
	case "recorder_type", "типрегистратора", "тип_регистратора":
		if predicateEntityHasField(entity, "recorder_type") {
			return "recorder_type", &metadata.Field{Name: "recorder_type", Type: metadata.FieldTypeString}, true
		}
		if predicateEntityHasField(entity, "регистратор_тип") {
			return "регистратор_тип", &metadata.Field{Name: "регистратор_тип", Type: metadata.FieldTypeString}, true
		}
	case "line_number", "номерстроки", "номер_строки":
		if predicateEntityHasField(entity, "line_number") {
			return "line_number", &metadata.Field{Name: "line_number", Type: metadata.FieldTypeNumber}, true
		}
	case "вид_движения", "виддвижения":
		if predicateEntityHasField(entity, "вид_движения") {
			return "вид_движения", &metadata.Field{Name: "вид_движения", Type: metadata.FieldTypeString}, true
		}
	}
	if entity == nil {
		return "", nil, false
	}
	for i := range entity.Fields {
		f := entity.Fields[i]
		if strings.EqualFold(f.Name, name) {
			return metadata.ColumnName(f), &f, true
		}
	}
	return "", nil, false
}

func predicateEntityHasField(entity *metadata.Entity, field string) bool {
	if entity == nil {
		return false
	}
	for i := range entity.Fields {
		if strings.EqualFold(entity.Fields[i].Name, field) {
			return true
		}
	}
	return false
}

func predicateSQLValue(d Dialect, field *metadata.Field, value any) any {
	if field == nil {
		return value
	}
	if field.RefEntity != "" || strings.EqualFold(field.Name, "id") || strings.EqualFold(field.Name, "parent_id") {
		if id, ok := parseAnyUUID(value); ok {
			return idArg(d, id)
		}
	}
	if field.Type == metadata.FieldTypeBool {
		if b, ok := parseAnyBool(value); ok {
			return b
		}
	}
	return value
}

func predicateStringLikeField(field *metadata.Field) bool {
	return field == nil || field.Type == metadata.FieldTypeString || field.RefEntity != "" ||
		strings.EqualFold(field.Name, "id") || strings.EqualFold(field.Name, "parent_id")
}

func parseAnyUUID(v any) (uuid.UUID, bool) {
	switch t := v.(type) {
	case uuidProvider:
		return parseAnyUUID(t.GetRefUUID())
	case uuid.UUID:
		return t, true
	case string:
		id, err := uuid.Parse(t)
		return id, err == nil
	default:
		s := fmt.Sprintf("%v", v)
		id, err := uuid.Parse(s)
		return id, err == nil
	}
}

func parseAnyBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "y", "да", "истина":
			return true, true
		case "false", "0", "no", "n", "нет", "ложь":
			return false, true
		}
	case int:
		return t != 0, true
	case int64:
		return t != 0, true
	case float64:
		return t != 0, true
	}
	return false, false
}

// MatchPredicate evaluates p against an already loaded row. It is used for
// direct get/update/delete checks where the row is loaded by id.
func MatchPredicate(row map[string]any, p *Predicate) bool {
	return MatchPredicateWithRefs(row, p, nil)
}

type PredicateRefResolver func(entity *metadata.Entity, id uuid.UUID) (map[string]any, bool)

func MatchPredicateWithRefs(row map[string]any, p *Predicate, resolver PredicateRefResolver) bool {
	if p == nil {
		return true
	}
	return matchPredicate(row, *p, resolver)
}

func MergeRowFields(row, fields map[string]any) map[string]any {
	out := make(map[string]any, len(row)+len(fields))
	for k, v := range row {
		out[k] = v
	}
	for k, v := range fields {
		for existing := range out {
			if strings.EqualFold(existing, k) {
				delete(out, existing)
			}
		}
		out[k] = v
	}
	return out
}

type predicateTruth uint8

const (
	predicateFalse predicateTruth = iota
	predicateTrue
	predicateUnknown
)

func matchPredicate(row map[string]any, p Predicate, resolver PredicateRefResolver) bool {
	return matchPredicateTruth(row, p, resolver) == predicateTrue
}

// matchPredicateTruth mirrors SQL three-valued boolean logic. A nullable value
// compared with an ordinary value is UNKNOWN, not false; in a WHERE clause only
// TRUE admits the row. Keeping UNKNOWN through NOT/AND/OR is essential: plain
// Go negation would turn `NOT (NULL = x)` into true and make direct RLS checks
// less restrictive than the SQL filter used by list/query paths.
func matchPredicateTruth(row map[string]any, p Predicate, resolver PredicateRefResolver) predicateTruth {
	if len(p.All) > 0 {
		result := predicateTrue
		for _, item := range p.All {
			switch matchPredicateTruth(row, item, resolver) {
			case predicateFalse:
				return predicateFalse
			case predicateUnknown:
				result = predicateUnknown
			}
		}
		return result
	}
	if len(p.Any) > 0 {
		result := predicateFalse
		for _, item := range p.Any {
			switch matchPredicateTruth(row, item, resolver) {
			case predicateTrue:
				return predicateTrue
			case predicateUnknown:
				result = predicateUnknown
			}
		}
		return result
	}
	if p.Not != nil {
		return negatePredicateTruth(matchPredicateTruth(row, *p.Not, resolver))
	}
	if p.RefPredicate != nil {
		if resolver == nil || p.RefEntity == nil {
			return predicateFalse
		}
		actual, ok := rowValue(row, p.Field)
		if !ok || actual == nil {
			return predicateFalse
		}
		id, ok := parseAnyUUID(actual)
		if !ok {
			return predicateFalse
		}
		refRow, ok := resolver(p.RefEntity, id)
		if !ok || matchPredicateTruth(refRow, *p.RefPredicate, resolver) != predicateTrue {
			// SQL represents reference predicates as EXISTS. UNKNOWN inside the
			// subquery does not select a row, so EXISTS itself is simply FALSE.
			return predicateFalse
		}
		return predicateTrue
	}
	actual, ok := rowValue(row, p.Field)
	op := strings.ToLower(strings.TrimSpace(p.Op))
	switch op {
	case "eq", "":
		if p.Value == nil {
			return predicateTruthFromBool(!ok || actual == nil)
		}
		if !ok || actual == nil {
			return predicateUnknown
		}
		return predicateTruthFromBool(valuesEqual(actual, p.Value))
	case "ne":
		if p.Value == nil {
			return predicateTruthFromBool(ok && actual != nil)
		}
		if !ok || actual == nil {
			return predicateUnknown
		}
		return predicateTruthFromBool(!valuesEqual(actual, p.Value))
	case "in":
		return predicateInTruth(actual, ok, predicateValues(p))
	case "not_in":
		return negatePredicateTruth(predicateInTruth(actual, ok, predicateValues(p)))
	case "empty":
		return predicateTruthFromBool(!ok || actual == nil || fmt.Sprintf("%v", actual) == "")
	case "not_empty":
		return predicateTruthFromBool(ok && actual != nil && fmt.Sprintf("%v", actual) != "")
	default:
		return predicateFalse
	}
}

func predicateTruthFromBool(value bool) predicateTruth {
	if value {
		return predicateTrue
	}
	return predicateFalse
}

func negatePredicateTruth(value predicateTruth) predicateTruth {
	switch value {
	case predicateTrue:
		return predicateFalse
	case predicateFalse:
		return predicateTrue
	default:
		return predicateUnknown
	}
}

func predicateInTruth(actual any, exists bool, values []any) predicateTruth {
	if len(values) == 0 {
		return predicateFalse
	}
	if !exists || actual == nil {
		return predicateUnknown
	}
	unknown := false
	for _, value := range values {
		if value == nil {
			unknown = true
			continue
		}
		if valuesEqual(actual, value) {
			return predicateTrue
		}
	}
	if unknown {
		return predicateUnknown
	}
	return predicateFalse
}

func rowValue(row map[string]any, field string) (any, bool) {
	for k, v := range row {
		if strings.EqualFold(k, field) {
			return v, true
		}
	}
	return nil, false
}

func predicateValues(p Predicate) []any {
	if len(p.Values) > 0 {
		return p.Values
	}
	if list, ok := p.Value.([]any); ok {
		return list
	}
	return nil
}

func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if isBoolValue(a) || isBoolValue(b) {
		ab, aok := parseAnyBool(a)
		bb, bok := parseAnyBool(b)
		return aok && bok && ab == bb
	}
	if au, ok := parseAnyUUID(a); ok {
		if bu, ok := parseAnyUUID(b); ok {
			return au == bu
		}
	}
	if ad, ok := numericDecimal(a); ok {
		if bd, ok := numericDecimal(b); ok {
			return ad.Equal(bd)
		}
		return false
	}
	if _, ok := numericDecimal(b); ok {
		return false
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok || bok {
		return aok && bok && as == bs
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func isBoolValue(v any) bool {
	_, ok := v.(bool)
	return ok
}

func numericDecimal(v any) (decimal.Decimal, bool) {
	switch t := v.(type) {
	case decimal.Decimal:
		return t, true
	case int:
		return decimal.NewFromInt(int64(t)), true
	case int32:
		return decimal.NewFromInt(int64(t)), true
	case int64:
		return decimal.NewFromInt(t), true
	case float32:
		return decimal.NewFromFloat32(t), true
	case float64:
		return decimal.NewFromFloat(t), true
	default:
		return decimal.Decimal{}, false
	}
}
