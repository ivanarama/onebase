package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// ListParams controls filtering, search, sorting and pagination for List queries.
type ListParams struct {
	Filters   map[string]FilterValue
	RowFilter *Predicate // additional SQL-side row-level access predicate
	// RowFilterEvaluated — строковый доступ был вычислен для этого списка (план
	// 79F): хендлер прошёл через applyRowFilter/rowFilterFor (даже если политика
	// неограничивающая — RowFilter при этом nil). Используется strict-RLS
	// чокпоинтом в List как признак «фильтр не забыли», иначе fail-closed.
	RowFilterEvaluated bool
	JournalRowFilters  map[string]*Predicate // per document name row-level predicates for journal UNIONs
	Sort               string                // field Name (empty = default sort by id)
	Dir                string                // "asc" or "desc"
	ParentStr          string                // "" = no filter; "root" = parent IS NULL; "<uuid>" = parent = uuid
	Search             string                // full-text search: ILIKE across all string fields
	ActivityScope      string                // "", "active", "inactive", "all"; applied only for opt-in catalogs
	Limit              int                   // 0 = no limit
	Offset             int                   // for pagination
	AfterID            *uuid.UUID            // exclusive keyset cursor; requires id ASC and Offset=0
	ThroughID          *uuid.UUID            // inclusive keyset high-water mark; requires id ASC and Offset=0
	ExcludeFolders     bool                  // for hierarchical catalogs: only non-folder elements
	OnlyFolders        bool                  // for hierarchical catalogs: only folder elements
}

// FilterValue holds a filter for one field.
type FilterValue struct {
	Value string // used for string and reference equality
	From  string // used for date range start (inclusive)
	To    string // used for date range end (inclusive)
}

