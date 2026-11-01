package storage

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Переименование таблиц и колонок с длинными именами в короткие (#1946).
//
// До metadata.SQLIdent имя длиннее 63 байт уходило в базу как есть: SQLite
// хранила его полностью, PostgreSQL — обрезанным. Теперь у такого имени другое
// физическое имя, и без этого шага миграция завела бы рядом новую пустую
// таблицу или колонку, а данные остались бы в старой, невидимой приложению.
// Переименование — ALTER … RENAME: данные не копируются, индексы и внешние
// ключи следуют за таблицей. Для коротких имён (почти всех) шаг ничего не
// делает и в базу не ходит.

// LongIdentAction — что миграция сделает с одним длинным именем (#1946):
// переименует прежнее физическое имя (Old → New) или откажет (Conflict).
// Имена, которым ничего не нужно (уже короткое, ещё не созданное), в план не
// попадают. План печатает `onebase migrate --dry-run`, по нему же работает
// сама миграция — инвентаризация и действие не расходятся.
type LongIdentAction struct {
	Table    string // таблица колонки; у переименования таблицы — пусто
	Old, New string
	Conflict string // непусто — миграция откажет, ничего не трогая в этом имени
}

func (a LongIdentAction) String() string {
	what := "таблица " + a.Old
	if a.Table != "" {
		what = "колонка " + a.Table + "." + a.Old
	}
	if a.Conflict != "" {
		return what + ": КОНФЛИКТ — " + a.Conflict
	}
	return what + " → " + a.New
}

func (a LongIdentAction) err() error {
	return fmt.Errorf("длинное имя (#1946): %s. Миграция остановлена: данные не переносятся без доказательства сохранности — см. docs/long-identifiers.md", a)
}

// planLegacyTable решает судьбу таблицы с логическим именем logical. claims —
// сколько таблиц конфигурации претендуют на имя: прежнее или текущее (nil — одна).
// Неоднозначность — отказ, а не угадывание (решение владельца по #1946):
// прежнее имя, общее для двух таблиц (PostgreSQL обрезал их одинаково), и
// пара «новая таблица + прежняя с данными» не разрешаются автоматически.
func (db *DB) planLegacyTable(ctx context.Context, logical string, claims map[string]int) (*LongIdentAction, error) {
	legacy := metadata.LegacySQLIdents(logical)
	if legacy == nil {
		return nil, nil
	}
	target := metadata.SQLIdent(logical)
	exists, err := db.tableExists(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("длинное имя %s: %w", logical, err)
	}
	// Сначала обследуем все физически возможные имена. На SQLite полное
	// прежнее имя не доказывает принадлежность его обрезки: она может быть
	// действующей таблицей другого объекта. Отказываем до первого ALTER.
	var foundNames []string
	for _, old := range db.physicalLegacyNames(legacy) {
		found, err := db.tableExists(ctx, old)
		if err != nil {
			return nil, fmt.Errorf("длинное имя %s: %w", logical, err)
		}
		if !found {
			continue
		}
		if claims[old] > 1 {
			return &LongIdentAction{Old: old, New: target, Conflict: fmt.Sprintf(
				"на прежнее имя претендуют %d таблицы конфигурации, чья это таблица — не установить", claims[old])}, nil
		}
		foundNames = append(foundNames, old)
	}
	if !exists && len(foundNames) > 1 {
		return &LongIdentAction{Old: foundNames[0], New: target, Conflict: "есть несколько прежних таблиц (" + strings.Join(foundNames, ", ") + "), чья это таблица — не установить"}, nil
	}
	for _, old := range foundNames {
		if exists {
			has, err := db.tableHasRows(ctx, old)
			if err != nil {
				return nil, fmt.Errorf("длинное имя %s: %w", logical, err)
			}
			if has {
				return &LongIdentAction{Old: old, New: target, Conflict: "есть и новая таблица " + target +
					", и прежняя с данными — какие данные верные, не установить"}, nil
			}
			continue // прежняя пуста — переносить нечего
		}
		return &LongIdentAction{Old: old, New: target}, nil
	}
	return nil, nil
}

// renameLegacyTable переименовывает таблицу с логическим именем logical из её
// прежнего физического имени в metadata.SQLIdent(logical); конфликт — отказ.
func (db *DB) renameLegacyTable(ctx context.Context, logical string) error {
	return db.renameLegacyTableClaimed(ctx, logical, nil)
}

func (db *DB) renameLegacyTableClaimed(ctx context.Context, logical string, claims map[string]int) error {
	a, err := db.planLegacyTable(ctx, logical, claims)
	if err != nil || a == nil {
		return err
	}
	if a.Conflict != "" {
		return a.err()
	}
	if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", a.Old, a.New)); err != nil {
		return fmt.Errorf("переименование таблицы %s → %s: %w", a.Old, a.New, err)
	}
	if err := db.renameSchemaMapTable(ctx, a.Old, a.New); err != nil {
		return err
	}
	slog.Info("длинное имя таблицы сокращено", "было", a.Old, "стало", a.New)
	return nil
}

