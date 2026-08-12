package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// История переходов между этапами (план 121).
//
// История пишется БЕЗУСЛОВНО, в отличие от журнала регистрации: тот выключаем
// настройкой (logUpdate молча ничего не пишет при !s.Enabled), а «где застряло» —
// не аудит, а рабочий отчёт, который обязан работать всегда. Ошибка записи
// истории возвращается наружу и откатывает объект: молча потерянный переход
// хуже отказа — по нему потом считают сроки.

// stageHistoryTimeLayout — формат момента перехода на SQLite.
//
// Отличается от общего sqliteTimeLayout долями секунды. Порядок событий на них
// НЕ держится (для этого есть event_no), но в интерфейсе два перехода одной
// секунды не должны выглядеть одновременными.
const stageHistoryTimeLayout = "2006-01-02 15:04:05.000000-07:00"

// stageTimeArg готовит момент времени к записи и к сравнению: на PostgreSQL —
// нативный timestamptz, на SQLite — строка того же формата, каким пишется
// колонка (сравнение там строковое, и форматы обязаны совпадать до знака).
func (db *DB) stageTimeArg(t time.Time) any {
	if db.IsSQLite() {
		return t.UTC().Format(stageHistoryTimeLayout)
	}
	return t.UTC()
}

// StageChange — одна запись истории переходов.
type StageChange struct {
	ID         string
	EntityName string
	RecordID   string
	// Field — имя реквизита на момент события, для показа.
	Field string
	// FieldID — устойчивая идентичность реквизита; именно она входит в ключ
	// последовательности, потому что имя переименовывают.
	FieldID string
	// EventNo — монотонный номер события в пределах (сущность, реквизит,
	// объект). Источник истины для «последнего» состояния: время им быть не
	// может — на PostgreSQL now() зафиксирован на старте транзакции, на SQLite
	// у datetime('now') секундная точность, а часы сервера могут отъехать назад.
	EventNo   int64
	FromStage string // пусто при создании
	ToStage   string
	At        time.Time
	UserID    string
	UserLogin string
	Source    string // local | exchange | migration
	SourceRef string // канонический JSON-массив происхождения синтетического перехода
	// Violation — переход был недопустим и прошёл только потому, что enforce: warn.
	Violation bool
}

// EnsureStageHistorySchema создаёт таблицу _stage_history, если её нет.
// Вызывается из всех точек подъёма базы рядом с EnsureAuditSchema — включая
// импорт универсальной резервной копии, иначе после restore таблицы не будет.
func (db *DB) EnsureStageHistorySchema(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _stage_history (
			id %s PRIMARY KEY,
			entity_name TEXT NOT NULL DEFAULT '',
			record_id %s,
			field TEXT NOT NULL DEFAULT '',
			field_id TEXT NOT NULL DEFAULT '',
			event_no BIGINT NOT NULL DEFAULT 1,
			from_stage TEXT NOT NULL DEFAULT '',
			to_stage TEXT NOT NULL DEFAULT '',
			at %s NOT NULL DEFAULT %s,
			user_id %s,
			user_login TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'local',
			source_ref TEXT NOT NULL DEFAULT '',
			violation %s NOT NULL DEFAULT %s
		)`, d.TypeUUID(), d.TypeUUID(), d.TypeTimestamp(), d.CurrentTimestampTZ(), d.TypeUUID(),
		d.TypeBool(), boolFalseLit(d))
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("stage history: create _stage_history: %w", err)
	}
	// Колонки, дотянутые к базам, созданным более ранней версией ветки.
	_ = db.AddColumnIfMissing(ctx, "_stage_history", "field_id", "TEXT NOT NULL DEFAULT ''")
	_ = db.AddColumnIfMissing(ctx, "_stage_history", "event_no", "BIGINT NOT NULL DEFAULT 1")
	_ = db.AddColumnIfMissing(ctx, "_stage_history", "source_ref", "TEXT NOT NULL DEFAULT ''")
	_ = db.AddColumnIfMissing(ctx, "_stage_history", "violation", d.TypeBool()+" NOT NULL DEFAULT "+boolFalseLit(d))

	// Уникальность последовательности — защита от гонки: два события с одним
	// event_no означали бы, что «последнее» неопределимо. Индексом, а не
	// табличным ограничением: ALTER TABLE … ADD CONSTRAINT на SQLite нет.
	if _, err := db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_stage_history_seq
		ON _stage_history (entity_name, field_id, record_id, event_no)`); err != nil {
		return fmt.Errorf("stage history: unique index: %w", err)
	}
	// «Сколько объектов на этапе и с какого момента» — без скана.
	_, _ = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_stage_history_stage ON _stage_history (entity_name, field_id, to_stage, at)`)
	return nil
}

// nextStageEventNo вычисляет следующий номер события. Вызывается внутри той же
// транзакции и под той же блокировкой записи, что и проверка перехода: иначе два
// параллельных перехода получили бы один номер, и уникальный индекс отверг бы
// второй уже после успешной записи объекта.
func (db *DB) nextStageEventNo(ctx context.Context, entityName, fieldID string, id uuid.UUID) (int64, error) {
	d := db.dialect
	q := fmt.Sprintf(`SELECT COALESCE(MAX(event_no), 0) + 1 FROM _stage_history
		WHERE entity_name = %s AND field_id = %s AND record_id = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	var n int64
	if err := db.QueryRow(ctx, q, entityName, fieldID, id.String()).Scan(&n); err != nil {
		return 0, fmt.Errorf("stage history: номер события %s.%s: %w", entityName, fieldID, err)
	}
	return n, nil
}

