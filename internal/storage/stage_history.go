package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Этапы объекта (план 121): гейт переходов и история «кто когда куда двигал».
//
// Почему это лежит в слое storage, а не выше. Путей записи сущности четыре
// (entityservice, DSL `Документы.X`, обмен данными, запись справочника), и
// сходятся они не в одну функцию, а в две: upsert (crud.go) и UpsertVersioned
// (optimistic_lock.go, все правки существующих объектов). Проверка, поставленная
// выше storage, была бы дефектом класса issue #611 — написана, зелёная в тестах,
// а на путях REST/DSL/импорта не вызывается. Поэтому обе точки записи зовут
// checkStageTransition и logStageTransition, и обе обязаны это делать: FTS и
// аудит в эти два места уже дописывали вдогонку, второй раз наступать не будем.
//
// История пишется БЕЗУСЛОВНО, в отличие от журнала регистрации: тот выключаем
// настройкой (logUpdate молча ничего не пишет при !s.Enabled), а «где застряло» —
// не аудит, а рабочий отчёт, который обязан работать всегда.

// Источники записи истории этапов.
const (
	// StageSourceExchange — переход приехал пакетом обмена (план 86). Гейт для
	// него не применяется: в чужой базе объект прошёл маршрут по её правилам, и
	// падать на приёмке значит рвать обмен из-за расхождения конфигураций.
	StageSourceExchange = "exchange"
)

type stageExternalCtxKey struct{}

// WithExternalStageWrite помечает контекст записи как внешний: гейт переходов
// пропускает любой переход, а история фиксирует источник. Ставится приёмкой
// пакета обмена.
func WithExternalStageWrite(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, stageExternalCtxKey{}, source)
}

// externalStageSource возвращает источник внешней записи («» — обычная запись).
func externalStageSource(ctx context.Context) string {
	s, _ := ctx.Value(stageExternalCtxKey{}).(string)
	return s
}

// StageChange — одна запись истории переходов.
type StageChange struct {
	ID         string
	EntityName string
	RecordID   string
	Field      string
	FromStage  string // пусто при создании объекта
	ToStage    string
	At         time.Time
	UserID     string
	UserLogin  string
	Source     string // «» — обычная запись, StageSourceExchange — обмен
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
			from_stage TEXT NOT NULL DEFAULT '',
			to_stage TEXT NOT NULL DEFAULT '',
			at %s NOT NULL DEFAULT %s,
			user_id %s,
			user_login TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT ''
		)`, d.TypeUUID(), d.TypeUUID(), d.TypeTimestamp(), d.CurrentTimestampTZ(), d.TypeUUID())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("stage history: create _stage_history: %w", err)
	}
	// История объекта: карточка читает переходы одного объекта по времени.
	_, _ = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_stage_history_record ON _stage_history (entity_name, record_id, at)`)
	// «Где застряло»: сколько объектов на этапе и с какого момента — без скана.
	_, _ = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_stage_history_stage ON _stage_history (entity_name, field, to_stage, at)`)
	return nil
}

// stageHistoryTimeLayout — формат момента перехода на SQLite.
//
// Отличается от общего sqliteTimeLayout ровно долями секунды, и они здесь
// обязательны: история одного объекта читается «сверху вниз», а несколько
// переходов подряд (создание и первый переход в одной обработке, маршрут из
// формы) укладываются в одну секунду. Без дробной части такие записи хранят
// одинаковую строку, и ORDER BY at выдаёт их в произвольном порядке — карточка
// показывала бы «Утверждена → Черновик» там, где было наоборот. Ширина дробной
// части фиксированная: колонка сравнивается как строка.
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

// LogStageChange пишет одну запись истории переходов.
//
// Время проставляется явно, а не умолчанием колонки: на SQLite datetime('now')
// даёт «2006-01-02 15:04:05», а связанный time.Time — «2006-01-02 15:04:05-07:00»
// (sqliteTimeLayout). Оба формата в одной колонке сравнивались бы как строки и
// сортировались бы вперемешку, поэтому формат в таблице ровно один.
func (db *DB) LogStageChange(ctx context.Context, ch *StageChange) error {
	d := db.dialect
	var userID any
	if ch.UserID != "" {
		if id, err := uuid.Parse(ch.UserID); err == nil {
			userID = id.String()
		}
	}
	var recordID any
	if ch.RecordID != "" {
		if id, err := uuid.Parse(ch.RecordID); err == nil {
			recordID = id.String()
		}
	}
	at := ch.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	q := fmt.Sprintf(`
		INSERT INTO _stage_history (id, entity_name, record_id, field, from_stage, to_stage, at, user_id, user_login, source)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10))
	if _, err := db.Exec(ctx, q, uuid.NewString(), ch.EntityName, recordID, ch.Field,
		ch.FromStage, ch.ToStage, db.stageTimeArg(at), userID, ch.UserLogin, ch.Source); err != nil {
		return fmt.Errorf("stage history: запись перехода %s.%s: %w", ch.EntityName, ch.Field, err)
	}
	return nil
}

