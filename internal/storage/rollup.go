package storage

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// RollupRecorderType — синтетический тип регистратора опорных движений, которые
// создаёт свёртка базы (план 74). Не соответствует никакой сущности
// конфигурации: остатки на дату свёртки самодостаточны и не зависят от
// документов. OrphanMovements/DeleteOrphanMovements обязаны его пропускать —
// иначе «Очистка регистров» удалила бы опорные остатки как «осиротевшие».
const RollupRecorderType = "_СвёрткаБазы"

// RollupOptions — параметры свёртки информационной базы.
type RollupOptions struct {
	// Date — дата свёртки D. Сворачиваются движения строго ДО начала дня D
	// (period < D 00:00); опорные остатки пишутся на момент D 00:00; движения
	// с period >= D 00:00 остаются нетронутыми.
	Date time.Time
	// Registers — имена регистров накопления, которые сворачиваем. Пустой
	// список = ничего не сворачивать (оборотные регистры админ исключает,
	// просто не включая их сюда).
	Registers []string
	// DeleteDocuments — true: физически удалить документы с датой < D (вместе с
	// табличными частями); false: снять у них проведение (движения всё равно
	// свёрнуты, а дата запрета проведения защищает от перепроведения).
	DeleteDocuments bool
	// AccountRegisters — имена регистров бухгалтерии (акк_*) к свёртке. Опорные
	// остатки вводятся проводками через вспомогательный счёт (см. resolveAuxAccount).
	AccountRegisters []string
	// InfoRegisters — имена периодических регистров сведений (инфо_*) к обрезке.
	// Для каждой комбинации измерений оставляется последний срез до D, более
	// ранние удаляются. Операция НЕСТАНДАРТНА для 1С (свёртка обычно не трогает
	// регистры сведений) и ломает СрезПервых/историю срезов до D — поэтому
	// включается только явным перечислением регистров. Непериодические
	// пропускаются.
	InfoRegisters []string
	// BeforeDeleteDocument runs inside the rollup transaction immediately before
	// each physical delete. Hosts use it to register exchange tombstones without
	// creating a storage → exchange package cycle.
	BeforeDeleteDocument func(context.Context, *metadata.Entity, uuid.UUID) error
}

// RollupRegReport — итог свёртки по одному регистру.
type RollupRegReport struct {
	Name            string
	FoldedMovements int    // движений (period < D), свёрнуто/будет свёрнуто
	OpeningRows     int    // опорных строк (ненулевых остатков), создано/создастся
	Note            string // напр. «пропущен: нет вспомогательного счёта»
}

// RollupReport — сводка операции свёртки (или её предпросмотра).
type RollupReport struct {
	Cutoff           time.Time
	Preview          bool
	Registers        []RollupRegReport // регистры накопления
	AccountRegisters []RollupRegReport // регистры бухгалтерии (акк_*)
	InfoRegisters    []RollupRegReport // регистры сведений (инфо_*): обрезка периодических
	DeletedDocs      int               // документов: удалено (run) или под удаление (preview); 0 при keep-режиме
	DanglingRefs     int               // ссылок на удаляемые документы из сохраняемых записей (delete-режим)
}

// FoldedMovements — сколько строк движений свёрнуто во всех регистрах вместе.
// По нему видно, стоит ли предлагать обслуживание хранилища: место после
// удаления само не возвращается (план 114).
func (r RollupReport) FoldedMovements() int {
	total := 0
	for _, group := range [][]RollupRegReport{r.Registers, r.AccountRegisters, r.InfoRegisters} {
		for _, reg := range group {
			total += reg.FoldedMovements
		}
	}
	return total
}

