package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// EnsureAccountsTable creates the _accounts table for storing chart of accounts data.
func (db *DB) EnsureAccountsTable(ctx context.Context) error {
	_, err := db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS _accounts (
    plan   TEXT NOT NULL,
    code   TEXT NOT NULL,
    name   TEXT,
    kind   TEXT NOT NULL DEFAULT 'active_passive',
    parent TEXT,
    PRIMARY KEY (plan, code)
)`)
	return err
}

// SyncAccounts upserts all accounts from a ChartOfAccounts into _accounts.
func (db *DB) SyncAccounts(ctx context.Context, charts []*metadata.ChartOfAccounts) error {
	for _, chart := range charts {
		for _, a := range chart.Accounts {
			_, err := db.Exec(ctx,
				`INSERT INTO _accounts (plan, code, name, kind, parent)
				 VALUES ($1,$2,$3,$4,$5)
				 ON CONFLICT (plan, code) DO UPDATE SET name=EXCLUDED.name, kind=EXCLUDED.kind, parent=EXCLUDED.parent`,
				chart.Name, a.Code, a.Name, a.Kind, nullStr(a.Parent),
			)
			if err != nil {
				return fmt.Errorf("sync accounts %s.%s: %w", chart.Name, a.Code, err)
			}
		}
	}
	return nil
}

// GetAccounts returns all accounts for a given plan, ordered by code.
func (db *DB) GetAccounts(ctx context.Context, plan string) ([]map[string]any, error) {
	rows, err := db.Query(ctx,
		`SELECT code, name, kind, parent FROM _accounts WHERE plan=$1 ORDER BY code`,
		plan,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		var code, name, kind string
		var parent *string
		if err := rows.Scan(&code, &name, &kind, &parent); err != nil {
			return nil, err
		}
		row := map[string]any{"code": code, "name": name, "kind": kind}
		if parent != nil {
			row["parent"] = *parent
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// MigrateAccountRegisters creates акк_<name> tables for all account registers.
func (db *DB) MigrateAccountRegisters(ctx context.Context, regs []*metadata.AccountRegister) error {
	for _, ar := range regs {
		if err := db.migrateAccountReg(ctx, ar); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) migrateAccountReg(ctx context.Context, ar *metadata.AccountRegister) error {
	d := db.dialect
	table := metadata.AccountRegTableName(ar.Name)
	var sb strings.Builder
	sb.WriteString("CREATE TABLE IF NOT EXISTS ")
	sb.WriteString(table)
	sb.WriteString(" (\n    id ")
	sb.WriteString(d.TypeUUID())
	sb.WriteString(" PRIMARY KEY,\n    period ")
	sb.WriteString(d.TypeTimestamp())
	sb.WriteString(" NOT NULL,\n    регистратор ")
	sb.WriteString(d.TypeUUID())
	sb.WriteString(" NOT NULL,\n    регистратор_тип TEXT NOT NULL,\n    счётдт TEXT NOT NULL,\n    счёткт TEXT NOT NULL")
	for _, r := range ar.Resources {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.ColumnName(r))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, r))
	}
	for i, s := range ar.Subconto {
		sb.WriteString(",\n    ")
		sb.WriteString(metadata.SubcontoColumn(i + 1))
		sb.WriteString(" ")
		sb.WriteString(fieldType(d, s))
	}
	sb.WriteString("\n)")
	if _, err := db.Exec(ctx, sb.String()); err != nil {
		return fmt.Errorf("migrate account register %s: %w", ar.Name, err)
	}
	idx1 := fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_dt ON %s (счётдт, period)", strings.ToLower(ar.Name), table)
	idx2 := fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_kt ON %s (счёткт, period)", strings.ToLower(ar.Name), table)
	idx3 := fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_reg ON %s (регистратор)", strings.ToLower(ar.Name), table)
	for _, s := range []string{idx1, idx2, idx3} {
		if _, err := db.Exec(ctx, s); err != nil {
			return fmt.Errorf("migrate account register %s index: %w", ar.Name, err)
		}
	}
	for _, r := range ar.Resources {
		if err := db.AddColumnIfMissing(ctx, table, metadata.ColumnName(r), fieldType(d, r)); err != nil {
			return fmt.Errorf("migrate account register %s.%s: %w", ar.Name, r.Name, err)
		}
	}
	for i, s := range ar.Subconto {
		col := metadata.SubcontoColumn(i + 1)
		if err := db.AddColumnIfMissing(ctx, table, col, fieldType(d, s)); err != nil {
			return i18nerr.Wrapf(err, "migrate account register %s субконто %s", ar.Name, s.Name)
		}
	}
	// Индекс по первому субконто ускоряет разворот остатков/оборотов по аналитике.
	if len(ar.Subconto) > 0 {
		sb1 := metadata.SubcontoColumn(1)
		idxDt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_dt_сб1 ON %s (счётдт, %s, period)", strings.ToLower(ar.Name), table, sb1)
		idxKt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_kt_сб1 ON %s (счёткт, %s, period)", strings.ToLower(ar.Name), table, sb1)
		for _, s := range []string{idxDt, idxKt} {
			if _, err := db.Exec(ctx, s); err != nil {
				return i18nerr.Wrapf(err, "migrate account register %s субконто index", ar.Name)
			}
		}
	}
	return nil
}

// WriteAccountMovements inserts account register movements for a document.
// It first clears existing movements for this recorder.
func (db *DB) WriteAccountMovements(ctx context.Context, regName, docType string, docID uuid.UUID, rows []map[string]any, ar *metadata.AccountRegister, period *time.Time) error {
	d := db.dialect
	table := metadata.AccountRegTableName(regName)
	if _, err := db.Exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE регистратор=%s", table, d.Placeholder(1)),
		idArg(d, docID),
	); err != nil {
		return fmt.Errorf("clear account movements %s: %w", regName, err)
	}
	if len(rows) == 0 {
		return nil
	}

	for _, row := range rows {
		p := period
		if pv, ok := row["период"]; ok && pv != nil {
			if t, ok := pv.(time.Time); ok {
				p = &t
			}
		}
		if p == nil {
			now := time.Now()
			p = &now
		}

		dtRaw := row["счётдт"]
		ktRaw := row["счёткт"]
		dtCode := fmt.Sprintf("%v", dtRaw)
		ktCode := fmt.Sprintf("%v", ktRaw)

		if err := validateSubconto(row, ar); err != nil {
			return fmt.Errorf("account movement %s: %w", regName, err)
		}

		var extraCols []string
		var extraArgs []any
		for _, r := range ar.Resources {
			col := metadata.ColumnName(r)
			extraCols = append(extraCols, col)
			extraArgs = append(extraArgs, ciGet(row, r.Name))
		}
		for i, s := range ar.Subconto {
			col := metadata.SubcontoColumn(i + 1)
			val := subcontoArg(row, i+1, s.Name)
			extraCols = append(extraCols, col)
			extraArgs = append(extraArgs, normalizeRegArg(d, val, metadata.IsReference(s.Type)))
		}

		// id — собственный PK движения. Без него колонка оставалась NULL, из-за
		// чего .Обороты() (HAVING по r.id IS NOT NULL) всегда возвращала пусто.
		colList := "id, period, регистратор, регистратор_тип, счётдт, счёткт"
		phs := []string{d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6)}
		args := []any{idArg(d, uuid.New()), *p, idArg(d, docID), docType, dtCode, ktCode}
		for i, ec := range extraCols {
			colList += ", " + ec
			phs = append(phs, d.Placeholder(7+i))
			args = append(args, extraArgs[i])
		}

		sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, colList, strings.Join(phs, ", "))
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("insert account movement %s: %w", regName, err)
		}
	}
	return nil
}

// ClearAccountMovements removes all movements for a given document from the register.
func (db *DB) ClearAccountMovements(ctx context.Context, regName string, docID uuid.UUID) error {
	d := db.dialect
	table := metadata.AccountRegTableName(regName)
	_, err := db.Exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE регистратор=%s", table, d.Placeholder(1)),
		idArg(d, docID),
	)
	return err
}

// GetAccountMovements returns all movements for a register, ordered by period desc.
func (db *DB) GetAccountMovements(ctx context.Context, regName string, ar *metadata.AccountRegister) ([]map[string]any, error) {
	table := metadata.AccountRegTableName(regName)

	var cols []string
	cols = append(cols, "id", "period", "регистратор", "регистратор_тип", "счётдт", "счёткт")
	for _, r := range ar.Resources {
		cols = append(cols, metadata.ColumnName(r))
	}
	for i := range ar.Subconto {
		cols = append(cols, metadata.SubcontoColumn(i+1))
	}

	sql := fmt.Sprintf("SELECT %s FROM %s ORDER BY period DESC LIMIT 500",
		strings.Join(cols, ", "), table)
	rows, err := db.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		dests := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dests {
			ptrs[i] = &dests[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = dests[i]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// AccountBalances returns balance rows for all accounts in a plan as of a given date.
// Each row: code, name, kind, оборотдт, оборотkt, сальдо (+ resource-specific amounts).
// Если у регистра объявлены субконто, остатки разворачиваются по ним: строка =
// счёт × комбинация субконто, а каждый субконто попадает в row под ключом субконто<N>
// (UUID для reference — резолвится в наименование на уровне UI).
func (db *DB) AccountBalances(ctx context.Context, regName, planName string, asOf time.Time, resources, subconto []metadata.Field) ([]map[string]any, error) {
	table := metadata.AccountRegTableName(regName)

	var resourceCols []string
	for _, r := range resources {
		col := metadata.ColumnName(r)
		resourceCols = append(resourceCols,
			fmt.Sprintf("COALESCE(SUM(CASE WHEN r.счётдт = a.code THEN r.%s ELSE 0 END),0) AS %s_дт", col, col),
			fmt.Sprintf("COALESCE(SUM(CASE WHEN r.счёткт = a.code THEN r.%s ELSE 0 END),0) AS %s_кт", col, col),
		)
	}

	selectCols := "a.code, a.name, a.kind"
	if len(resourceCols) > 0 {
		selectCols += ", " + strings.Join(resourceCols, ", ")
	}
	// Субконто-колонки идут после ресурсов, чтобы индексация ресурсов в Scan не
	// сдвигалась.
	groupBy := "a.code, a.name, a.kind"
	for i := range subconto {
		col := metadata.SubcontoColumn(i + 1)
		selectCols += ", r." + col + " AS " + col
		groupBy += ", r." + col
	}

	d := db.dialect
	query := fmt.Sprintf(`
SELECT %s
FROM _accounts a
LEFT JOIN %s r ON (r.счётдт = a.code OR r.счёткт = a.code) AND r.period <= %s
WHERE a.plan = %s
GROUP BY %s
ORDER BY a.code`, selectCols, table, d.Placeholder(1), d.Placeholder(2), groupBy)

	rows, err := db.Query(ctx, query, asOf, planName)
	if err != nil {
		return nil, fmt.Errorf("account balances: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var code, name, kind string
		dests := []any{&code, &name, &kind}
		for range resourceCols {
			var v float64
			dests = append(dests, &v)
		}
		subVals := make([]sql.NullString, len(subconto))
		for i := range subconto {
			dests = append(dests, &subVals[i])
		}
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}
		row := map[string]any{"code": code, "name": name, "kind": kind}
		// populate resource dt/kt pairs
		ei := 0
		for _, r := range resources {
			col := metadata.ColumnName(r)
			dtVal := *dests[3+ei*2].(*float64)
			ktVal := *dests[3+ei*2+1].(*float64)
			row[col+"_дт"] = dtVal
			row[col+"_кт"] = ktVal
			switch kind {
			case "active":
				row[col] = dtVal - ktVal
			case "passive":
				row[col] = ktVal - dtVal
			default: // active_passive
				row[col+"_сальдо_дт"] = max0(dtVal - ktVal)
				row[col+"_сальдо_кт"] = max0(ktVal - dtVal)
				row[col] = dtVal - ktVal
			}
			ei++
		}
		for i := range subconto {
			col := metadata.SubcontoColumn(i + 1)
			if subVals[i].Valid {
				row[col] = subVals[i].String
			} else {
				row[col] = ""
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// AccountTurnovers returns turnover rows for all accounts in a plan within a period.
func (db *DB) AccountTurnovers(ctx context.Context, regName, planName string, from, to time.Time, resources []metadata.Field) ([]map[string]any, error) {
	table := metadata.AccountRegTableName(regName)

	var resourceCols []string
	for _, r := range resources {
		col := metadata.ColumnName(r)
		resourceCols = append(resourceCols,
			fmt.Sprintf("COALESCE(SUM(CASE WHEN r.счётдт = a.code THEN r.%s ELSE 0 END),0) AS %s_дт", col, col),
			fmt.Sprintf("COALESCE(SUM(CASE WHEN r.счёткт = a.code THEN r.%s ELSE 0 END),0) AS %s_кт", col, col),
		)
	}

	selectCols := "a.code, a.name, a.kind"
	if len(resourceCols) > 0 {
		selectCols += ", " + strings.Join(resourceCols, ", ")
	}

	d := db.dialect
	query := fmt.Sprintf(`
SELECT %s
FROM _accounts a
LEFT JOIN %s r ON (r.счётдт = a.code OR r.счёткт = a.code) AND r.period >= %s AND r.period <= %s
WHERE a.plan = %s
GROUP BY a.code, a.name, a.kind
HAVING SUM(CASE WHEN r.счётдт = a.code THEN 1 ELSE 0 END) > 0
    OR SUM(CASE WHEN r.счёткт = a.code THEN 1 ELSE 0 END) > 0
ORDER BY a.code`, selectCols, table, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))

	rows, err := db.Query(ctx, query, from, to, planName)
	if err != nil {
		return nil, fmt.Errorf("account turnovers: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var code, name, kind string
		dests := []any{&code, &name, &kind}
		for range resourceCols {
			var v float64
			dests = append(dests, &v)
		}
		if err := rows.Scan(dests...); err != nil {
			return nil, err
		}
		row := map[string]any{"code": code, "name": name, "kind": kind}
		ei := 0
		for _, r := range resources {
			col := metadata.ColumnName(r)
			row[col+"_дт"] = *dests[3+ei*2].(*float64)
			row[col+"_кт"] = *dests[3+ei*2+1].(*float64)
			ei++
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// subcontoArg извлекает значение субконто из строки движения. Поддерживает обе
// формы адресации из DSL: краткую по номеру (Дв.СубконтоN → ключ субконто<N>) и
// именованную (Дв.Субконто.<Имя> → ключ субконто_<имя>).
func subcontoArg(row map[string]any, idx int, name string) any {
	if v := ciGet(row, metadata.SubcontoColumn(idx)); v != nil {
		return v
	}
	if v := ciGet(row, "субконто_"+strings.ToLower(name)); v != nil {
		return v
	}
	return nil
}

// validateSubconto проверяет, что в строке движения нет присваиваний несуществующим
// субконто. Главный урок аудита (Plans/44): неизвестное субконто — ошибка проведения,
// а не молчаливая потеря данных.
func validateSubconto(row map[string]any, ar *metadata.AccountRegister) error {
	declared := make(map[string]bool, len(ar.Subconto))
	for _, s := range ar.Subconto {
		declared[strings.ToLower(s.Name)] = true
	}
	n := len(ar.Subconto)
	for k := range row {
		lk := strings.ToLower(k)
		switch {
		case strings.HasPrefix(lk, "субконто_"):
			nm := strings.TrimPrefix(lk, "субконто_")
			if !declared[nm] {
				return i18nerr.Errorf("неизвестное субконто %q (объявлены: %s)", nm, declaredSubcontoNames(ar))
			}
		case strings.HasPrefix(lk, "субконто"):
			numPart := strings.TrimPrefix(lk, "субконто")
			idx, err := strconv.Atoi(numPart)
			if err != nil {
				continue // не нумерованное субконто (напр. опечатка) — пропускаем
			}
			if idx < 1 || idx > n {
				return i18nerr.Errorf("субконто%d не существует (объявлено субконто: %d)", idx, n)
			}
		}
	}
	return nil
}

func declaredSubcontoNames(ar *metadata.AccountRegister) string {
	names := make([]string, len(ar.Subconto))
	for i, s := range ar.Subconto {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
