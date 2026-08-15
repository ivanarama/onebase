package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// InfoRegGetExact reads the exact record by full primary key (dimensions, plus
// period for periodic registers). Returns (nil, nil) if there is no such record.
// Used by exchange (план 86) to re-read a changed record's current resources when
// building an outgoing package.
func (db *DB) InfoRegGetExact(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, period *time.Time) (map[string]any, error) {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	lookupKey, err := db.resolveInfoRegLookupKey(ctx, ir, dimKey, period)
	if err != nil {
		return nil, err
	}
	where, args := physicalDimWhere(d, ir, lookupKey, 1)
	if ir.Periodic && period != nil {
		where = fmt.Sprintf("%s AND period = %s", where, d.Placeholder(len(args)+1))
		args = append(args, *period)
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1",
		strings.Join(resourceAndDimCols(ir), ", "), table, where)
	record, err := db.infoRegScan(ctx, ir, query, args)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return record, err
}

// InfoRegGetExactWithKeyValues is the exchange-registration variant of
// InfoRegGetExact. The returned record also carries InfoRegKeyValuesField with
// the physical primary-key spelling selected by the database. This readback is
// what keeps an initial exchange object ID identical to a later tombstone when
// PostgreSQL applies NUMERIC typmods or SQLite stores a time.Time as TEXT.
func (db *DB) InfoRegGetExactWithKeyValues(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time) (map[string]any, error) {
	lookupKey, err := db.resolveInfoRegLookupKey(ctx, ir, dimKey, period)
	if err != nil {
		return nil, err
	}
	return db.infoRegGetExactWithKeyValues(ctx, ir, lookupKey, period)
}

func (db *DB) infoRegGetExactWithKeyValues(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time) (map[string]any, error) {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	where, args := physicalDimWhere(d, ir, dimKey, 1)
	if ir.Periodic && period != nil {
		where = fmt.Sprintf("%s AND period = %s", where, d.Placeholder(len(args)+1))
		args = append(args, *period)
	}
	cols := resourceAndDimCols(ir)
	if ir.Periodic {
		cols = append([]string{"period"}, cols...)
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1", strings.Join(cols, ", "), table, where)
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records, err := scanInfoRegRowsMode(rows, ir, cols, true)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// InfoRegExactMatchesRowFilter reports whether the row identified by the full
// primary key satisfies rowFilter. Unlike RegFilter, every dimension value is
// significant here, including an empty string. This is the fail-closed SQL
// authority used after a provisional record-set upsert: an allowed sibling in
// the same slice must never stand in for the row which was actually written.
func (db *DB) InfoRegExactMatchesRowFilter(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time, rowFilter *Predicate) (bool, error) {
	if rowFilter == nil {
		return false, errors.New("info register exact row filter is required")
	}
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	lookupKey, err := db.resolveInfoRegLookupKey(ctx, ir, dimKey, period)
	if err != nil {
		return false, err
	}
	where, args := physicalDimWhere(d, ir, lookupKey, 1)
	if ir.Periodic {
		if period == nil {
			return false, fmt.Errorf("info register %s is periodic: period is required", ir.Name)
		}
		where = fmt.Sprintf("%s AND period = %s", where, d.Placeholder(len(args)+1))
		args = append(args, *period)
	} else if period != nil {
		return false, fmt.Errorf("info register %s is non-periodic: period is not allowed", ir.Name)
	}
	condition, filterArgs, _, err := PredicateSQLQualified(
		d, InfoRegisterPredicateEntity(ir), rowFilter, len(args)+1, table,
	)
	if err != nil {
		return false, fmt.Errorf("info register %s exact row filter: %w", ir.Name, err)
	}
	if condition == "" {
		return false, fmt.Errorf("info register %s exact row filter is empty", ir.Name)
	}
	args = append(args, filterArgs...)
	query := fmt.Sprintf("SELECT 1 FROM %s WHERE (%s) AND (%s) LIMIT 1", table, where, condition)
	var one int
	if err := db.QueryRow(ctx, query, args...).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("info register %s exact row filter query: %w", ir.Name, err)
	}
	return true, nil
}

// InfoRegApplyExchange применяет запись регистра сведений из пакета обмена
// (план 86). Значения измерений/ресурсов проходят через те же канонические
// storage-boundary функции, что обычная запись; deletion=true удаляет запись
// по exact-first ключу, сохраняя адресуемость legacy SQLite NUMBER keys.
func (db *DB) InfoRegApplyExchange(ctx context.Context, ir *metadata.InfoRegister, dims, resources map[string]any, period *time.Time, deletion bool) error {
	if deletion {
		return db.InfoRegDelete(ctx, ir, dims, period)
	}
	return db.InfoRegSet(ctx, ir, dims, resources, period)
}

// InfoRegSet upserts a record in an info register.
// For periodic registers, period must be non-nil.
func (db *DB) InfoRegSet(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, resources map[string]any, period *time.Time) error {
	return db.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		_, err := db.infoRegSet(txCtx, ir, dimKey, resources, period, nil)
		return err
	})
}