// EnsureRollupTable создаёт служебный журнал свёрток _rollup. Времена хранятся
// строками (RFC3339 / дата) — это дёшево и не зависит от диалекта (логика свёртки
// берёт дату из RollupOptions, а не из этой таблицы; таблица — только аудит).
func (db *DB) EnsureRollupTable(ctx context.Context) error {
	_, err := db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS _rollup (
    id                TEXT PRIMARY KEY,
    cutoff            TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    created_by        TEXT NOT NULL DEFAULT '',
    registers         TEXT NOT NULL DEFAULT '',
    folded_movements  INTEGER NOT NULL DEFAULT 0,
    opening_rows      INTEGER NOT NULL DEFAULT 0,
    deleted_documents INTEGER NOT NULL DEFAULT 0,
    documents_deleted INTEGER NOT NULL DEFAULT 0
)`)
	return err
}

// dayStart обрезает момент до начала дня (00:00 в той же зоне) — граница свёртки.
func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func rollupNameKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func selectedRollupNameSet(kind string, names []string) (map[string]bool, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		key := rollupNameKey(n)
		if key == "" {
			return nil, fmt.Errorf("%s: пустое имя", kind)
		}
		want[key] = true
	}
	return want, nil
}

// selectRollupRegs отбирает из всех регистров только включённые в свёртку
// (по имени, регистронезависимо). Оборотные регистры отклоняются явно: их
// нельзя сворачивать в остаток.
func selectRollupRegs(all []*metadata.Register, names []string) ([]*metadata.Register, error) {
	want, err := selectedRollupNameSet("регистр накопления", names)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*metadata.Register, len(all))
	for _, reg := range all {
		byName[rollupNameKey(reg.Name)] = reg
	}
	for _, n := range names {
		reg, ok := byName[rollupNameKey(n)]
		if !ok {
			return nil, fmt.Errorf("регистр накопления %q не найден", strings.TrimSpace(n))
		}
		if reg.IsTurnover() {
			return nil, fmt.Errorf("регистр накопления %q оборотный: его нельзя сворачивать", reg.Name)
		}
	}
	var out []*metadata.Register
	for _, reg := range all {
		if want[rollupNameKey(reg.Name)] {
			out = append(out, reg)
		}
	}
	return out, nil
}

// RollupPreview считает, что сделает свёртка, ничего не записывая (для UI-мастера
// и CLI --dry-run).
func (db *DB) RollupPreview(ctx context.Context, regs []*metadata.Register, ents []*metadata.Entity, accountRegs []*metadata.AccountRegister, infoRegs []*metadata.InfoRegister, opts RollupOptions) (RollupReport, error) {
	cutoff := dayStart(opts.Date)
	rep := RollupReport{Cutoff: cutoff, Preview: true}
	included, err := selectRollupRegs(regs, opts.Registers)
	if err != nil {
		return rep, err
	}
	includedAcc, err := selectRollupAccountRegs(accountRegs, opts.AccountRegisters)
	if err != nil {
		return rep, err
	}
	includedInfo, err := selectRollupInfoRegs(infoRegs, opts.InfoRegisters)
	if err != nil {
		return rep, err
	}
	for _, reg := range included {
		folded, err := db.countMovementsBefore(ctx, reg.Name, cutoff)
		if err != nil {
			return rep, err
		}
		open, err := db.balancesBefore(ctx, reg, cutoff)
		if err != nil {
			return rep, err
		}
		rep.Registers = append(rep.Registers, RollupRegReport{
			Name: reg.Name, FoldedMovements: folded, OpeningRows: len(open),
		})
	}
	for _, ar := range includedAcc {
		r, _, err := db.accountRegReport(ctx, ar, cutoff)
		if err != nil {
			return rep, err
		}
		rep.AccountRegisters = append(rep.AccountRegisters, r)
	}
	for _, ir := range includedInfo {
		r, err := db.infoRegTrimReport(ctx, ir, cutoff)
		if err != nil {
			return rep, err
		}
		rep.InfoRegisters = append(rep.InfoRegisters, r)
	}
	if opts.DeleteDocuments {
		n, err := db.countDocumentsBefore(ctx, ents, cutoff)
		if err != nil {
			return rep, err
		}
		rep.DeletedDocs = n
		dangling, err := db.countDanglingRefs(ctx, ents, cutoff)
		if err != nil {
			return rep, err
		}
		rep.DanglingRefs = dangling
	}
	return rep, nil
}

// countDanglingRefs считает, сколько ссылок повиснет при удалении документов до
// cutoff: записи, которые СОХРАНЯТСЯ после свёртки, но чьё ссылочное поле
// указывает на удаляемый документ (дата < cutoff). Точный счёт (а не оценка):
// источник-документ с датой < cutoff сам удаляется, поэтому его ссылки не висят
// и не учитываются; строки ТЧ удаляются каскадом вместе с шапкой. Используется и
// для отчёта-предупреждения, и для жёсткого гейта в Rollup.
func (db *DB) countDanglingRefs(ctx context.Context, ents []*metadata.Entity, cutoff time.Time) (int, error) {
	d := db.dialect
	docDateCol := make(map[string]string)
	for _, e := range ents {
		if e.Kind == metadata.KindDocument {
			if c := documentDateColumn(e); c != "" {
				docDateCol[e.Name] = c
			}
		}
	}
	if len(docDateCol) == 0 {
		return 0, nil
	}
	total := 0
	// count: COUNT(*) по from, где refExpr указывает на удаляемый документ
	// refTarget; survFilter (если задан) ограничивает счёт сохраняемыми
	// источниками (дата источника >= cutoff).
	count := func(from, refExpr, refTarget, survFilter string) {
		dateCol, ok := docDateCol[refTarget]
		if !ok {
			return // ссылка не на удаляемый по дате документ
		}
		args := []any{cutoff}
		q := fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE %s IN (SELECT id FROM %s WHERE %s < %s)",
			from, refExpr, metadata.TableName(refTarget), dateCol, d.Placeholder(1))
		if survFilter != "" {
			q += fmt.Sprintf(" AND %s >= %s", survFilter, d.Placeholder(2))
			args = append(args, cutoff)
		}
		var n int
		if err := db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			return // нет колонки/таблицы — пропускаем
		}
		total += n
	}
	for _, e := range ents {
		etable := metadata.TableName(e.Name)
		// Шапка сохраняется, если это не удаляемый по дате документ.
		headSurv := ""
		if dc, ok := docDateCol[e.Name]; ok {
			headSurv = dc
		}
		for _, f := range e.Fields {
			if f.RefEntity != "" {
				count(etable, metadata.ColumnName(f), f.RefEntity, headSurv)
			}
		}
		// Строки ТЧ удаляются каскадом с шапкой (parent_id ON DELETE CASCADE) —
		// выживают вместе с родителем, поэтому к датируемому документу-источнику
		// присоединяем шапку и фильтруем по её дате.
		for _, tp := range e.TableParts {
			tptable := metadata.TablePartTableName(e.Name, tp.Name)
			from, refPrefix, survFilter := tptable, "", ""
			if dc, ok := docDateCol[e.Name]; ok {
				from = tptable + " tp JOIN " + etable + " e ON tp.parent_id = e.id"
				refPrefix, survFilter = "tp.", "e."+dc
			}
			for _, f := range tp.Fields {
				if f.RefEntity != "" {
					count(from, refPrefix+metadata.ColumnName(f), f.RefEntity, survFilter)
				}
			}
		}
	}
	return total, nil
}

// Rollup выполняет свёртку базы в одной транзакции с пост-чеком «остатки до ==
// остатки после»: при расхождении — откат и ошибка.
func (db *DB) Rollup(ctx context.Context, regs []*metadata.Register, ents []*metadata.Entity, accountRegs []*metadata.AccountRegister, infoRegs []*metadata.InfoRegister, opts RollupOptions) (RollupReport, error) {
	cutoff := dayStart(opts.Date)
	included, err := selectRollupRegs(regs, opts.Registers)
	if err != nil {
		return RollupReport{Cutoff: cutoff}, err
	}
	includedAcc, err := selectRollupAccountRegs(accountRegs, opts.AccountRegisters)
	if err != nil {
		return RollupReport{Cutoff: cutoff}, err
	}
	includedInfo, err := selectRollupInfoRegs(infoRegs, opts.InfoRegisters)
	if err != nil {
		return RollupReport{Cutoff: cutoff}, err
	}
	if err := db.EnsureRollupTable(ctx); err != nil {
		return RollupReport{}, err
	}
	rep := RollupReport{Cutoff: cutoff}

	err = db.WithTx(ctx, func(ctx context.Context) error {
		if opts.DeleteDocuments {
			// Жёсткий гейт по повисшим ссылкам: отказываем, если на удаляемые
			// документы ссылаются СОХРАНЯЕМЫЕ записи (иначе ссылки повиснут).
			// Безопасный выход — режим «оставить документы» (keep).
			dangling, err := db.countDanglingRefs(ctx, ents, cutoff)
			if err != nil {
				return err
			}
			if dangling > 0 {
				return fmt.Errorf("свёртка отменена: на удаляемые документы ссылается %d сохраняемых записей — ссылки повиснут; "+
					"снимите «Удалить документы» (режим keep) или устраните ссылки", dangling)
			}
			// Откладываем проверку FK до коммита: документы удаляются массово и в
			// произвольном порядке (документ может ссылаться на другой удаляемый
			// документ — порядок удаления тогда не важен). На коммите FK всё равно
			// проверяются — это аппаратный дубль гейта на уровне БД.
			if err := db.deferFKChecks(ctx); err != nil {
				return err
			}
		}

		// Снимок остатков ДО (полные, без фильтра) — для пост-чека.
		before := make(map[string][]map[string]any, len(included))
		for _, reg := range included {
			b, err := db.GetBalances(ctx, reg.Name, reg, RegFilter{})
			if err != nil {
				return err
			}
			before[reg.Name] = b
		}

		var totalFolded, totalOpening int
		for _, reg := range included {
			folded, err := db.countMovementsBefore(ctx, reg.Name, cutoff)
			if err != nil {
				return err
			}
			open, err := db.balancesBefore(ctx, reg, cutoff)
			if err != nil {
				return err
			}
			// Опорные движения на момент cutoff (recorder = новый uuid операции).
			if len(open) > 0 {
				if err := db.WriteMovements(ctx, reg.Name, RollupRecorderType, uuid.New(), open, reg, &cutoff); err != nil {
					return err
				}
			}
			// Удаляем всё свёрнутое: period < cutoff (включая опорные строки
			// прежних свёрток — они станут частью нового опорного остатка).
			// Только что вставленные опорные на period == cutoff не попадают.
			if _, err := db.Exec(ctx, fmt.Sprintf(
				"DELETE FROM %s WHERE period < %s",
				metadata.RegisterTableName(reg.Name), db.dialect.Placeholder(1)), cutoff); err != nil {
				return err
			}
			// Итоги (план 80) считаются из движений и поддерживаются только через
			// WriteMovements. Массовое удаление свёрнутых строк идёт мимо него, поэтому
			// помесячные строки итогов за периоды до cutoff остались бы на месте, а
			// опорные остатки легли бы сверху — быстрый путь query отдал бы задвоенные
			// остатки. Пересчитываем в той же транзакции, уже после удаления.
			if err := db.RecalcRegisterTotals(ctx, reg); err != nil {
				return fmt.Errorf("свёртка регистра %s: пересчёт итогов: %w", reg.Name, err)
			}
			rep.Registers = append(rep.Registers, RollupRegReport{
				Name: reg.Name, FoldedMovements: folded, OpeningRows: len(open),
			})
			totalFolded += folded
			totalOpening += len(open)
		}

		// Пост-чек: полные остатки должны совпасть с зафиксированными ДО.
		for _, reg := range included {
			after, err := db.GetBalances(ctx, reg.Name, reg, RegFilter{})
			if err != nil {
				return err
			}
			if !balancesEqual(before[reg.Name], after, reg) {
				return fmt.Errorf("свёртка регистра %s: остатки до и после не совпали — откат", reg.Name)
			}
		}

		// Регистры бухгалтерии: опорные проводки через вспомогательный счёт.
		for _, ar := range includedAcc {
			r, err := db.foldAccountReg(ctx, ar, cutoff)
			if err != nil {
				return err
			}
			rep.AccountRegisters = append(rep.AccountRegisters, r)
		}

		// Регистры сведений: обрезка периодических (оставить последний срез до D).
		for _, ir := range includedInfo {
			r, err := db.trimInfoReg(ctx, ir, cutoff)
			if err != nil {
				return err
			}
			rep.InfoRegisters = append(rep.InfoRegisters, r)
		}

		// Документы: удалить или снять проведение.
		deleted, err := db.applyRollupDocPolicy(ctx, ents, cutoff, opts.DeleteDocuments, opts.BeforeDeleteDocument)
		if err != nil {
			return err
		}
		rep.DeletedDocs = deleted

		// Дата запрета проведения = cutoff (защищает опорные остатки и keep-режим).
		if err := db.SavePostingLockDate(ctx, cutoff); err != nil {
			return err
		}

		return db.logRollup(ctx, cutoff, included, totalFolded, totalOpening, deleted, opts.DeleteDocuments)
	})
	if err != nil {
		return RollupReport{}, err
	}
	return rep, nil
}

// countMovementsBefore — число движений регистра с period < cutoff.
func (db *DB) countMovementsBefore(ctx context.Context, regName string, cutoff time.Time) (int, error) {
	var n int
	err := db.QueryRow(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE period < %s",
		metadata.RegisterTableName(regName), db.dialect.Placeholder(1)), cutoff).Scan(&n)
	return n, err
}

// balancesBefore считает остатки регистра по движениям строго ДО cutoff
// (period < cutoff), сгруппированные по измерениям. Знаковая сумма ресурсов —
// идентично GetBalances, чтобы пост-чек и UI считали остатки одинаково. Нулевые
// комбинации (все ресурсы ≈ 0) пропускаются — опорная строка им не нужна.
func (db *DB) balancesBefore(ctx context.Context, reg *metadata.Register, cutoff time.Time) ([]map[string]any, error) {
	d := db.dialect
	table := metadata.RegisterTableName(reg.Name)

	var selectParts, groupBy, dimNames, resNames []string
	for _, f := range reg.Dimensions {
		col := metadata.ColumnName(f)
		selectParts = append(selectParts, col)
		groupBy = append(groupBy, col)
		dimNames = append(dimNames, f.Name)
	}
	for _, f := range reg.Resources {
		col := metadata.ColumnName(f)
		selectParts = append(selectParts, fmt.Sprintf(
			"SUM(CASE WHEN вид_движения = 'Приход' THEN %s ELSE -%s END) AS %s", col, col, col))
		resNames = append(resNames, f.Name)
	}

	q := fmt.Sprintf("SELECT %s FROM %s WHERE period < %s",
		strings.Join(selectParts, ", "), table, d.Placeholder(1))
	if len(groupBy) > 0 {
		q += " GROUP BY " + strings.Join(groupBy, ", ")
	}
	rows, err := db.Query(ctx, q, cutoff)
	if err != nil {
		return nil, fmt.Errorf("rollup balances %s: %w", reg.Name, err)
	}
	defer rows.Close()

	total := len(reg.Dimensions) + len(reg.Resources)
	var result []map[string]any
	for rows.Next() {
		dest := make([]any, total)
		ptrs := make([]any, total)
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, total)
		for i, name := range dimNames {
			row[name] = normalizeValue(dest[i])
		}
		nonZero := false
		for i, name := range resNames {
			v := normalizeNumber(dest[len(reg.Dimensions)+i])
			row[name] = v
			if absFloat(toFloat(v)) > 1e-9 {
				nonZero = true
			}
		}
		if !nonZero {
			continue
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// balancesEqual сравнивает два набора остатков (как их возвращает GetBalances) по
// комбинации измерений с допуском по ресурсам. Используется пост-чеком свёртки.
func balancesEqual(before, after []map[string]any, reg *metadata.Register) bool {
	key := func(row map[string]any) string {
		var parts []string
		for _, f := range reg.Dimensions {
			parts = append(parts, fmt.Sprintf("%v", row[f.Name]))
		}
		return strings.Join(parts, "\x1f")
	}
	index := func(rows []map[string]any) map[string]map[string]float64 {
		m := make(map[string]map[string]float64, len(rows))
		for _, row := range rows {
			res := make(map[string]float64, len(reg.Resources))
			for _, f := range reg.Resources {
				res[f.Name] = toFloat(row[f.Name])
			}
			m[key(row)] = res
		}
		return m
	}
	bi, ai := index(before), index(after)
	if len(bi) != len(ai) {
		return false
	}
	for k, bres := range bi {
		ares, ok := ai[k]
		if !ok {
			return false
		}
		for _, f := range reg.Resources {
			if absFloat(bres[f.Name]-ares[f.Name]) > 1e-6 {
				return false
			}
		}
	}
	return true
}

// deferFKChecks откладывает проверку внешних ключей до коммита текущей
// транзакции, чтобы массовое удаление документов не упиралось в порядок удаления
// (документ, на который ссылается другой удаляемый документ). SQLite умеет
// PRAGMA defer_foreign_keys (сбрасывается на коммите). На PostgreSQL наши FK не
// объявлены DEFERRABLE — там порядок удаления решает orderByDependency при
// миграции, а гейт countDanglingRefs защищает от висячих ссылок; поэтому no-op.
func (db *DB) deferFKChecks(ctx context.Context) error {
	if db.dialect.Name() != "sqlite" {
		return nil
	}
	_, err := db.Exec(ctx, "PRAGMA defer_foreign_keys=ON")
	return err
}

// applyRollupDocPolicy удаляет (или снимает проведение) документы с датой < cutoff.
// Возвращает число удалённых документов (0 при keep-режиме). Дата документа —
// первое поле типа date; сущности без даты пропускаются.
func (db *DB) applyRollupDocPolicy(ctx context.Context, ents []*metadata.Entity, cutoff time.Time, del bool, beforeDelete func(context.Context, *metadata.Entity, uuid.UUID) error) (int, error) {
	d := db.dialect
	deleted := 0
	for _, e := range ents {
		if e.Kind != metadata.KindDocument {
			continue
		}
		dateCol := documentDateColumn(e)
		if dateCol == "" {
			continue
		}
		table := metadata.TableName(e.Name)
		if del {
			rows, err := db.Query(ctx, fmt.Sprintf(
				"SELECT id FROM %s WHERE %s < %s", table, dateCol, d.Placeholder(1)), cutoff)
			if err != nil {
				return deleted, err
			}
			var ids []uuid.UUID
			for rows.Next() {
				var raw any
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return deleted, err
				}
				if id, ok := parseUUIDValue(raw); ok {
					ids = append(ids, id)
				}
			}
			rows.Close()
			for _, id := range ids {
				if beforeDelete != nil {
					if err := beforeDelete(ctx, e, id); err != nil {
						return deleted, fmt.Errorf("свёртка: регистрация удаления %s %s: %w", e.Name, id, err)
					}
				}
				if err := db.Delete(ctx, e.Name, id); err != nil {
					return deleted, fmt.Errorf("свёртка: удаление %s %s: %w", e.Name, id, err)
				}
				deleted++
			}
		} else if e.Posting {
			// Снять проведение у старых документов: их движения уже свёрнуты,
			// «проведён» без движений — противоречивое состояние.
			if _, err := db.Exec(ctx, fmt.Sprintf(
				"UPDATE %s SET posted = %s WHERE %s < %s AND posted = %s",
				table, boolFalseLit(d), dateCol, d.Placeholder(1), boolTrueLitS(d)), cutoff); err != nil {
				return deleted, err
			}
		}
	}
	return deleted, nil
}

// countDocumentsBefore — число документов с датой < cutoff (для предпросмотра
// удаления).
func (db *DB) countDocumentsBefore(ctx context.Context, ents []*metadata.Entity, cutoff time.Time) (int, error) {
	d := db.dialect
	total := 0
	for _, e := range ents {
		if e.Kind != metadata.KindDocument {
			continue
		}
		dateCol := documentDateColumn(e)
		if dateCol == "" {
			continue
		}
		var n int
		if err := db.QueryRow(ctx, fmt.Sprintf(
			"SELECT COUNT(*) FROM %s WHERE %s < %s",
			metadata.TableName(e.Name), dateCol, d.Placeholder(1)), cutoff).Scan(&n); err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// documentDateColumn возвращает имя колонки первого date-поля документа (его
// «даты»), либо "" если такого поля нет.
func documentDateColumn(e *metadata.Entity) string {
	for _, f := range e.Fields {
		if f.Type == metadata.FieldTypeDate {
			return metadata.ColumnName(f)
		}
	}
	return ""
}

// logRollup пишет запись в журнал _rollup.
func (db *DB) logRollup(ctx context.Context, cutoff time.Time, regs []*metadata.Register, folded, opening, deletedDocs int, docsDeleted bool) error {
	names := make([]string, 0, len(regs))
	for _, r := range regs {
		names = append(names, r.Name)
	}
	d := db.dialect
	docsDel := 0
	if docsDeleted {
		docsDel = 1
	}
	user, _ := auditUserFromCtx(ctx)
	_, err := db.Exec(ctx, fmt.Sprintf(
		`INSERT INTO _rollup (id, cutoff, created_at, created_by, registers, folded_movements, opening_rows, deleted_documents, documents_deleted)
		 VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9)),
		uuid.New().String(), cutoff.Format("2006-01-02"), time.Now().Format(time.RFC3339),
		user.UserLogin, strings.Join(names, ", "), folded, opening, deletedDocs, docsDel)
	return err
}

