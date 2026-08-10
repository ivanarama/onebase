package storage

// Исполнение плана реструктуризации (план 81). Планировщик — в schemaplan.go.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// ErrColumnInUniqueIndex — колонку нельзя удалить обычным DROP COLUMN, потому что
// она входит в UNIQUE/PRIMARY KEY, объявленный при создании таблицы (SQLite
// хранит его в sqlite_autoindex). Для регистра сведений это измерение из ключа:
// его удаляет пересоздание таблицы (fixInfoRegPKSQLite), а не ALTER, поэтому
// MigrateInfoRegisters ловит этот случай и доводит удаление там.
var ErrColumnInUniqueIndex = errors.New("колонка входит в UNIQUE/PRIMARY KEY, объявленный при создании таблицы")

// maxConvertExamples — сколько непреобразуемых значений показать в ошибке.
// Список нужен, чтобы админ понял, что именно чинить; полный дамп колонки в
// сообщении об ошибке бесполезен и опасен (в нём данные).
const maxConvertExamples = 5

// restructureTable приводит таблицу к желаемым полям: строит план и применяет
// его с учётом режима (SchemaOptions). Возвращает ошибку, если изменение
// невозможно выполнить безопасно.
func (db *DB) restructureTable(ctx context.Context, table string, fields []metadata.Field) error {
	if !anyFieldHasID(fields) {
		// Ни у одного поля нет устойчивого id — реструктурировать нечего,
		// работает прежний аддитивный путь.
		return nil
	}
	changes, err := db.PlanTableChanges(ctx, table, fields)
	if err != nil {
		return err
	}
	opts := db.schemaOpts

	// Весь план одной таблицы и запись карты полей — в одной транзакции (issue
	// #588). Иначе сбой посреди плана оставлял базу в состоянии, о котором карта
	// `_schema_fields` не знает: часть переименований применена, а карта (её
	// пишет saveSchemaMap последней строкой) о них не в курсе — следующий прогон
	// строит план по устаревшей карте и разбирает частично применённое
	// состояние. DDL транзакционен на обоих диалектах; retypeSQLite вложится
	// сюда савпоинтом (WithTxScope это умеет).
	type reportItem struct {
		change  SchemaChange
		applied bool
	}
	var reports []reportItem
	err = db.WithTxScope(ctx, func(ctx context.Context) error {
		reports = reports[:0]
		// deferred: field_id → фактическая (прежняя) подпись поля, чьё
		// разрушительное изменение отложено. В карту нужно записать состояние,
		// которое реально в базе, а не желаемое: иначе следующий план сравнит
		// метаданные с картой, расхождения не увидит и никогда не переприменит
		// отложенное изменение, а --allow-destructive его уже не догонит (#612).
		deferred := map[string]string{}
		for _, c := range changes {
			applied := false
			if !c.Destructive() || opts.AllowDestructive {
				if err := db.applySchemaChange(ctx, c); err != nil {
					return err
				}
				applied = true
			} else if c.Kind == ChangeRetype && c.FieldID != "" {
				// Сужающий ретайп отложен: колонка физически осталась прежнего
				// типа (c.From). Отложенный drop сюда не попадает — поля уже нет
				// в fields, и его подпись хранит прежняя запись карты, которую
				// saveSchemaMap не трогает, пока колонка жива.
				deferred[c.FieldID] = c.From
			}
			// Иначе колонка остаётся осиротевшей — осознанный отказ, не сбой.
			reports = append(reports, reportItem{c, applied})
		}
		return db.saveSchemaMap(ctx, table, fields, deferred)
	})
	if err != nil {
		return err
	}
	// Смена типа колонки меняет тип результата уже подготовленных запросов, а
	// pgx кэширует планы на каждом соединении пула. Без сброса первое же
	// чтение этой таблицы после ретайпа падает с «cached plan must not change
	// result type» (SQLSTATE 0A000) — и падает не в тесте, а у живого сервера,
	// который отмигрировал схему и продолжает работать на том же пуле.
	// Reset закрывает простаивающие соединения и помечает занятые на закрытие,
	// так что следующий запрос готовит план заново. Делается ПОСЛЕ коммита:
	// откаченный план типов не менял.
	for _, rep := range reports {
		if rep.applied && rep.change.Kind == ChangeRetype {
			db.resetConnPool()
			break
		}
	}
	// Report только после фиксации транзакции: при откате (сбой посреди плана)
	// ничего не применилось, и сообщать «применено» об откаченных изменениях —
	// значит ввести администратора в заблуждение о состоянии базы.
	if opts.Report != nil {
		for _, rep := range reports {
			opts.Report(rep.change, rep.applied)
		}
	}
	return nil
}