// Upsert inserts or updates the object fields.
func (db *DB) Upsert(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error {
	return db.upsert(ctx, entityName, id, fields, entity, true, upsertAuditAuto)
}

// UpsertProvisional inserts a transaction-local parent row without writing an
// audit event. entityservice uses it before a new-object hook so FK children can
// refer to the parent. The final UpsertPreserveVersion writes the single
// externally visible create event after the hook and all final fields succeed.
func (db *DB) UpsertProvisional(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error {
	return db.upsert(ctx, entityName, id, fields, entity, true, upsertAuditSkip)
}

// UpsertPreserveVersion updates fields without advancing _version on conflict.
// It is intentionally narrow: entityservice uses it only for the final write of
// a new row provisionally inserted earlier in the SAME transaction so a hook can
// create FK children. It records one create event for the final object state;
// the provisional insert deliberately does not audit. The externally visible
// committed object still starts at version 1. Ordinary callers must use Upsert.
func (db *DB) UpsertPreserveVersion(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error {
	return db.upsert(ctx, entityName, id, fields, entity, false, upsertAuditCreate)
}

// UpsertAfterVersionBump persists fields without advancing _version after the
// caller has already performed the one versioned write for the same logical
// operation in the same transaction. Unlike UpsertPreserveVersion, this is for
// an existing row and therefore records an ordinary update audit diff rather
// than a synthetic create event.
func (db *DB) UpsertAfterVersionBump(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity) error {
	if !HasTx(ctx) {
		return errors.New("storage: UpsertAfterVersionBump requires an active transaction")
	}
	return db.upsert(ctx, entityName, id, fields, entity, false, upsertAuditAuto)
}

type upsertAuditMode uint8

const (
	upsertAuditAuto upsertAuditMode = iota
	upsertAuditSkip
	upsertAuditCreate
)

func (db *DB) upsert(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity, bumpVersion bool, auditMode upsertAuditMode) error {
	if err := db.enumBackstop(ctx, entity, fields); err != nil {
		return err
	}
	// Сущность с объявленными этапами (план 121) пишется сериализованным циклом
	// «прочитать → проверить переход → записать → записать историю». Решение о
	// допустимости принимается по прочитанному значению, поэтому между чтением и
	// записью объект не должен меняться: иначе два запроса читают один и тот же
	// «Черновик» и оба выполняют разные переходы из него.
	//
	// Сущность БЕЗ этапов идёт прежним путём — без транзакции-обёртки, без
	// блокировки и с прежними ошибками. Это условие важно держать узким: цена
	// сериализации не должна доставаться тем, кто про этапы ничего не объявлял.
	if !stagedEntity(entity) {
		return db.upsertInTx(ctx, entityName, id, fields, entity, bumpVersion, auditMode)
	}
	return db.WithTxScope(ctx, func(txCtx context.Context) error {
		return db.upsertInTx(txCtx, entityName, id, fields, entity, bumpVersion, auditMode)
	})
}

func (db *DB) upsertInTx(ctx context.Context, entityName string, id uuid.UUID, fields map[string]any, entity *metadata.Entity, bumpVersion bool, auditMode upsertAuditMode) error {
	d := db.dialect
	staged := stagedEntity(entity)
	if staged {
		// Блокировка записи ДО чтения. На PostgreSQL — advisory lock: обычный
		// FOR UPDATE не блокирует отсутствующую строку, а создание объекта —
		// такой же переход («» → начальный этап).
		if err := db.lockStageRecord(ctx, entityName, id); err != nil {
			return err
		}
	}
	// Read old value for audit diff (best-effort, ignore errors)
	var oldRow map[string]any
	isNew := false
	if existing, err := db.getByID(ctx, entityName, id, entity, staged); err != nil {
		// Для аудита чтение старого значения best-effort: не прочитали — считаем
		// объект новым, худшее последствие — неточная строка в журнале. Для гейта
		// этапов (план 121) так нельзя: сбой чтения означал бы «объекта нет», то
		// есть создание, а создание на начальном этапе разрешено всегда — ошибка
		// БД открывала бы проход мимо маршрута. Поэтому у сущности с этапами
		// «новый объект» — только настоящее отсутствие строки.
		if staged && !IsNotFound(errors.Unwrap(err)) && !IsNotFound(err) {
			return fmt.Errorf("upsert %s: чтение текущего этапа: %w", entityName, err)
		}
		// Для required-полей нельзя продолжать с неизвестным старым состоянием:
		// частичная запись должна либо сохранить заполненное значение, либо
		// честно отказаться. Ошибка чтения не вправе притвориться созданием.
		if auditMode != upsertAuditSkip && stageModeFromCtx(ctx).Source != StageSourceExchange &&
			hasRequiredEntityFields(entity) && !IsNotFound(err) {
			return fmt.Errorf("upsert %s: чтение текущих значений required-реквизитов: %w", entityName, err)
		}
		isNew = true
	} else {
		oldRow = existing
	}

	// Частичная запись сохраняет отсутствующие реквизиты. Проверяем итоговый
	// снимок, а не только входную map: direct create обязан передать все
	// required-поля, update — не стереть их пропущенным ключом. Провизорная
	// вставка перед OnWrite исключена; финальный UpsertPreserveVersion проверит
	// уже дополненный хуком объект в той же транзакции.
	effectiveFields := effectiveEntityValues(entity, oldRow, fields)
	if auditMode != upsertAuditSkip {
		// effectiveFields is the complete persisted snapshot for both create and
		// update, so every required field must now be present. The only incomplete
		// state permitted is the explicit provisional row above.
		if err := db.requiredBackstop(ctx, entity, effectiveFields); err != nil {
			return err
		}
	}

	// Гейт переходов между этапами (план 121) — до построения запроса, на уже
	// прочитанном старом значении. Вторая точка записи — UpsertVersioned
	// (optimistic_lock.go), там стоит такая же пара вызовов: разъехаться им
	// нельзя, иначе правка объекта из формы пройдёт мимо проверки.
	stageTr, err := db.checkStageTransition(ctx, entityName, entity, oldRow, fields)
	if err != nil {
		return err
	}

	table := metadata.TableName(entityName)
	cols := []string{"id"}
	placeholders := []string{d.Placeholder(1)}
	args := []any{idArg(d, id)}
	updates := []string{}

	argIdx := 2
	for _, f := range entity.Fields {
		_, given := canonicalFieldValue(fields, f.Name)
		col := metadata.ColumnName(f)
		ph := d.Placeholder(argIdx)
		argIdx++
		val, err := canonicalNumberArg(f, fieldValueDialect(d, f, fields))
		if err != nil {
			return err
		}
		cols = append(cols, col)
		placeholders = append(placeholders, ph)
		args = append(args, val)
		if given {
			updates = append(updates, col+" = EXCLUDED."+col)
		}
	}

	if entity.Hierarchical {
		// Иерархия обновляется, ТОЛЬКО если о ней сказали. Раньше служебные
		// колонки писались всегда: отсутствие ключа означало false и NULL, а не
		// «не трогать». ПолучитьОбъект() эти поля не читает (их нет в
		// метаданных), поэтому любая правка группы из DSL — переименование,
		// простановка слага, обновление при повторном импорте — молча
		// превращала раздел в обычный элемент и обнуляла родителя (#1040).
		//
		// На INSERT значения по умолчанию прежние (не группа, без родителя):
		// колонка в списке остаётся, из UPDATE исключается только она.
		parentValue, parentGiven := hierarchyValue(fields, "parent_id", "родитель", "parent")
		folderValue, folderGiven := hierarchyValue(fields, "is_folder", "этогруппа", "isfolder")

		parentIDStr := ""
		if parentValue != nil {
			parentIDStr = refUUIDString(parentValue)
		}
		if pID, err := uuid.Parse(parentIDStr); err == nil {
			if pID != id {
				if cycle, _ := db.WouldCycle(ctx, table, id, pID); cycle {
					return i18nerr.New("нельзя переместить группу в её подчинённую группу")
				}
			}
			cols = append(cols, "parent_id")
			placeholders = append(placeholders, d.Placeholder(argIdx))
			args = append(args, idArg(d, pID))
			argIdx++
			updates = append(updates, "parent_id = EXCLUDED.parent_id")
		} else {
			cols = append(cols, "parent_id")
			placeholders = append(placeholders, "NULL")
			if parentGiven {
				// Родителя передали пустым — это «убрать родителя», а не
				// «не трогать».
				updates = append(updates, "parent_id = NULL")
			}
		}
		isFolder := false
		switch tv := folderValue.(type) {
		case bool:
			isFolder = tv
		case string:
			isFolder = tv == "true" || tv == "Истина"
		}
		cols = append(cols, "is_folder")
		placeholders = append(placeholders, d.Placeholder(argIdx))
		args = append(args, isFolder)
		argIdx++
		if folderGiven {
			updates = append(updates, "is_folder = EXCLUDED.is_folder")
		}
	}
	// Оптимистическая блокировка: на каждом UPDATE инкрементируем _version.
	// На INSERT — DEFAULT 1 из DDL. См. UpsertVersioned для проверки ожидаемой
	// ревизии перед записью.
	if bumpVersion {
		updates = append(updates, "_version = "+table+"._version + 1")
	}

	var sql string
	switch {
	case len(updates) == 0:
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO NOTHING",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	case staged && isNew:
		// Создание объекта с этапами: строку вставляет ровно этот запрос. Если
		// её успел создать кто-то другой, DO NOTHING оставит ноль изменённых
		// строк — и запись отвергается, а не превращается молча в правку,
		// проверку перехода для которой никто не делал.
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO NOTHING",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	case staged:
		// Правка объекта с этапами: сравнение с прочитанной ревизией (CAS).
		// На SQLite это единственная защита — advisory-локов там нет, а два
		// подключения к одному файлу читают каждый свой снимок.
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO UPDATE SET %s WHERE %s._version = %s",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "),
			table, d.Placeholder(argIdx))
		args = append(args, stageReadVersion(oldRow))
	default:
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (id) DO UPDATE SET %s",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "))
	}
	tag, err := db.Exec(ctx, sql, args...)
	if err != nil {
		if staged {
			if conflict := stageConcurrencyErr(err); errors.Is(conflict, ErrStageConcurrentWrite) {
				return conflict
			}
		}
		// Дубль кода/номера — ошибка пользователя, а не сбой: он ввёл занятое
		// значение. Текст драйвера («UNIQUE constraint failed: контрагенты.код»)
		// ему ничего не говорит, поэтому подменяем его на человеческий и не
		// оборачиваем в «upsert <объект>» (план 117E).
		if explained := ExplainUniqueViolation(err, entity, fields); errors.Is(explained, ErrCodeDuplicate) {
			return explained
		}
		return fmt.Errorf("upsert %s: %w", entityName, classifyConstraintErr(err))
	}
	if staged && tag.RowsAffected != 1 {
		// Ноль изменённых строк на пути с этапами означает ровно одно: между
		// чтением и записью объект тронул кто-то ещё, и проверенный переход
		// относится к состоянию, которого уже нет.
		return ErrStageConcurrentWrite
	}

	// Полнотекстовый индекс (план 82) — в той же транзакции, что и запись:
	// откат записи откатывает и индекс, поэтому разъехаться они не могут.
	// Здесь, а не в entityservice, потому что путей записи несколько
	// (entityservice.Save, ui/dsl_documents, обмен, приёмка) — общий у них
	// только этот upsert.
	if err := db.IndexObject(ctx, entity, id, effectiveFields); err != nil {
		return err
	}

	// История переходов (план 121) — в той же транзакции, что и запись, и
	// безусловно: журнал регистрации ниже выключается настройкой, а отчёт «где
	// застряло» обязан работать всегда.
	//
	// Режим аудита здесь СОЗНАТЕЛЬНО не учитывается. Провизорная вставка нового
	// объекта (upsertAuditSkip) — это и есть момент, когда «» → начальный этап;
	// её запись в истории откатится вместе с транзакцией, если хук упадёт.
	// Audit mode intentionally does not affect stage history: a provisional
	// insert is still the real transition from no stage to the initial stage,
	// and its history row is committed or rolled back with the transaction.
	if err := db.logStageTransition(ctx, entityName, id, stageTr); err != nil {
		return err
	}

	// Audit (best-effort, non-blocking)
	kind := string(entity.Kind)
	switch {
	case auditMode == upsertAuditSkip:
		// A provisional row is not externally visible and is followed by the
		// final create audit in the same transaction.
	case auditMode == upsertAuditCreate:
		db.logCreate(ctx, kind, entityName, id)
	case isNew:
		db.logCreate(ctx, kind, entityName, id)
	case oldRow != nil:
		changes := AuditDiff(oldRow, effectiveFields, entity)
		if len(changes) > 0 {
			db.logUpdate(ctx, kind, entityName, id, changes)
		}
	}
	return nil
}