// ── Свёртка регистров бухгалтерии (акк_*, план 74) ───────────────────────────

// selectRollupAccountRegs отбирает регистры бухгалтерии по именам (как
// selectRollupRegs для накопления).
func selectRollupAccountRegs(all []*metadata.AccountRegister, names []string) ([]*metadata.AccountRegister, error) {
	want, err := selectedRollupNameSet("регистр бухгалтерии", names)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*metadata.AccountRegister, len(all))
	for _, ar := range all {
		byName[rollupNameKey(ar.Name)] = ar
	}
	for _, n := range names {
		if _, ok := byName[rollupNameKey(n)]; !ok {
			return nil, fmt.Errorf("регистр бухгалтерии %q не найден", strings.TrimSpace(n))
		}
	}
	var out []*metadata.AccountRegister
	for _, ar := range all {
		if want[rollupNameKey(ar.Name)] {
			out = append(out, ar)
		}
	}
	return out, nil
}

const rollupAuxAccountKey = "rollup.aux_account"

// GetRollupAuxAccount возвращает код вспомогательного счёта свёртки из настроек ("" если не задан).
func (db *DB) GetRollupAuxAccount(ctx context.Context) string {
	var v string
	if err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+db.dialect.Placeholder(1), rollupAuxAccountKey).Scan(&v); err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// SaveRollupAuxAccount сохраняет код вспомогательного счёта свёртки.