// physicalLegacyNames — прежние имена, которые могли физически существовать в
// этой СУБД. На PostgreSQL имя длиннее 63 байт в базе быть не могло, а поиск
// по нему находит обрезанное (сервер обрезает идентификатор и в запросе):
// таблица с именем, общим для двух логических, выглядела бы как «своя» по
// полному имени, и отказ по неоднозначности не срабатывал.
func (db *DB) physicalLegacyNames(legacy []string) []string {
	out := uniqueStrings(legacy)
	if db.IsSQLite() {
		return out
	}
	kept := out[:0:0]
	for _, n := range out {
		if len(n) <= metadata.MaxSQLIdentBytes {
			kept = append(kept, n)
		}
	}
	return kept
}

// legacyTableClaims — сколько логических таблиц претендуют на каждое прежнее
// или текущее имя. Учитываем также короткие имена: они могут совпасть
// с обрезкой чужого длинного имени, но принадлежат действующему объекту.
func legacyTableClaims(logicals []string) map[string]int {
	claims := map[string]int{}
	for _, l := range logicals {
		for _, old := range uniqueStrings(append(metadata.LegacySQLIdents(l), metadata.SQLIdent(l))) {
			claims[old]++
		}
	}
	return claims
}

// finalNameOwners — конечные физические имена (SQLIdent), на которые
// претендуют несколько логических имён, хотя бы одно из них длинное: короткое
// имя, совпавшее с «началом_хешем» длинного, или два длинных с одним хешем.
// Прежнего имени у такого случая может не быть вовсе — новое длинное поле
// просто легло бы в существующую колонку с чужими данными, — поэтому
// проверяется сама конфигурация, а не только найденная в базе обрезка.
// Ключ — физическое имя, значение — его логические владельцы по порядку.
func finalNameOwners(logicals []string) map[string][]string {
	owners := map[string][]string{}
	var order []string
	for _, l := range uniqueStrings(logicals) {
		target := metadata.SQLIdent(l)
		if _, ok := owners[target]; !ok {
			order = append(order, target)
		}
		owners[target] = append(owners[target], l)
	}
	out := map[string][]string{}
	for _, target := range order {
		names := owners[target]
		if len(names) < 2 {
			continue
		}
		for _, l := range names {
			if metadata.LegacySQLIdents(l) != nil {
				out[target] = names
				break
			}
		}
	}
	return out
}

// finalNameConflict — отказ по общему конечному имени: table пусто — таблица.
func finalNameConflict(table, target string, names []string) LongIdentAction {
	what, object := "таблиц", "одна таблица"
	if table != "" {
		what, object = "полей", "одна колонка"
	}
	return LongIdentAction{Table: table, Old: target, New: target, Conflict: fmt.Sprintf(
		"на физическое имя претендуют %d %s конфигурации (%s) — %s на разные данные",
		len(names), what, strings.Join(names, ", "), object)}
}