// StageHistory возвращает переходы одного объекта, самые новые сверху.
func (db *DB) StageHistory(ctx context.Context, entityName string, id uuid.UUID) ([]StageChange, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, entity_name, record_id, field, from_stage, to_stage, at, user_id, user_login, source
		FROM _stage_history
		WHERE entity_name = %s AND record_id = %s
		ORDER BY at DESC, id DESC`, d.Placeholder(1), d.Placeholder(2))
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
		if err := rows.Scan(&rowID, &ch.EntityName, &recordID, &ch.Field,
			&ch.FromStage, &ch.ToStage, &at, &userID, &ch.UserLogin, &ch.Source); err != nil {
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
	// Unknown — из них таких, по которым истории нет (объект существовал до
	// объявления этапов). Для них время на этапе неизвестно, и считать его от
	// нуля нельзя — это выдало бы «только что пришёл» за факт.
	Unknown int
	// Since — момент самого давнего перехода на этот этап среди объектов с
	// историей. Нулевое время — истории нет ни у одного.
	Since time.Time
	// DeadlineDays — объявленный срок этапа (0 — срока нет).
	DeadlineDays int
	// Overdue — объекты с историей, попавшие на этап раньше, чем DeadlineDays
	// назад. При DeadlineDays == 0 всегда 0.
	Overdue int
}

// StageSummary считает отчёт «где застряло» по объявленным этапам сущности.
//
// Текущий этап берётся из самого объекта, а момент попадания на него — из
// последней записи истории. Объекты, помеченные на удаление, в отчёт не
// попадают: они уже выведены из работы, и «застрявшими» их показывать незачем.
//
// rowFilter — предикат построчного доступа (план 79). Он обязателен там, где
// политика ограничивает видимость: отчёт считает объекты, и без фильтра
// пользователь, которому видны только свои документы, узнавал бы количество
// чужих. nil означает «ограничений нет» — так его передаёт только код, где
// доступ уже проверен целиком.
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

	args := []any{entity.Name, f.Name}
	rowCond, rowArgs, _, err := PredicateSQLQualified(d, entity, rowFilter, 3, "o")
	if err != nil {
		return nil, fmt.Errorf("stage summary %s row filter: %w", entity.Name, err)
	}
	if rowCond != "" {
		args = append(args, rowArgs...)
		rowCond = " AND " + rowCond
	}

	// Последний переход по каждому объекту — подзапросом, чтобы отчёт не тащил
	// в память всю таблицу объектов.
	q := fmt.Sprintf(`
		SELECT o.%s AS stage, COUNT(*) AS cnt,
		       SUM(CASE WHEN h.at IS NULL THEN 1 ELSE 0 END) AS unknown_cnt,
		       MIN(h.at) AS oldest
		FROM %s o
		LEFT JOIN (
			SELECT record_id, MAX(at) AS at
			FROM _stage_history
			WHERE entity_name = %s AND field = %s
			GROUP BY record_id
		) h ON h.record_id = o.id
		WHERE COALESCE(o.deletion_mark, %s) = %s%s
		GROUP BY o.%s`,
		col, table, d.Placeholder(1), d.Placeholder(2), boolFalseLit(d), boolFalseLit(d), rowCond, col)
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	byStage := make(map[string]*StageBucket, len(s.Order))
	for rows.Next() {
		var stage *string
		var cnt, unknown int
		var oldest any
		if err := rows.Scan(&stage, &cnt, &unknown, &oldest); err != nil {
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
			// перечисление правили мимо него) — в отчёт по этапам не сводится.
			continue
		}
		b := byStage[canon]
		if b == nil {
			b = &StageBucket{Stage: canon}
			byStage[canon] = b
		}
		b.Count += cnt
		b.Unknown += unknown
		if t := parseAuditTime(oldest); !t.IsZero() && (b.Since.IsZero() || t.Before(b.Since)) {
			b.Since = t
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
		if b.DeadlineDays > 0 && b.Count > b.Unknown {
			overdue, err := db.stageOverdueCount(ctx, entity, *f, stage, b.DeadlineDays, rowFilter)
			if err != nil {
				return nil, err
			}
			b.Overdue = overdue
		}
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

// stageOverdueCount считает объекты, попавшие на этап раньше срока.
//
// Отсечка вычисляется в Go и уезжает параметром: арифметика дат в SQL у SQLite
// и PostgreSQL записывается по-разному, а сравнение с параметром одинаково на
// обоих (time.Time на PG биндится нативно, на SQLite приводится к
// sqliteTimeLayout — тому же формату, которым пишет LogStageChange).
func (db *DB) stageOverdueCount(ctx context.Context, entity *metadata.Entity, f metadata.Field, stage string, days int, rowFilter *Predicate) (int, error) {
	d := db.dialect
	table := metadata.TableName(entity.Name)
	col := metadata.ColumnName(f)
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	args := []any{entity.Name, f.Name, stage, db.stageTimeArg(cutoff)}
	rowCond, rowArgs, _, err := PredicateSQLQualified(d, entity, rowFilter, 5, "o")
	if err != nil {
		return 0, fmt.Errorf("stage overdue %s row filter: %w", entity.Name, err)
	}
	if rowCond != "" {
		args = append(args, rowArgs...)
		rowCond = " AND " + rowCond
	}
	q := fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s o
		JOIN (
			SELECT record_id, MAX(at) AS at
			FROM _stage_history
			WHERE entity_name = %s AND field = %s
			GROUP BY record_id
		) h ON h.record_id = o.id
		WHERE COALESCE(o.deletion_mark, %s) = %s
		  AND %s = %s
		  AND h.at < %s%s`,
		table, d.Placeholder(1), d.Placeholder(2), boolFalseLit(d), boolFalseLit(d),
		d.LowerLike("o."+col), d.LowerLike(d.Placeholder(3)), d.Placeholder(4), rowCond)
	var n int
	if err := db.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// stageTransition — вычисленный переход поля-этапа одной записи.