// stageReadVersion — ревизия, прочитанная перед проверкой перехода. Она уезжает
// в CAS-условие записи; отсутствие значения (старая строка без _version) даёт 0,
// и такая запись честно не пройдёт — лучше отказ, чем незамеченная гонка.
func stageReadVersion(oldRow map[string]any) int64 {
	if oldRow == nil {
		return 0
	}
	switch v := oldRow["_version"].(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// GetByID retrieves a single object by ID, returning fields as map[string]any.
// For documents, also returns "posted" bool.
func (db *DB) GetByID(ctx context.Context, entityName string, id uuid.UUID, entity *metadata.Entity) (map[string]any, error) {
	return db.getByID(ctx, entityName, id, entity, false)
}

// getByID — GetByID с опциональным FOR UPDATE: на PostgreSQL строка с этапами
// читается под блокировкой, чтобы между проверкой перехода и записью её никто
// не изменил. На SQLite блокировки строк нет, роль защиты играет CAS по
// _version при записи.
func (db *DB) getByID(ctx context.Context, entityName string, id uuid.UUID, entity *metadata.Entity, forUpdate bool) (map[string]any, error) {
	d := db.dialect
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	for _, f := range entity.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	if entity.Kind == metadata.KindDocument {
		cols = append(cols, "posted")
	}
	cols = append(cols, "deletion_mark", "_version")
	if entity.Hierarchical {
		cols = append(cols, "is_folder", "parent_id")
	}
	sql := fmt.Sprintf("SELECT %s FROM %s WHERE id = %s", strings.Join(cols, ", "), table, d.Placeholder(1))
	if forUpdate && db.IsPostgres() && HasTx(ctx) {
		sql += " FOR UPDATE"
	}
	row := db.QueryRow(ctx, sql, idArg(d, id))

	dest := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		return nil, fmt.Errorf("getbyid %s: %w", entityName, err)
	}

	result := make(map[string]any, len(cols))
	result["id"] = normalizeValue(dest[0])
	for i, f := range entity.Fields {
		result[f.Name] = normalizeFieldValue(f, dest[i+1])
	}
	off := len(entity.Fields) + 1
	if entity.Kind == metadata.KindDocument {
		result["posted"] = normalizeBool(dest[off])
		off++
	}
	result["deletion_mark"] = normalizeValue(dest[off])
	off++
	result["_version"] = normalizeValue(dest[off])
	off++
	if entity.Hierarchical {
		result["is_folder"] = normalizeValue(dest[off])
		off++
		result["parent_id"] = normalizeValue(dest[off])
	}
	return result, nil
}

// normalizeValue converts pgx scan results to display-friendly Go types.
// pgtype.Numeric (PG NUMERIC) → decimal.Decimal без потери точности: значение
// строится из big.Int и экспоненты напрямую, минуя float64.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case [16]byte:
		return uuid.UUID(t).String()
	case uuid.UUID:
		return t.String()
	case pgtype.Numeric:
		if !t.Valid || t.NaN || t.Int == nil {
			return nil
		}
		return decimal.NewFromBigInt(t.Int, t.Exp)
	case int64:
		return t
	}
	return v
}

