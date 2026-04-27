package storage

import (
	"context"
	"fmt"
	"unicode"

	"github.com/ivantit66/onebase/internal/metadata"
)

// toSnakeCase converts CamelCase (including Cyrillic) to snake_case.
// Used to detect and rename columns created by older schema versions.
func toSnakeCase(s string) string {
	runes := []rune(s)
	out := make([]rune, 0, len(runes)+4)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}

// renameSnakeCols renames old snake_case columns (e.g. тип_контрагента)
// to the current lowercase style (типконтрагента) if they exist in the table.
func (db *DB) renameSnakeCols(ctx context.Context, table string, fields []metadata.Field) {
	for _, f := range fields {
		newCol := metadata.ColumnName(f)
		oldCol := toSnakeCase(f.Name)
		if oldCol == newCol {
			continue
		}
		var oldExists bool
		db.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`,
			table, oldCol).Scan(&oldExists)
		if !oldExists {
			continue
		}
		var newExists bool
		db.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`,
			table, newCol).Scan(&newExists)
		if newExists {
			// Both columns exist (old migration ran ADD COLUMN before rename could happen):
			// copy data from old into new where new is NULL, then drop old.
			db.pool.Exec(ctx, fmt.Sprintf(
				"UPDATE %s SET %s = %s WHERE %s IS NOT NULL AND %s IS NULL",
				table, newCol, oldCol, oldCol, newCol))
			db.pool.Exec(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, oldCol))
		} else {
			db.pool.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", table, oldCol, newCol))
		}
	}
}

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
		// rename any old snake_case columns before adding new ones
		db.renameSnakeCols(ctx, table, allFields)
		for _, f := range allFields {
			if _, err := db.pool.Exec(ctx, AddColumnSQL(table, metadata.ColumnName(f), pgType(f))); err != nil {
				return fmt.Errorf("migrate register %s.%s: %w", reg.Name, f.Name, err)
			}
		}
	}
	return nil
}

// MigrateInfoRegisters creates tables for info registers (CREATE TABLE IF NOT EXISTS + ADD COLUMN).
func (db *DB) MigrateInfoRegisters(ctx context.Context, regs []*metadata.InfoRegister) error {
	for _, ir := range regs {
		if _, err := db.pool.Exec(ctx, CreateInfoRegisterSQL(ir)); err != nil {
			return fmt.Errorf("migrate info register %s: %w", ir.Name, err)
		}
		table := metadata.InfoRegTableName(ir.Name)
		if _, err := db.pool.Exec(ctx, AddColumnSQL(table, "updated_at", "TIMESTAMPTZ")); err != nil {
			return fmt.Errorf("migrate info register %s.updated_at: %w", ir.Name, err)
		}
		for _, f := range ir.Resources {
			if _, err := db.pool.Exec(ctx, AddColumnSQL(table, metadata.ColumnName(f), pgType(f))); err != nil {
				return fmt.Errorf("migrate info register %s.%s: %w", ir.Name, f.Name, err)
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
		// rename old snake_case columns before adding new ones
		db.renameSnakeCols(ctx, table, e.Fields)
		for _, f := range e.Fields {
			col := metadata.ColumnName(f)
			addSQL := AddColumnSQL(table, col, pgType(f))
			if _, err := db.pool.Exec(ctx, addSQL); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, f.Name, err)
			}
		}
		// soft-delete support
		if _, err := db.pool.Exec(ctx, AddColumnSQL(table, "deletion_mark", "BOOLEAN NOT NULL DEFAULT FALSE")); err != nil {
			return fmt.Errorf("migrate %s.deletion_mark: %w", e.Name, err)
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