// InfoRegSetIfExistingAllowed inserts a new information-register row or
// updates an existing row only when that existing row matches existingFilter.
// A filtered conflict is an intentional no-op and returns (false, nil).
//
// Record-set replacement uses this after its filtered DELETE. Without the
// conflict predicate, a row hidden by write/delete RLS could survive DELETE
// and then be overwritten by ON CONFLICT, disclosing its existence and
// bypassing the policy. The caller must separately validate the proposed row.
func (db *DB) InfoRegSetIfExistingAllowed(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, resources map[string]any, period *time.Time,
	existingFilter *Predicate) (bool, error) {
	if existingFilter == nil {
		return false, errors.New("info register conditional upsert: existing-row filter is required")
	}
	var changed bool
	err := db.WithTxIfNeeded(ctx, func(txCtx context.Context) error {
		var err error
		changed, err = db.infoRegSet(txCtx, ir, dimKey, resources, period, existingFilter)
		return err
	})
	return changed, err
}

func (db *DB) infoRegSet(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, resources map[string]any, period *time.Time,
	existingFilter *Predicate) (bool, error) {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	writeKey, err := db.resolveInfoRegWriteKey(ctx, ir, dimKey, period)
	if err != nil {
		return false, err
	}

	cols := []string{}
	phs := []string{}
	args := []any{}
	idx := 1

	if ir.Periodic {
		if period == nil {
			return false, fmt.Errorf("info register %s is periodic: period is required", ir.Name)
		}
		cols = append(cols, "period")
		phs = append(phs, d.Placeholder(idx))
		args = append(args, *period)
		idx++
	}

	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		cols = append(cols, col)
		phs = append(phs, d.Placeholder(idx))
		args = append(args, physicalRegFieldArg(d, f, writeKey[f.Name]))
		idx++
	}
	for _, f := range ir.Resources {
		col := metadata.ColumnName(f)
		cols = append(cols, col)
		phs = append(phs, d.Placeholder(idx))
		value, err := normalizeRegField(d, f, resources[f.Name])
		if err != nil {
			return false, fmt.Errorf("info register %s resource %s: %w", ir.Name, f.Name, err)
		}
		args = append(args, value)
		idx++
	}
	cols = append(cols, "updated_at")
	phs = append(phs, d.Placeholder(idx))
	args = append(args, time.Now())

	// Build ON CONFLICT update clause for all non-PK columns
	var updates []string
	for _, f := range ir.Resources {
		col := metadata.ColumnName(f)
		updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
	}
	updates = append(updates, "updated_at = EXCLUDED.updated_at")

	sqlText := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		table,
		strings.Join(cols, ", "),
		strings.Join(phs, ", "),
		strings.Join(pkCols(ir), ", "),
		strings.Join(updates, ", "),
	)
	if existingFilter != nil {
		condition, filterArgs, _, err := PredicateSQLQualified(
			d, InfoRegisterPredicateEntity(ir), existingFilter, len(args)+1, table,
		)
		if err != nil {
			return false, fmt.Errorf("info register %s conditional upsert filter: %w", ir.Name, err)
		}
		if condition == "" {
			return false, fmt.Errorf("info register %s conditional upsert: empty existing-row filter", ir.Name)
		}
		sqlText += " WHERE " + condition
		args = append(args, filterArgs...)
	}
	tag, err := db.Exec(ctx, sqlText, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected > 0, nil
}

