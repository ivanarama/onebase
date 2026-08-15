package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/metadata"
)

// SQLite historically treated a NUMBER dimension's physical TEXT spelling as
// part of an information-register key. New writes are canonical, but an exact
// machine key emitted by InfoRegListWithKeyValues must keep addressing the old
// row. Resolution is therefore exact-first on SQLite, canonical second.
func (db *DB) resolveInfoRegLookupKey(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time) (map[string]any, error) {
	if err := validateInfoRegExactPeriod(ir, period); err != nil {
		return nil, err
	}
	if !db.IsSQLite() || !infoRegHasNumberDimension(ir) {
		return canonicalInfoRegKey(db.dialect, ir, dimKey)
	}
	raw := physicalInfoRegKey(db.dialect, ir, dimKey)
	exists, err := db.infoRegPhysicalKeyExists(ctx, ir, raw, period)
	if err != nil {
		return nil, err
	}
	if exists {
		return raw, nil
	}
	return canonicalInfoRegKey(db.dialect, ir, dimKey)
}

func (db *DB) resolveInfoRegLastLookupKey(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, onDate time.Time) (map[string]any, error) {
	if !db.IsSQLite() || !infoRegHasNumberDimension(ir) {
		return canonicalInfoRegKey(db.dialect, ir, dimKey)
	}
	raw := physicalInfoRegKey(db.dialect, ir, dimKey)
	where, args := physicalDimWhere(db.dialect, ir, raw, 1)
	args = append(args, onDate)
	query := fmt.Sprintf("SELECT 1 FROM %s WHERE %s AND period <= %s LIMIT 1",
		metadata.InfoRegTableName(ir.Name), where, db.dialect.Placeholder(len(args)))
	var one int
	err := db.QueryRow(ctx, query, args...).Scan(&one)
	if err == nil {
		return raw, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("info register %s legacy numeric key lookup: %w", ir.Name, err)
	}
	return canonicalInfoRegKey(db.dialect, ir, dimKey)
}

func (db *DB) resolveInfoRegWriteKey(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time) (map[string]any, error) {
	if err := validateInfoRegExactPeriod(ir, period); err != nil {
		return nil, err
	}
	if !db.IsSQLite() || !infoRegHasNumberDimension(ir) {
		return canonicalInfoRegKey(db.dialect, ir, dimKey)
	}

	// Check the raw spelling before parsing it. This is what keeps legacy keys
	// such as " 1.005 ", "1e0" or even old comma values maintainable: they are
	// accepted only when that exact physical row already exists.
	raw := physicalInfoRegKey(db.dialect, ir, dimKey)
	exists, err := db.infoRegPhysicalKeyExists(ctx, ir, raw, period)
	if err != nil {
		return nil, err
	}
	if exists {
		return raw, nil
	}

	canonical, err := canonicalInfoRegKey(db.dialect, ir, dimKey)
	if err != nil {
		return nil, err
	}
	exists, err = db.infoRegPhysicalKeyExists(ctx, ir, canonical, period)
	if err != nil {
		return nil, err
	}
	if exists {
		return canonical, nil
	}

	// A third spelling numerically equal to the canonical key is neither the
	// caller's exact machine key nor the new key. Inserting would silently make
	// another logical duplicate; updating it could overwrite the wrong historic
	// row when several spellings coexist. Fail explicitly instead.
	conflict, err := db.infoRegHasEquivalentLegacyKey(ctx, ir, canonical, period)
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, fmt.Errorf("info register %s: числовой ключ совпадает с существующим SQLite-ключом в прежнем написании; используйте точный машинный ключ", ir.Name)
	}
	return canonical, nil
}

func validateInfoRegExactPeriod(ir *metadata.InfoRegister, period *time.Time) error {
	if ir.Periodic && period == nil {
		return fmt.Errorf("info register %s is periodic: period is required", ir.Name)
	}
	if !ir.Periodic && period != nil {
		return fmt.Errorf("info register %s is non-periodic: period is not allowed", ir.Name)
	}
	return nil
}

