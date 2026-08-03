package storage

// Безопасная реструктуризация схемы (план 81) — аналог «реструктуризации» 1С.
//
// До этого миграция была чисто аддитивной: `AddColumnIfMissing` добавлял новые
// колонки и не трогал существующие. Значит, переименование реквизита выглядело
// как «одно поле исчезло, другое появилось» и заводило новую ПУСТУЮ колонку —
// накопленные данные оставались в осиротевшей старой, невидимой приложению.
// Смена типа игнорировалась молча: колонка уже есть, добавлять нечего.
//
// Здесь появляется недостающее звено — устойчивая идентичность поля. Поле несёт
// необязательный `id`, а база помнит, какой колонке этот `id` соответствовал
// (таблица `_schema_fields`). Сравнив желаемое с запомненным и с фактической
// схемой, миграция отличает переименование от «удалить и создать».
//
// Три инварианта, которые тут важнее удобства:
//
//  1. Трогаем только СВОИ колонки — те, что записаны в карте. Колонки полей без
//     `id`, служебные (id, deletion_mark, _version, posted, period) и всё, чего
//     мы не заводили, планировщик не рассматривает вовсе.
//  2. Удаление — только по явному разрешению. Без него колонка остаётся
//     осиротевшей, а в отчёт идёт предупреждение.
//  3. Преобразование типа отменяется ДО потери данных: непреобразуемые значения
//     ищутся заранее, и миграция отказывается, а не портит колонку.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ChangeKind — вид изменения схемы.
type ChangeKind string

// Виды изменений.
const (
	ChangeAdd    ChangeKind = "add"    // новое поле → новая колонка
	ChangeRename ChangeKind = "rename" // поле переименовано → колонка переименована, данные на месте
	ChangeRetype ChangeKind = "retype" // изменился тип → преобразование колонки
	ChangeDrop   ChangeKind = "drop"   // поле удалено из метаданных → колонка лишняя
)

// SchemaChange — одно запланированное изменение схемы.
type SchemaChange struct {
	Table   string
	FieldID string
	Kind    ChangeKind
	// From/To — колонки для rename, подписи типа для retype, имя колонки для
	// add/drop (в To и From соответственно).
	From string
	To   string
	// Field — желаемое состояние поля (пусто для drop: поля уже нет).
	Field metadata.Field
	// Note — пояснение для человека: почему изменение опасно или что оно
	// сделает с данными.
	Note string
}

// Destructive сообщает, что изменение необратимо теряет данные.
func (c SchemaChange) Destructive() bool { return c.Kind == ChangeDrop }

// String — строка плана для вывода `onebase migrate --dry-run`.
func (c SchemaChange) String() string {
	switch c.Kind {
	case ChangeAdd:
		return fmt.Sprintf("%s: добавить колонку %s (поле %s)", c.Table, c.To, c.Field.Name)
	case ChangeRename:
		return fmt.Sprintf("%s: переименовать колонку %s → %s (поле %s, данные сохраняются)", c.Table, c.From, c.To, c.Field.Name)
	case ChangeRetype:
		return fmt.Sprintf("%s: изменить тип колонки %s: %s → %s (поле %s)", c.Table, c.To, c.From, metadata.FieldSignature(c.Field), c.Field.Name)
	case ChangeDrop:
		return fmt.Sprintf("%s: удалить колонку %s вместе с данными (поле с id %s убрано из конфигурации)", c.Table, c.From, c.FieldID)
	}
	return c.Table + ": " + string(c.Kind)
}

// SchemaOptions управляют реструктуризацией. Живут на подключении, а не в
// сигнатуре Migrate: миграцию вызывают из CLI, конфигуратора, проверки
// конфигурации и десятка тестов, и протаскивать флаги через все точки —
// шум, который ничего не даёт.
type SchemaOptions struct {
	// AllowDestructive разрешает удаление колонок. Без него лишние колонки
	// остаются осиротевшими и попадают в отчёт предупреждением.
	//
	// Пробного прогона здесь намеренно нет: Migrate в любом случае создаёт
	// таблицы и добавляет недостающие колонки, поэтому «dry-run миграции»
	// был бы обещанием, которого эта функция не выполняет. План строится
	// отдельным входом — PlanMigration, он не меняет ничего.
	AllowDestructive bool
	// Report вызывается на каждое изменение — применённое или отложенное.
	// Второй аргумент: применено ли оно на самом деле.
	Report func(change SchemaChange, applied bool)
}

// SetSchemaOptions задаёт режим реструктуризации для последующих миграций.
func (db *DB) SetSchemaOptions(opts SchemaOptions) { db.schemaOpts = opts }

// SchemaOptions возвращает текущий режим.
func (db *DB) SchemaOptions() SchemaOptions { return db.schemaOpts }

