package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Upsert inserts or updates the object fields.
func (db *DB) Upsert(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error {
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	placeholders := []string{"$1"}
	args := []any{id}
	updates := []string{}

	for i, f := range entity.Fields {
		col := metadata.ColumnName(f)
		ph := fmt.Sprintf("$%d", i+2)
		cols = append(cols, col)
		placeholders = append(placeholders, ph)
		args = append(args, fieldValue(f, fields))
		updates = append(updates, col+" = EXCLUDED."+col)
	}

	var sql string
	if len(updates) == 0 {
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO NOTHING",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	} else {
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO UPDATE SET %s",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "))
	}
	_, err := db.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("upsert %s: %w", entityName, err)
	}
	return nil
}

// GetByID retrieves a single object by ID, returning fields as map[string]any.
func (db *DB) GetByID(ctx context.Context, entityName string, id uuid.UUID, entity *metadata.Entity) (map[string]any, error) {
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	for _, f := range entity.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1", strings.Join(cols, ", "), table)
	row := db.pool.QueryRow(ctx, sql, id)

	dest := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, fmt.Errorf("getbyid %s: %w", entityName, err)
	}

	result := make(map[string]any, len(cols))
	result["id"] = normalizeUUID(dest[0])
	for i, f := range entity.Fields {
		v := dest[i+1]
		if f.RefEntity != "" {
			v = normalizeUUID(v)
		}
		result[f.Name] = v
	}
	return result, nil
}

// normalizeUUID converts pgx UUID scan results ([16]byte) to string.
func normalizeUUID(v any) any {
	switch t := v.(type) {
	case [16]byte:
		return uuid.UUID(t).String()
	case uuid.UUID:
		return t.String()
	}
	return v
}

// List returns all rows for an entity ordered by id.
func (db *DB) List(ctx context.Context, entityName string, entity *metadata.Entity) ([]map[string]any, error) {
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	for _, f := range entity.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	sql := fmt.Sprintf("SELECT %s FROM %s ORDER BY id", strings.Join(cols, ", "), table)
	rows, err := db.pool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", entityName, err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		row["id"] = normalizeUUID(dest[0])
		for i, f := range entity.Fields {
			v := dest[i+1]
			if f.RefEntity != "" {
				v = normalizeUUID(v)
			}
			row[f.Name] = v
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// fieldValue extracts the value for a field from the fields map, handling reference UUID strings.
func fieldValue(f metadata.Field, fields map[string]any) any {
	v := fields[f.Name]
	if f.RefEntity != "" && v != nil {
		switch s := v.(type) {
		case string:
			if id, err := uuid.Parse(s); err == nil {
				return id
			}
		}
	}
	return v
}
