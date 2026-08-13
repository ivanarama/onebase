package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// toSnakeCase converts CamelCase (including Cyrillic) to snake_case.
// Used to detect and rename columns created by older schema versions.
func toSnakeCase(s string) string {
	runes := []rune(s)
	out := make([]rune, 0, len(runes)+4)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}

// renameSnakeCols renames old snake_case columns (e.g. тип_контрагента)
// to the current lowercase style (типконтрагента) if they exist in the table.
// PG-only: uses information_schema. No-op on SQLite (legacy data isn't a concern there).
func (db *DB) renameSnakeCols(ctx context.Context, table string, fields []metadata.Field) error {
	if db.IsSQLite() {
		return nil
	}
	for _, f := range fields {
		newCol := metadata.ColumnName(f)
		oldCol := toSnakeCase(f.Name)
		if oldCol == newCol {
			continue
		}
		// Сбой пробы наличия колонки трактуем как «её нет» и пропускаем поле:
		// разрушительных действий при этом не выполняется.
		var oldExists bool
		if err := db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`,
			table, oldCol).Scan(&oldExists); err != nil || !oldExists {
			continue
		}
		var newExists bool
		if err := db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`,
			table, newCol).Scan(&newExists); err != nil {
			continue
		}
		if newExists {
			// Перенос данных и удаление старой колонки — одна операция по смыслу.
			// Раньше обе ошибки игнорировались: неудачный UPDATE с последующим
			// успешным DROP COLUMN уничтожал единственную копию данных молча.
			// Теперь сбой переноса прерывает миграцию до удаления.
			if _, err := db.Exec(ctx, fmt.Sprintf(
				"UPDATE %s SET %s = %s WHERE %s IS NOT NULL AND %s IS NULL",
				table, newCol, oldCol, oldCol, newCol)); err != nil {
				return fmt.Errorf("перенос данных %s.%s → %s: %w", table, oldCol, newCol, err)
			}
			if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, oldCol)); err != nil {
				return fmt.Errorf("удаление устаревшей колонки %s.%s: %w", table, oldCol, err)
			}
		} else {
			if _, err := db.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s",
				table, oldCol, newCol)); err != nil {
				return fmt.Errorf("переименование %s.%s → %s: %w", table, oldCol, newCol, err)
			}
		}
	}
	return nil
}

// MigrateRegisters creates register tables.
func (db *DB) MigrateRegisters(ctx context.Context, registers []*metadata.Register) error {
	d := db.dialect
	for _, reg := range registers {
		if _, err := db.Exec(ctx, CreateRegisterSQL(d, reg)); err != nil {
			return fmt.Errorf("migrate register %s: %w", reg.Name, err)
		}
		table := metadata.RegisterTableName(reg.Name)
		if err := db.AddColumnIfMissing(ctx, table, "period", d.TypeTimestamp()); err != nil {
			return fmt.Errorf("migrate register %s.period: %w", reg.Name, err)
		}
		allFields := append(append([]metadata.Field{}, reg.Dimensions...), append(reg.Resources, reg.Attributes...)...)
		if err := db.renameSnakeCols(ctx, table, allFields); err != nil {
			return fmt.Errorf("migrate register %s: %w", reg.Name, err)
		}
		if err := db.restructureTable(ctx, table, allFields); err != nil {
			return fmt.Errorf("migrate register %s: %w", reg.Name, err)
		}
		for _, f := range allFields {
			if err := db.AddColumnIfMissing(ctx, table, metadata.ColumnName(f), fieldType(d, f)); err != nil {
				return fmt.Errorf("migrate register %s.%s: %w", reg.Name, f.Name, err)
			}
		}
		if err := db.ensureRegisterIndexes(ctx, reg); err != nil {
			return fmt.Errorf("migrate register %s indexes: %w", reg.Name, err)
		}
		if err := db.syncRegisterTotals(ctx, reg); err != nil {
			return fmt.Errorf("migrate register %s totals: %w", reg.Name, err)
		}
	}
	return nil
}