// InfoRegGet returns the record matching the given dimension key (non-periodic).
func (db *DB) InfoRegGet(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any) (map[string]any, error) {
	table := metadata.InfoRegTableName(ir.Name)
	allCols := resourceAndDimCols(ir)
	lookupKey, err := db.resolveInfoRegLookupKey(ctx, ir, dimKey, nil)
	if err != nil {
		return nil, err
	}
	where, args := physicalDimWhere(db.dialect, ir, lookupKey, 1)
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1",
		strings.Join(allCols, ", "), table, where)
	return db.infoRegScan(ctx, ir, sql, args)
}

// InfoRegGetLast returns the most recent record on or before onDate for the given dimensions.
func (db *DB) InfoRegGetLast(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, onDate time.Time) (map[string]any, error) {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	allCols := append([]string{"period"}, resourceAndDimCols(ir)...)
	lookupKey, err := db.resolveInfoRegLastLookupKey(ctx, ir, dimKey, onDate)
	if err != nil {
		return nil, err
	}
	where, args := physicalDimWhere(d, ir, lookupKey, 1)
	args = append(args, onDate)
	sql := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s AND period <= %s ORDER BY period DESC LIMIT 1",
		strings.Join(allCols, ", "), table, where, d.Placeholder(len(args)))
	return db.infoRegScanWithPeriod(ctx, ir, sql, args)
}

// InfoRegKeyValuesField is the reserved row member used only by
// InfoRegListWithKeyValues. Its value is map[string]string with the lossless
// database representation of every dimension in the primary key. The NUL
// prefix cannot collide with a SQL/metadata field name.
//
// A separate transport value is necessary on SQLite: NUMBER and DATE columns
// are TEXT, so a key such as "1.00" or an RFC3339 date must not be silently
// reformatted into a different physical primary-key value by an HTTP round trip.
const InfoRegKeyValuesField = "\x00onebase_info_reg_key_values"

// InfoRegList returns records, optionally filtered by dimension values and
// period (период учитывается только для periodic-регистров, issue #45).
func (db *DB) InfoRegList(ctx context.Context, ir *metadata.InfoRegister, f RegFilter) ([]map[string]any, error) {
	return db.infoRegList(ctx, ir, f, false)
}

// InfoRegListWithKeyValues is the UI-list variant of InfoRegList. In addition
// to the typed display values, each row carries InfoRegKeyValuesField so a
// delete form can round-trip the exact stored primary key. Callers must remove
// that member before exposing a row whose key is field-masked.
func (db *DB) InfoRegListWithKeyValues(ctx context.Context, ir *metadata.InfoRegister, f RegFilter) ([]map[string]any, error) {
	return db.infoRegList(ctx, ir, f, true)
}

func (db *DB) infoRegList(ctx context.Context, ir *metadata.InfoRegister, f RegFilter, withKeyValues bool) ([]map[string]any, error) {
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

	where, args, err := dimWhereClause(db.dialect, ir.Dimensions, f, 1, ir.Periodic, ir.Periodic)
	if err != nil {
		return nil, fmt.Errorf("info reg list %s: %w", ir.Name, err)
	}
	whereParts := make([]string, 0, 2)
	if where != "" {
		whereParts = append(whereParts, where)
	}
	if cond, condArgs, _, err := PredicateSQLQualified(
		db.dialect, InfoRegisterPredicateEntity(ir), f.RowFilter, len(args)+1, table,
	); err != nil {
		return nil, fmt.Errorf("info reg list %s row filter: %w", ir.Name, err)
	} else if cond != "" {
		whereParts = append(whereParts, cond)
		args = append(args, condArgs...)
	}
	orderBy := strings.Join(pkCols(ir), ", ")
	sql := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selCols, ", "), table)
	if len(whereParts) > 0 {
		sql += " WHERE " + strings.Join(whereParts, " AND ")
	}
	sql += " ORDER BY " + orderBy

	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("info reg list %s: %w", ir.Name, err)
	}
	defer rows.Close()
	raw, err := scanInfoRegRowsMode(rows, ir, selCols, withKeyValues)
	if err != nil {
		return nil, err
	}
	return infoRegListRows(ir, raw), nil
}

