package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Итоги регистра накопления (план 80): таблица итоги_<рег> хранит чистый знаковый
// оборот ресурсов по каждому набору измерений ЗА МЕСЯЦ (колонка «месяц» —
// строковый ключ YYYY-MM). Поддерживается в той же транзакции, что и движения
// (WriteMovements), поэтому всегда согласована с рег_<рег>. Знаковая сумма
// зеркалит query.genBalances дословно.
//
// Помесячные обороты обслуживают оба сценария:
//   - текущие Остатки() = SUM оборотов по всем месяцам (этап 1);
//   - Остатки(&Момент) = SUM оборотов месяцев ДО месяца момента + хвост
//     движений внутри месяца момента (этап 2).
// Месяц-ключ строк итогов совпадает по формату с time.Format("2006-01")
// (YYYY-MM), поэтому границу момента можно вычислить в Go и сравнивать строки
// лексикографически (хронологически для YYYY-MM), без диалектных дат в запросе.

// totalsMonthCol — колонка месяц-ключа в таблице итогов (общий идентификатор с
// query — см. metadata.RegisterTotalsMonthCol).
const totalsMonthCol = metadata.RegisterTotalsMonthCol

// monthKeyExpr — SQL-выражение месяц-ключа YYYY-MM для колонки периода. Формат
// совпадает с Go time.Format("2006-01"), чтобы границы момента (вычисленные в Go)
// сравнивались со строками итогов согласованно на обоих диалектах.
func monthKeyExpr(d Dialect, col string) string {
	if d.Name() == "sqlite" {
		return "strftime('%Y-%m', " + col + ")"
	}
	return "to_char(" + col + " AT TIME ZONE 'UTC', 'YYYY-MM')"
}

// SignedResourceSum возвращает выражение знаковой суммы ресурса (как в
// genBalances): приход со знаком плюс, всё остальное — минус.
//
// Экспортирована, потому что этим же выражением обязан пользоваться всякий, кто
// сверяет движения с итогами: итоги заполняются знаковой суммой, и беззнаковый
// SUM по сырой колонке не сойдётся с ними ни на одном регистре, где есть расход.
// Проверка состояния базы (internal/dbcheck) на этом и погорела — держим одно
// выражение на всех, чтобы разойтись было негде.
//
// Аргумент подставляется как есть, так что вызывающий волен передать не голую
// колонку, а выражение — например CAST(x AS NUMERIC).
func SignedResourceSum(col string) string {
	return "SUM(CASE WHEN вид_движения = 'Приход' THEN " + col + " ELSE -" + col + " END)"
}

// CreateRegisterTotalsSQL — DDL таблицы итогов: измерения + месяц (ключ) +
// ресурсы (числовой оборот). Ресурсы NUMERIC, чтобы на SQLite, где сырые колонки
// регистра могут иметь TEXT-аффинность, хранить именно числа.
func CreateRegisterTotalsSQL(d Dialect, reg *metadata.Register) string {
	table := metadata.RegisterTotalsTableName(reg.Name)
	var cols []string
	for _, f := range reg.Dimensions {
		cols = append(cols, metadata.ColumnName(f)+" "+fieldType(d, f))
	}
	cols = append(cols, totalsMonthCol+" TEXT NOT NULL")
	for _, f := range reg.Resources {
		cols = append(cols, metadata.ColumnName(f)+" NUMERIC")
	}
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    ")
	sb.WriteString(strings.Join(cols, ",\n    "))
	sb.WriteString("\n)")
	return sb.String()
}

func CreateRegisterTotalsIndexSQL(reg *metadata.Register) string {
	cols := append(dimColNames(reg.Dimensions), totalsMonthCol)
	return "CREATE INDEX IF NOT EXISTS idx_" + metadata.RegisterTotalsTableName(reg.Name) +
		"_dims_month ON " + metadata.RegisterTotalsTableName(reg.Name) + " (" + strings.Join(cols, ", ") + ")"
}

func dimColNames(dims []metadata.Field) []string {
	names := make([]string, len(dims))
	for i, f := range dims {
		names[i] = metadata.ColumnName(f)
	}
	return names
}