// resetConnPool сбрасывает подготовленные планы пула. На SQLite не нужен:
// там пул из одного соединения и кэша планов уровня pgx нет.
func (db *DB) resetConnPool() {
	if db.pool != nil {
		db.pool.Reset()
	}
}

func anyFieldHasID(fields []metadata.Field) bool {
	for _, f := range fields {
		if f.ID != "" {
			return true
		}
	}
	return false
}

func (db *DB) applySchemaChange(ctx context.Context, c SchemaChange) error {
	switch c.Kind {
	case ChangeAdd:
		return db.AddColumnIfMissing(ctx, c.Table, c.To, fieldType(db.dialect, c.Field))
	case ChangeRename:
		// RENAME COLUMN есть в обоих диалектах (PostgreSQL и SQLite ≥ 3.25) —
		// это единственная операция, ради которой всё затевалось: данные
		// остаются на месте, меняется только имя.
		_, err := db.Exec(ctx, "ALTER TABLE "+quoteIdent(c.Table)+
			" RENAME COLUMN "+quoteIdent(c.From)+" TO "+quoteIdent(c.To))
		if err != nil {
			return fmt.Errorf("%s: переименование колонки %s → %s: %w", c.Table, c.From, c.To, err)
		}
		return nil
	case ChangeRetype:
		return db.applyRetype(ctx, c)
	case ChangeDrop:
		return db.dropColumn(ctx, c.Table, c.From)
	}
	return fmt.Errorf("%s: неизвестный вид изменения %q", c.Table, c.Kind)
}

// applyRetype меняет тип колонки.
//
// Сначала — проверка данных, потом DDL. Порядок принципиален: и PostgreSQL
// (ALTER … USING), и SQLite (CAST) при непреобразуемом значении либо роняют
// миграцию посреди работы, либо, что хуже, молча подставляют ноль.
func (db *DB) applyRetype(ctx context.Context, c SchemaChange) error {
	d := db.dialect
	oldSQL := sqlTypeForSignature(d, c.From)
	newSQL := fieldType(d, c.Field)
	sameStorage := strings.EqualFold(oldSQL, newSQL)

	bad, examples, err := db.checkConvertible(ctx, c.Table, c.To, c.Field)
	if err != nil {
		return err
	}
	if bad > 0 {
		if !sameStorage {
			return fmt.Errorf(
				"%s.%s: %d значен. не преобразуются в тип %s (например: %s) — исправьте данные или верните прежний тип; колонка не изменена",
				c.Table, c.To, bad, metadata.FieldSignature(c.Field), strings.Join(examples, ", "))
		}
		// Хранение не меняется (на SQLite почти все типы — TEXT), поэтому
		// данные не портятся: миграцию не срываем, но молчать нельзя.
		storageLog().Warn("значения не соответствуют новому типу поля",
			"таблица", c.Table, "колонка", c.To, "тип", metadata.FieldSignature(c.Field),
			"значений", bad, "примеры", strings.Join(examples, ", "))
	}
	if sameStorage {
		return nil
	}

	if db.IsSQLite() {
		return db.retypeSQLite(ctx, c, newSQL)
	}
	_, err = db.Exec(ctx, "ALTER TABLE "+quoteIdent(c.Table)+" ALTER COLUMN "+quoteIdent(c.To)+
		" TYPE "+newSQL+" USING "+pgUsingExpr(quoteIdent(c.To), newSQL))
	if err != nil {
		return fmt.Errorf("%s.%s: смена типа на %s: %w", c.Table, c.To, newSQL, err)
	}
	return nil
}