// scanInfoRegRowsMode decodes the common storage projection used by InfoRegList
// and by DELETE ... RETURNING. It deliberately keeps the system period as a
// typed time.Time. In particular, delete-RLS must compare the database value
// rather than the localized display string produced for the HTML list.
func scanInfoRegRowsMode(rows Rows, ir *metadata.InfoRegister, selCols []string, withKeyValues bool) ([]map[string]any, error) {
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
			period := normalizeDate(dest[0])
			typed, ok := period.(time.Time)
			if !ok {
				return nil, fmt.Errorf("info register %s: invalid stored period %T(%v)", ir.Name, dest[0], dest[0])
			}
			row["period"] = typed
			i = 1
		}
		var keyValues map[string]string
		if withKeyValues {
			keyValues = make(map[string]string, len(ir.Dimensions))
		}
		for _, f := range ir.Dimensions {
			normalized := normalizeFieldValue(f, dest[i])
			row[f.Name] = normalized
			if withKeyValues {
				keyValues[f.Name] = infoRegKeyText(f, dest[i], normalized)
			}
			i++
		}
		if withKeyValues {
			row[InfoRegKeyValuesField] = keyValues
		}
		for _, f := range ir.Resources {
			row[f.Name] = normalizeFieldValue(f, dest[i])
			i++
		}
		// DELETE ... RETURNING additionally projects every system field that
		// may occur in an information-register row policy. InfoRegList does not
		// expose these transport columns, so they are optional here.
		if i < len(selCols) && strings.EqualFold(selCols[i], "recorder") {
			row["recorder"] = normalizeValue(dest[i])
			i++
		}
		if i < len(selCols) && strings.EqualFold(selCols[i], "recorder_type") {
			row["recorder_type"] = normalizeValue(dest[i])
			i++
		}
		if i != len(selCols) {
			return nil, fmt.Errorf("info register %s: unsupported row projection %v", ir.Name, selCols[i:])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func infoRegKeyText(field metadata.Field, raw, normalized any) string {
	// SQLite TEXT columns must retain their exact lexical representation. In
	// particular, decimal.Decimal.String() intentionally trims zeroes and would
	// turn a stored primary-key component "1.00" into the different TEXT "1".
	if field.RefEntity != "" {
		return fmt.Sprint(normalizeValue(raw))
	}
	if field.Type == metadata.FieldTypeNumber || field.Type == metadata.FieldTypeDate {
		switch value := raw.(type) {
		case string:
			return value
		case []byte:
			return string(value)
		}
	}
	switch value := normalized.(type) {
	case decimal.Decimal:
		if scale := -value.Exponent(); scale > 0 {
			return value.StringFixed(scale)
		}
		return value.String()
	case time.Time:
		return value.UTC().Format(time.RFC3339Nano)
	case *time.Time:
		if value == nil {
			return ""
		}
		return value.UTC().Format(time.RFC3339Nano)
	}
	if field.Type == metadata.FieldTypeBool {
		switch value := raw.(type) {
		case bool:
			return fmt.Sprintf("%t", value)
		case int64:
			return fmt.Sprintf("%t", value != 0)
		case int:
			return fmt.Sprintf("%t", value != 0)
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "1":
				return "true"
			case "false", "0":
				return "false"
			}
		}
	}
	switch value := raw.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	}
	return fmt.Sprint(normalized)
}

// infoRegListRows is the presentation boundary for the list/UI contract.
// Storage scanners keep period typed for policy evaluation; only an ordinary
// list gets the localized cell value and the lossless machine key used by HTTP
// delete and by the DSL record-set adapter.
func infoRegListRows(ir *metadata.InfoRegister, raw []map[string]any) []map[string]any {
	if !ir.Periodic {
		return raw
	}
	out := make([]map[string]any, 0, len(raw))
	for _, source := range raw {
		row := make(map[string]any, len(source)+1)
		for name, value := range source {
			row[name] = value
		}
		period := source["period"].(time.Time)
		row["period"] = period.In(time.Local).Format("02.01.2006")
		// Ключ — В UTC. Иначе его текст зависит от таймзоны ПРОЦЕССА: pgx по
		// умолчанию сканирует timestamptz в time.Local, и одна и та же запись у
		// приложения в Europe/Moscow давала «…T12:30:45.123+03:00», а в UTC —
		// «…T09:30:45.123Z» (#945). Момент один, текст разный — а этим текстом
		// строка адресуется на удаление и едет в пакет обмена. Разбор обратно
		// принимает обе формы (regPeriodLayouts), поэтому старые ключи из уже
		// открытых форм продолжают работать.
		row["period_key"] = period.UTC().Format(time.RFC3339Nano)
		out = append(out, row)
	}
	return out
}