func (db *DB) SaveRollupAuxAccount(ctx context.Context, code string) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	_, err := db.Exec(ctx, q, rollupAuxAccountKey, strings.TrimSpace(code))
	return err
}

// resolveAuxAccount определяет вспомогательный счёт для плана: настройка
// rollup.aux_account (если задана и счёт есть в плане), иначе счёт с кодом «000».
func (db *DB) resolveAuxAccount(ctx context.Context, plan string) (string, bool) {
	if code := db.GetRollupAuxAccount(ctx); code != "" && db.accountExists(ctx, plan, code) {
		return code, true
	}
	if db.accountExists(ctx, plan, "000") {
		return "000", true
	}
	return "", false
}

func (db *DB) accountExists(ctx context.Context, plan, code string) bool {
	d := db.dialect
	var one int
	err := db.QueryRow(ctx, fmt.Sprintf(
		"SELECT 1 FROM _accounts WHERE plan = %s AND code = %s", d.Placeholder(1), d.Placeholder(2)),
		plan, code).Scan(&one)
	return err == nil
}

func (db *DB) countAccountMovementsBefore(ctx context.Context, regName string, cutoff time.Time) (int, error) {
	var n int
	err := db.QueryRow(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE period < %s",
		metadata.AccountRegTableName(regName), db.dialect.Placeholder(1)), cutoff).Scan(&n)
	return n, err
}