// insertTotalsSelectSQL строит INSERT в итоги: группировка движений по
// (измерения, месяц) со знаковой суммой ресурсов. where — условие отбора по
// кортежу измерений (или пусто для полного пересчёта).
func insertTotalsSelectSQL(d Dialect, reg *metadata.Register, where string) string {
	totals := metadata.RegisterTotalsTableName(reg.Name)
	src := metadata.RegisterTableName(reg.Name)
	dims := dimColNames(reg.Dimensions)
	monthExpr := monthKeyExpr(d, "period")

	insertCols := append([]string{}, dims...)
	insertCols = append(insertCols, totalsMonthCol)
	selectCols := append([]string{}, dims...)
	selectCols = append(selectCols, monthExpr)
	groupCols := append([]string{}, dims...)
	groupCols = append(groupCols, monthExpr)
	for _, f := range reg.Resources {
		col := metadata.ColumnName(f)
		insertCols = append(insertCols, col)
		selectCols = append(selectCols, SignedResourceSum(col))
	}

	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(totals)
	sb.WriteString(" (")
	sb.WriteString(strings.Join(insertCols, ", "))
	sb.WriteString(") SELECT ")
	sb.WriteString(strings.Join(selectCols, ", "))
	sb.WriteString(" FROM ")
	sb.WriteString(src)
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	sb.WriteString(" GROUP BY ")
	sb.WriteString(strings.Join(groupCols, ", "))
	// HAVING COUNT(*)>0 отсекает пустой агрегат (для отбора без строк).
	sb.WriteString(" HAVING COUNT(*) > 0")
	return sb.String()
}

const totalsMetaTable = "_register_totals_meta"

func registerTotalsFingerprint(reg *metadata.Register) string {
	var b strings.Builder
	b.WriteString("totals-v3-utc-nullable-dims|")
	for _, f := range reg.Dimensions {
		fmt.Fprintf(&b, "d:%s:%s:%s|", strings.ToLower(metadata.ColumnName(f)), f.Type, strings.ToLower(f.RefEntity))
	}
	for _, f := range reg.Resources {
		fmt.Fprintf(&b, "r:%s:%s|", strings.ToLower(metadata.ColumnName(f)), f.Type)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}

func (db *DB) ensureTotalsMeta(ctx context.Context) error {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+totalsMetaTable+` (
		register_name TEXT PRIMARY KEY,
		state TEXT NOT NULL
	)`)
	return err
}

func (db *DB) totalsState(ctx context.Context, regName string) (string, error) {
	d := db.dialect
	var state string
	err := db.QueryRow(ctx, `SELECT state FROM `+totalsMetaTable+` WHERE register_name = `+d.Placeholder(1), strings.ToLower(regName)).Scan(&state)
	if exchangeNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return state, nil
}

func (db *DB) saveTotalsState(ctx context.Context, regName, state string) error {
	d := db.dialect
	q := fmt.Sprintf(`INSERT INTO %s (register_name, state) VALUES (%s, %s)
		ON CONFLICT (register_name) DO UPDATE SET state = EXCLUDED.state`, totalsMetaTable, d.Placeholder(1), d.Placeholder(2))
	_, err := db.Exec(ctx, q, strings.ToLower(regName), state)
	return err
}

// syncRegisterTotals records disabled periods and rebuilds totals whenever the
// usable state or the dimensions/resources schema changes.
func (db *DB) syncRegisterTotals(ctx context.Context, reg *metadata.Register) error {
	return db.WithTxIfNeeded(ctx, func(ctx context.Context) error {
		if err := db.ensureTotalsMeta(ctx); err != nil {
			return err
		}
		fingerprint := registerTotalsFingerprint(reg)
		if !reg.TotalsUsable() {
			return db.saveTotalsState(ctx, reg.Name, "disabled:"+fingerprint)
		}
		if err := db.AdvisoryXactLock(ctx, []string{"register-totals|" + strings.ToLower(reg.Name)}); err != nil {
			return err
		}
		return db.ensureRegisterTotals(ctx, reg, fingerprint)
	})
}

func (db *DB) ensureRegisterTotals(ctx context.Context, reg *metadata.Register, fingerprint string) error {
	table := metadata.RegisterTotalsTableName(reg.Name)
	state, err := db.totalsState(ctx, reg.Name)
	if err != nil {
		return err
	}
	exists, err := db.dialect.ColumnExists(ctx, db, table, totalsMonthCol)
	if err != nil {
		return err
	}
	if state == "active:"+fingerprint && exists {
		return nil
	}
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		return fmt.Errorf("drop stale totals table %s: %w", reg.Name, err)
	}
	if _, err := db.Exec(ctx, CreateRegisterTotalsSQL(db.dialect, reg)); err != nil {
		return fmt.Errorf("create totals table %s: %w", reg.Name, err)
	}
	if _, err := db.Exec(ctx, CreateRegisterTotalsIndexSQL(reg)); err != nil {
		return fmt.Errorf("create totals index %s: %w", reg.Name, err)
	}
	if err := db.recalcRegisterTotalsInTx(ctx, reg); err != nil {
		return err
	}
	return db.saveTotalsState(ctx, reg.Name, "active:"+fingerprint)
}