// regPeriodLayouts — форматы, которыми период записи регистра сведений
// сериализуется в period_key (InfoRegList) и принимается обратно при удалении.
// RFC3339 несёт инстант (PostgreSQL timestamptz); зононезависимые форматы —
// стенные часы (SQLite TEXT, см. sqliteTimeLayout). time.Parse трактует
// зононезависимый ввод как UTC; normalizeSQLiteArgs тоже хранит UTC,
// поэтому сравнение period одинаково на SQLite и PostgreSQL.
var regPeriodLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05-07",
	"2006-01-02 15:04:05Z07:00",
}

var localRegPeriodLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02",
}

// ParseRegPeriod разбирает машинный ключ периода (period_key) обратно в time.Time.
// Возвращает (_, false), если значение не распознано — вызывающая сторона ОБЯЗАНА
// отказать в удалении, а не продолжать с nil-периодом (иначе DELETE сносит все
// периоды комбинации измерений).
func ParseRegPeriod(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range regPeriodLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	// Legacy SQLite values did not contain an offset and represented local
	// wall time. Preserve that interpretation while all new values carry UTC.
	for _, layout := range localRegPeriodLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// InfoRegDelete removes a record by its primary key.
func (db *DB) InfoRegDelete(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, period *time.Time) error {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	lookupKey, err := db.resolveInfoRegLookupKey(ctx, ir, dimKey, period)
	if err != nil {
		return err
	}
	args := []any{}
	conds := []string{}
	idx := 1
	if ir.Periodic && period != nil {
		conds = append(conds, fmt.Sprintf("period = %s", d.Placeholder(idx)))
		args = append(args, *period)
		idx++
	}
	for _, f := range ir.Dimensions {
		conds = append(conds, fmt.Sprintf("%s = %s", metadata.ColumnName(f), d.Placeholder(idx)))
		args = append(args, physicalRegFieldArg(d, f, lookupKey[f.Name]))
		idx++
	}
	if len(conds) == 0 {
		return fmt.Errorf("info reg delete: no key provided")
	}
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", table, strings.Join(conds, " AND "))
	return db.exec(ctx, sql, args...)
}

// WriteInfoMovements заменяет все строки info-регистра, ранее записанные
// данным документом (recorder). Затем INSERT новых строк. Замечание #23:
// до этого «Движения.X.Добавить()» для info-регистров тихо терялся —
// saveMovements не обрабатывал InfoRegister'ы, и pending-строки никто
// не материализовывал в БД.
//
// Каждая строка должна содержать значения измерений и ресурсов; если
// регистр periodic — то либо row["Период"], либо общий period из mc.Period.
// recorder/recorder_type заполняются автоматически из аргументов.
//
// При перезаписи строк используется ON CONFLICT по PK — это безопасно
// для регистров, чья primary key включает (period, dims) и где нет
// конфликта с другими источниками (например, ручной ввод того же набора).
func (db *DB) WriteInfoMovements(ctx context.Context, regName, recorderType string, recorderID uuid.UUID, rows []map[string]any, ir *metadata.InfoRegister, period *time.Time) error {
	return db.WithTxScope(ctx, func(txCtx context.Context) error {
		return db.writeInfoMovementsInTx(txCtx, regName, recorderType, recorderID, rows, ir, period)
	})
}

func (db *DB) writeInfoMovementsInTx(ctx context.Context, regName, recorderType string, recorderID uuid.UUID, rows []map[string]any, ir *metadata.InfoRegister, period *time.Time) error {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)

	if err := db.exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE recorder = %s AND recorder_type = %s",
			table, d.Placeholder(1), d.Placeholder(2)),
		idArg(d, recorderID), recorderType,
	); err != nil {
		return fmt.Errorf("clear info movements %s: %w", regName, err)
	}

	for i, row := range rows {
		cols := []string{}
		phs := []string{}
		args := []any{}
		idx := 1
		var rowPeriod *time.Time

		if ir.Periodic {
			// Период: явно в row либо общий период документа.
			var p time.Time
			switch v := ciGet(row, "Период").(type) {
			case time.Time:
				p = v
			default:
				if period != nil {
					p = *period
				} else {
					return i18nerr.Errorf("info register %s: row %d has no Период and document has no period", regName, i+1)
				}
			}
			cols = append(cols, "period")
			phs = append(phs, d.Placeholder(idx))
			args = append(args, p)
			idx++
			rowPeriod = &p
		}

		dimKey := make(map[string]any, len(ir.Dimensions))
		for _, f := range ir.Dimensions {
			dimKey[f.Name] = ciGet(row, f.Name)
		}
		writeKey, err := db.resolveInfoRegWriteKey(ctx, ir, dimKey, rowPeriod)
		if err != nil {
			return fmt.Errorf("write info movement %s row %d key: %w", regName, i+1, err)
		}
		for _, f := range ir.Dimensions {
			col := metadata.ColumnName(f)
			cols = append(cols, col)
			phs = append(phs, d.Placeholder(idx))
			args = append(args, physicalRegFieldArg(d, f, writeKey[f.Name]))
			idx++
		}
		for _, f := range ir.Resources {
			col := metadata.ColumnName(f)
			cols = append(cols, col)
			phs = append(phs, d.Placeholder(idx))
			v := ciGet(row, f.Name)
			var err error
			v, err = normalizeRegField(d, f, v)
			if err != nil {
				return fmt.Errorf("write info movement %s row %d resource %s: %w", regName, i+1, f.Name, err)
			}
			args = append(args, v)
			idx++
		}
		cols = append(cols, "recorder", "recorder_type", "updated_at")
		phs = append(phs, d.Placeholder(idx), d.Placeholder(idx+1), d.Placeholder(idx+2))
		args = append(args, idArg(d, recorderID), recorderType, time.Now())

		// ON CONFLICT update: переписываем не-PK колонки. Без OR REPLACE,
		// чтобы PG/SQLite одинаково отработали (PG не понимает OR REPLACE,
		// а SQLite понимает оба).
		var updates []string
		for _, f := range ir.Resources {
			c := metadata.ColumnName(f)
			updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", c, c))
		}
		updates = append(updates,
			"recorder = EXCLUDED.recorder",
			"recorder_type = EXCLUDED.recorder_type",
			"updated_at = EXCLUDED.updated_at",
		)

		pk := pkCols(ir)
		var sql string
		if len(pk) > 0 {
			sql = fmt.Sprintf(
				"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
				table,
				strings.Join(cols, ", "),
				strings.Join(phs, ", "),
				strings.Join(pk, ", "),
				strings.Join(updates, ", "),
			)
		} else {
			sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
				table, strings.Join(cols, ", "), strings.Join(phs, ", "))
		}
		if err := db.exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("write info movement %s row %d: %w", regName, i+1, err)
		}
	}
	return nil
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