// retypeSQLite меняет тип колонки в SQLite: ALTER COLUMN … TYPE там нет,
// поэтому заводим колонку нужного типа, переносим значения приведением, старую
// удаляем и переименовываем новую. Дешевле полного пересоздания таблицы и не
// требует воспроизводить её DDL.
func (db *DB) retypeSQLite(ctx context.Context, c SchemaChange, newSQL string) error {
	tmp := c.To + "__ob_retype"
	q := quoteIdent(c.Table)
	// Все четыре шага — в одной транзакции: SQLite умеет транзакционный DDL,
	// и без неё обрыв между DROP и RENAME оставлял базу в состоянии, из
	// которого она не выходит сама. Данные лежали бы в <колонка>__ob_retype,
	// самой колонки не было бы, а повторный прогон падал бы на ADD COLUMN
	// («duplicate column name») — и так до ручной правки, причём миграция
	// срывается, то есть сервер уже не поднять.
	return db.WithTxScope(ctx, func(ctx context.Context) error {
		if _, err := db.Exec(ctx, "ALTER TABLE "+q+" ADD COLUMN "+quoteIdent(tmp)+" "+newSQL); err != nil {
			return fmt.Errorf("%s.%s: временная колонка: %w", c.Table, c.To, err)
		}
		if _, err := db.Exec(ctx, "UPDATE "+q+" SET "+quoteIdent(tmp)+" = "+
			sqliteRetypeExpr(quoteIdent(c.To), c.Field, newSQL)); err != nil {
			return fmt.Errorf("%s.%s: перенос значений: %w", c.Table, c.To, err)
		}
		if err := db.dropColumn(ctx, c.Table, c.To); err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "ALTER TABLE "+q+" RENAME COLUMN "+quoteIdent(tmp)+" TO "+quoteIdent(c.To)); err != nil {
			return fmt.Errorf("%s.%s: переименование временной колонки: %w", c.Table, c.To, err)
		}
		return nil
	})
}

// sqliteRetypeExpr — выражение переноса значений при смене типа на SQLite.
//
// Обычно достаточно CAST, но для булева типа он ломает данные: SQLite приводит
// к целому только числовые литералы, поэтому CAST('true' AS INTEGER) = 0 —
// «истина» молча превращается в «ложь» (issue #607). При этом valueChecker
// считает 'true'/'t'/'yes'/'on' годными значениями, так что checkConvertible
// пропускает такую миграцию как безопасную.
//
// Набор распознаваемых слов ОБЯЗАН совпадать с valueChecker для
// metadata.FieldTypeBool: проверка и перенос должны договариваться об одном и
// том же, иначе «проверили одно, записали другое» повторится.
//
// На PostgreSQL этой правки не нужно: там ALTER … USING с pgUsingExpr, и
// CAST('true' AS BOOLEAN) отрабатывает верно.
func sqliteRetypeExpr(col string, f metadata.Field, newSQL string) string {
	if f.Type != metadata.FieldTypeBool {
		return "CAST(" + col + " AS " + newSQL + ")"
	}
	return "CASE" +
		" WHEN " + col + " IS NULL OR TRIM(" + col + ") = '' THEN NULL" +
		" WHEN LOWER(TRIM(" + col + ")) IN ('true','t','yes','on','1') THEN 1" +
		" ELSE 0 END"
}

