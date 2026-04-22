package storage

import (
	"context"
	"fmt"
)

// RunQuery executes a compiled SQL query and returns rows with column names.
func (db *DB) RunQuery(ctx context.Context, sql string, args []any) ([]map[string]any, []string, error) {
	rows, err := db.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("run query: %w", err)
	}
	defer rows.Close()

	fds := rows.FieldDescriptions()
	cols := make([]string, len(fds))
	for i, f := range fds {
		cols[i] = string(f.Name)
	}

	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeValue(dest[i])
		}
		result = append(result, row)
	}
	return result, cols, rows.Err()
}