func (db *DB) infoRegScan(ctx context.Context, ir *metadata.InfoRegister, sql string, args []any) (map[string]any, error) {
	return db.infoRegScanMode(ctx, ir, sql, args, false)
}

func (db *DB) infoRegScanWithPeriod(ctx context.Context, ir *metadata.InfoRegister, sql string, args []any) (map[string]any, error) {
	return db.infoRegScanMode(ctx, ir, sql, args, true)
}

func (db *DB) infoRegScanMode(ctx context.Context, ir *metadata.InfoRegister, sql string, args []any, withPeriod bool) (map[string]any, error) {
	row := db.QueryRow(ctx, sql, args...)
	allCols := resourceAndDimCols(ir)
	offset := 0
	if withPeriod {
		offset = 1
	}
	dest := make([]any, len(allCols)+offset)
	ptrs := make([]any, len(dest))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(allCols)+offset)
	i := offset
	if withPeriod {
		period := normalizeDate(dest[0])
		typed, ok := period.(time.Time)
		if !ok {
			return nil, fmt.Errorf("info register %s: invalid stored period %T(%v)", ir.Name, dest[0], dest[0])
		}
		result["period"] = typed
	}
	for _, f := range ir.Dimensions {
		result[f.Name] = normalizeFieldValue(f, dest[i])
		i++
	}
	for _, f := range ir.Resources {
		result[f.Name] = normalizeFieldValue(f, dest[i])
		i++
	}
	return result, nil
}

