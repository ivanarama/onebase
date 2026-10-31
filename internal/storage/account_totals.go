package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Итоги бухрегистра (план 80): таблица итоги_акк_<name> хранит ПОМЕСЯЧНЫЕ обороты
// Дт и Кт каждого ресурса по ключу (счёт, субконто1…N, месяц YYYY-MM). В отличие
// от регистра накопления, где строка движения относится к одному кортежу
// измерений, проводка бухрегистра затрагивает ДВА счёта: сумма идёт в дебет счётдт
// и в кредит счёткт. Поэтому итоги строятся разворотом каждой проводки на две
// половины (UNION ALL по счётдт и счёткт).
//
// Сальдо счёта = Σ(Дт − Кт) по месяцам (зеркалит query.genAccountBalances, где
// остаток = SUM(CASE WHEN счётдт THEN res ELSE −res)); обороты за период — Σ Дт /
// Σ Кт по месяцам. Месяц-ключ совпадает по формату с time.Format("2006-01"),
// поэтому границу момента можно вычислять в Go (общий helper monthKeyExpr).

// accountTotalsDebitCol / accountTotalsCreditCol — колонки оборота ресурса по
// дебету и кредиту в таблице итогов бухрегистра.
func accountTotalsDebitCol(r metadata.Field) string {
	return metadata.SQLIdent(accountTotalsDebitLogical(r))
}
func accountTotalsCreditCol(r metadata.Field) string {
	return metadata.SQLIdent(accountTotalsCreditLogical(r))
}

// Логические имена оборотных колонок — до сокращения длинных имён (#1946).
func accountTotalsDebitLogical(r metadata.Field) string {
	return metadata.LogicalColumnName(r) + "_дт"
}
func accountTotalsCreditLogical(r metadata.Field) string {
	return metadata.LogicalColumnName(r) + "_кт"
}

// accountSubcontoCols возвращает стабильные имена колонок субконто (субконто1…N).
func accountSubcontoCols(ar *metadata.AccountRegister) []string {
	cols := make([]string, len(ar.Subconto))
	for i := range ar.Subconto {
		cols[i] = metadata.SubcontoColumn(i + 1)
	}
	return cols
}

// CreateAccountTotalsSQL — DDL таблицы итогов бухрегистра: счёт + субконто (ключ) +
// месяц + по два NUMERIC-оборота (Дт/Кт) на ресурс.
func CreateAccountTotalsSQL(d Dialect, ar *metadata.AccountRegister) string {
	table := metadata.AccountRegTotalsTableName(ar.Name)
	cols := []string{"счёт TEXT NOT NULL"}
	for i, s := range ar.Subconto {
		cols = append(cols, metadata.SubcontoColumn(i+1)+" "+fieldType(d, s))
	}
	cols = append(cols, totalsMonthCol+" TEXT NOT NULL")
	for _, r := range ar.Resources {
		cols = append(cols, accountTotalsDebitCol(r)+" NUMERIC", accountTotalsCreditCol(r)+" NUMERIC")
	}
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    ")
	sb.WriteString(strings.Join(cols, ",\n    "))
	sb.WriteString("\n)")
	return sb.String()
}

// CreateAccountTotalsIndexSQL — индекс по ключу (счёт, субконто…, месяц).
func CreateAccountTotalsIndexSQL(ar *metadata.AccountRegister) string {
	table := metadata.AccountRegTotalsTableName(ar.Name)
	cols := append([]string{"счёт"}, accountSubcontoCols(ar)...)
	cols = append(cols, totalsMonthCol)
	idx := metadata.SQLIdent("idx_" + metadata.AccountRegTotalsTableLogical(ar.Name) + "_key")
	return "CREATE INDEX IF NOT EXISTS " + idx + " ON " + table + " (" + strings.Join(cols, ", ") + ")"
}