// dropColumn удаляет колонку. В SQLite DROP COLUMN отказывается работать, если
// колонка входит в индекс, поэтому зависимые индексы снимаются заранее —
// объявленные в конфигурации пересоздаются тем же прогоном миграции
// (ensureEntityIndexes идёт следом).
func (db *DB) dropColumn(ctx context.Context, table, column string) error {
	if db.IsSQLite() {
		if err := db.dropIndexesOnColumn(ctx, table, column); err != nil {
			return err
		}
	}
	_, err := db.Exec(ctx, "ALTER TABLE "+quoteIdent(table)+" DROP COLUMN "+quoteIdent(column))
	if err == nil {
		return nil
	}
	// SQLite не удаляет колонку, упомянутую в ограничении таблицы, а ссылочные
	// реквизиты платформа объявляет именно так (FOREIGN KEY в CreateTableSQL).
	// Тогда идём рекомендованным движком путём — пересоздаём таблицу без этой
	// колонки (#615). Ошибки другой природы наверх как есть.
	if db.IsSQLite() && isSQLiteConstraintDropErr(err) {
		if rebuildErr := db.dropColumnRebuildSQLite(ctx, table, column); rebuildErr != nil {
			return fmt.Errorf("%s: удаление колонки %s пересозданием таблицы: %w", table, column, rebuildErr)
		}
		return nil
	}
	return fmt.Errorf("%s: удаление колонки %s: %w", table, column, err)
}

// isSQLiteConstraintDropErr — отказ удалить колонку из-за ограничения таблицы.
// Текст, а не код: SQLite отдаёт для этого случая общий SQLITE_ERROR (1), и
// отличить его от прочих логических ошибок кодом нельзя. Сообщения движка не
// локализуются, поэтому разбор по тексту здесь безопасен — в отличие от
// PostgreSQL, где такой разбор ломался на локали (#672).
func isSQLiteConstraintDropErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key definition") ||
		strings.Contains(msg, "error in table") && strings.Contains(msg, "after drop column")
}

