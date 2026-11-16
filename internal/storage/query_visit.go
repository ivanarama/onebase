package storage

import (
	"context"
	"fmt"
)

// VisitQuery streams a compiled query without adding a row limit. Returning
// false stops the cursor. The callback must not retain the column slice.
// Values cross the same driver normalization boundary as RunQueryLimit.
func (db *DB) VisitQuery(ctx context.Context, sql string, args []any, visit func(map[string]any, []string) (bool, error)) ([]string, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("run query: %w", err)
	}
	defer rows.Close()
	cols := rows.FieldNames()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeValue(dest[i])
		}
		more, err := visit(row, cols)
		if err != nil {
			return nil, err
		}
		if !more {
			return cols, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}