// InfoRegDeleteByFilter удаляет строки регистра сведений по ОТБОРУ (подмножеству
// измерений и, для периодического регистра, диапазону периода). Нужен набору
// записей: «записать набор» — это заместить содержимое по отбору, то есть
// удалить старое и вставить накопленное одной транзакцией.
//
// Пустой отбор отклоняется намеренно. `Набор.Записать()` без отбора означал бы
// «снести регистр целиком и положить пару строк» — самая дорогая опечатка из
// возможных, и заметили бы её не сразу.
func (db *DB) InfoRegDeleteByFilter(ctx context.Context, ir *metadata.InfoRegister, f RegFilter) error {
	if f.IsEmpty() {
		return fmt.Errorf("info reg delete by filter %s: пустой отбор", ir.Name)
	}
	where, args, err := dimWhereClause(db.dialect, ir.Dimensions, f, 1, ir.Periodic, ir.Periodic)
	if err != nil {
		return fmt.Errorf("info reg delete by filter %s: %w", ir.Name, err)
	}
	if where == "" {
		return fmt.Errorf("info reg delete by filter %s: пустой отбор", ir.Name)
	}
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", metadata.InfoRegTableName(ir.Name), where)
	return db.exec(ctx, sql, args...)
}

// LockInfoRegisterForPolicyWrite serializes a policy pre-read plus its write
// with every ordinary INSERT/UPDATE/DELETE on the same information-register
// table. Row locks cannot protect an absent key, so without this boundary a
// concurrent INSERT can land after the pre-read/DELETE and be overwritten by
// ON CONFLICT without ever passing the caller's row policy.
//
// PostgreSQL uses a SHARE ROW EXCLUSIVE table lock, conflicting with writers'
// ordinary ROW EXCLUSIVE locks. SQLite starts its write transaction with an
// empty DELETE; the real policy read/write then runs while other writers are
// excluded. Both mechanisms are transaction-scoped, so a non-transactional
// context is rejected.
func (db *DB) LockInfoRegisterForPolicyWrite(ctx context.Context, ir *metadata.InfoRegister) error {
	if !HasTx(ctx) {
		return errors.New("info register policy write lock requires active storage transaction")
	}
	table := metadata.InfoRegTableName(ir.Name)
	if !db.IsPostgres() {
		if _, err := db.Exec(ctx, "DELETE FROM "+table+" WHERE 1=0"); err != nil {
			return fmt.Errorf("info register %s policy write lock: %w", ir.Name, err)
		}
		return nil
	}
	if _, err := db.Exec(ctx, "SET LOCAL lock_timeout = '"+advisoryLockTimeout+"'"); err != nil {
		return fmt.Errorf("info register %s policy write lock timeout: %w", ir.Name, err)
	}
	if _, err := db.Exec(ctx, "LOCK TABLE "+table+" IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		if isLockTimeoutErr(err) {
			return fmt.Errorf("info register %s policy write lock not acquired in %s: %w", ir.Name, advisoryLockTimeout, err)
		}
		return fmt.Errorf("info register %s policy write lock: %w", ir.Name, err)
	}
	if _, err := db.Exec(ctx, "SET LOCAL lock_timeout = '0'"); err != nil {
		return fmt.Errorf("info register %s policy write lock timeout reset: %w", ir.Name, err)
	}
	return nil
}

// InfoRegDeleteByFilterReturning atomically deletes the selected slice and
// returns the rows that the DELETE statement actually removed. A preceding
// SELECT is not equivalent: under PostgreSQL READ COMMITTED (and between
// SQLite autocommit statements) concurrent changes can make that snapshot
// stale, causing missing/phantom exchange tombstones and an RLS TOCTOU gap.
//
// The caller is expected to run this inside WithTxScope. It may then validate
// delete access against the returned rows and return an error; the scope rolls
// the provisional DELETE back, including when it is nested in a caller-owned
// transaction.
func (db *DB) InfoRegDeleteByFilterReturning(ctx context.Context, ir *metadata.InfoRegister, f RegFilter) ([]map[string]any, error) {
	if f.IsEmpty() {
		return nil, fmt.Errorf("info reg delete by filter %s: пустой отбор", ir.Name)
	}
	where, args, err := dimWhereClause(db.dialect, ir.Dimensions, f, 1, ir.Periodic, ir.Periodic)
	if err != nil {
		return nil, fmt.Errorf("info reg delete by filter %s: %w", ir.Name, err)
	}
	if where == "" {
		return nil, fmt.Errorf("info reg delete by filter %s: пустой отбор", ir.Name)
	}
	return db.infoRegDeleteReturning(ctx, ir, where, args, f.RowFilter, "info reg delete by filter", false)
}