// EnsureSchemaMapSchema создаёт таблицу соответствия «id поля → колонка».
//
// Соответствие приходится хранить в базе: сама база знает только имена колонок,
// а связать имя с идентификатором поля можно лишь по памяти о том, как это поле
// называлось в прошлый раз.
func (db *DB) EnsureSchemaMapSchema(ctx context.Context) error {
	if _, err := db.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS _schema_fields (
			table_name  TEXT NOT NULL,
			field_id    TEXT NOT NULL,
			column_name TEXT NOT NULL,
			field_type  TEXT NOT NULL,
			PRIMARY KEY (table_name, field_id)
		)`); err != nil {
		return fmt.Errorf("schema map: create _schema_fields: %w", err)
	}
	return nil
}

// schemaMapEntry — запомненное состояние поля.
type schemaMapEntry struct {
	Column string
	Type   string
}

// loadSchemaMap читает соответствие для таблицы (id поля → колонка и тип).
func (db *DB) loadSchemaMap(ctx context.Context, table string) (map[string]schemaMapEntry, error) {
	out := map[string]schemaMapEntry{}
	if !tableExistsIn(ctx, db, "_schema_fields") {
		return out, nil
	}
	rows, err := db.Query(ctx,
		`SELECT field_id, column_name, field_type FROM _schema_fields WHERE table_name = `+db.dialect.Placeholder(1),
		table)
	if err != nil {
		return nil, fmt.Errorf("schema map: read %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, col, typ string
		if err := rows.Scan(&id, &col, &typ); err != nil {
			return nil, fmt.Errorf("schema map: read %s: %w", table, err)
		}
		out[id] = schemaMapEntry{Column: col, Type: typ}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema map: read %s: %w", table, err)
	}
	return out, nil
}

// saveSchemaMap перезаписывает соответствие для таблицы по текущим полям.
// Поля без id в карту не попадают — они живут в старом аддитивном режиме.
func (db *DB) saveSchemaMap(ctx context.Context, table string, fields []metadata.Field) error {
	if err := db.EnsureSchemaMapSchema(ctx); err != nil {
		return err
	}
	d := db.dialect
	keep := map[string]bool{}
	for _, f := range fields {
		if f.ID == "" {
			continue
		}
		keep[f.ID] = true
		q := fmt.Sprintf(
			`INSERT INTO _schema_fields (table_name, field_id, column_name, field_type) VALUES (%s, %s, %s, %s)
			 ON CONFLICT (table_name, field_id) DO UPDATE SET column_name = EXCLUDED.column_name, field_type = EXCLUDED.field_type`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
		if _, err := db.Exec(ctx, q, table, f.ID, metadata.ColumnName(f), metadata.FieldSignature(f)); err != nil {
			return fmt.Errorf("schema map: save %s.%s: %w", table, f.ID, err)
		}
	}
	// Забываем поле, только когда его колонки в таблице уже нет.
	//
	// Пока колонка жива, запись в карте — единственное, что помнит: эта колонка
	// принадлежала полю, которое убрали из конфигурации. Сотри её раньше — и
	// колонка станет «ничьей», а значит, `--allow-destructive` на следующем
	// запуске уже не найдёт, что удалять (ровно на это упал регресс).
	stored, err := db.loadSchemaMap(ctx, table)
	if err != nil {
		return err
	}
	actual, err := db.tableColumns(ctx, table)
	if err != nil {
		return err
	}
	for id, st := range stored {
		if keep[id] {
			continue
		}
		if _, exists := actual[strings.ToLower(st.Column)]; exists {
			continue
		}
		if _, err := db.Exec(ctx,
			`DELETE FROM _schema_fields WHERE table_name = `+d.Placeholder(1)+` AND field_id = `+d.Placeholder(2),
			table, id); err != nil {
			return fmt.Errorf("schema map: forget %s.%s: %w", table, id, err)
		}
	}
	return nil
}

// tableColumns возвращает фактические колонки таблицы: имя (в нижнем регистре)
// → тип, как его отдаёт диалект. Отсутствие таблицы — пустая карта.
func (db *DB) tableColumns(ctx context.Context, table string) (map[string]string, error) {
	out := map[string]string{}
	var (
		rows Rows
		err  error
	)
	if db.IsSQLite() {
		// PRAGMA не принимает параметр — имя таблицы приходит из метаданных
		// (не из запроса пользователя) и уже нормализовано ColumnName/TableName.
		rows, err = db.Query(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
	} else {
		rows, err = db.Query(ctx,
			`SELECT column_name, data_type FROM information_schema.columns
			  WHERE table_schema = 'public' AND table_name = $1`, table)
	}
	if err != nil {
		return nil, fmt.Errorf("schema: колонки %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, fmt.Errorf("schema: колонки %s: %w", table, err)
		}
		out[strings.ToLower(name)] = typ
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("schema: колонки %s: %w", table, err)
	}
	return out, nil
}

// PlanMigration строит план реструктуризации по всей конфигурации, не меняя
// базу, — это и есть пробный прогон `onebase migrate --dry-run`.
//
// Таблицы, которых ещё нет, пропускаются: их создаст обычная миграция, и
// перечислять каждое поле новой сущности как «добавить колонку» — шум, за
// которым потеряется единственное, ради чего план читают: что будет
// переименовано, преобразовано и удалено в уже существующих таблицах.
func (db *DB) PlanMigration(
	ctx context.Context,
	entities []*metadata.Entity,
	registers []*metadata.Register,
	infoRegs []*metadata.InfoRegister,
) ([]SchemaChange, error) {
	var out []SchemaChange
	plan := func(table string, fields []metadata.Field) error {
		if !anyFieldHasID(fields) {
			return nil
		}
		cols, err := db.tableColumns(ctx, table)
		if err != nil {
			return err
		}
		if len(cols) == 0 {
			return nil // таблицы ещё нет
		}
		changes, err := db.PlanTableChanges(ctx, table, fields)
		if err != nil {
			return err
		}
		out = append(out, changes...)
		return nil
	}

	for _, e := range entities {
		if err := plan(metadata.TableName(e.Name), e.Fields); err != nil {
			return nil, err
		}
		for _, tp := range e.TableParts {
			if err := plan(metadata.TablePartTableName(e.Name, tp.Name), tp.Fields); err != nil {
				return nil, err
			}
		}
	}
	for _, reg := range registers {
		fields := append(append([]metadata.Field{}, reg.Dimensions...), append(reg.Resources, reg.Attributes...)...)
		if err := plan(metadata.RegisterTableName(reg.Name), fields); err != nil {
			return nil, err
		}
	}
	for _, ir := range infoRegs {
		fields := append(append([]metadata.Field{}, ir.Dimensions...), ir.Resources...)
		if err := plan(metadata.InfoRegTableName(ir.Name), fields); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PlanTableChanges строит план приведения таблицы к желаемым полям.
//
// План строится только по полям с `id` и только по колонкам, записанным в
// карте: всё остальное — не наше, и планировщик о нём не рассуждает.
func (db *DB) PlanTableChanges(ctx context.Context, table string, fields []metadata.Field) ([]SchemaChange, error) {
	stored, err := db.loadSchemaMap(ctx, table)
	if err != nil {
		return nil, err
	}
	actual, err := db.tableColumns(ctx, table)
	if err != nil {
		return nil, err
	}

	var changes []SchemaChange
	alive := map[string]bool{}
	for _, f := range fields {
		if f.ID == "" {
			continue
		}
		alive[f.ID] = true
		want := metadata.ColumnName(f)
		sig := metadata.FieldSignature(f)
		st, known := stored[f.ID]

		if !known {
			// Поле впервые видят с идентификатором. Колонка уже есть — значит,
			// это существующая база, которой просто дописали id: изменение не
			// нужно, достаточно запомнить соответствие (это делает saveSchemaMap).
			if _, exists := actual[want]; !exists {
				changes = append(changes, SchemaChange{
					Table: table, FieldID: f.ID, Kind: ChangeAdd, To: want, Field: f,
				})
			}
			continue
		}

		if st.Column != want {
			_, oldExists := actual[st.Column]
			_, newExists := actual[want]
			switch {
			case oldExists && !newExists:
				changes = append(changes, SchemaChange{
					Table: table, FieldID: f.ID, Kind: ChangeRename,
					From: st.Column, To: want, Field: f,
				})
			case !oldExists && !newExists:
				changes = append(changes, SchemaChange{
					Table: table, FieldID: f.ID, Kind: ChangeAdd, To: want, Field: f,
				})
			case oldExists && newExists:
				// Колонка с новым именем уже занята — вероятно, второе поле с
				// тем же именем или ручная правка. Молча слить данные нельзя.
				return nil, fmt.Errorf(
					"%s: поле %s (id %s) переименовано в %s, но колонка %s уже существует — разберите вручную",
					table, st.Column, f.ID, want, want)
			}
			// Переименование могло совпасть со сменой типа: тип проверим ниже
			// уже по новому имени.
		}

		if st.Type != sig {
			changes = append(changes, SchemaChange{
				Table: table, FieldID: f.ID, Kind: ChangeRetype,
				From: st.Type, To: want, Field: f,
				Note: retypeNote(st.Type, sig),
			})
		}
	}

	for id, st := range stored {
		if alive[id] {
			continue
		}
		if _, exists := actual[st.Column]; !exists {
			continue
		}
		changes = append(changes, SchemaChange{
			Table: table, FieldID: id, Kind: ChangeDrop, From: st.Column,
			Note: "данные колонки будут потеряны безвозвратно",
		})
	}

	sort.SliceStable(changes, func(i, j int) bool { return changeOrder(changes[i]) < changeOrder(changes[j]) })
	return changes, nil
}

// changeOrder задаёт порядок применения: сначала переименования (иначе
// добавление создало бы колонку с новым именем и переименовывать было бы не во
// что), затем смена типа, затем добавления, и в самом конце — удаления.
func changeOrder(c SchemaChange) int {
	switch c.Kind {
	case ChangeRename:
		return 0
	case ChangeRetype:
		return 1
	case ChangeAdd:
		return 2
	default:
		return 3
	}
}

// retypeNote объясняет, чем грозит конкретное преобразование.
func retypeNote(from, to string) string {
	if strings.HasPrefix(from, "ref:") && strings.HasPrefix(to, "ref:") {
		return "колонка не меняется (обе ссылки — UUID), но записанные ссылки указывают на прежний справочник — проверьте данные"
	}
	return ""
}