// insertAccountTotalsSelectSQL строит INSERT в итоги: разворот проводок на
// дебетовую (счётдт) и кредитовую (счёткт) половины через UNION ALL и агрегат по
// (счёт, субконто, месяц). whereDt/whereKt — условия отбора половин (для
// инкрементального пересчёта одного кортежа); пусто — полный пересчёт.
func insertAccountTotalsSelectSQL(d Dialect, ar *metadata.AccountRegister, whereDt, whereKt string) string {
	totals := metadata.AccountRegTotalsTableName(ar.Name)
	src := metadata.AccountRegTableName(ar.Name)
	subCols := accountSubcontoCols(ar)
	monthExpr := monthKeyExpr(d, "period")

	insertCols := append([]string{"счёт"}, subCols...)
	insertCols = append(insertCols, totalsMonthCol)
	outerSel := append([]string{"счёт"}, subCols...)
	outerSel = append(outerSel, totalsMonthCol)
	groupCols := append([]string{"счёт"}, subCols...)
	groupCols = append(groupCols, totalsMonthCol)
	for _, r := range ar.Resources {
		dc, cc := accountTotalsDebitCol(r), accountTotalsCreditCol(r)
		insertCols = append(insertCols, dc, cc)
		outerSel = append(outerSel, "SUM("+dc+") AS "+dc, "SUM("+cc+") AS "+cc)
	}

	half := func(acctCol string, debit bool, where string) string {
		sel := append([]string{acctCol + " AS счёт"}, subCols...)
		sel = append(sel, monthExpr+" AS "+totalsMonthCol)
		for _, r := range ar.Resources {
			col := metadata.ColumnName(r)
			dc, cc := accountTotalsDebitCol(r), accountTotalsCreditCol(r)
			if debit {
				sel = append(sel, col+" AS "+dc, "0 AS "+cc)
			} else {
				sel = append(sel, "0 AS "+dc, col+" AS "+cc)
			}
		}
		q := "SELECT " + strings.Join(sel, ", ") + " FROM " + src
		if where != "" {
			q += " WHERE " + where
		}
		return q
	}

	inner := half("счётдт", true, whereDt) + " UNION ALL " + half("счёткт", false, whereKt)

	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(totals)
	sb.WriteString(" (")
	sb.WriteString(strings.Join(insertCols, ", "))
	sb.WriteString(") SELECT ")
	sb.WriteString(strings.Join(outerSel, ", "))
	sb.WriteString(" FROM (")
	sb.WriteString(inner)
	sb.WriteString(") u GROUP BY ")
	sb.WriteString(strings.Join(groupCols, ", "))
	return sb.String()
}

func accountTotalsFingerprint(ar *metadata.AccountRegister) string {
	var b strings.Builder
	b.WriteString("acct-totals-v1|")
	for i, s := range ar.Subconto {
		fmt.Fprintf(&b, "s%d:%s:%s|", i+1, s.Type, strings.ToLower(s.RefEntity))
	}
	for _, r := range ar.Resources {
		fmt.Fprintf(&b, "r:%s:%s|", strings.ToLower(metadata.ColumnName(r)), r.Type)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(b.String())))
}

// accountTotalsMetaKey — ключ состояния в общей таблице _register_totals_meta.
// Префикс «акк:» разводит бухрегистр и регистр накопления с тем же именем.
func accountTotalsMetaKey(name string) string { return "акк:" + name }

// syncAccountRegisterTotals создаёт/пересобирает таблицу итогов при изменении
// набора субконто/ресурсов или флага включённости. Вызывается из миграции.
func (db *DB) syncAccountRegisterTotals(ctx context.Context, ar *metadata.AccountRegister) error {
	return db.WithTxIfNeeded(ctx, func(ctx context.Context) error {
		if err := db.ensureTotalsMeta(ctx); err != nil {
			return err
		}
		fingerprint := accountTotalsFingerprint(ar)
		key := accountTotalsMetaKey(ar.Name)
		if !ar.TotalsUsable() {
			return db.saveTotalsState(ctx, key, "disabled:"+fingerprint)
		}
		if err := db.AdvisoryXactLock(ctx, []string{"account-totals|" + strings.ToLower(ar.Name)}); err != nil {
			return err
		}
		return db.ensureAccountTotals(ctx, ar, fingerprint)
	})
}

