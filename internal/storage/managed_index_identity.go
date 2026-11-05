package storage

import (
	"context"
	"fmt"
	"sort"

	"github.com/ivantit66/onebase/internal/metadata"
)

// CheckEntityIndexIdentity is read-only, including on pre-registry databases.
// A table/column rename changes the exact definition signed by the registry.
// Ownership transfer is unsupported; neither adoption nor AllowDestructive
// authorizes rewriting a signature or treating its physical index as external.
func (db *DB) CheckEntityIndexIdentity(ctx context.Context, entities []*metadata.Entity) error {
	for _, e := range entities {
		table := metadata.TableName(e.Name)
		for _, old := range db.physicalLegacyNames(metadata.LegacySQLIdents(metadata.TableLogical(e.Name))) {
			registered, err := db.registeredIndexes(ctx, old)
			if err != nil {
				return err
			}
			if len(registered) > 0 {
				return indexIdentityError(old, table, firstRegisteredIndex(registered))
			}
		}
		registered, err := db.registeredIndexes(ctx, table)
		if err != nil || len(registered) == 0 {
			if err != nil {
				return err
			}
			continue
		}
		catalog, err := db.readEntityIndexes(ctx, table)
		if err != nil {
			return err
		}
		actual := map[string]EntityIndexDefinition{}
		for _, index := range catalog {
			actual[index.Name] = index
		}
		names := make([]string, 0, len(registered))
		for name := range registered {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			index := registered[name]
			if found, ok := actual[name]; !ok || found.Fingerprint() != index.Fingerprint() {
				return fmt.Errorf("%s.%s: managed index differs from catalog (signed columns %v → current columns %v); ownership transfer is unsupported; no indexes changed", table, name, index.Columns, found.Columns)
			}
		}
		plan, err := db.planLegacyColumns(ctx, table, logicalColumnNames(e.Fields))
		if err != nil {
			return err
		}
		for _, action := range plan {
			if action.Conflict != "" {
				return action.err()
			}
			for _, name := range names {
				for _, column := range registered[name].Columns {
					if column == action.Old {
						return indexIdentityError(table+"."+action.Old, table+"."+action.New, name)
					}
				}
			}
		}
	}
	return nil
}

func firstRegisteredIndex(indexes map[string]EntityIndexDefinition) string {
	names := make([]string, 0, len(indexes))
	for name := range indexes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names[0]
}

func indexIdentityError(old, target, index string) error {
	return fmt.Errorf("%s → %s: managed index %s: ownership transfer is unsupported; migration stopped before changes", old, target, index)
}

// Lock the actual existing names, then repeat the read-only gates before any
// setup DDL or schema-map write. Renames share the same transaction and locks.
// This leaves the existing per-entity restructuring and global CREATE-before-
// DROP index batch intact, including their deferred numerator failure behavior.
func (db *DB) prepareEntityLegacyIdents(ctx context.Context, entities []*metadata.Entity) error {
	specs := longIdentTables(entities, nil, nil, nil)
	var logicals, physical []string
	for _, spec := range specs {
		logicals = append(logicals, spec.logical)
		physical = append(physical, metadata.SQLIdent(spec.logical))
		physical = append(physical, db.physicalLegacyNames(metadata.LegacySQLIdents(spec.logical))...)
	}
	physical = uniqueStrings(physical)
	sort.Strings(physical)
	return db.WithTxScope(ctx, func(ctx context.Context) error {
		for _, table := range physical {
			exists, err := db.TableExists(ctx, table)
			if err != nil {
				return err
			}
			if exists {
				if err := db.lockEntityTable(ctx, table); err != nil {
					return err
				}
			}
		}
		if err := db.CheckLongIdents(ctx, entities, nil, nil, nil); err != nil {
			return err
		}
		if err := db.CheckEntityIndexIdentity(ctx, entities); err != nil {
			return err
		}
		claims := legacyTableClaims(logicals)
		// All tables precede columns: FK targets must already have current names.
		for _, spec := range specs {
			if err := db.renameLegacyTableClaimed(ctx, spec.logical, claims); err != nil {
				return err
			}
		}
		for _, spec := range specs {
			if err := db.renameLegacyColumns(ctx, metadata.SQLIdent(spec.logical), spec.columns); err != nil {
				return err
			}
		}
		return nil
	})
}