func (db *DB) ensureRegisterIndexes(ctx context.Context, reg *metadata.Register) error {
	if _, err := db.Exec(ctx, CreateRegisterPeriodIndexSQL(reg.Name)); err != nil {
		return err
	}
	if sql := CreateRegisterDimPeriodIndexSQL(reg); sql != "" {
		if _, err := db.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

// MigrateInfoRegisters creates tables for info registers and дотягивает схему
// существующих таблиц — добавляет недостающие колонки. Так регистры можно
// расширять (добавление periodic, новое измерение / ресурс) без ручной
// миграции БД.
//
// Что НЕ покрыто: изменение PRIMARY KEY (нельзя через ALTER в SQLite,
// нужен пересоздаваемый ALTER TABLE RENAME + INSERT SELECT). Поэтому если
// добавляется periodic-флаг к существующей непустой таблице — данные
// останутся, но PK не обновится; могут быть дубли по (dim) ключу.
func (db *DB) MigrateInfoRegisters(ctx context.Context, regs []*metadata.InfoRegister) error {
	d := db.dialect
	for _, ir := range regs {
		if _, err := db.Exec(ctx, CreateInfoRegisterSQL(d, ir)); err != nil {
			return fmt.Errorf("migrate info register %s: %w", ir.Name, err)
		}
		table := metadata.InfoRegTableName(ir.Name)
		// period колонка для periodic-регистров — может отсутствовать,
		// если в YAML только что добавили `periodic: true`. ALLOW NULL,
		// потому что existing rows иначе не вставить.
		if ir.Periodic {
			if err := db.AddColumnIfMissing(ctx, table, "period", d.TypeTimestamp()); err != nil {
				return fmt.Errorf("migrate info register %s.period: %w", ir.Name, err)
			}
		}
		// Реструктуризация измерений и ресурсов (план 81). Измерения входят в
		// первичный ключ: переименование безопасно, а вот удаление измерения
		// СУБД отклонит — и это правильнее, чем разрушить ключ регистра.
		if err := db.restructureTable(ctx, table, append(append([]metadata.Field{}, ir.Dimensions...), ir.Resources...)); err != nil {
			// Удаление измерения из ключа обычным DROP COLUMN на SQLite невозможно
			// (колонка входит в PRIMARY KEY). Это не сбой: удаление измерения меняет
			// ключ, и его доводит пересоздание таблицы в fixInfoRegPK ниже — с той же
			// защитой плана 81 (без --allow-destructive колонка и данные сохранены).
			if !errors.Is(err, ErrColumnInUniqueIndex) {
				return fmt.Errorf("migrate info register %s: %w", ir.Name, err)
			}
		}
		// Измерения и ресурсы — добавляем если их нет.
		for _, f := range ir.Dimensions {
			if err := db.AddColumnIfMissing(ctx, table, metadata.ColumnName(f), fieldType(d, f)); err != nil {
				return fmt.Errorf("migrate info register %s.%s: %w", ir.Name, f.Name, err)
			}
		}
		if err := db.AddColumnIfMissing(ctx, table, "updated_at", d.TypeTimestamp()); err != nil {
			return fmt.Errorf("migrate info register %s.updated_at: %w", ir.Name, err)
		}
		// recorder/recorder_type для записи из документа.
		if err := db.AddColumnIfMissing(ctx, table, "recorder", d.TypeUUID()); err != nil {
			return fmt.Errorf("migrate info register %s.recorder: %w", ir.Name, err)
		}
		if err := db.AddColumnIfMissing(ctx, table, "recorder_type", d.TypeText()); err != nil {
			return fmt.Errorf("migrate info register %s.recorder_type: %w", ir.Name, err)
		}
		for _, f := range ir.Resources {
			if err := db.AddColumnIfMissing(ctx, table, metadata.ColumnName(f), fieldType(d, f)); err != nil {
				return fmt.Errorf("migrate info register %s.%s: %w", ir.Name, f.Name, err)
			}
		}
		// фактический PK таблицы может не
		// совпадать с pkCols(ir) — наследие старого CREATE до того как
		// регистр стал periodic / добавили измерения. SQLite не позволяет
		// ALTER PK, поэтому при mismatch пересоздаём таблицу через
		// CREATE + INSERT SELECT + DROP + RENAME.
		if err := db.fixInfoRegPK(ctx, ir); err != nil {
			return fmt.Errorf("migrate info register %s PK: %w", ir.Name, err)
		}
	}
	return nil
}

// fixInfoRegPK сверяет фактический PRIMARY KEY таблицы регистра сведений
// с ожидаемым (pkCols(ir)). При несовпадении пересоздаёт таблицу с
// правильным PK, копируя данные. Безопасно если в существующих строках
// нет дубликатов по новому ключу — иначе INSERT упадёт с UNIQUE constraint,
// и пользователь должен будет разобраться с дубликатами.
func (db *DB) fixInfoRegPK(ctx context.Context, ir *metadata.InfoRegister) error {
	switch db.dialect.Name() {
	case "sqlite":
		return db.fixInfoRegPKSQLite(ctx, ir)
	case "postgres":
		return db.fixInfoRegPKPostgres(ctx, ir)
	default:
		return nil
	}
}

func (db *DB) fixInfoRegPKSQLite(ctx context.Context, ir *metadata.InfoRegister) error {
	table := metadata.InfoRegTableName(ir.Name)

	// Снимаем фактический PK.
	rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteIdent(table)+")")
	if err != nil {
		return err
	}
	type pkCol struct {
		name string
		pos  int
	}
	var actual []pkCol
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if pk > 0 {
			actual = append(actual, pkCol{name: name, pos: pk})
		}
	}
	rows.Close()
	// Сортируем по pos
	for i := 1; i < len(actual); i++ {
		for j := i; j > 0 && actual[j-1].pos > actual[j].pos; j-- {
			actual[j-1], actual[j] = actual[j], actual[j-1]
		}
	}

	expected := pkCols(ir)
	if len(actual) == len(expected) {
		match := true
		for i := range actual {
			if actual[i].name != expected[i] {
				match = false
				break
			}
		}
		if match {
			return nil
		}
	}

	// PK mismatch — rebuild.
	d := db.dialect
	tmp := table + "_new"

	// CREATE + INSERT SELECT + DROP + RENAME — в одной транзакции: без неё обрыв
	// между DROP <table> и RENAME <tmp> оставлял базу вовсе без таблицы регистра,
	// и повторный прогон её не восстанавливал (#616). Тот же урок, что у
	// retypeSQLite. Пропуск пересоздания (нет разрешения на потерю колонки) тоже
	// проходит через откат транзакции — sentinel errSkipInfoRegRebuild.
	err = db.WithTxScope(ctx, func(ctx context.Context) error {
		if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+sqliteIdent(tmp)); err != nil {
			return err
		}
		createSQL := CreateInfoRegisterSQL(d, ir)
		// Подменяем имя таблицы на _new
		createSQL = strings.Replace(createSQL, "CREATE TABLE IF NOT EXISTS "+table, "CREATE TABLE "+tmp, 1)
		if _, err := db.Exec(ctx, createSQL); err != nil {
			return err
		}

		oldCols, err := tableColumnNames(ctx, db, table)
		if err != nil {
			return err
		}
		newCols, err := tableColumnNames(ctx, db, tmp)
		if err != nil {
			return err
		}
		newSet := make(map[string]bool, len(newCols))
		for _, c := range newCols {
			newSet[c] = true
		}
		// Пересоздание копирует лишь пересечение колонок. Колонки, которых нет в
		// новой таблице (удалённое измерение), пропали бы вместе с данными. Для
		// регистра сведений измерения входят в PK, поэтому удаление измерения
		// ВСЕГДА даёт mismatch и попадало сюда, а restructureTable выше уже отложил
		// этот ChangeDrop как разрушительный — колонка осталась осиротевшей. Без
		// --allow-destructive пересоздание тихо доводило удаление до конца, теряя
		// данные. Поэтому: если rebuild уронил бы колонки, а разрешения нет, —
		// откатываемся и оставляем таблицу как есть (смена ключа тоже требует
		// --allow-destructive).
		var common, dropped []string
		for _, c := range oldCols {
			if newSet[c] {
				common = append(common, c)
			} else {
				dropped = append(dropped, c)
			}
		}
		if len(dropped) > 0 && !db.schemaOpts.AllowDestructive {
			return errSkipInfoRegRebuild
		}

		if len(common) > 0 {
			copySQL := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
				sqliteIdent(tmp),
				strings.Join(common, ", "),
				strings.Join(common, ", "),
				sqliteIdent(table))
			if _, err := db.Exec(ctx, copySQL); err != nil {
				return i18nerr.Wrapf(err, "copy data (возможно дубликаты по новому PK)")
			}
		}
		if _, err := db.Exec(ctx, "DROP TABLE "+sqliteIdent(table)); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "ALTER TABLE "+sqliteIdent(tmp)+" RENAME TO "+sqliteIdent(table)); err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, errSkipInfoRegRebuild) {
		storageLog().Warn("смена ключа регистра сведений отложена: требует --allow-destructive; колонка и данные сохранены",
			"регистр", ir.Name)
		return nil
	}
	return err
}