// accountOpeningRows строит опорные проводки по остаткам счетов до cutoff: для
// дебетового сальдо ресурса — Дт счёт / Кт вспомогательный, для кредитового —
// наоборот. Субконто переносятся (пустые → NULL, чтобы совпасть с группировкой
// исходных движений). Вспомогательный счёт пропускается.
func (db *DB) accountOpeningRows(ctx context.Context, ar *metadata.AccountRegister, aux string, cutoff time.Time) ([]map[string]any, error) {
	bal, err := db.AccountBalances(ctx, ar.Name, ar.Accounts, cutoff.Add(-time.Second), ar.Resources, ar.Subconto)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, b := range bal {
		code, _ := b["code"].(string)
		if code == "" || code == aux {
			continue
		}
		sub := make(map[string]any, len(ar.Subconto))
		for i := range ar.Subconto {
			col := metadata.SubcontoColumn(i + 1)
			v := b[col]
			if s, ok := v.(string); ok && s == "" {
				v = nil // NULL, чтобы группировка GROUP BY совпала с исходными
			}
			sub[col] = v
		}
		dtRow := map[string]any{"счётдт": code, "счёткт": aux}
		ktRow := map[string]any{"счётдт": aux, "счёткт": code}
		for k, v := range sub {
			dtRow[k] = v
			ktRow[k] = v
		}
		dtHas, ktHas := false, false
		for _, r := range ar.Resources {
			col := metadata.ColumnName(r)
			// Сырой дебет минус кредит (НЕ b[col]: тот скорректирован по виду
			// счёта — для пассивного это Кт−Дт, что исказило бы сторону проводки).
			// Считаем в decimal: опорные проводки — это денежные суммы, которые
			// становятся входящим остатком, и округление float64 осело бы в базе
			// навсегда.
			net := toDecimal(b[col+"_дт"]).Sub(toDecimal(b[col+"_кт"]))
			switch {
			case net.IsPositive():
				dtRow[r.Name] = net
				dtHas = true
			case net.IsNegative():
				ktRow[r.Name] = net.Neg()
				ktHas = true
			}
		}
		if dtHas {
			rows = append(rows, dtRow)
		}
		if ktHas {
			rows = append(rows, ktRow)
		}
	}
	return rows, nil
}

