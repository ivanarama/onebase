package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// InfoRegEditRevisionColumn is local concurrency state, never a business field
// or a version supplied by an exchange partner or universal backup.
const InfoRegEditRevisionColumn = "_edit_revision"

// InfoRegEditKey contains the complete physical key. Dimensions returned by
// InfoRegReadForEdit retain the database spelling (including SQLite NUMBER).
type InfoRegEditKey struct {
	Dimensions map[string]any
	Period     *time.Time
}

// InfoRegEditRecord is internal edit-service input. Row contains only business
// values; Revision is deliberately separate from the public row projection.
type InfoRegEditRecord struct {
	Key      InfoRegEditKey
	Revision string
	Row      map[string]any
}

func (db *DB) validateInfoRegEditRevisionSchema(ctx context.Context, ir *metadata.InfoRegister) error {
	if ir == nil {
		return errors.New("info register edit revision: nil metadata")
	}
	for _, f := range append(append([]metadata.Field{}, ir.Dimensions...), ir.Resources...) {
		if strings.EqualFold(f.Name, InfoRegEditRevisionColumn) || strings.EqualFold(metadata.ColumnName(f), InfoRegEditRevisionColumn) {
			return fmt.Errorf("info register %s: reserved field %s", ir.Name, InfoRegEditRevisionColumn)
		}
	}
	stored, err := db.loadSchemaMap(ctx, metadata.InfoRegTableName(ir.Name))
	if err != nil {
		return err
	}
	for _, f := range stored {
		if strings.EqualFold(f.Column, InfoRegEditRevisionColumn) {
			return fmt.Errorf("info register %s: reserved column %s belongs to a business field", ir.Name, InfoRegEditRevisionColumn)
		}
	}
	return nil
}