// normalizeNumber приводит значение числового поля к decimal.Decimal независимо
// от движка: PG отдаёт pgtype.Numeric, SQLite — строку (колонка TEXT).
func normalizeNumber(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case decimal.Decimal:
		return t
	case pgtype.Numeric:
		if !t.Valid || t.NaN || t.Int == nil {
			return nil
		}
		return decimal.NewFromBigInt(t.Int, t.Exp)
	case string:
		if t == "" {
			return nil
		}
		if d, err := decimal.NewFromString(t); err == nil {
			return d
		}
		return nil
	case float64:
		return decimal.NewFromFloat(t)
	case int64:
		return decimal.NewFromInt(t)
	case int32:
		return decimal.NewFromInt(int64(t))
	case int:
		return decimal.NewFromInt(int64(t))
	}
	return v
}

// normalizeFieldValue нормализует значение с учётом типа поля. Числовые поля
// всегда возвращаются как decimal.Decimal, поля-даты — как time.Time — единая
// типизация на PG и SQLite.
func normalizeFieldValue(f metadata.Field, v any) any {
	if f.Type == metadata.FieldTypeNumber {
		return normalizeNumber(v)
	}
	if f.Type == metadata.FieldTypeDate {
		return normalizeDate(v)
	}
	return normalizeValue(v)
}

// normalizeDate приводит значение поля-даты к time.Time. На PostgreSQL драйвер
// уже отдаёт time.Time; на SQLite дата хранится строкой (RFC3339), и без
// приведения загруженный объект (Ссылка.ПолучитьОбъект) нёс бы Дату строкой —
// в отличие от свежесозданного (Создать), у которого это time.Time. Из-за
// этого арифметика дат (КонецДня, Дата + Число) и сравнения в проведении
// перезаписанного документа молча ломались. Нераспознанную строку и nil
// оставляем как есть (безопасный откат к прежнему поведению).
func normalizeDate(v any) any {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		if parsed, ok := ParseRegPeriod(t); ok {
			return parsed
		}
	}
	return normalizeValue(v)
}

// normalizeBool converts any DB boolean representation (bool, int64 0/1) to bool.
// SQLite stores booleans as integers; PostgreSQL returns bool directly.
func normalizeBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case int:
		return t != 0
	}
	return false
}

// List returns rows for an entity with optional filtering and sorting.
// For documents, also returns "posted" bool.
// dateUpperBound возвращает верхнюю границу фильтра по дате и оператор. Для
// суточного значения «2006-01-02» — следующий день и «<» (включить весь день,
// DST-устойчиво через AddDate). Если задано время — оставляет значение и «<=».
func dateUpperBound(to string) (string, string) {
	if t, err := time.Parse("2006-01-02", to); err == nil {
		return t.AddDate(0, 0, 1).Format("2006-01-02"), "<"
	}
	return to, "<="
}

func activityWhere(d Dialect, entity *metadata.Entity, scope string) string {
	if entity == nil || entity.Activity == nil || scope == "" || scope == metadata.ActivityScopeAll {
		return ""
	}
	col := metadata.ColumnName(metadata.Field{Name: entity.Activity.Field})
	falseLit := boolFalseLit(d)
	switch scope {
	case metadata.ActivityScopeActive:
		return fmt.Sprintf("(%s IS NULL OR %s <> %s)", col, col, falseLit)
	case metadata.ActivityScopeInactive:
		return fmt.Sprintf("%s = %s", col, falseLit)
	default:
		return ""
	}
}