// accountRegReport — отчёт по бухрегистру для предпросмотра (без записи).
// Возвращает также построенные опорные строки (переиспользуются при свёртке).
func (db *DB) accountRegReport(ctx context.Context, ar *metadata.AccountRegister, cutoff time.Time) (RollupRegReport, []map[string]any, error) {
	rep := RollupRegReport{Name: ar.Name}
	folded, err := db.countAccountMovementsBefore(ctx, ar.Name, cutoff)
	if err != nil {
		return rep, nil, err
	}
	rep.FoldedMovements = folded
	aux, ok := db.resolveAuxAccount(ctx, ar.Accounts)
	if !ok {
		rep.Note = "пропущен: нет вспомогательного счёта (задайте rollup.aux_account или счёт 000)"
		return rep, nil, nil
	}
	rows, err := db.accountOpeningRows(ctx, ar, aux, cutoff)
	if err != nil {
		return rep, nil, err
	}
	rep.OpeningRows = len(rows)
	return rep, rows, nil
}

// foldAccountReg сворачивает один бухрегистр: пишет опорные проводки, удаляет
// движения до cutoff, проверяет неизменность остатков (кроме вспомогательного
// счёта-абсорбера) — иначе ошибка и откат всей транзакции.
func (db *DB) foldAccountReg(ctx context.Context, ar *metadata.AccountRegister, cutoff time.Time) (RollupRegReport, error) {
	rep, rows, err := db.accountRegReport(ctx, ar, cutoff)
	if err != nil || rep.Note != "" {
		return rep, err // пропущен (нет вспомогательного счёта) либо ошибка
	}
	aux, _ := db.resolveAuxAccount(ctx, ar.Accounts)
	before, err := db.AccountBalances(ctx, ar.Name, ar.Accounts, cutoff, ar.Resources, ar.Subconto)
	if err != nil {
		return rep, err
	}
	if len(rows) > 0 {
		if err := db.WriteAccountMovements(ctx, ar.Name, RollupRecorderType, uuid.New(), rows, ar, &cutoff); err != nil {
			return rep, err
		}
	}
	if _, err := db.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE period < %s",
		metadata.AccountRegTableName(ar.Name), db.dialect.Placeholder(1)), cutoff); err != nil {
		return rep, err
	}
	// См. комментарий в Rollup: удаление свёрнутых проводок идёт мимо
	// WriteAccountMovements, поэтому итоги бухрегистра пересчитываем явно.
	if err := db.RecalcAccountRegisterTotals(ctx, ar); err != nil {
		return rep, fmt.Errorf("свёртка бухрегистра %s: пересчёт итогов: %w", ar.Name, err)
	}
	after, err := db.AccountBalances(ctx, ar.Name, ar.Accounts, cutoff, ar.Resources, ar.Subconto)
	if err != nil {
		return rep, err
	}
	if !accountBalancesEqual(before, after, ar, aux) {
		return rep, fmt.Errorf("свёртка бухрегистра %s: остатки до и после не совпали — откат", ar.Name)
	}
	return rep, nil
}

// accountBalancesEqual сравнивает остатки счетов (нетто Дт−Кт по ресурсам) по
// комбинации счёт×субконто, исключая вспомогательный счёт (он абсорбирует опорные
// проводки и меняется по замыслу) и нулевые комбинации.
func accountBalancesEqual(before, after []map[string]any, ar *metadata.AccountRegister, aux string) bool {
	key := func(b map[string]any) string {
		code, _ := b["code"].(string)
		parts := []string{code}
		for i := range ar.Subconto {
			parts = append(parts, fmt.Sprintf("%v", b[metadata.SubcontoColumn(i+1)]))
		}
		return strings.Join(parts, "\x1f")
	}
	index := func(rows []map[string]any) map[string]map[string]float64 {
		m := make(map[string]map[string]float64)
		for _, b := range rows {
			if code, _ := b["code"].(string); code == aux {
				continue
			}
			res := make(map[string]float64, len(ar.Resources))
			nonZero := false
			for _, r := range ar.Resources {
				col := metadata.ColumnName(r)
				v := toFloat(b[col+"_дт"]) - toFloat(b[col+"_кт"]) // сырой Дт−Кт, вид-независимо
				res[r.Name] = v
				if absFloat(v) > 1e-6 {
					nonZero = true
				}
			}
			if nonZero {
				m[key(b)] = res
			}
		}
		return m
	}
	bi, ai := index(before), index(after)
	if len(bi) != len(ai) {
		return false
	}
	for k, bres := range bi {
		ares, ok := ai[k]
		if !ok {
			return false
		}
		for _, r := range ar.Resources {
			if absFloat(bres[r.Name]-ares[r.Name]) > 1e-6 {
				return false
			}
		}
	}
	return true
}