// currentLegacyConflicts проверяет конфигурацию, а не состояние базы: конечное
// имя одного владельца не может одновременно быть возможным прежним именем
// другого. Иначе свежая схема сама создаёт неоднозначность для второго запуска.
// Общая legacy-обрезка двух длинных имён без действующего владельца допустима:
// только реальные старые данные под ней требуют отказа planLegacy*.
func (db *DB) currentLegacyConflicts(table string, logicals []string) []LongIdentAction {
	logicals = uniqueStrings(logicals)
	current := map[string][]string{}
	for _, logical := range logicals {
		target := metadata.SQLIdent(logical)
		current[target] = append(current[target], logical)
	}
	var out []LongIdentAction
	for _, logical := range logicals {
		for _, old := range db.physicalLegacyNames(metadata.LegacySQLIdents(logical)) {
			for _, owner := range current[old] {
				if owner == logical {
					continue
				}
				what := "таблицы"
				if table != "" {
					what = "поля"
				}
				out = append(out, LongIdentAction{Table: table, Old: old, New: metadata.SQLIdent(logical), Conflict: fmt.Sprintf(
					"на прежнее имя претендуют 2 %s конфигурации: конечное имя %s (%s) совпадает с прежним именем %s (конечное %s); пересечение запрещено до создания схемы",
					what, owner, old, logical, metadata.SQLIdent(logical))})
			}
		}
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// planLegacyColumns — план для колонок таблицы table с логическими именами
// logical (lower(поле) или lower(поле)_id).
func (db *DB) planLegacyColumns(ctx context.Context, table string, logical []string) ([]LongIdentAction, error) {
	// Два логических поля на одно физическое имя — отказ до обращения к
	// базе: такая колонка на любой базе (и на пустой) смешала бы данные.
	if owners := finalNameOwners(logical); len(owners) > 0 {
		var out []LongIdentAction
		for _, target := range sortedKeys(owners) {
			out = append(out, finalNameConflict(table, target, owners[target]))
		}
		return out, nil
	}
	if conflicts := db.currentLegacyConflicts(table, logical); len(conflicts) > 0 {
		return conflicts, nil
	}
	claims := map[string]int{}
	var long []string
	for _, l := range logical {
		legacy := metadata.LegacySQLIdents(l)
		for _, name := range uniqueStrings(append(legacy, metadata.SQLIdent(l))) {
			claims[name]++
		}
		if legacy == nil {
			continue
		}
		long = append(long, l)
	}
	if len(long) == 0 {
		return nil, nil
	}
	exists, err := db.tableExists(ctx, table)
	if err != nil || !exists {
		return nil, err
	}
	cols, err := db.tableColumns(ctx, table)
	if err != nil {
		return nil, err
	}
	var out []LongIdentAction
	for _, l := range long {
		target := metadata.SQLIdent(l)
		_, hasTarget := cols[target]
		// Не выбираем полное SQLite-имя, пока не проверили обрезку и её
		// владельца. Иначе первый запуск проходит, а повторный отказывает.
		var foundNames []string
		conflict := false
		for _, old := range db.physicalLegacyNames(metadata.LegacySQLIdents(l)) {
			if _, ok := cols[old]; !ok {
				continue
			}
			if claims[old] > 1 {
				out = append(out, LongIdentAction{Table: table, Old: old, New: target, Conflict: fmt.Sprintf(
					"на прежнее имя претендуют %d поля, чьё это значение — не установить", claims[old])})
				conflict = true
				break
			}
			foundNames = append(foundNames, old)
		}
		if conflict || len(foundNames) == 0 {
			continue
		}
		if !hasTarget && len(foundNames) > 1 {
			out = append(out, LongIdentAction{Table: table, Old: foundNames[0], New: target, Conflict: "есть несколько прежних колонок (" + strings.Join(foundNames, ", ") + "), чьё это значение — не установить"})
			continue
		}
		for _, old := range foundNames {
			if hasTarget {
				has, err := db.columnHasData(ctx, table, old)
				if err != nil {
					return nil, err
				}
				if has {
					out = append(out, LongIdentAction{Table: table, Old: old, New: target, Conflict: "есть и новая колонка " +
						target + ", и прежняя с данными — какие данные верные, не установить"})
				}
				continue
			}
			out = append(out, LongIdentAction{Table: table, Old: old, New: target})
			break
		}
	}
	return out, nil
}

// renameLegacyColumns переименовывает колонки таблицы table с длинными
// логическими именами из прежних физических имён в metadata.SQLIdent; конфликт
// — отказ до любого переименования в этой таблице.
func (db *DB) renameLegacyColumns(ctx context.Context, table string, logical []string) error {
	plan, err := db.planLegacyColumns(ctx, table, logical)
	if err != nil {
		return err
	}
	for _, a := range plan {
		if a.Conflict != "" {
			return a.err()
		}
	}
	for _, a := range plan {
		if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", table, a.Old, a.New)); err != nil {
			return fmt.Errorf("переименование колонки %s.%s → %s: %w", table, a.Old, a.New, err)
		}
		if err := db.renameSchemaMapColumn(ctx, table, a.Old, a.New); err != nil {
			return err
		}
		slog.Info("длинное имя колонки сокращено", "таблица", table, "было", a.Old, "стало", a.New)
	}
	return nil
}

func (db *DB) tableHasRows(ctx context.Context, table string) (bool, error) {
	rows, err := db.Query(ctx, "SELECT 1 FROM "+table+" LIMIT 1")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	has := rows.Next()
	return has, rows.Err()
}

func (db *DB) columnHasData(ctx context.Context, table, column string) (bool, error) {
	rows, err := db.Query(ctx, "SELECT 1 FROM "+table+" WHERE "+column+" IS NOT NULL LIMIT 1")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	has := rows.Next()
	return has, rows.Err()
}

// longIdentTable описывает все имена, которые использует миграция таблицы.
// Общий список нужен и dry-run, и проверке конфликтов до первого ALTER.
type longIdentTable struct {
	logical string
	columns []string
}

func longIdentTables(entities []*metadata.Entity, registers []*metadata.Register, infoRegs []*metadata.InfoRegister, accountRegs []*metadata.AccountRegister) []longIdentTable {
	var tables []longIdentTable
	add := func(name string, fields []metadata.Field) {
		tables = append(tables, longIdentTable{name, logicalColumnNames(fields)})
	}
	for _, e := range entities {
		add(metadata.TableLogical(e.Name), e.Fields)
		for _, tp := range e.TableParts {
			add(metadata.TablePartTableLogical(e.Name, tp.Name), tp.Fields)
		}
	}
	for _, r := range registers {
		fields := append(append(append([]metadata.Field{}, r.Dimensions...), r.Resources...), r.Attributes...)
		add(metadata.RegisterTableLogical(r.Name), fields)
		if r.TotalsUsable() {
			add(metadata.RegisterTotalsTableLogical(r.Name), append(append([]metadata.Field{}, r.Dimensions...), r.Resources...))
		}
	}
	for _, r := range infoRegs {
		add(metadata.InfoRegTableLogical(r.Name), append(append([]metadata.Field{}, r.Dimensions...), r.Resources...))
	}
	for _, ar := range accountRegs {
		add(metadata.AccountRegTableLogical(ar.Name), ar.Resources)
		if ar.TotalsUsable() {
			var cols []string
			for _, r := range ar.Resources {
				cols = append(cols, accountTotalsDebitLogical(r), accountTotalsCreditLogical(r))
			}
			tables = append(tables, longIdentTable{metadata.AccountRegTotalsTableLogical(ar.Name), cols})
		}
	}
	return tables
}

// PlanLongIdents — инвентаризация таблиц и колонок сущностей, табличных частей,
// всех регистров и включённых итогов. Ничего не меняет. Последний аргумент
// необязателен для совместимости прежних вызовов без бухрегистров.
func (db *DB) PlanLongIdents(ctx context.Context, entities []*metadata.Entity, registers []*metadata.Register, infoRegs []*metadata.InfoRegister, accountRegs ...[]*metadata.AccountRegister) ([]LongIdentAction, error) {
	var accounts []*metadata.AccountRegister
	for _, regs := range accountRegs {
		accounts = append(accounts, regs...)
	}
	tables := longIdentTables(entities, registers, infoRegs, accounts)
	var logicals []string
	for _, table := range tables {
		logicals = append(logicals, table.logical)
	}
	claims := legacyTableClaims(logicals)
	var out []LongIdentAction
	// Таблицы с общим конечным именем: отказ до обследования чужих колонок.
	shared := finalNameOwners(logicals)
	for _, target := range sortedKeys(shared) {
		out = append(out, finalNameConflict("", target, shared[target]))
	}
	// Все статические пересечения проверяются до чтения существующей схемы,
	// включая колонки ещё не созданных таблиц. План на пустой базе и после
	// частичной/прежней установки сообщает тот же запрет до любого DDL.
	out = append(out, db.currentLegacyConflicts("", logicals)...)
	for _, spec := range tables {
		owners := finalNameOwners(spec.columns)
		for _, target := range sortedKeys(owners) {
			out = append(out, finalNameConflict(metadata.SQLIdent(spec.logical), target, owners[target]))
		}
		out = append(out, db.currentLegacyConflicts(metadata.SQLIdent(spec.logical), spec.columns)...)
	}
	if len(out) > 0 {
		return out, nil
	}
	for _, spec := range tables {
		action, err := db.planLegacyTable(ctx, spec.logical, claims)
		if err != nil {
			return nil, err
		}
		if action != nil {
			out = append(out, *action)
		}
		// Если переименование конфликтует, чужую таблицу не обследуем как свою.
		if action != nil && action.Conflict != "" {
			continue
		}
		table := metadata.SQLIdent(spec.logical)
		if action != nil {
			table = action.Old
		}
		cols, err := db.planLegacyColumns(ctx, table, spec.columns)
		if err != nil {
			return nil, err
		}
		out = append(out, cols...)
	}
	return out, nil
}

// CheckLongIdents отказывает при конфликте имён полного проекта до миграции.
func (db *DB) CheckLongIdents(ctx context.Context, entities []*metadata.Entity, registers []*metadata.Register, infoRegs []*metadata.InfoRegister, accounts []*metadata.AccountRegister) error {
	plan, err := db.PlanLongIdents(ctx, entities, registers, infoRegs, accounts)
	if err != nil {
		return err
	}
	for _, action := range plan {
		if action.Conflict != "" {
			return action.err()
		}
	}
	return nil
}

// indexName — физическое имя индекса с логическим именем logical на таблице
// table. Для длинного имени удаляет индекс под прежним именем, если он есть и
// стоит на той же таблице: следом создаётся индекс с новым именем, и без
// удаления в базе оставались бы два одинаковых. Индекс чужой таблицы с тем же
// обрезанным именем (так PostgreSQL сводил разные имена в одно) не трогается.
func (db *DB) indexName(ctx context.Context, table, logical string) (string, error) {
	target := metadata.SQLIdent(logical)
	for _, old := range uniqueStrings(metadata.LegacySQLIdents(logical)) {
		owner, err := db.indexTable(ctx, old)
		if err != nil {
			return "", fmt.Errorf("индекс %s: %w", old, err)
		}
		if owner == "" || owner != table {
			continue
		}
		if _, err := db.Exec(ctx, "DROP INDEX IF EXISTS "+old); err != nil {
			return "", fmt.Errorf("удаление индекса %s: %w", old, err)
		}
	}
	return target, nil
}

// indexTable — таблица индекса name ("" — индекса нет).
func (db *DB) indexTable(ctx context.Context, name string) (string, error) {
	var q string
	if db.IsSQLite() {
		q = `SELECT tbl_name FROM sqlite_master WHERE type = 'index' AND name = ?`
	} else {
		q = `SELECT tablename FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`
	}
	rows, err := db.Query(ctx, q, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var table string
	if rows.Next() {
		if err := rows.Scan(&table); err != nil {
			return "", err
		}
	}
	return table, rows.Err()
}

// renameSchemaMapTable / renameSchemaMapColumn держат карту полей плана 81 в
// согласии с переименованием: иначе планировщик искал бы поле под старым
// именем таблицы или колонки.
func (db *DB) renameSchemaMapTable(ctx context.Context, old, target string) error {
	if !tableExistsIn(ctx, db, "_schema_fields") {
		return nil
	}
	d := db.dialect
	if _, err := db.Exec(ctx, `UPDATE _schema_fields SET table_name = `+d.Placeholder(1)+` WHERE table_name = `+d.Placeholder(2), target, old); err != nil {
		return fmt.Errorf("schema map: переименование таблицы %s: %w", old, err)
	}
	return nil
}

func (db *DB) renameSchemaMapColumn(ctx context.Context, table, old, target string) error {
	if !tableExistsIn(ctx, db, "_schema_fields") {
		return nil
	}
	d := db.dialect
	if _, err := db.Exec(ctx, `UPDATE _schema_fields SET column_name = `+d.Placeholder(1)+` WHERE table_name = `+d.Placeholder(2)+` AND column_name = `+d.Placeholder(3), target, table, old); err != nil {
		return fmt.Errorf("schema map: переименование колонки %s.%s: %w", table, old, err)
	}
	return nil
}

// logicalColumnNames — логические имена колонок полей (до SQLIdent).
func logicalColumnNames(fields []metadata.Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, metadata.LogicalColumnName(f))
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