func canonicalInfoRegKey(d Dialect, ir *metadata.InfoRegister, dimKey map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(ir.Dimensions))
	for _, field := range ir.Dimensions {
		value, err := normalizeRegField(d, field, dimKey[field.Name])
		if err != nil {
			return nil, fmt.Errorf("info register %s dimension %s: %w", ir.Name, field.Name, err)
		}
		result[field.Name] = value
	}
	return result, nil
}

func physicalInfoRegKey(d Dialect, ir *metadata.InfoRegister, dimKey map[string]any) map[string]any {
	result := make(map[string]any, len(ir.Dimensions))
	for _, field := range ir.Dimensions {
		result[field.Name] = physicalRegFieldArg(d, field, dimKey[field.Name])
	}
	return result
}

func physicalRegFieldArg(d Dialect, field metadata.Field, value any) any {
	// NUMBER is intentionally not parsed here: this path addresses an existing
	// legacy TEXT key byte-for-byte. References still need dialect UUID encoding.
	return normalizeRegArg(d, value, field.RefEntity != "")
}

func infoRegHasNumberDimension(ir *metadata.InfoRegister) bool {
	for _, field := range ir.Dimensions {
		if field.Type == metadata.FieldTypeNumber {
			return true
		}
	}
	return false
}

func (db *DB) infoRegPhysicalKeyExists(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time) (bool, error) {
	where, args := physicalDimWhere(db.dialect, ir, dimKey, 1)
	if ir.Periodic {
		if period == nil {
			return false, fmt.Errorf("info register %s is periodic: period is required", ir.Name)
		}
		where += " AND period = " + db.dialect.Placeholder(len(args)+1)
		args = append(args, *period)
	} else if period != nil {
		return false, fmt.Errorf("info register %s is non-periodic: period is not allowed", ir.Name)
	}
	var one int
	err := db.QueryRow(ctx, fmt.Sprintf("SELECT 1 FROM %s WHERE %s LIMIT 1",
		metadata.InfoRegTableName(ir.Name), where), args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("info register %s physical key lookup: %w", ir.Name, err)
	}
	return true, nil
}

func physicalDimWhere(d Dialect, ir *metadata.InfoRegister, dimKey map[string]any, startIdx int) (string, []any) {
	conditions := make([]string, 0, len(ir.Dimensions))
	args := make([]any, 0, len(ir.Dimensions))
	idx := startIdx
	for _, field := range ir.Dimensions {
		conditions = append(conditions, fmt.Sprintf("%s = %s", metadata.ColumnName(field), d.Placeholder(idx)))
		args = append(args, physicalRegFieldArg(d, field, dimKey[field.Name]))
		idx++
	}
	if len(conditions) == 0 {
		return "1=1", nil
	}
	return strings.Join(conditions, " AND "), args
}

func (db *DB) infoRegHasEquivalentLegacyKey(ctx context.Context, ir *metadata.InfoRegister,
	canonical map[string]any, period *time.Time) (bool, error) {
	var numberFields []metadata.Field
	var selectCols []string
	var conditions []string
	var args []any
	idx := 1
	if ir.Periodic {
		conditions = append(conditions, "period = "+db.dialect.Placeholder(idx))
		args = append(args, *period)
		idx++
	}
	for _, field := range ir.Dimensions {
		if field.Type == metadata.FieldTypeNumber {
			numberFields = append(numberFields, field)
			selectCols = append(selectCols, metadata.ColumnName(field))
			continue
		}
		conditions = append(conditions, fmt.Sprintf("%s = %s", metadata.ColumnName(field), db.dialect.Placeholder(idx)))
		args = append(args, physicalRegFieldArg(db.dialect, field, canonical[field.Name]))
		idx++
	}
	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectCols, ", "), metadata.InfoRegTableName(ir.Name))
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("info register %s legacy numeric key scan: %w", ir.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(numberFields))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return false, fmt.Errorf("info register %s legacy numeric key scan: %w", ir.Name, err)
		}
		equivalent := true
		for i, field := range numberFields {
			value, err := canonicalNumberArg(field, values[i])
			if err != nil || value != canonical[field.Name] {
				equivalent = false
				break
			}
		}
		if equivalent {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("info register %s legacy numeric key scan: %w", ir.Name, err)
	}
	return false, nil
}