func folderScopeWhere(d Dialect, entity *metadata.Entity, onlyFolders, excludeFolders bool) string {
	if entity == nil || !entity.Hierarchical {
		return ""
	}
	if onlyFolders {
		return fmt.Sprintf("is_folder = %s", boolTrueLit(d))
	}
	if !excludeFolders {
		return ""
	}
	return fmt.Sprintf("(is_folder IS NULL OR is_folder = %s)", boolFalseLit(d))
}

func (db *DB) List(ctx context.Context, entityName string, entity *metadata.Entity, params ListParams) ([]map[string]any, error) {
	// План 79F (defense-in-depth, по умолчанию выключен): если у сущности есть
	// строковая политика, но список запрошен без вычисления строкового доступа
	// (хендлер не прошёл applyRowFilter/rowFilterFor), отклоняем fail-closed —
	// чтобы обход RLS новым list-хендлером всплывал сразу, а не тихо.
	if db.rlsGuard != nil && !params.RowFilterEvaluated && db.rlsGuard(strings.ToLower(entityName)) {
		return nil, fmt.Errorf("strict RLS: список %q запрошен без вычисления строкового доступа (fail-closed, план 79F)", entityName)
	}
	keyset := params.AfterID != nil || params.ThroughID != nil
	if keyset {
		if params.Offset != 0 {
			return nil, fmt.Errorf("list %s: keyset pagination cannot be combined with offset", entityName)
		}
		if params.Sort != "" && !strings.EqualFold(params.Sort, "id") {
			return nil, fmt.Errorf("list %s: keyset pagination requires sort by id", entityName)
		}
		if params.Dir != "" && !strings.EqualFold(params.Dir, "asc") {
			return nil, fmt.Errorf("list %s: keyset pagination requires ascending order", entityName)
		}
	}
	d := db.dialect
	table := metadata.TableName(entityName)
	cols := []string{"id"}
	for _, f := range entity.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	if entity.Kind == metadata.KindDocument {
		cols = append(cols, "posted")
	}
	cols = append(cols, "deletion_mark")
	hasPredefined := entity.Kind == metadata.KindCatalog && len(entity.Predefined) > 0
	if hasPredefined {
		cols = append(cols, "_is_predefined")
	}
	if entity.Hierarchical {
		cols = append(cols, "is_folder", "parent_id")
	}

	var whereParts []string
	var args []any
	argIdx := 1

	// Parent filter for hierarchical catalogs
	if entity.Hierarchical && params.ParentStr != "" {
		if params.ParentStr == "root" {
			whereParts = append(whereParts, "parent_id IS NULL")
		} else if pID, err := uuid.Parse(params.ParentStr); err == nil {
			whereParts = append(whereParts, fmt.Sprintf("parent_id = %s", d.Placeholder(argIdx)))
			args = append(args, idArg(d, pID))
			argIdx++
		}
	}
	if cond := activityWhere(d, entity, params.ActivityScope); cond != "" {
		whereParts = append(whereParts, cond)
	}
	if cond := folderScopeWhere(d, entity, params.OnlyFolders, params.ExcludeFolders); cond != "" {
		whereParts = append(whereParts, cond)
	}

	for _, f := range entity.Fields {
		fv, ok := params.Filters[f.Name]
		if !ok {
			continue
		}
		col := metadata.ColumnName(f)
		switch {
		case f.Type == metadata.FieldTypeDate:
			if fv.From != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s >= %s", col, d.Placeholder(argIdx)))
				args = append(args, fv.From)
				argIdx++
			}
			if fv.To != "" {
				// Включаем весь выбранный день: для суточного «по дату» сравниваем
				// «< следующего дня», иначе документы этого дня с временем > 00:00
				// выпадали бы (а на SQLite, где дата хранится как RFC3339-строка,
				// исключался весь граничный день).
				bound, op := dateUpperBound(fv.To)
				whereParts = append(whereParts, fmt.Sprintf("%s %s %s", col, op, d.Placeholder(argIdx)))
				args = append(args, bound)
				argIdx++
			}
		case f.RefEntity != "":
			if fv.Value != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s = %s", col, d.Placeholder(argIdx)))
				if id, err := uuid.Parse(fv.Value); err == nil {
					args = append(args, idArg(d, id))
				} else {
					args = append(args, fv.Value)
				}
				argIdx++
			}
		default:
			if fv.Value != "" {
				whereParts = append(whereParts, d.LowerLike(col)+" LIKE "+d.LowerLike(d.Placeholder(argIdx)))
				args = append(args, "%"+fv.Value+"%")
				argIdx++
			}
		}
	}

	// Поиск подстроки по реквизитам объекта (состав — см. metadata.SearchFields).
	// SQLite '?' placeholders are positional with no repetition; for each
	// field we allocate a fresh placeholder and bind the pattern again.
	if params.Search != "" {
		var searchParts []string
		pattern := "%" + params.Search + "%"
		// Состав полей задаёт metadata.SearchFields: по умолчанию все строковые
		// реквизиты (как было всегда), а блок search_fields в YAML позволяет
		// перечислить свои — например артикул или штрихкод, которые часто хранят
		// числом и в поиск не попадали. Приведение к тексту делает LowerLike.
		for _, f := range metadata.SearchFields(entity) {
			col := metadata.ColumnName(f)
			searchParts = append(searchParts, d.LowerLike(col)+" LIKE "+d.LowerLike(d.Placeholder(argIdx)))
			args = append(args, pattern)
			argIdx++
		}
		if len(searchParts) > 0 {
			whereParts = append(whereParts, "("+strings.Join(searchParts, " OR ")+")")
		} else {
			// Искать не по чему (пустой search_fields или объект без текстовых
			// реквизитов). Выдача обязана быть пустой: полный список в ответ на
			// запрос пользователь принимает за результат поиска.
			whereParts = append(whereParts, "1=0")
		}
	}
	if cond, condArgs, next, err := PredicateSQL(d, entity, params.RowFilter, argIdx); err != nil {
		return nil, fmt.Errorf("list %s row filter: %w", entityName, err)
	} else if cond != "" {
		whereParts = append(whereParts, cond)
		args = append(args, condArgs...)
		argIdx = next
	}
	if params.AfterID != nil {
		whereParts = append(whereParts, fmt.Sprintf("id > %s", d.Placeholder(argIdx)))
		args = append(args, idArg(d, *params.AfterID))
		argIdx++
	}
	if params.ThroughID != nil {
		whereParts = append(whereParts, fmt.Sprintf("id <= %s", d.Placeholder(argIdx)))
		args = append(args, idArg(d, *params.ThroughID))
		argIdx++
	}
	_ = argIdx

	baseQuery := fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), table)
	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = " WHERE " + strings.Join(whereParts, " AND ")
	}
	query := baseQuery + whereClause

	// sorting
	if keyset {
		query += " ORDER BY id ASC"
	} else if entity.Hierarchical && params.Sort == "" {
		firstStrCol := "id"
		for _, f := range entity.Fields {
			if f.Type == metadata.FieldTypeString {
				firstStrCol = metadata.ColumnName(f)
				break
			}
		}
		query += fmt.Sprintf(" ORDER BY is_folder DESC, %s ASC", firstStrCol)
	} else {
		orderCol := "id"
		if params.Sort != "" {
			for _, f := range entity.Fields {
				if f.Name == params.Sort {
					orderCol = metadata.ColumnName(f)
					break
				}
			}
		}
		orderDir := "ASC"
		if strings.ToLower(params.Dir) == "desc" {
			orderDir = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY %s %s", orderCol, orderDir)
	}

	if params.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", params.Limit, params.Offset)
	}

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", entityName, err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		row["id"] = normalizeValue(dest[0])
		for i, f := range entity.Fields {
			row[f.Name] = normalizeFieldValue(f, dest[i+1])
		}
		off := len(entity.Fields) + 1
		if entity.Kind == metadata.KindDocument {
			row["posted"] = normalizeBool(dest[off])
			off++
		}
		row["deletion_mark"] = normalizeValue(dest[off])
		off++
		if hasPredefined {
			row["_is_predefined"] = normalizeValue(dest[off])
			off++
		}
		if entity.Hierarchical {
			row["is_folder"] = normalizeValue(dest[off])
			off++
			row["parent_id"] = normalizeValue(dest[off])
			// off++
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// CountList returns the total number of rows matching the given params
// (ignoring pagination: Limit, Offset, AfterID and ThroughID).
func (db *DB) CountList(ctx context.Context, entityName string, entity *metadata.Entity, params ListParams) (int, error) {
	d := db.dialect
	table := metadata.TableName(entityName)
	var whereParts []string
	var args []any
	argIdx := 1

	if entity.Hierarchical && params.ParentStr != "" {
		if params.ParentStr == "root" {
			whereParts = append(whereParts, "parent_id IS NULL")
		} else if pID, err := uuid.Parse(params.ParentStr); err == nil {
			whereParts = append(whereParts, fmt.Sprintf("parent_id = %s", d.Placeholder(argIdx)))
			args = append(args, idArg(d, pID))
			argIdx++
		}
	}
	if cond := activityWhere(d, entity, params.ActivityScope); cond != "" {
		whereParts = append(whereParts, cond)
	}
	if cond := folderScopeWhere(d, entity, params.OnlyFolders, params.ExcludeFolders); cond != "" {
		whereParts = append(whereParts, cond)
	}

	for _, f := range entity.Fields {
		fv, ok := params.Filters[f.Name]
		if !ok {
			continue
		}
		col := metadata.ColumnName(f)
		switch {
		case f.Type == metadata.FieldTypeDate:
			if fv.From != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s >= %s", col, d.Placeholder(argIdx)))
				args = append(args, fv.From)
				argIdx++
			}
			if fv.To != "" {
				// Включаем весь выбранный день: для суточного «по дату» сравниваем
				// «< следующего дня», иначе документы этого дня с временем > 00:00
				// выпадали бы (а на SQLite, где дата хранится как RFC3339-строка,
				// исключался весь граничный день).
				bound, op := dateUpperBound(fv.To)
				whereParts = append(whereParts, fmt.Sprintf("%s %s %s", col, op, d.Placeholder(argIdx)))
				args = append(args, bound)
				argIdx++
			}
		case f.RefEntity != "":
			if fv.Value != "" {
				whereParts = append(whereParts, fmt.Sprintf("%s = %s", col, d.Placeholder(argIdx)))
				if id, err := uuid.Parse(fv.Value); err == nil {
					args = append(args, idArg(d, id))
				} else {
					args = append(args, fv.Value)
				}
				argIdx++
			}
		default:
			if fv.Value != "" {
				whereParts = append(whereParts, d.LowerLike(col)+" LIKE "+d.LowerLike(d.Placeholder(argIdx)))
				args = append(args, "%"+fv.Value+"%")
				argIdx++
			}
		}
	}

	if params.Search != "" {
		var searchParts []string
		pattern := "%" + params.Search + "%"
		// Состав полей задаёт metadata.SearchFields: по умолчанию все строковые
		// реквизиты (как было всегда), а блок search_fields в YAML позволяет
		// перечислить свои — например артикул или штрихкод, которые часто хранят
		// числом и в поиск не попадали. Приведение к тексту делает LowerLike.
		for _, f := range metadata.SearchFields(entity) {
			col := metadata.ColumnName(f)
			searchParts = append(searchParts, d.LowerLike(col)+" LIKE "+d.LowerLike(d.Placeholder(argIdx)))
			args = append(args, pattern)
			argIdx++
		}
		if len(searchParts) > 0 {
			whereParts = append(whereParts, "("+strings.Join(searchParts, " OR ")+")")
		} else {
			// Искать не по чему (пустой search_fields или объект без текстовых
			// реквизитов). Выдача обязана быть пустой: полный список в ответ на
			// запрос пользователь принимает за результат поиска.
			whereParts = append(whereParts, "1=0")
		}
	}
	if cond, condArgs, next, err := PredicateSQL(d, entity, params.RowFilter, argIdx); err != nil {
		return 0, fmt.Errorf("count %s row filter: %w", entityName, err)
	} else if cond != "" {
		whereParts = append(whereParts, cond)
		args = append(args, condArgs...)
		argIdx = next
	}
	_ = argIdx

	q := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if len(whereParts) > 0 {
		q += " WHERE " + strings.Join(whereParts, " AND ")
	}
	var count int
	if err := db.QueryRow(ctx, q, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s: %w", entityName, err)
	}
	return count, nil
}