// ── Обрезка периодических регистров сведений (план 74, опционально) ───────────
//
// Нестандартно для 1С: свёртка обычно не трогает регистры сведений. Обрезка
// оставляет по одному (последнему) срезу до даты свёртки на каждую комбинацию
// измерений, удаляя более раннюю историю. СрезПоследних на любую дату >= cutoff
// не меняется; СрезПервых и историю срезов до cutoff обрезка ломает — поэтому
// включается только явным перечислением регистров.

// selectRollupInfoRegs отбирает регистры сведений по именам (как selectRollupRegs).
func selectRollupInfoRegs(all []*metadata.InfoRegister, names []string) ([]*metadata.InfoRegister, error) {
	want, err := selectedRollupNameSet("регистр сведений", names)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*metadata.InfoRegister, len(all))
	for _, ir := range all {
		byName[rollupNameKey(ir.Name)] = ir
	}
	for _, n := range names {
		if _, ok := byName[rollupNameKey(n)]; !ok {
			return nil, fmt.Errorf("регистр сведений %q не найден", strings.TrimSpace(n))
		}
	}
	var out []*metadata.InfoRegister
	for _, ir := range all {
		if want[rollupNameKey(ir.Name)] {
			out = append(out, ir)
		}
	}
	return out, nil
}

// infoTrimCounts считает по периодическому регистру: сколько строк до cutoff
// будет удалено (toTrim) и сколько срезов останется (kept — по одному на
// комбинацию измерений). kept = число различных комбинаций измерений среди строк
// с period < cutoff; toTrim = всего таких строк − kept.
func (db *DB) infoTrimCounts(ctx context.Context, ir *metadata.InfoRegister, cutoff time.Time) (toTrim, kept int, err error) {
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)
	var total int
	if err = db.QueryRow(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE period < %s", table, d.Placeholder(1)), cutoff).Scan(&total); err != nil {
		return 0, 0, err
	}
	if total == 0 {
		return 0, 0, nil
	}
	if len(ir.Dimensions) == 0 {
		return total - 1, 1, nil // одна «серия» — остаётся последний срез
	}
	if err = db.QueryRow(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM (SELECT DISTINCT %s FROM %s WHERE period < %s) t",
		strings.Join(infoDimCols(ir), ", "), table, d.Placeholder(1)), cutoff).Scan(&kept); err != nil {
		return 0, 0, err
	}
	return total - kept, kept, nil
}

// infoDimCols — имена колонок измерений регистра сведений.
func infoDimCols(ir *metadata.InfoRegister) []string {
	cols := make([]string, 0, len(ir.Dimensions))
	for _, f := range ir.Dimensions {
		cols = append(cols, metadata.ColumnName(f))
	}
	return cols
}

// infoRegTrimReport — предпросмотр обрезки одного регистра сведений (без записи).
func (db *DB) infoRegTrimReport(ctx context.Context, ir *metadata.InfoRegister, cutoff time.Time) (RollupRegReport, error) {
	rep := RollupRegReport{Name: ir.Name}
	if !ir.Periodic {
		rep.Note = "пропущен: непериодический регистр сведений не обрезается"
		return rep, nil
	}
	toTrim, kept, err := db.infoTrimCounts(ctx, ir, cutoff)
	if err != nil {
		return rep, err
	}
	rep.FoldedMovements, rep.OpeningRows = toTrim, kept
	return rep, nil
}

// trimInfoReg обрезает один периодический регистр сведений: для каждой комбинации
// измерений оставляет запись с наибольшим period < cutoff, удаляя более ранние;
// записи с period >= cutoff не трогаются. Пост-чек: после обрезки на каждую
// комбинацию приходится не более одной строки до cutoff, а их общее число равно
// зафиксированному kept — иначе ошибка и откат всей транзакции.
func (db *DB) trimInfoReg(ctx context.Context, ir *metadata.InfoRegister, cutoff time.Time) (RollupRegReport, error) {
	rep, err := db.infoRegTrimReport(ctx, ir, cutoff)
	if err != nil || rep.Note != "" {
		return rep, err // непериодический — пропуск, либо ошибка
	}
	kept := rep.OpeningRows
	d := db.dialect
	table := metadata.InfoRegTableName(ir.Name)

	// Удаляем строки до cutoff, для которых в той же комбинации измерений есть
	// более поздняя строка (тоже до cutoff) — то есть всё, кроме последнего среза.
	// Сравнение измерений NULL-safe (поле могло быть добавлено миграцией).
	exists := fmt.Sprintf(
		"SELECT 1 FROM %s t2 WHERE t2.period < %s AND t2.period > %s.period",
		table, d.Placeholder(2), table)
	for _, f := range ir.Dimensions {
		col := metadata.ColumnName(f)
		exists += fmt.Sprintf(" AND (t2.%s = %s.%s OR (t2.%s IS NULL AND %s.%s IS NULL))",
			col, table, col, col, table, col)
	}
	del := fmt.Sprintf("DELETE FROM %s WHERE period < %s AND EXISTS (%s)", table, d.Placeholder(1), exists)
	if _, err := db.Exec(ctx, del, cutoff, cutoff); err != nil {
		return rep, fmt.Errorf("обрезка регистра сведений %s: %w", ir.Name, err)
	}

	// Пост-чек: до cutoff осталось ровно kept строк (по одной на комбинацию).
	var after int
	if err := db.QueryRow(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s WHERE period < %s", table, d.Placeholder(1)), cutoff).Scan(&after); err != nil {
		return rep, err
	}
	if after != kept {
		return rep, fmt.Errorf("обрезка регистра сведений %s: осталось %d срезов до даты, ожидалось %d — откат", ir.Name, after, kept)
	}
	if len(ir.Dimensions) > 0 {
		var dup int
		if err := db.QueryRow(ctx, fmt.Sprintf(
			"SELECT COUNT(*) FROM (SELECT 1 FROM %s WHERE period < %s GROUP BY %s HAVING COUNT(*) > 1) t",
			table, d.Placeholder(1), strings.Join(infoDimCols(ir), ", ")), cutoff).Scan(&dup); err != nil {
			return rep, err
		}
		if dup > 0 {
			return rep, fmt.Errorf("обрезка регистра сведений %s: остались дубли срезов до даты — откат", ir.Name)
		}
	}
	return rep, nil
}