// errSkipInfoRegRebuild — внутренний сигнал: пересоздание таблицы регистра
// сведений уронило бы колонку с данными, а --allow-destructive не задан.
// Пробрасывается наружу транзакции, чтобы её откатить, и там гасится.
var errSkipInfoRegRebuild = errors.New("info register rebuild skipped: destructive")

// tableColumnNames возвращает имена колонок таблицы в порядке их объявления.
func tableColumnNames(ctx context.Context, db *DB, table string) ([]string, error) {
	rows, err := db.Query(ctx, "PRAGMA table_info("+sqliteIdent(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (db *DB) fixInfoRegPKPostgres(ctx context.Context, ir *metadata.InfoRegister) error {
	table := metadata.InfoRegTableName(ir.Name)
	expected := pkCols(ir)
	actual, _, err := db.pgPrimaryKey(ctx, table)
	if err != nil {
		return err
	}
	if stringSlicesEqual(actual, expected) {
		return nil
	}

	return db.WithTx(ctx, func(txCtx context.Context) error {
		actual, constraint, err := db.pgPrimaryKey(txCtx, table)
		if err != nil {
			return err
		}
		if stringSlicesEqual(actual, expected) {
			return nil
		}
		if len(expected) > 0 {
			if err := db.pgEnsurePKColumnsUsable(txCtx, table, expected); err != nil {
				return err
			}
		}
		if constraint != "" {
			if _, err := db.Exec(txCtx, "ALTER TABLE "+pgQuoteIdent(table)+" DROP CONSTRAINT "+pgQuoteIdent(constraint)); err != nil {
				return err
			}
		}
		if len(expected) == 0 {
			return nil
		}
		for _, col := range expected {
			if _, err := db.Exec(txCtx, "ALTER TABLE "+pgQuoteIdent(table)+" ALTER COLUMN "+pgQuoteIdent(col)+" SET NOT NULL"); err != nil {
				return err
			}
		}
		_, err = db.Exec(txCtx, "ALTER TABLE "+pgQuoteIdent(table)+" ADD CONSTRAINT "+
			pgQuoteIdent(table+"_pkey")+" PRIMARY KEY ("+pgIdentList(expected)+")")
		return err
	})
}

func (db *DB) pgPrimaryKey(ctx context.Context, table string) ([]string, string, error) {
	rows, err := db.Query(ctx, `
		SELECT c.conname, a.attname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON TRUE
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE c.contype = 'p' AND n.nspname = current_schema() AND t.relname = $1
		ORDER BY k.ord`, table)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var cols []string
	var constraint string
	for rows.Next() {
		var name, col string
		if err := rows.Scan(&name, &col); err != nil {
			return nil, "", err
		}
		if constraint == "" {
			constraint = name
		}
		cols = append(cols, col)
	}
	return cols, constraint, rows.Err()
}

func (db *DB) pgEnsurePKColumnsUsable(ctx context.Context, table string, cols []string) error {
	qtable := pgQuoteIdent(table)
	nullChecks := make([]string, len(cols))
	for i, col := range cols {
		nullChecks[i] = pgQuoteIdent(col) + " IS NULL"
	}
	var hasNull bool
	if err := db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+qtable+" WHERE "+strings.Join(nullChecks, " OR ")+")").Scan(&hasNull); err != nil {
		return err
	}
	if hasNull {
		return i18nerr.Errorf("existing rows contain NULL in new primary key columns (%s)", strings.Join(cols, ", "))
	}

	pkColsSQL := pgIdentList(cols)
	var hasDuplicates bool
	dupSQL := "SELECT EXISTS (SELECT 1 FROM (SELECT " + pkColsSQL + " FROM " + qtable +
		" GROUP BY " + pkColsSQL + " HAVING COUNT(*) > 1 LIMIT 1) d)"
	if err := db.QueryRow(ctx, dupSQL).Scan(&hasDuplicates); err != nil {
		return err
	}
	if hasDuplicates {
		return i18nerr.Errorf("existing rows contain duplicates by new primary key (%s)", strings.Join(cols, ", "))
	}
	return nil
}

func pgIdentList(cols []string) string {
	out := make([]string, len(cols))
	for i, col := range cols {
		out[i] = pgQuoteIdent(col)
	}
	return strings.Join(out, ", ")
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Migrate applies CREATE TABLE and ADD COLUMN IF NOT EXISTS for all entities.
func (db *DB) Migrate(ctx context.Context, entities []*metadata.Entity) error {
	d := db.dialect
	if err := db.EnsureSeqTable(ctx); err != nil {
		return fmt.Errorf("migrate: sequences table: %w", err)
	}
	if err := db.EnsureNumeratorSchema(ctx); err != nil {
		return fmt.Errorf("migrate: numerators table: %w", err)
	}
	needFTSBackfill, err := db.EnsureFullTextSchema(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// История переходов между этапами (план 121). Стоит здесь, а не только
	// поимённо в командах: от этой таблицы зависит запись объекта, у которого
	// объявлен блок `stages`, — ровно как от таблицы нумераторов и от FTS.
	// Список ручных вызовов Ensure*Schema по командам пополнять забывают, и
	// компилятор об этом молчит; путь через Migrate забыть нельзя.
	if err := db.EnsureStageHistorySchema(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	ordered := orderByDependency(entities)
	for _, e := range ordered {
		if _, err := db.Exec(ctx, CreateTableSQL(d, e)); err != nil {
			return fmt.Errorf("migrate %s: %w", e.Name, err)
		}
		if err := db.EnsurePredefinedColumns(ctx, []*metadata.Entity{e}); err != nil {
			return fmt.Errorf("migrate: predefined columns: %w", err)
		}
		table := metadata.TableName(e.Name)
		if e.Kind == metadata.KindDocument {
			if err := db.AddColumnIfMissing(ctx, table, "posted", d.TypeBool()+" NOT NULL DEFAULT "+boolFalseLit(d)); err != nil {
				return fmt.Errorf("migrate %s.posted: %w", e.Name, err)
			}
		}
		if err := db.renameSnakeCols(ctx, table, e.Fields); err != nil {
			return fmt.Errorf("migrate %s: %w", e.Name, err)
		}
		// Реструктуризация (план 81) идёт ДО добавления колонок: иначе
		// переименованное поле успело бы завести новую пустую колонку, и
		// переименовывать было бы уже не во что.
		if err := db.restructureTable(ctx, table, e.Fields); err != nil {
			return fmt.Errorf("migrate %s: %w", e.Name, err)
		}
		for _, f := range e.Fields {
			if err := db.AddColumnIfMissing(ctx, table, metadata.ColumnName(f), fieldType(d, f)); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, f.Name, err)
			}
		}
		if err := db.AddColumnIfMissing(ctx, table, "deletion_mark", d.TypeBool()+" NOT NULL DEFAULT "+boolFalseLit(d)); err != nil {
			return fmt.Errorf("migrate %s.deletion_mark: %w", e.Name, err)
		}
		// _version — счётчик ревизий для оптимистических блокировок.
		// Инкрементируется в Upsert при каждом UPDATE. UpsertVersioned
		// сравнивает с ожидаемым значением и возвращает ErrVersionConflict,
		// если кто-то опередил. BIGINT работает на обоих диалектах: в PG —
		// нативный, в SQLite — через INTEGER affinity.
		if err := db.AddColumnIfMissing(ctx, table, "_version", "BIGINT NOT NULL DEFAULT 1"); err != nil {
			return fmt.Errorf("migrate %s._version: %w", e.Name, err)
		}
		if e.Hierarchical {
			if err := db.AddHierarchyColumns(ctx, table); err != nil {
				return fmt.Errorf("migrate %s hierarchy: %w", e.Name, err)
			}
		}
		if err := db.ensureEntityIndexes(ctx, e); err != nil {
			return fmt.Errorf("migrate %s indexes: %w", e.Name, err)
		}
		for _, tp := range e.TableParts {
			if _, err := db.Exec(ctx, CreateTablePartSQL(d, e, tp)); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, tp.Name, err)
			}
			tpTable := metadata.TablePartTableName(e.Name, tp.Name)
			if err := db.restructureTable(ctx, tpTable, tp.Fields); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", e.Name, tp.Name, err)
			}
			for _, f := range tp.Fields {
				if err := db.AddColumnIfMissing(ctx, tpTable, metadata.ColumnName(f), fieldType(d, f)); err != nil {
					return fmt.Errorf("migrate %s.%s.%s: %w", e.Name, tp.Name, f.Name, err)
				}
			}
			if _, err := db.Exec(ctx, CreateTablePartParentIndexSQL(e.Name, tp.Name)); err != nil {
				return fmt.Errorf("migrate %s.%s parent index: %w", e.Name, tp.Name, err)
			}
		}
	}
	if err := db.SyncAllPredefined(ctx, entities); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Первичное наполнение полнотекстового индекса (план 82). Схема заводится в
	// начале Migrate, но наполнять её можно только сейчас — таблицы объектов
	// созданы. Инкрементально индекс пишется при каждой записи, поэтому backfill
	// нужен ровно один раз: при обновлении платформы на базе, где данные уже
	// есть. Без него строка поиска работает, но не находит ничего, и понять
	// это можно только догадавшись выполнить `onebase reindex`.
	//
	// Признак берётся из отметки в _settings, а не из «таблицы не было»:
	// наполнение идёт вне транзакции, и обрыв посреди него оставлял пустую
	// таблицу, которая при следующем запуске выглядела как уже наполненная
	// (#615, см. ftsBackfillDoneKey). Отметку ставит сама RebuildFullTextIndex,
	// и только дойдя до конца.
	if needFTSBackfill {
		if _, err := db.RebuildFullTextIndex(ctx, entities, 0, nil); err != nil {
			return fmt.Errorf("migrate: первичное наполнение полнотекстового индекса: %w", err)
		}
	}
	return nil
}

