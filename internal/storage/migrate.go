package storage

import (
	"context"
	"fmt"

	"github.com/ivantit66/onebase/internal/metadata"
)

// MigrateRegisters creates register tables (CREATE TABLE IF NOT EXISTS + ADD COLUMN).
func (db *DB) MigrateRegisters(ctx context.Context, registers []*metadata.Register) error {
	for _, reg := range registers {
		if _, err := db.pool.Exec(ctx, CreateRegisterSQL(reg)); err != nil {
			return fmt.Errorf("migrate register %s: %w", reg.Name, err)
		}
		table := metadata.RegisterTableName(reg.Name)
		// ensure system column exists on pre-existing tables
		if _, err := db.pool.Exec(ctx, AddColumnSQL(table, "period", "TIMESTAMPTZ")); err != nil {
			return fmt.Errorf("migrate register %s.period: %w", reg.Name, err)
		}
		allFields := append(append([]metadata.Field{}, reg.Dimensions...), append(reg.Resources, reg.Attributes...)...)
		for _, f := range allFields {
			if _, err := db.pool.Exec(ctx, AddColumnSQL(table, metadata.ColumnName(f), pgType(f))); err != nil {
				return fmt.Errorf("migrate register %s.%s: %w", reg.Name, f.Name, err)
			}
		}
	}
	return nil
}

// Migrate applies CREATE TABLE and ADD COLUMN IF NOT EXISTS for all entities.
// Also ensures system tables (_sequences) exist.
// Deletions and renames are out of scope for MVP.
func (db *DB) Migrate(ctx context.Context, entities []*metadata.Entity) error {
	if err := db.EnsureSeqTable(ctx); err != nil {
		return fmt.Errorf("migrate: sequences table: %w", err)
	}
	// create tables in dependency order (catalogs first, then documents)
	ordered := orderByDependency(entities)
	for _, e := range ordered {
		sql := CreateTableSQL(e)
		if _, err := db.pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("migrate %s: %w", e.Name, err)
		}
		// add any missing columns
		table := metadata.TableName(e.Name)
		// system columns for documents
		if e.Kind == metadata.KindDocument {
			if _, err := db.pool.Exec(ctx, AddColumnSQL(table, "posted", "BOOLEAN NOT NULL DEFAULT FALSE")); err != nil {
				return fmt.Errorf("migrate %s.posted: %w", e.Name, err)
			}
		}
		for _, f := range e.Fields {
			col := metadata.ColumnName(f)
			addSQL := AddColumnSQL(table, col, pgType(f))
			if _, err := db.pool.Exec(ctx, addSQL); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, f.Name, err)
			}
		}
		// create tablepart tables
		for _, tp := range e.TableParts {
			tpSQL := CreateTablePartSQL(e, tp)
			if _, err := db.pool.Exec(ctx, tpSQL); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, tp.Name, err)
			}
			tpTable := metadata.TablePartTableName(e.Name, tp.Name)
			for _, f := range tp.Fields {
				col := metadata.ColumnName(f)
				addSQL := AddColumnSQL(tpTable, col, pgType(f))
				if _, err := db.pool.Exec(ctx, addSQL); err != nil {
					return fmt.Errorf("migrate %s.%s.%s: %w", e.Name, tp.Name, f.Name, err)
				}
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