// fillInfoRegEditRevisions runs inside the caller's locked migration or
// maintenance scope. Cursors are closed before UPDATE (SQLite has one connection).
// Physical row locators also cover legacy keyless registers; they never leave tx.
func (db *DB) fillInfoRegEditRevisions(ctx context.Context, ir *metadata.InfoRegister, invalidate bool) error {
	if !HasTx(ctx) {
		return errors.New("info register edit revisions require active transaction")
	}
	table := metadata.InfoRegTableName(ir.Name)
	locator := "ctid"
	cast := "::tid"
	if db.IsSQLite() {
		cast = ""
		cols, err := db.tableColumns(ctx, table)
		if err != nil {
			return err
		}
		locator = ""
		for _, candidate := range []string{"rowid", "_rowid_", "oid"} {
			shadowed := false
			for col := range cols {
				if strings.EqualFold(col, candidate) {
					shadowed = true
				}
			}
			if !shadowed {
				locator = candidate
				break
			}
		}
		if locator == "" {
			return fmt.Errorf("info register %s: no unshadowed physical row locator", ir.Name)
		}
	}
	// Validate all existing nonempty versions before changing any of them. Invalid
	// nonempty state is corruption, not a reason to silently revive stale forms.
	rows, err := db.Query(ctx, "SELECT "+InfoRegEditRevisionColumn+" FROM "+table+" WHERE "+InfoRegEditRevisionColumn+" IS NOT NULL AND "+InfoRegEditRevisionColumn+" <> ''")
	if err != nil {
		return err
	}
	for rows.Next() {
		var revision string
		if err := rows.Scan(&revision); err != nil {
			rows.Close()
			return err
		}
		if id, err := uuid.Parse(revision); err != nil || id == uuid.Nil {
			rows.Close()
			return fmt.Errorf("info register %s: invalid edit revision", ir.Name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Invalidate in one pass; backfill uses bounded batches, even on a large base.
	condition := InfoRegEditRevisionColumn + " IS NULL OR " + InfoRegEditRevisionColumn + " = ''"
	if invalidate {
		condition = "1=1"
	}
	query := "SELECT CAST(" + locator + " AS TEXT) FROM " + table + " WHERE " + condition
	if !invalidate {
		query += " LIMIT 512"
	}
	for {
		rows, err := db.Query(ctx, query)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			revision, err := uuid.NewRandom()
			if err != nil {
				return fmt.Errorf("info register %s edit revision: %w", ir.Name, err)
			}
			_, err = db.Exec(ctx, "UPDATE "+table+" SET "+InfoRegEditRevisionColumn+" = "+db.dialect.Placeholder(1)+" WHERE "+locator+" = "+db.dialect.Placeholder(2)+cast, revision.String(), id)
			if err != nil {
				return err
			}
		}
		if invalidate || len(ids) == 0 {
			return nil
		}
	}
}

// InvalidateInfoRegEditRevisions is for maintenance writers which change rows
// outside ordinary upsert. It deliberately invalidates every open expectation
// for the register. Call inside the same transaction as the maintenance write.
func (db *DB) InvalidateInfoRegEditRevisions(ctx context.Context, ir *metadata.InfoRegister) error {
	if ir == nil {
		return errors.New("info register edit revisions: nil metadata")
	}
	if err := db.LockInfoRegisterForPolicyWrite(ctx, ir); err != nil {
		return err
	}
	return db.fillInfoRegEditRevisions(ctx, ir, true)
}

// A deferred destructive migration can leave a wider physical PK than current
// metadata. Such a key is incomplete even when its current lookup has one row.
func (db *DB) infoRegCheckEditPrimaryKey(ctx context.Context, ir *metadata.InfoRegister) error {
	table := metadata.InfoRegTableName(ir.Name)
	var actual []string
	if db.IsSQLite() {
		rows, err := db.Query(ctx, "SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk", table)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			actual = append(actual, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	} else {
		var err error
		actual, _, err = db.pgPrimaryKey(ctx, table)
		if err != nil {
			return err
		}
	}
	if !stringSlicesEqual(actual, pkCols(ir)) {
		return fmt.Errorf("info register %s: physical primary key differs from metadata", ir.Name)
	}
	return nil
}

// InfoRegReadForEdit performs one exact SQL read with SQL read RLS. No form or
// mutation endpoint is enabled by this storage primitive. Callers must enforce
// object/field rights before passing a locator or exposing the returned values.
// nil readFilter is the unrestricted admin case. Missing and filtered rows both
// return nil; malformed keys, recorder rows and invalid revisions fail closed.
func (db *DB) InfoRegReadForEdit(ctx context.Context, ir *metadata.InfoRegister, key InfoRegEditKey, readFilter *Predicate) (*InfoRegEditRecord, error) {
	if ir == nil {
		return nil, errors.New("info register read for edit: nil metadata")
	}
	if ir.Recorder {
		return nil, fmt.Errorf("info register %s: recorder register cannot be edited manually", ir.Name)
	}
	if len(pkCols(ir)) == 0 {
		return nil, fmt.Errorf("info register %s: no primary key", ir.Name)
	}
	if len(key.Dimensions) != len(ir.Dimensions) {
		return nil, fmt.Errorf("info register %s: complete dimension key required", ir.Name)
	}
	for _, f := range ir.Dimensions {
		v, ok := key.Dimensions[f.Name]
		if !ok || v == nil {
			return nil, fmt.Errorf("info register %s: missing dimension %s", ir.Name, f.Name)
		}
	}
	if err := db.infoRegCheckEditPrimaryKey(ctx, ir); err != nil {
		return nil, err
	}
	lookup, err := db.resolveInfoRegLookupKey(ctx, ir, key.Dimensions, key.Period)
	if err != nil {
		return nil, err
	}
	where, args := physicalDimWhere(db.dialect, ir, lookup, 1)
	// A periodic register without dimensions still has an exact period key.
	if len(ir.Dimensions) == 0 {
		where = "1=1"
	}
	if key.Period != nil {
		where += " AND period = " + db.dialect.Placeholder(len(args)+1)
		args = append(args, *key.Period)
	}
	table := metadata.InfoRegTableName(ir.Name)
	condition, filterArgs, _, err := PredicateSQLQualified(db.dialect, InfoRegisterPredicateEntity(ir), readFilter, len(args)+1, table)
	if err != nil {
		return nil, err
	}
	if readFilter != nil && condition == "" {
		return nil, errors.New("info register read for edit: empty row filter")
	}
	if condition != "" {
		where = "(" + where + ") AND (" + condition + ")"
		args = append(args, filterArgs...)
	}
	cols := resourceAndDimCols(ir)
	if ir.Periodic {
		cols = append([]string{"period"}, cols...)
	}
	// Keep revision out of the business row, using the same field and key
	// normalizers as ordinary information-register reads.
	rows, err := db.Query(ctx, "SELECT "+strings.Join(cols, ", ")+", recorder, "+InfoRegEditRevisionColumn+" FROM "+table+" WHERE "+where+" LIMIT 1", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	values := make([]any, len(cols)+2)
	ptrs := make([]any, len(values))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	if owner := normalizeValue(values[len(cols)]); owner != nil && fmt.Sprint(owner) != "" {
		return nil, fmt.Errorf("info register %s: row has recorder", ir.Name)
	}
	revision, ok := normalizeValue(values[len(cols)+1]).(string)
	if !ok {
		return nil, fmt.Errorf("info register %s: missing edit revision", ir.Name)
	}
	if id, err := uuid.Parse(revision); err != nil || id == uuid.Nil {
		return nil, fmt.Errorf("info register %s: invalid edit revision", ir.Name)
	}
	record := &InfoRegEditRecord{Key: InfoRegEditKey{Dimensions: map[string]any{}}, Revision: revision, Row: map[string]any{}}
	i := 0
	if ir.Periodic {
		p, ok := normalizeDate(values[0]).(time.Time)
		if !ok {
			return nil, fmt.Errorf("info register %s: invalid period", ir.Name)
		}
		record.Key.Period = &p
		record.Row["period"] = p
		i++
	}
	for _, f := range ir.Dimensions {
		value := normalizeFieldValue(f, values[i])
		record.Row[f.Name] = value
		record.Key.Dimensions[f.Name] = value
		// Only NUMBER/DATE need lexical spelling. Boolean machine keys must
		// stay typed: binding TEXT "true" would miss SQLite INTEGER 1 when
		// a numeric dimension selects the exact-first lookup path.
		if f.Type == metadata.FieldTypeNumber || f.Type == metadata.FieldTypeDate {
			record.Key.Dimensions[f.Name] = infoRegKeyText(f, values[i], value)
		}
		i++
	}
	for _, f := range ir.Resources {
		record.Row[f.Name] = normalizeFieldValue(f, values[i])
		i++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return record, nil
}