func (db *DB) ensureAccountTotals(ctx context.Context, ar *metadata.AccountRegister, fingerprint string) error {
	table := metadata.AccountRegTotalsTableName(ar.Name)
	if err := db.renameLegacyTable(ctx, metadata.AccountRegTotalsTableLogical(ar.Name)); err != nil {
		return err
	}
	var turnoverCols []string
	for _, r := range ar.Resources {
		turnoverCols = append(turnoverCols, accountTotalsDebitLogical(r), accountTotalsCreditLogical(r))
	}
	if err := db.renameLegacyColumns(ctx, table, turnoverCols); err != nil {
		return err
	}
	key := accountTotalsMetaKey(ar.Name)
	state, err := db.totalsState(ctx, key)
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
		return fmt.Errorf("drop stale account totals %s: %w", ar.Name, err)
	}
	if _, err := db.Exec(ctx, CreateAccountTotalsSQL(db.dialect, ar)); err != nil {
		return fmt.Errorf("create account totals %s: %w", ar.Name, err)
	}
	if _, err := db.Exec(ctx, CreateAccountTotalsIndexSQL(ar)); err != nil {
		return fmt.Errorf("create account totals index %s: %w", ar.Name, err)
	}
	if err := db.recalcAccountTotalsInTx(ctx, ar); err != nil {
		return err
	}
	return db.saveTotalsState(ctx, key, "active:"+fingerprint)
}

// RecalcAccountRegisterTotals полностью пересчитывает итоги бухрегистра из проводок
// (восстановление после сбоя/массовой правки; используется командой recalc-totals).
func (db *DB) RecalcAccountRegisterTotals(ctx context.Context, ar *metadata.AccountRegister) error {
	if !ar.TotalsUsable() {
		return nil
	}
	return db.WithTxIfNeeded(ctx, func(ctx context.Context) error {
		if err := db.AdvisoryXactLock(ctx, []string{"account-totals|" + strings.ToLower(ar.Name)}); err != nil {
			return err
		}
		return db.recalcAccountTotalsInTx(ctx, ar)
	})
}

func (db *DB) recalcAccountTotalsInTx(ctx context.Context, ar *metadata.AccountRegister) error {
	totals := metadata.AccountRegTotalsTableName(ar.Name)
	if err := db.exec(ctx, "DELETE FROM "+totals); err != nil {
		return fmt.Errorf("recalc account totals %s: clear: %w", ar.Name, err)
	}
	if err := db.exec(ctx, insertAccountTotalsSelectSQL(db.dialect, ar, "", "")); err != nil {
		return fmt.Errorf("recalc account totals %s: fill: %w", ar.Name, err)
	}
	return nil
}

