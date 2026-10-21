package storage

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ListEquality and ListOrderKey are metadata-derived options for bounded form
// projections. Existing list/picker/keyset callers leave them nil.
type ListEquality struct {
	Field string
	Value any
}
type ListOrderKey struct {
	Field string
	Desc  bool
}

func listEqualitySQL(d Dialect, e *metadata.Entity, eq []ListEquality, start int) (string, []any, int, error) {
	if len(eq) > 8 {
		return "", nil, start, fmt.Errorf("maximum eight equalities")
	}
	var parts []string
	var args []any
	next := start
	seen := map[string]bool{}
	for _, x := range eq {
		f := metadata.EntityListField(e, x.Field)
		key := strings.ToLower(x.Field)
		if f == nil || seen[key] {
			return "", nil, start, fmt.Errorf("invalid equality field %q", x.Field)
		}
		seen[key] = true
		col := metadata.ColumnName(*f)
		sql, values, n, err := predicateCompareSQL(d, e, f, col, "=", x.Value, next)
		if err != nil {
			return "", nil, start, err
		}
		if f.Type == metadata.FieldTypeNumber && x.Value != nil && d.Name() == "sqlite" {
			sql = "CAST(" + col + " AS NUMERIC) = CAST(" + d.Placeholder(next) + " AS NUMERIC)"
		}
		parts = append(parts, sql)
		args = append(args, values...)
		next = n
	}
	return strings.Join(parts, " AND "), args, next, nil
}
func listProjectionOrder(d Dialect, e *metadata.Entity, keys []ListOrderKey) (string, error) {
	if len(keys) > 4 {
		return "", fmt.Errorf("maximum four order keys")
	}
	var parts []string
	seen := map[string]bool{}
	for _, key := range keys {
		name := strings.ToLower(key.Field)
		if seen[name] {
			return "", fmt.Errorf("duplicate order field")
		}
		seen[name] = true
		if name == "id" {
			if key.Desc {
				return "", fmt.Errorf("id tie breaker must be ascending")
			}
			continue
		}
		f := metadata.EntityListField(e, key.Field)
		if f == nil {
			return "", fmt.Errorf("unknown order field %q", key.Field)
		}
		col := metadata.ColumnName(*f)
		empty := col + " IS NULL"
		blank := f.RefEntity == "" && (f.Type == metadata.FieldTypeString || f.EnumName != "")
		if blank {
			empty = "(" + empty + " OR " + col + " = '')"
		}
		parts = append(parts, "CASE WHEN "+empty+" THEN 1 ELSE 0 END ASC")
		expr := col
		if f.Type == metadata.FieldTypeNumber && d.Name() == "sqlite" {
			expr = "CAST(" + col + " AS NUMERIC)"
		}
		if blank {
			expr = "CASE WHEN " + empty + " THEN NULL ELSE " + expr + " END"
		}
		dir := "ASC"
		if key.Desc {
			dir = "DESC"
		}
		parts = append(parts, expr+" "+dir)
	}
	parts = append(parts, "id ASC")
	return strings.Join(parts, ", "), nil
}
