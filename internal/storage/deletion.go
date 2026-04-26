package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// EnsureDeletionMark adds deletion_mark column to all entity tables if missing.
func (db *DB) EnsureDeletionMark(ctx context.Context, entities []*metadata.Entity) error {
	for _, e := range entities {
		table := metadata.TableName(e.Name)
		sql := AddColumnSQL(table, "deletion_mark", "BOOLEAN NOT NULL DEFAULT FALSE")
		if _, err := db.pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("ensure deletion_mark %s: %w", e.Name, err)
		}
	}
	return nil
}

// MarkForDeletion sets or clears the deletion_mark flag for a record.
func (db *DB) MarkForDeletion(ctx context.Context, entityName string, id uuid.UUID, mark bool) error {
	table := metadata.TableName(entityName)
	return db.exec(ctx, fmt.Sprintf("UPDATE %s SET deletion_mark = $1 WHERE id = $2", table), mark, id)
}

// RefInfo describes a referencing record.
type RefInfo struct {
	EntityName string
	FieldName  string
	Count      int
}

// CheckRefs returns all entities/fields that reference the given object.
func (db *DB) CheckRefs(ctx context.Context, entityName string, id uuid.UUID, allEntities []*metadata.Entity) []RefInfo {
	var refs []RefInfo
	for _, e := range allEntities {
		for _, f := range e.Fields {
			if f.RefEntity != entityName {
				continue
			}
			col := metadata.ColumnName(f)
			var count int
			db.pool.QueryRow(ctx,
				fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = $1", metadata.TableName(e.Name), col),
				id).Scan(&count)
			if count > 0 {
				refs = append(refs, RefInfo{EntityName: e.Name, FieldName: f.Name, Count: count})
			}
		}
		for _, tp := range e.TableParts {
			for _, f := range tp.Fields {
				if f.RefEntity != entityName {
					continue
				}
				col := metadata.ColumnName(f)
				table := metadata.TablePartTableName(e.Name, tp.Name)
				var count int
				db.pool.QueryRow(ctx,
					fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = $1", table, col),
					id).Scan(&count)
				if count > 0 {
					refs = append(refs, RefInfo{
						EntityName: e.Name + "." + tp.Name,
						FieldName:  f.Name,
						Count:      count,
					})
				}
			}
		}
	}
	return refs
}

// ListMarked returns all records with deletion_mark=true for the given entity.
func (db *DB) ListMarked(ctx context.Context, entityName string, entity *metadata.Entity) ([]map[string]any, error) {
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	for _, f := range entity.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE deletion_mark = TRUE", strings.Join(cols, ", "), table)
	rows, err := db.pool.Query(ctx, query)
	if err != nil {
		return nil, err
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
		row["id"] = normalizeValue(dest[0])
		for i, f := range entity.Fields {
			row[f.Name] = normalizeValue(dest[i+1])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