// distinctAccountTuples возвращает уникальные кортежи (счёт, субконто…),
// затронутые проводками регистратора. Счёт берётся и из счётдт, и из счёткт —
// проводка бьёт по обоим счетам. Значения берутся из самой таблицы акк_ (уже
// нормализованы), что совпадает с ключом итогов.
func (db *DB) distinctAccountTuples(ctx context.Context, ar *metadata.AccountRegister, recorderID any) ([][]any, error) {
	d := db.dialect
	table := metadata.AccountRegTableName(ar.Name)
	var subSel strings.Builder
	for _, c := range accountSubcontoCols(ar) {
		subSel.WriteString(", ")
		subSel.WriteString(c)
	}
	sql := "SELECT DISTINCT счётдт" + subSel.String() + " FROM " + table + " WHERE регистратор = " + d.Placeholder(1) +
		" UNION SELECT DISTINCT счёткт" + subSel.String() + " FROM " + table + " WHERE регистратор = " + d.Placeholder(2)
	rows, err := db.Query(ctx, sql, recorderID, recorderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	n := 1 + len(ar.Subconto)
	var tuples [][]any
	for rows.Next() {
		dest := make([]any, n)
		ptrs := make([]any, n)
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

// accountTupleWhere строит условие отбора кортежа (счёт=?, субконто…) для заданной
// колонки счёта (счёт в итогах; счётдт/счёткт в движениях), начиная с плейсхолдера
// startPh. NULL-субконто сравнивается через IS NULL (без аргумента).
func accountTupleWhere(d Dialect, ar *metadata.AccountRegister, acctCol string, tuple []any, startPh int) (string, []any) {
	var conds []string
	var args []any
	ph := startPh
	if len(tuple) == 0 || tuple[0] == nil {
		conds = append(conds, acctCol+" IS NULL")
	} else {
		conds = append(conds, acctCol+" = "+d.Placeholder(ph))
		args = append(args, tuple[0])
		ph++
	}
	for i := range ar.Subconto {
		col := metadata.SubcontoColumn(i + 1)
		var v any
		if i+1 < len(tuple) {
			v = tuple[i+1]
		}
		if v == nil {
			conds = append(conds, col+" IS NULL")
			continue
		}
		conds = append(conds, col+" = "+d.Placeholder(ph))
		args = append(args, v)
		ph++
	}
	return strings.Join(conds, " AND "), args
}

// recomputeAccountTupleTotals пересчитывает помесячные строки итогов одного кортежа
// (счёт, субконто): удаляет их и заново считает из проводок обеих половин разворота.
// Если проводок для кортежа не осталось — строки просто исчезают.
func (db *DB) recomputeAccountTupleTotals(ctx context.Context, ar *metadata.AccountRegister, tuple []any) error {
	totals := metadata.AccountRegTotalsTableName(ar.Name)
	delWhere, delArgs := accountTupleWhere(db.dialect, ar, "счёт", tuple, 1)
	delSQL := "DELETE FROM " + totals
	if delWhere != "" {
		delSQL += " WHERE " + delWhere
	}
	if err := db.exec(ctx, delSQL, delArgs...); err != nil {
		return fmt.Errorf("account totals %s: delete tuple: %w", ar.Name, err)
	}
	// Плейсхолдеры кредитовой половины идут после дебетовой (порядок половин в SQL).
	whereDt, argsDt := accountTupleWhere(db.dialect, ar, "счётдт", tuple, 1)
	whereKt, argsKt := accountTupleWhere(db.dialect, ar, "счёткт", tuple, 1+len(argsDt))
	ins := insertAccountTotalsSelectSQL(db.dialect, ar, whereDt, whereKt)
	if err := db.exec(ctx, ins, append(argsDt, argsKt...)...); err != nil {
		return fmt.Errorf("account totals %s: recompute tuple: %w", ar.Name, err)
	}
	return nil
}

// updateAccountTotalsForRecorder поддерживает итоги после замены проводок
// регистратора: пересчитывает кортежи (счёт, субконто), затронутые старыми
// (снятыми до удаления) и новыми проводками. Вызывается в транзакции записи.
func (db *DB) updateAccountTotalsForRecorder(ctx context.Context, ar *metadata.AccountRegister, recorderID any, oldTuples [][]any) error {
	newTuples, err := db.distinctAccountTuples(ctx, ar, recorderID)
	if err != nil {
		return fmt.Errorf("account totals %s: new tuples: %w", ar.Name, err)
	}
	for _, t := range dedupTuples(append(oldTuples, newTuples...)) {
		if err := db.recomputeAccountTupleTotals(ctx, ar, t); err != nil {
			return err
		}
	}
	return nil
}