// InfoRegDeleteExactReturning atomically deletes at most one information-
// register row by its complete primary key and returns the row actually
// removed. Every declared dimension is significant, including an empty string;
// this is intentionally different from the slice semantics of RegFilter.
// rowFilter is appended to the same DELETE statement, closing the RLS TOCTOU
// window between a policy read and the write. Returned rows also contain
// InfoRegKeyValuesField so callers can keep exchange object identity aligned
// with lexical SQLite NUMBER/DATE keys.
func (db *DB) InfoRegDeleteExactReturning(ctx context.Context, ir *metadata.InfoRegister,
	dimKey map[string]any, period *time.Time, rowFilter *Predicate) ([]map[string]any, error) {
	if ir == nil {
		return nil, errors.New("info reg delete exact: nil register")
	}
	if len(ir.Dimensions) == 0 && !ir.Periodic {
		return nil, fmt.Errorf("info reg delete exact %s: register has no primary-key fields", ir.Name)
	}
	for _, field := range ir.Dimensions {
		value, ok := dimKey[field.Name]
		if !ok {
			return nil, fmt.Errorf("info reg delete exact %s: missing dimension %q", ir.Name, field.Name)
		}
		if value == nil {
			return nil, fmt.Errorf("info reg delete exact %s: nil dimension %q", ir.Name, field.Name)
		}
	}
	if ir.Periodic && period == nil {
		return nil, fmt.Errorf("info reg delete exact %s: period is required", ir.Name)
	}
	if !ir.Periodic && period != nil {
		return nil, fmt.Errorf("info reg delete exact %s: period is not allowed", ir.Name)
	}

	lookupKey, err := db.resolveInfoRegLookupKey(ctx, ir, dimKey, period)
	if err != nil {
		return nil, err
	}
	where, args := physicalDimWhere(db.dialect, ir, lookupKey, 1)
	if period != nil {
		where += " AND period = " + db.dialect.Placeholder(len(args)+1)
		args = append(args, *period)
	}
	return db.infoRegDeleteReturning(ctx, ir, where, args, rowFilter, "info reg delete exact", true)
}

func (db *DB) infoRegDeleteReturning(ctx context.Context, ir *metadata.InfoRegister,
	where string, args []any, rowFilter *Predicate, operation string, withKeyValues bool) ([]map[string]any, error) {
	whereParts := []string{where}
	table := metadata.InfoRegTableName(ir.Name)
	if condition, filterArgs, _, err := PredicateSQLQualified(
		db.dialect, InfoRegisterPredicateEntity(ir), rowFilter, len(args)+1, table,
	); err != nil {
		return nil, fmt.Errorf("%s %s row filter: %w", operation, ir.Name, err)
	} else if condition != "" {
		whereParts = append(whereParts, condition)
		args = append(args, filterArgs...)
	}

	var cols []string
	if ir.Periodic {
		cols = append(cols, "period")
	}
	for _, field := range ir.Dimensions {
		cols = append(cols, metadata.ColumnName(field))
	}
	for _, field := range ir.Resources {
		cols = append(cols, metadata.ColumnName(field))
	}
	// These are physical columns on every information register and valid RLS
	// fields. Returning them is necessary for the defense-in-depth in-memory
	// policy check performed by the DSL record-set path.
	cols = append(cols, "recorder", "recorder_type")

	query := fmt.Sprintf("DELETE FROM %s WHERE %s RETURNING %s",
		table, strings.Join(whereParts, " AND "), strings.Join(cols, ", "))
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s %s returning: %w", operation, ir.Name, err)
	}
	defer rows.Close()
	deleted, err := scanInfoRegRowsMode(rows, ir, cols, withKeyValues)
	if err != nil {
		return nil, fmt.Errorf("%s %s returning: %w", operation, ir.Name, err)
	}
	return deleted, nil
}