// RecalcRegisterTotals полностью пересчитывает итоги регистра из движений.
func (db *DB) RecalcRegisterTotals(ctx context.Context, reg *metadata.Register) error {
	if !reg.TotalsUsable() {
		return nil
	}
	return db.WithTxIfNeeded(ctx, func(ctx context.Context) error {
		if err := db.AdvisoryXactLock(ctx, []string{"register-totals|" + strings.ToLower(reg.Name)}); err != nil {
			return err
		}
		return db.recalcRegisterTotalsInTx(ctx, reg)
	})
}

func (db *DB) recalcRegisterTotalsInTx(ctx context.Context, reg *metadata.Register) error {
	totals := metadata.RegisterTotalsTableName(reg.Name)
	if err := db.exec(ctx, "DELETE FROM "+totals); err != nil {
		return fmt.Errorf("recalc totals %s: clear: %w", reg.Name, err)
	}
	if err := db.exec(ctx, insertTotalsSelectSQL(db.dialect, reg, "")); err != nil {
		return fmt.Errorf("recalc totals %s: fill: %w", reg.Name, err)
	}
	return nil
}

// dimTupleWhere строит условие отбора одного кортежа измерений
// (`d1 = ? AND d2 IS NULL AND ...`), начиная с плейсхолдера startPh.
func dimTupleWhere(d Dialect, dims []metadata.Field, tuple []any, startPh int) (string, []any) {
	var conds []string
	var args []any
	ph := startPh
	for i, f := range dims {
		col := metadata.ColumnName(f)
		if i >= len(tuple) || tuple[i] == nil {
			conds = append(conds, col+" IS NULL")
			continue
		}
		conds = append(conds, col+" = "+d.Placeholder(ph))
		args = append(args, tuple[i])
		ph++
	}
	return strings.Join(conds, " AND "), args
}

// distinctDimTuples возвращает уникальные кортежи измерений движений
// регистратора (для определения затронутых итогов). Значения берутся из самой
// таблицы рег_* — уже в нормализованном виде, что совпадает с ключом итогов.
func (db *DB) distinctDimTuples(ctx context.Context, reg *metadata.Register, recorderType string, recorderID any) ([][]any, error) {
	if len(reg.Dimensions) == 0 {
		return [][]any{{}}, nil // единственный кортеж — «без измерений»
	}
	d := db.dialect
	cols := dimColNames(reg.Dimensions)
	sql := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE recorder = %s AND recorder_type = %s",
		strings.Join(cols, ", "), metadata.RegisterTableName(reg.Name), d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(ctx, sql, recorderID, recorderType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tuples [][]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		tuples = append(tuples, dest)
	}
	return tuples, rows.Err()
}

// recomputeTupleTotals пересчитывает все помесячные строки итогов одного кортежа
// измерений: удаляет их и заново считает из движений. Если движений для кортежа
// не осталось — строки просто исчезают.
func (db *DB) recomputeTupleTotals(ctx context.Context, reg *metadata.Register, tuple []any) error {
	totals := metadata.RegisterTotalsTableName(reg.Name)
	where, args := dimTupleWhere(db.dialect, reg.Dimensions, tuple, 1)
	delSQL := "DELETE FROM " + totals
	if where != "" {
		delSQL += " WHERE " + where
	}
	if err := db.exec(ctx, delSQL, args...); err != nil {
		return fmt.Errorf("totals %s: delete tuple: %w", reg.Name, err)
	}
	insWhere, insArgs := dimTupleWhere(db.dialect, reg.Dimensions, tuple, 1)
	if err := db.exec(ctx, insertTotalsSelectSQL(db.dialect, reg, insWhere), insArgs...); err != nil {
		return fmt.Errorf("totals %s: recompute tuple: %w", reg.Name, err)
	}
	return nil
}

// updateTotalsForRecorder поддерживает итоги после замены движений
// регистратора: пересчитывает кортежи измерений, затронутые старыми (снятыми до
// удаления) и новыми движениями. Вызывается из WriteMovements в той же транзакции.
func (db *DB) updateTotalsForRecorder(ctx context.Context, reg *metadata.Register, recorderType string, recorderID any, oldTuples [][]any) error {
	newTuples, err := db.distinctDimTuples(ctx, reg, recorderType, recorderID)
	if err != nil {
		return fmt.Errorf("totals %s: new tuples: %w", reg.Name, err)
	}
	for _, t := range dedupTuples(append(oldTuples, newTuples...)) {
		if err := db.recomputeTupleTotals(ctx, reg, t); err != nil {
			return err
		}
	}
	return nil
}

// dedupTuples убирает повторяющиеся кортежи (по строковому представлению).
func dedupTuples(tuples [][]any) [][]any {
	seen := make(map[string]bool, len(tuples))
	var out [][]any
	for _, t := range tuples {
		key := tupleKey(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

func tupleKey(tuple []any) string {
	var sb strings.Builder
	for _, v := range tuple {
		fmt.Fprintf(&sb, "%T:%#v\x00", v, v)
	}
	return sb.String()
}
