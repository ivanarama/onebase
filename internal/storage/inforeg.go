package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	where, args := dimWhere(d, ir, dimKey, 1)
	if ir.Periodic && period != nil {
		where = fmt.Sprintf("%s AND period = %s", where, d.Placeholder(len(args)+1))
		args = append(args, *period)
	}
	sql2 := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1",
		strings.Join(resourceAndDimCols(ir), ", "), table, where)
	rec, err := db.infoRegScan(ctx, ir, sql2, args)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rec, err
}

// InfoRegApplyExchange применяет запись регистра сведений из пакета обмена
// (план 86). Значения измерений/ресурсов приходят канонизированными (ссылки —
// строкой-UUID, даты — RFC3339), поэтому приводим их к аргументам БД тем же
// normalizeRegArg, что и запись движений. deletion=true удаляет запись по ключу.
func (db *DB) InfoRegApplyExchange(ctx context.Context, ir *metadata.InfoRegister, dims, resources map[string]any, period *time.Time, deletion bool) error {
	d := db.dialect
	cdims := make(map[string]any, len(ir.Dimensions))
	for _, f := range ir.Dimensions {
		cdims[f.Name] = normalizeRegArg(d, dims[f.Name], f.RefEntity != "")
	}
	if deletion {
		return db.InfoRegDelete(ctx, ir, cdims, period)
	}
	cres := make(map[string]any, len(ir.Resources))
	for _, f := range ir.Resources {
		cres[f.Name] = normalizeRegArg(d, resources[f.Name], f.RefEntity != "")
	}
	return db.InfoRegSet(ctx, ir, cdims, cres, period)
}

// InfoRegSet upserts a record in an info register.
// For periodic registers, period must be non-nil.
func (db *DB) InfoRegSet(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, resources map[string]any, period *time.Time) error {
	d := db.dialect
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
		phs = append(phs, d.Placeholder(idx))
		args = append(args, *period)
		idx++
	}

	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		cols = append(cols, col)
		phs = append(phs, d.Placeholder(idx))
		args = append(args, dimKey[f.Name])
		idx++
	}
	for _, f := range ir.Resources {
		col := metadata.ColumnName(f)
		cols = append(cols, col)
		phs = append(phs, d.Placeholder(idx))
		args = append(args, resources[f.Name])
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
	where, args := dimWhere(db.dialect, ir, dimKey, 1)
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT 1",
		strings.Join(allCols, ", "), table, where)
	return db.infoRegScan(ctx, ir, sql, args)
}

// InfoRegGetLast returns the most recent record on or before onDate for the given dimensions.
func (db *DB) InfoRegGetLast(ctx context.Context, ir *metadata.InfoRegister, dimKey map[string]any, onDate time.Time) (map[string]any, error) {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	allCols := append([]string{"period"}, resourceAndDimCols(ir)...)
	where, args := dimWhere(d, ir, dimKey, 1)
	args = append(args, onDate)
	sql := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s AND period <= %s ORDER BY period DESC LIMIT 1",
		strings.Join(allCols, ", "), table, where, d.Placeholder(len(args)))
	return db.infoRegScan(ctx, ir, sql, args)
}

// InfoRegList returns records, optionally filtered by dimension values and
// period (период учитывается только для periodic-регистров, issue #45).
func (db *DB) InfoRegList(ctx context.Context, ir *metadata.InfoRegister, f RegFilter) ([]map[string]any, error) {
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

	where, args := dimWhereClause(db.dialect, ir.Dimensions, f, 1, ir.Periodic, ir.Periodic)
	whereParts := make([]string, 0, 2)
	if where != "" {
		whereParts = append(whereParts, where)
	}
	if cond, condArgs, _, err := PredicateSQL(db.dialect, InfoRegisterPredicateEntity(ir), f.RowFilter, len(args)+1); err != nil {
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
			// period — человекочитаемое представление для ячейки списка;
			// period_key — машинный ключ для round-trip через HTML-форму
			// удаления (см. ParseRegPeriod и ui.infoRegDelete). На PostgreSQL
			// период приходит как time.Time → RFC3339 несёт инстант; на SQLite
			// как TEXT-строка UTC → её и отдаём ключом.
			switch v := dest[0].(type) {
			case time.Time:
				row["period"] = v.Format("02.01.2006")
				row["period_key"] = v.Format(time.RFC3339)
			case string:
				row["period_key"] = v
				if t, ok := ParseRegPeriod(v); ok {
					row["period"] = t.In(time.Local).Format("02.01.2006")
				} else {
					row["period"] = v
				}
			default:
				row["period"] = dest[0]
			}
			i = 1
		}
		for _, f := range ir.Dimensions {
			row[f.Name] = normalizeFieldValue(f, dest[i])
			i++
		}
		for _, f := range ir.Resources {
			row[f.Name] = normalizeFieldValue(f, dest[i])
			i++
		}
		result = append(result, row)
	}
	return result, rows.Err()
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
		args = append(args, dimKey[f.Name])
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
		}

		for _, f := range ir.Dimensions {
			col := metadata.ColumnName(f)
			cols = append(cols, col)
			phs = append(phs, d.Placeholder(idx))
			v := ciGet(row, f.Name)
			v = normalizeRegArg(d, v, f.RefEntity != "")
			args = append(args, v)
			idx++
		}
		for _, f := range ir.Resources {
			col := metadata.ColumnName(f)
			cols = append(cols, col)
			phs = append(phs, d.Placeholder(idx))
			v := ciGet(row, f.Name)
			v = normalizeRegArg(d, v, f.RefEntity != "")
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

func dimWhere(d Dialect, ir *metadata.InfoRegister, dimKey map[string]any, startIdx int) (string, []any) {
	var conds []string
	var args []any
	idx := startIdx
	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		conds = append(conds, fmt.Sprintf("%s = %s", col, d.Placeholder(idx)))
		args = append(args, dimKey[f.Name])
		idx++
	}
	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

func (db *DB) infoRegScan(ctx context.Context, ir *metadata.InfoRegister, sql string, args []any) (map[string]any, error) {
	row := db.QueryRow(ctx, sql, args...)
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
	where, args := dimWhereClause(db.dialect, ir.Dimensions, f, 1, ir.Periodic, ir.Periodic)
	if where == "" {
		return fmt.Errorf("info reg delete by filter %s: пустой отбор", ir.Name)
	}
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", metadata.InfoRegTableName(ir.Name), where)
	return db.exec(ctx, sql, args...)
}