func (db *DB) ensureEntityIndexes(ctx context.Context, e *metadata.Entity) error {
	table := metadata.TableName(e.Name)
	indexes := e.Indexes
	// numerator.unique выражается через тот же механизм indexes: отдельной
	// реализации уникальности заводить незачем (план 117E). Проверка до DDL:
	// пустые значения уникальный индекс не ловит (NULL не конфликтуют), и без
	// неё «включили уникальность» проходило бы молча.
	if spec, need := UniqueCodeIndexSpec(e); need {
		if err := db.CheckCodeUniquePrecondition(ctx, e); err != nil {
			return err
		}
		indexes = append(append([]metadata.IndexSpec{}, indexes...), spec)
	}
	keep := make(map[string]bool, len(indexes))
	for _, idx := range indexes {
		cols, err := entityIndexColumns(e, idx)
		if err != nil {
			return err
		}
		keep[stableIndexName(table, cols, idx.Unique)] = true
		if _, err := db.Exec(ctx, CreateEntityIndexSQL(table, cols, idx.Unique)); err != nil {
			return err
		}
	}
	return db.dropStaleUniqueCodeIndexes(ctx, e, keep)
}

func entityIndexColumns(e *metadata.Entity, idx metadata.IndexSpec) ([]string, error) {
	cols := make([]string, 0, len(idx.Fields))
	for _, name := range idx.Fields {
		var found *metadata.Field
		for i := range e.Fields {
			if e.Fields[i].Name == name {
				found = &e.Fields[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("index references unknown field %s", name)
		}
		cols = append(cols, metadata.ColumnName(*found))
	}
	return cols, nil
}

// orderByDependency sorts entities so referenced entities come before referencing ones.
func orderByDependency(entities []*metadata.Entity) []*metadata.Entity {
	byName := make(map[string]*metadata.Entity, len(entities))
	for _, e := range entities {
		byName[e.Name] = e
	}
	visited := make(map[string]bool)
	var result []*metadata.Entity
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		e := byName[name]
		if e == nil {
			return
		}
		for _, f := range e.Fields {
			if f.RefEntity != "" {
				visit(f.RefEntity)
			}
		}
		result = append(result, e)
	}
	for _, e := range entities {
		visit(e.Name)
	}
	return result
}
