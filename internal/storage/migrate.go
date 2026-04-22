package storage

import (
	"context"
	"fmt"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Migrate applies CREATE TABLE and ADD COLUMN IF NOT EXISTS for all entities.
// Deletions and renames are out of scope for MVP.
func (db *DB) Migrate(ctx context.Context, entities []*metadata.Entity) error {
	// create tables in dependency order (catalogs first, then documents)
	ordered := orderByDependency(entities)
	for _, e := range ordered {
		sql := CreateTableSQL(e)
		if _, err := db.pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("migrate %s: %w", e.Name, err)
		}
		// add any missing columns
		table := metadata.TableName(e.Name)
		for _, f := range e.Fields {
			col := metadata.ColumnName(f)
			addSQL := AddColumnSQL(table, col, pgType(f))
			if _, err := db.pool.Exec(ctx, addSQL); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, f.Name, err)
			}
		}
	}
	return nil
}

// orderByDependency sorts entities so referenced entities come before referencing ones.
func orderByDependency(entities []*metadata.Entity) []*metadata.Entity {
	byName := make(map[string]*metadata.Entity, len(entities))
	for _, e := range entities {
		byName[e.Name] = e
	}
	visited := make(map[string]bool)
	var result []*metadata.Entity
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		e := byName[name]
		if e == nil {
			return
		}
		for _, f := range e.Fields {
			if f.RefEntity != "" {
				visit(f.RefEntity)
			}
		}
		result = append(result, e)
	}
	for _, e := range entities {
		visit(e.Name)
	}
	return result
}