// LogStageChange пишет одну запись истории переходов.
//
// Время проставляется явно, а не умолчанием колонки: на SQLite datetime('now')
// даёт «2006-01-02 15:04:05», а связанный time.Time — другой формат, и записи
// лежали бы в одной колонке вперемешку.
func (db *DB) LogStageChange(ctx context.Context, ch *StageChange) error {
	d := db.dialect
	var userID any
	if ch.UserID != "" {
		if id, err := uuid.Parse(ch.UserID); err == nil {
			userID = id.String()
		}
	}
	recordUUID, err := uuid.Parse(ch.RecordID)
	if err != nil {
		return fmt.Errorf("stage history: некорректный идентификатор записи %q", ch.RecordID)
	}
	at := ch.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	source := ch.Source
	if source == "" {
		source = StageSourceLocal
	}
	fieldID := ch.FieldID
	if fieldID == "" {
		fieldID = strings.ToLower(ch.Field)
	}
	eventNo := ch.EventNo
	if eventNo <= 0 {
		eventNo, err = db.nextStageEventNo(ctx, ch.EntityName, fieldID, recordUUID)
		if err != nil {
			return err
		}
	}
	q := fmt.Sprintf(`
		INSERT INTO _stage_history (id, entity_name, record_id, field, field_id, event_no,
			from_stage, to_stage, at, user_id, user_login, source, source_ref, violation)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10),
		d.Placeholder(11), d.Placeholder(12), d.Placeholder(13), d.Placeholder(14))
	if _, err := db.Exec(ctx, q, uuid.NewString(), ch.EntityName, recordUUID.String(), ch.Field, fieldID, eventNo,
		ch.FromStage, ch.ToStage, db.stageTimeArg(at), userID, ch.UserLogin, source, ch.SourceRef,
		ch.Violation); err != nil {
		return fmt.Errorf("stage history: запись перехода %s.%s: %w", ch.EntityName, ch.Field, err)
	}
	return nil
}

// logStageTransition пишет переход в историю. Вызывается обеими точками записи
// после успешной записи объекта — в той же транзакции и под той же блокировкой,
// поэтому откат записи откатывает и историю.
func (db *DB) logStageTransition(ctx context.Context, entityName string, id uuid.UUID, tr *stageTransition) error {
	if tr == nil {
		return nil
	}
	ch := &StageChange{
		EntityName: entityName,
		RecordID:   id.String(),
		Field:      tr.Field,
		FieldID:    tr.FieldID,
		FromStage:  tr.From,
		ToStage:    tr.To,
		At:         time.Now().UTC(),
		Source:     tr.Source,
		SourceRef:  tr.SourceRef,
		Violation:  tr.Violation,
	}
	// Актор берётся из контекста только для обычной записи: синтетический
	// переход миграции или обмена не вправе выглядеть действием человека,
	// который в этот момент оказался в контексте.
	if ch.Source == "" || ch.Source == StageSourceLocal {
		u, _ := auditUserFromCtx(ctx)
		ch.UserID = u.UserID
		ch.UserLogin = u.UserLogin
	}
	return db.LogStageChange(ctx, ch)
}

// StageHistory возвращает переходы одного объекта, самые новые сверху.
// Порядок — по event_no: время для этого не годится (см. StageChange.EventNo).
func (db *DB) StageHistory(ctx context.Context, entityName string, id uuid.UUID) ([]StageChange, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, entity_name, record_id, field, field_id, event_no, from_stage, to_stage,
		       at, user_id, user_login, source, source_ref, violation
		FROM _stage_history
		WHERE entity_name = %s AND record_id = %s
		ORDER BY event_no DESC`, d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(ctx, q, entityName, id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StageChange
	for rows.Next() {
		var ch StageChange
		var rowID uuid.UUID
		var recordID, userID *uuid.UUID
		var at any
		if err := rows.Scan(&rowID, &ch.EntityName, &recordID, &ch.Field, &ch.FieldID, &ch.EventNo,
			&ch.FromStage, &ch.ToStage, &at, &userID, &ch.UserLogin, &ch.Source, &ch.SourceRef,
			&ch.Violation); err != nil {
			return nil, err
		}
		ch.ID = rowID.String()
		if recordID != nil {
			ch.RecordID = recordID.String()
		}
		if userID != nil {
			ch.UserID = userID.String()
		}
		ch.At = parseAuditTime(at)
		out = append(out, ch)
	}
	return out, rows.Err()
}

// StageBucket — строка отчёта «где застряло»: сколько объектов стоит на этапе и
// сколько из них висит дольше объявленного срока.
type StageBucket struct {
	Stage string
	// Count — всего объектов на этапе.
	Count int
	// Unknown — из них таких, по которым время на этапе неизвестно: истории нет
	// (объект существовал до объявления этапов) либо последнее событие ведёт в
	// другой этап (значение поменяли мимо истории). Считать такой срок от нуля
	// нельзя — это выдало бы «только что пришёл» за факт.
	Unknown int
	// Since — момент самого давнего перехода на этот этап среди объектов, у
	// которых история согласована с текущим значением.
	Since time.Time
	// DeadlineDays — объявленный срок этапа (0 — срока нет).
	DeadlineDays int
	// Overdue — объекты, попавшие на этап раньше, чем DeadlineDays назад.
	Overdue int
}

// StageSummary считает отчёт «где застряло» по объявленным этапам сущности.
//
// Текущий этап берётся из самого объекта, а момент попадания на него — из
// события с максимальным event_no. Событие с максимальным ВРЕМЕНЕМ для этого не
// годится: время не задаёт порядок. Если последнее событие ведёт в другой этап,
// длительность неизвестна — историю и значение развели мимо платформы, и
// подставлять чужой срок нельзя.
//
// Объекты, помеченные на удаление, в отчёт не попадают: они выведены из работы.
//
// rowFilter — предикат построчного доступа (план 79). Он обязателен там, где
// политика ограничивает видимость: отчёт считает объекты, и без фильтра
// пользователь, которому видны только свои документы, узнавал бы количество
// чужих.
func (db *DB) StageSummary(ctx context.Context, entity *metadata.Entity, rowFilter *Predicate) ([]StageBucket, error) {
	s := entity.Stages
	if s == nil {
		return nil, nil
	}
	f := entity.StageField()
	if f == nil {
		return nil, nil
	}
	d := db.dialect
	table := metadata.TableName(entity.Name)
	col := metadata.ColumnName(*f)
	fieldID := stageFieldID(f)

	// Имя сущности и идентичность поля повторяются в запросе дважды, и оба раза
	// передаются отдельным параметром: нумерованный плейсхолдер PostgreSQL можно
	// повторить, а «?» SQLite позиционный — повтор там означает ещё один
	// аргумент, а не ссылку на прежний.
	args := []any{entity.Name, fieldID, entity.Name, fieldID}
	rowCond, rowArgs, _, err := PredicateSQLQualified(d, entity, rowFilter, 5, "o")
	if err != nil {
		return nil, fmt.Errorf("stage summary %s row filter: %w", entity.Name, err)
	}
	if rowCond != "" {
		args = append(args, rowArgs...)
		rowCond = " AND " + rowCond
	}

	// Последнее событие объекта — по event_no, а не по времени; из него берём и
	// момент, и этап, чтобы отличить согласованную историю от разъехавшейся.
	q := fmt.Sprintf(`
		SELECT o.%s AS stage, h.to_stage AS last_stage, h.at AS since
		FROM %s o
		LEFT JOIN (
			SELECT s.record_id, s.to_stage, s.at
			FROM _stage_history s
			JOIN (
				SELECT record_id, MAX(event_no) AS event_no
				FROM _stage_history
				WHERE entity_name = %s AND field_id = %s
				GROUP BY record_id
			) m ON m.record_id = s.record_id AND m.event_no = s.event_no
			WHERE s.entity_name = %s AND s.field_id = %s
		) h ON h.record_id = o.id
		WHERE COALESCE(o.deletion_mark, %s) = %s%s`,
		col, table, d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		boolFalseLit(d), boolFalseLit(d), rowCond)
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	byStage := make(map[string]*StageBucket, len(s.Order))
	for rows.Next() {
		var stage, lastStage *string
		var since any
		if err := rows.Scan(&stage, &lastStage, &since); err != nil {
			rows.Close()
			return nil, err
		}
		name := ""
		if stage != nil {
			name = *stage
		}
		canon := s.Canonical(name)
		if canon == "" {
			// Значение вне объявленного порядка (данные старше блока stages либо
			// перечисление правили мимо него) — в отчёт по этапам не сводится,
			// такие объекты показывает отдельная строка «вне маршрута».
			continue
		}
		b := byStage[canon]
		if b == nil {
			b = &StageBucket{Stage: canon}
			byStage[canon] = b
		}
		b.Count++

		last := ""
		if lastStage != nil {
			last = *lastStage
		}
		at := parseAuditTime(since)
		if last == "" || !strings.EqualFold(last, name) || at.IsZero() || at.After(now) {
			// Истории нет, она ведёт в другой этап или её время в будущем
			// (часы отъехали) — длительность неизвестна.
			b.Unknown++
			continue
		}
		if b.Since.IsZero() || at.Before(b.Since) {
			b.Since = at
		}
		if days := s.Deadline(canon); days > 0 && at.Before(now.AddDate(0, 0, -days)) {
			b.Overdue++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]StageBucket, 0, len(s.Order))
	for _, stage := range s.Order {
		b := byStage[stage]
		if b == nil {
			b = &StageBucket{Stage: stage}
		}
		b.DeadlineDays = s.Deadline(stage)
		out = append(out, *b)
	}
	return out, nil
}

// StageOffRouteCount считает объекты, чьё текущее состояние не объявлено в
// `order`, — данные вне маршрута.
//
// Это и есть отчёт «нарушения в существующих данных», который нельзя получить
// от `onebase check`: тот работает с временной базой, где есть схема и нет ни
// одной строки. Показать такие объекты нужно до включения enforce: strict —
// иначе первая же их правка будет отвергнута, и выяснится это у пользователя.
func (db *DB) StageOffRouteCount(ctx context.Context, entity *metadata.Entity, rowFilter *Predicate) (int, error) {
	s := entity.Stages
	if s == nil || len(s.Order) == 0 {
		return 0, nil
	}
	f := entity.StageField()
	if f == nil {
		return 0, nil
	}
	d := db.dialect
	table := metadata.TableName(entity.Name)
	col := metadata.ColumnName(*f)
	args := make([]any, 0, len(s.Order))
	placeholders := make([]string, 0, len(s.Order))
	for i, stage := range s.Order {
		placeholders = append(placeholders, d.LowerLike(d.Placeholder(i+1)))
		args = append(args, stage)
	}
	rowCond, rowArgs, _, err := PredicateSQLQualified(d, entity, rowFilter, len(args)+1, "o")
	if err != nil {
		return 0, fmt.Errorf("stage off-route %s row filter: %w", entity.Name, err)
	}
	if rowCond != "" {
		args = append(args, rowArgs...)
		rowCond = " AND " + rowCond
	}
	// Пустое значение не считается нарушением: объект этап ещё не получил.
	q := fmt.Sprintf(`
		SELECT COUNT(*) FROM %s o
		WHERE COALESCE(o.%s, %s) = %s
		  AND COALESCE(o.%s, '') <> ''
		  AND %s NOT IN (%s)%s`,
		table, "deletion_mark", boolFalseLit(d), boolFalseLit(d),
		col, d.LowerLike("o."+col), strings.Join(placeholders, ", "), rowCond)
	var n int
	if err := db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