type stageTransition struct {
	Field string
	From  string
	To    string
}

// stageFieldValue достаёт значение поля-этапа из карты полей записи. Второй
// результат — был ли ключ в карте вообще: отсутствие ключа означает «поле в
// этой записи не участвует», и переходом это не считается.
func stageFieldValue(fields map[string]any, name string) (string, bool) {
	v, ok := fields[name]
	if !ok {
		v, ok = fields[strings.ToLower(name)]
	}
	if !ok {
		return "", false
	}
	return stageValueString(v), true
}

// stageValueString приводит значение этапа к строке. Перечисление хранится
// строкой, но через DSL и обмен сюда доезжают и другие представления.
func stageValueString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	if p, ok := v.(*string); ok {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// checkStageTransition — гейт переходов, общий для обеих точек записи.
//
// Возвращает вычисленный переход (nil — двигать нечего: сущность без этапов,
// поле не участвует в записи или значение не изменилось) и ошибку, если переход
// недопустим при enforce: strict. При enforce: warn нарушение только пишется в
// лог, а переход возвращается — история фиксирует то, что произошло на самом
// деле, а не то, что разрешено.
func (db *DB) checkStageTransition(ctx context.Context, entityName string, entity *metadata.Entity, oldRow, fields map[string]any) (*stageTransition, error) {
	if entity == nil || entity.Stages == nil {
		return nil, nil
	}
	s := entity.Stages
	f := entity.StageField()
	if f == nil {
		return nil, nil
	}
	to, present := stageFieldValue(fields, f.Name)
	if !present {
		return nil, nil
	}
	from := ""
	if oldRow != nil {
		from = stageValueString(oldRow[f.Name])
	}
	if strings.EqualFold(from, to) {
		return nil, nil
	}
	tr := &stageTransition{Field: f.Name, From: from, To: to}
	if src := externalStageSource(ctx); src != "" {
		// Внешняя запись (обмен): маршрут объект прошёл в базе-источнике.
		return tr, nil
	}
	if s.Allowed(from, to) {
		return tr, nil
	}
	if s.Strict() {
		if from == "" {
			return nil, i18nerr.Errorf("объект %s нельзя создать сразу на этапе «%s» — маршрут начинается с «%s»",
				entityName, to, s.Initial())
		}
		if to == "" {
			return nil, i18nerr.Errorf("у объекта %s нельзя очистить этап «%s» — этап меняется только переходом", entityName, from)
		}
		return nil, i18nerr.Errorf("переход «%s» → «%s» у объекта %s не разрешён", from, to, entityName)
	}
	storageLog().Warn("недопустимый переход этапа (enforce: warn — запись пропущена)",
		"сущность", entityName, "реквизит", f.Name, "было", from, "стало", to)
	return tr, nil
}

// logStageTransition пишет переход в историю. Вызывается обеими точками записи
// после успешной записи объекта — в той же транзакции, поэтому откат записи
// откатывает и историю.
func (db *DB) logStageTransition(ctx context.Context, entityName string, id uuid.UUID, tr *stageTransition) error {
	if tr == nil {
		return nil
	}
	u, _ := auditUserFromCtx(ctx)
	return db.LogStageChange(ctx, &StageChange{
		EntityName: entityName,
		RecordID:   id.String(),
		Field:      tr.Field,
		FromStage:  tr.From,
		ToStage:    tr.To,
		At:         time.Now().UTC(),
		UserID:     u.UserID,
		UserLogin:  u.UserLogin,
		Source:     externalStageSource(ctx),
	})
}
