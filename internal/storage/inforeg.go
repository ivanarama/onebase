package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// InfoRegSet upserts a record in an info register.
// For periodic registers, period must be non-nil.
func (db *DB) InfoRegSet(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, resources map[string]any, period *time.Time) error {
	table := metadata.InfoRegTableName(ir.Name)

	cols := []string{}
	phs := []string{}
	args := []any{}
	idx := 1

	if ir.Periodic {
		if period == nil {
			return fmt.Errorf("info register %s is periodic: period is required", ir.Name)
		}
		cols = append(cols, "period")
		phs = append(phs, fmt.Sprintf("$%d", idx))
		args = append(args, *period)
		idx++
	}

	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		cols = append(cols, col)
		phs = append(phs, fmt.Sprintf("$%d", idx))
		args = append(args, dimKey[f.Name])
		idx++
	}
	for _, f := range ir.Resources {
		col := metadata.ColumnName(f)
		cols = append(cols, col)
		phs = append(phs, fmt.Sprintf("$%d", idx))
		args = append(args, resources[f.Name])
		idx++
	}
	cols = append(cols, "updated_at")
	phs = append(phs, fmt.Sprintf("$%d", idx))
	args = append(args, time.Now())
	idx++

	// Build ON CONFLICT update clause for all non-PK columns
	var updates []string
	for _, f := range ir.Resources {
		col := metadata.ColumnName(f)
		updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
	}
	updates = append(updates, "updated_at = EXCLUDED.updated_at")

	sql := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		table,
		strings.Join(cols, ", "),
		strings.Join(phs, ", "),
		strings.Join(pkCols(ir), ", "),
		strings.Join(updates, ", "),
	)
	return db.exec(ctx, sql, args...)
}

// InfoRegGet returns the record matching the given dimension key (non-periodic).
func (db *DB) InfoRegGet(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any) (map[string]any, error) {
	table := metadata.InfoRegTableName(ir.Name)
	allCols := resourceAndDimCols(ir)
	where, args := dimWhere(ir, dimKey, 1)
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1",
		strings.Join(allCols, ", "), table, where)
	return db.infoRegScan(ctx, ir, sql, args)
}

// InfoRegGetLast returns the most recent record on or before onDate for the given dimensions.
func (db *DB) InfoRegGetLast(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, onDate time.Time) (map[string]any, error) {
	table := metadata.InfoRegTableName(ir.Name)
	allCols := append([]string{"period"}, resourceAndDimCols(ir)...)
	where, args := dimWhere(ir, dimKey, 1)
	args = append(args, onDate)
	sql := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s AND period <= $%d ORDER BY period DESC LIMIT 1",
		strings.Join(allCols, ", "), table, where, len(args))
	return db.infoRegScan(ctx, ir, sql, args)
}

// InfoRegList returns all records, optionally filtered by dimension values.
func (db *DB) InfoRegList(ctx context.Context, ir *metadata.InfoRegister) ([]map[string]any, error) {
	table := metadata.InfoRegTableName(ir.Name)
	var selCols []string
	if ir.Periodic {
		selCols = append(selCols, "period")
	}
	for _, f := range ir.Dimensions {
		selCols = append(selCols, metadata.ColumnName(f))
	}
	for _, f := range ir.Resources {
		selCols = append(selCols, metadata.ColumnName(f))
	}

	orderBy := strings.Join(pkCols(ir), ", ")
	sql := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s",
		strings.Join(selCols, ", "), table, orderBy)

	rows, err := db.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("info reg list %s: %w", ir.Name, err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(selCols))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(selCols))
		i := 0
		if ir.Periodic {
			if t, ok := dest[0].(time.Time); ok {
				row["period"] = t.Format("02.01.2006")
			} else {
				row["period"] = dest[0]
			}
			i = 1
		}
		for _, f := range ir.Dimensions {
			row[f.Name] = normalizeValue(dest[i])
			i++
		}
		for _, f := range ir.Resources {
			row[f.Name] = normalizeValue(dest[i])
			i++
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// InfoRegDelete removes a record by its primary key.
func (db *DB) InfoRegDelete(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, period *time.Time) error {
	table := metadata.InfoRegTableName(ir.Name)
	args := []any{}
	conds := []string{}
	idx := 1
	if ir.Periodic && period != nil {
		conds = append(conds, fmt.Sprintf("period = $%d", idx))
		args = append(args, *period)
		idx++
	}
	for _, f := range ir.Dimensions {
		conds = append(conds, fmt.Sprintf("%s = $%d", metadata.ColumnName(f), idx))
		args = append(args, dimKey[f.Name])
		idx++
	}
	if len(conds) == 0 {
		return fmt.Errorf("info reg delete: no key provided")
	}
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(conds, " AND "))
	return db.exec(ctx, sql, args...)
}

// pkCols returns the primary key column names for an info register.
func pkCols(ir *metadata.InfoRegister) []string {
	var cols []string
	if ir.Periodic {
		cols = append(cols, "period")
	}
	for _, f := range ir.Dimensions {
		cols = append(cols, metadata.ColumnName(f))
	}
	return cols
}

func resourceAndDimCols(ir *metadata.InfoRegister) []string {
	var cols []string
	for _, f := range ir.Dimensions {
		cols = append(cols, metadata.ColumnName(f))
	}
	for _, f := range ir.Resources {
		cols = append(cols, metadata.ColumnName(f))
	}
	return cols
}

func dimWhere(ir *metadata.InfoRegister, dimKey map[string]any, startIdx int) (string, []any) {
	var conds []string
	var args []any
	idx := startIdx
	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		conds = append(conds, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, dimKey[f.Name])
		idx++
	}
	if len(conds) == 0 {
		return "TRUE", nil
	}
	return strings.Join(conds, " AND "), args
}

func (db *DB) infoRegScan(ctx context.Context, ir *metadata.InfoRegister, sql string, args []any) (map[string]any, error) {
	row := db.pool.QueryRow(ctx, sql, args...)
	allCols := resourceAndDimCols(ir)
	dest := make([]any, len(allCols))
	ptrs := make([]any, len(dest))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(allCols))
	i := 0
	for _, f := range ir.Dimensions {
		result[f.Name] = normalizeValue(dest[i])
		i++
	}
	for _, f := range ir.Resources {
		result[f.Name] = normalizeValue(dest[i])
		i++
	}
	return result, nil
}