// GetTablePartRows returns rows of a tablepart for a given parent id, ordered by строка.
func (db *DB) GetTablePartRows(ctx context.Context, entityName, tpName string, parentID uuid.UUID, tp metadata.TablePart) ([]map[string]any, error) {
	d := db.dialect
	table := metadata.TablePartTableName(entityName, tpName)
	cols := []string{"строка"}
	for _, f := range tp.Fields {
		cols = append(cols, metadata.ColumnName(f))
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE parent_id = %s ORDER BY строка",
		strings.Join(cols, ", "), table, d.Placeholder(1))
	rows, err := db.Query(ctx, query, idArg(d, parentID))
	if err != nil {
		return nil, fmt.Errorf("get tablepart %s.%s: %w", entityName, tpName, err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		row["строка"] = dest[0]
		for i, f := range tp.Fields {
			row[f.Name] = normalizeFieldValue(f, dest[i+1])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// UpsertTablePartRows replaces all rows for the given parent with the provided rows.
func (db *DB) UpsertTablePartRows(ctx context.Context, entityName, tpName string, parentID uuid.UUID, rows []map[string]any, tp metadata.TablePart) error {
	// Страховка значений перечислений в строках (#962, Н3): шапочная проверка
	// сюда не достаёт — строки пишутся отдельным вызовом.
	if err := db.enumBackstopRows(ctx, entityName, tp, rows); err != nil {
		return err
	}
	if err := db.requiredBackstopRows(ctx, entityName, tp, rows); err != nil {
		return err
	}
	d := db.dialect
	table := metadata.TablePartTableName(entityName, tpName)

	if err := db.exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE parent_id = %s", table, d.Placeholder(1)),
		idArg(d, parentID)); err != nil {
		return fmt.Errorf("delete tablepart %s.%s: %w", entityName, tpName, err)
	}

	for i, row := range rows {
		cols := []string{"id", "parent_id", "строка"}
		placeholders := []string{d.Placeholder(1), d.Placeholder(2), d.Placeholder(3)}
		args := []any{idArg(d, uuid.New()), idArg(d, parentID), i + 1}
		for j, f := range tp.Fields {
			val, err := canonicalNumberArg(f, fieldValueDialect(d, f, row))
			if err != nil {
				return err
			}
			cols = append(cols, metadata.ColumnName(f))
			placeholders = append(placeholders, d.Placeholder(j+4))
			args = append(args, val)
		}
		sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
		if err := db.exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("insert tablepart %s.%s row %d: %w", entityName, tpName, i+1, err)
		}
	}
	return nil
}

// Delete removes an entity record by id. Tablepart rows cascade automatically.
// Returns an error if the record is a predefined item (_is_predefined = TRUE).
func (db *DB) Delete(ctx context.Context, entityName string, id uuid.UUID) error {
	d := db.dialect
	tbl := metadata.TableName(entityName)
	isPredefined, err := db.isPredefinedRecord(ctx, tbl, id)
	if err != nil {
		return err
	}
	if isPredefined {
		return i18nerr.Errorf("нельзя удалить предопределённый элемент %s", entityName)
	}

	// Only hierarchical catalogs have parent_id. Probing the optional column
	// with a failing SELECT would abort a PostgreSQL transaction.
	hasParent, err := d.ColumnExists(ctx, db, tbl, "parent_id")
	if err != nil {
		return fmt.Errorf("check %s.parent_id: %w", tbl, err)
	}
	if hasParent {
		var childCount int
		if err := db.QueryRow(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE parent_id = %s AND deletion_mark = %s",
				tbl, d.Placeholder(1), boolFalseLit(d)),
			idArg(d, id),
		).Scan(&childCount); err != nil {
			return fmt.Errorf("count children of %s: %w", entityName, err)
		} else if childCount > 0 {
			return i18nerr.Errorf("нельзя удалить группу: в ней есть элементы (%d шт.)", childCount)
		}
	}

	err = db.exec(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE id = %s", tbl, d.Placeholder(1)), idArg(d, id))
	if err == nil {
		// План 82: удалённый объект уходит и из полнотекстового индекса, иначе
		// глобальный поиск отдавал бы битые ссылки на несуществующие карточки.
		if ftsErr := db.DeleteFromFullTextIndex(ctx, entityName, id); ftsErr != nil {
			return ftsErr
		}
		if s := db.GetAuditSettings(ctx); s.Enabled && s.Delete {
			u, _ := auditUserFromCtx(ctx)
			_ = db.Log(ctx, &AuditEntry{
				UserID:     u.UserID,
				UserLogin:  u.UserLogin,
				Action:     "delete",
				EntityName: entityName,
				RecordID:   id.String(),
			})
		}
	}
	return err
}

// SetPosted sets the posted flag on a document.
func (db *DB) SetPosted(ctx context.Context, entityName string, id uuid.UUID, posted bool) error {
	d := db.dialect
	if posted {
		// Инвариант: помеченный на удаление документ нельзя провести. Backstop —
		// точки входа проведения сторожат раньше, это страховка от будущих путей.
		if marked, mErr := db.IsMarkedForDeletion(ctx, entityName, id); mErr != nil {
			return mErr
		} else if marked {
			return ErrPostingDeletionMarked
		}
	}
	err := db.exec(ctx,
		fmt.Sprintf("UPDATE %s SET posted = %s WHERE id = %s",
			metadata.TableName(entityName), d.Placeholder(1), d.Placeholder(2)),
		posted, idArg(d, id))
	if err == nil {
		if s := db.GetAuditSettings(ctx); s.Enabled && s.Post {
			u, _ := auditUserFromCtx(ctx)
			action := "post"
			if !posted {
				action = "unpost"
			}
			_ = db.Log(ctx, &AuditEntry{
				UserID:     u.UserID,
				UserLogin:  u.UserLogin,
				Action:     action,
				EntityKind: "document",
				EntityName: entityName,
				RecordID:   id.String(),
			})
		}
	}
	return err
}

// uuidProvider is implemented by *interpreter.Ref to expose its UUID without
// creating an import cycle between storage and interpreter packages.
type uuidProvider interface{ GetRefUUID() string }

// fieldValueDialect extracts a field value and normalizes UUIDs:
// PG accepts uuid.UUID directly; SQLite stores them as TEXT strings.
// refValueAsPointer возвращает указатель на копию значения, если интерфейс
// uuidProvider реализован НА УКАЗАТЕЛЕ (как у interpreter.Ref), а в поле лежит
// сама структура. Так ссылка, скопированная по значению, всё равно доезжает до
// SQL как uuid, а не как непонятная драйверу структура.
func refValueAsPointer(v any) (uuidProvider, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return nil, false
	}
	ptr := reflect.New(rv.Type())
	ptr.Elem().Set(rv)
	p, ok := ptr.Interface().(uuidProvider)
	return p, ok
}

func fieldValueDialect(d Dialect, f metadata.Field, fields map[string]any) any {
	v, _ := canonicalFieldValue(fields, f.Name)
	if f.RefEntity != "" {
		if v == nil {
			return nil
		}
		// Ссылка могла прийти ЗНАЧЕНИЕМ, а не указателем: GetRefUUID объявлен на
		// указателе, поэтому такая копия не проходила проверку ниже и уезжала в
		// драйвер как есть — «unsupported type interpreter.Ref, a struct», причём
		// в момент записи документа и без подсказки, какое поле виновато.
		if p, ok := refValueAsPointer(v); ok {
			v = p
		}
		if rv, ok := v.(uuidProvider); ok {
			s := rv.GetRefUUID()
			if s == "" {
				return nil
			}
			if id, err := uuid.Parse(s); err == nil {
				return idArg(d, id)
			}
			return nil
		}
		if s, ok := v.(string); ok {
			if s == "" {
				return nil
			}
			if id, err := uuid.Parse(s); err == nil {
				return idArg(d, id)
			}
			return nil
		}
	}
	// Ссылка в НЕссылочной колонке (поле объявлено строкой, а DSL положил ссылку —
	// например копированием реквизита между документами). Пишем представление, как
	// это давно делает регистр (normalizeRegArg): раньше сюда доезжала структура и
	// драйвер падал с «unsupported type», хотя терять было нечего.
	if f.RefEntity == "" && v != nil {
		if p, ok := refValueAsPointer(v); ok {
			v = p
		}
		if _, isRef := v.(uuidProvider); isRef {
			if s, ok := v.(interface{ String() string }); ok {
				v = s.String()
			}
		}
	}
	// SQLite stores time.Time as its .String() representation ("2006-01-02 15:04:05 -0700 MST")
	// which is unreadable by modernc. Normalize to RFC3339 for reliable round-trip.
	if f.Type == metadata.FieldTypeDate && d.Name() == "sqlite" {
		if t, ok := v.(time.Time); ok {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return v
}

// idArg encodes a UUID for the active backend: PG → uuid.UUID, SQLite → string.
func idArg(d Dialect, id uuid.UUID) any {
	if d.Name() == "sqlite" {
		return id.String()
	}
	return id
}