func (db *DB) dropIndexesOnColumn(ctx context.Context, table, column string) error {
	rows, err := db.Query(ctx, `SELECT name FROM pragma_index_list(?)`, table)
	if err != nil {
		return fmt.Errorf("%s: список индексов: %w", table, err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return fmt.Errorf("%s: список индексов: %w", table, err)
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%s: список индексов: %w", table, err)
	}

	for _, idx := range names {
		cols, err := db.indexColumns(ctx, idx)
		if err != nil {
			return err
		}
		if !cols[strings.ToLower(column)] {
			continue
		}
		// Автоиндексы (UNIQUE в объявлении таблицы) удалить нельзя — про них
		// честно сообщаем, а не падаем с невнятной ошибкой SQLite.
		if strings.HasPrefix(idx, "sqlite_autoindex") {
			return fmt.Errorf("%s: колонка %s входит в UNIQUE-ограничение таблицы — удалите ограничение вручную: %w", table, column, ErrColumnInUniqueIndex)
		}
		if _, err := db.Exec(ctx, "DROP INDEX IF EXISTS "+quoteIdent(idx)); err != nil {
			return fmt.Errorf("%s: снятие индекса %s: %w", table, idx, err)
		}
	}
	return nil
}

func (db *DB) indexColumns(ctx context.Context, index string) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT name FROM pragma_index_info(?)`, index)
	if err != nil {
		return nil, fmt.Errorf("индекс %s: %w", index, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n *string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("индекс %s: %w", index, err)
		}
		if n != nil {
			out[strings.ToLower(*n)] = true
		}
	}
	return out, rows.Err()
}

// checkConvertible ищет значения, которые не станут значением нового типа.
// Возвращает их количество и до maxConvertExamples примеров.
//
// Проверка идёт в Go, а не выражением в SQL, по двум причинам: SQLite приводит
// «abc» к нулю молча (никакой ошибки, данные тихо обнулились бы), а PostgreSQL
// роняет ALTER на первом же плохом значении, не показав, сколько их всего и
// каких. Один проход по колонке для операции такого класса — приемлемая цена.
func (db *DB) checkConvertible(ctx context.Context, table, column string, f metadata.Field) (int, []string, error) {
	parse := valueChecker(f)
	if parse == nil {
		return 0, nil, nil // в текст преобразуется что угодно
	}
	// Колонку надо проверить на существование отдельно: SQLite для неизвестного
	// идентификатора в двойных кавычках подставляет СТРОКОВЫЙ ЛИТЕРАЛ (наследие
	// совместимости), поэтому запрос ниже не упал бы, а вернул одно «значение»,
	// равное имени колонки, — и мы обвинили бы данные в том, что колонки нет.
	cols, err := db.tableColumns(ctx, table)
	if err != nil {
		return 0, nil, err
	}
	if _, ok := cols[strings.ToLower(column)]; !ok {
		return 0, nil, fmt.Errorf("%s.%s: колонки нет в таблице — схема расходится с картой полей", table, column)
	}
	rows, err := db.Query(ctx, "SELECT DISTINCT CAST("+quoteIdent(column)+" AS TEXT) FROM "+quoteIdent(table)+
		" WHERE "+quoteIdent(column)+" IS NOT NULL")
	if err != nil {
		return 0, nil, fmt.Errorf("%s.%s: проверка значений: %w", table, column, err)
	}
	defer rows.Close()

	bad := 0
	var examples []string
	for rows.Next() {
		var v *string
		if err := rows.Scan(&v); err != nil {
			return 0, nil, fmt.Errorf("%s.%s: проверка значений: %w", table, column, err)
		}
		if v == nil || strings.TrimSpace(*v) == "" {
			continue // пустое значение станет NULL — это преобразование законно
		}
		if parse(strings.TrimSpace(*v)) {
			continue
		}
		bad++
		if len(examples) < maxConvertExamples {
			examples = append(examples, strconv.Quote(*v))
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("%s.%s: проверка значений: %w", table, column, err)
	}
	return bad, examples, nil
}

// valueChecker возвращает функцию «значение годится для этого типа».
// nil означает «проверять нечего».
func valueChecker(f metadata.Field) func(string) bool {
	if f.RefEntity != "" {
		return func(v string) bool { _, err := uuid.Parse(v); return err == nil }
	}
	switch f.Type {
	case metadata.FieldTypeNumber:
		return func(v string) bool { _, err := decimal.NewFromString(v); return err == nil }
	case metadata.FieldTypeBool:
		return func(v string) bool {
			switch strings.ToLower(v) {
			case "true", "false", "t", "f", "yes", "no", "on", "off", "1", "0":
				return true
			}
			return false
		}
	case metadata.FieldTypeDate:
		return func(v string) bool {
			for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
				if _, err := time.Parse(layout, v); err == nil {
					return true
				}
			}
			return false
		}
	}
	return nil
}

// pgUsingExpr — выражение преобразования для ALTER … TYPE … USING.
// Пустая строка приводится к NULL: иначе «» не станет ни числом, ни датой.
func pgUsingExpr(col, newSQL string) string {
	upper := strings.ToUpper(newSQL)
	target := upper
	if idx := strings.Index(target, "("); idx > 0 {
		target = target[:idx]
	}
	switch strings.TrimSpace(target) {
	case "TEXT":
		return col + "::text"
	default:
		return "NULLIF(btrim(" + col + "::text),'')::" + newSQL
	}
}

// sqlTypeForSignature возвращает SQL-тип, соответствующий запомненной подписи
// поля. Нужен, чтобы понять, меняется ли хранение: на SQLite почти все типы —
// TEXT, и смена «строка → число» там не требует никакого DDL.
func sqlTypeForSignature(d Dialect, sig string) string {
	f := metadata.Field{}
	switch {
	case strings.HasPrefix(sig, "ref:"):
		f.RefEntity = strings.TrimPrefix(sig, "ref:")
	case strings.HasPrefix(sig, "number("):
		f.Type = metadata.FieldTypeNumber
		f.Length, f.Scale = parseSignatureNumber(sig)
	default:
		f.Type = metadata.FieldType(sig)
	}
	return fieldType(d, f)
}

func parseSignatureNumber(sig string) (length, scale int) {
	inner := strings.TrimSuffix(strings.TrimPrefix(sig, "number("), ")")
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) > 0 {
		length, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	}
	if len(parts) > 1 {
		scale, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	return length, scale
}

// quoteIdent экранирует идентификатор. Двойные кавычки понимают оба диалекта.
func quoteIdent(s string) string { return pgQuoteIdent(s) }