// ── Дата запрета проведения (план 74) ────────────────────────────────────────

const postingLockKey = "rollup.posting_lock_date"

// GetPostingLockDate возвращает дату запрета проведения (документы с датой <=
// этой даты нельзя проводить/перепроводить) и признак её наличия. Отсутствие
// ключа/таблицы — (нулевое время, false).
func (db *DB) GetPostingLockDate(ctx context.Context) (time.Time, bool) {
	d := db.dialect
	var v string
	if err := db.QueryRow(ctx,
		`SELECT value FROM _settings WHERE key = `+d.Placeholder(1), postingLockKey).Scan(&v); err != nil {
		return time.Time{}, false
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// SavePostingLockDate сохраняет дату запрета проведения. Нулевое время очищает
// настройку (запрет снят).
func (db *DB) SavePostingLockDate(ctx context.Context, t time.Time) error {
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	if t.IsZero() {
		_, err := db.Exec(ctx, `DELETE FROM _settings WHERE key = `+d.Placeholder(1), postingLockKey)
		return err
	}
	q := fmt.Sprintf(
		`INSERT INTO _settings (key, value) VALUES (%s, %s)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		d.Placeholder(1), d.Placeholder(2))
	_, err := db.Exec(ctx, q, postingLockKey, dayStart(t).Format("2006-01-02"))
	return err
}

// PostingLockViolation сообщает, запрещено ли проведение документа из-за даты
// запрета: true, если дата документа <= даты запрета. Сущности без date-поля и
// отсутствие запрета → false.
func (db *DB) PostingLockViolation(ctx context.Context, entity *metadata.Entity, id uuid.UUID) (bool, time.Time, error) {
	lock, ok := db.GetPostingLockDate(ctx)
	if !ok {
		return false, time.Time{}, nil
	}
	dateCol := documentDateColumn(entity)
	if dateCol == "" {
		return false, lock, nil
	}
	d := db.dialect
	var raw any
	err := db.QueryRow(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE id = %s",
		dateCol, metadata.TableName(entity.Name), d.Placeholder(1)), idArg(d, id)).Scan(&raw)
	if err != nil {
		return false, lock, nil
	}
	docDate, ok := parseTimeValue(raw)
	if !ok {
		return false, lock, nil
	}
	// Запрет включает саму дату запрета: документ этого дня и ранее «заморожен».
	return !dayStart(docDate).After(lock), lock, nil
}

// PostingFrozen сообщает, попадает ли дата документа в свёрнутый («заморожённый»)
// период: true, если date по дню <= даты запрета проведения. Используется
// guard'ами проведения (план 74) в ui/entityservice.
func PostingFrozen(lock, date time.Time) bool {
	return !dayStart(date).After(lock)
}

// PostingFrozenError — единый текст ошибки запрета проведения с датой запрета.
func PostingFrozenError(lock time.Time) error {
	return fmt.Errorf("проведение запрещено: документ относится к свёрнутому периоду (дата запрета — %s)", lock.Format("02.01.2006"))
}

// ── мелкие помощники ─────────────────────────────────────────────────────────

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// toDecimal приводит значение остатка к decimal.Decimal (деньги считаем точно).
// Непонятный тип/NULL → 0, как COALESCE в запросах остатков.
func toDecimal(v any) decimal.Decimal {
	if d, ok := normalizeNumber(v).(decimal.Decimal); ok {
		return d
	}
	return decimal.Zero
}

// toFloat приводит значение остатка (decimal/float64/строка/json.Number/…) к
// float64. Годится для сравнений с допуском, но не для денежной арифметики —
// для неё toDecimal.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case nil:
		return 0
	case decimal.Decimal:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(fmt.Sprintf("%v", v)), 64)
	return f
}

// boolTrueLitS — строковый литерал true для текущего диалекта (пара к boolFalseLit).
func boolTrueLitS(d Dialect) string {
	if d.Name() == "sqlite" {
		return "1"
	}
	return "TRUE"
}

// parseUUIDValue извлекает uuid из значения колонки id (string/[]byte/uuid.UUID).
func parseUUIDValue(v any) (uuid.UUID, bool) {
	switch x := v.(type) {
	case uuid.UUID:
		return x, true
	case string:
		if id, err := uuid.Parse(strings.TrimSpace(x)); err == nil {
			return id, true
		}
	case []byte:
		if id, err := uuid.Parse(strings.TrimSpace(string(x))); err == nil {
			return id, true
		}
	}
	return uuid.UUID{}, false
}

// parseTimeValue извлекает время из значения колонки даты (time.Time/строка).
func parseTimeValue(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(x)); err == nil {
				return t, true
			}
		}
	case []byte:
		return parseTimeValue(string(x))
	}
	return time.Time{}, false
}
