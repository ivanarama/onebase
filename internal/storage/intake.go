package storage

// Хранилище входного шлюза (план 90, пробел G6):
//   _intake_log — стор идемпотентности. UNIQUE(intake, scope, key) + атомарный
//     INSERT … ON CONFLICT DO NOTHING даёт «insert-if-new» без гонки TOCTOU:
//     из N конкурентных дублей ровно один получает inserted=true (тот же приём,
//     что _numerators и _exchange_*).
//   _intake_dlq — карантин: сырое тело + причина + попытки, для ручного replay.
//
// Время хранится как BIGINT-эпоха (секунды), как changed_at обмена, — чтобы TTL
// считался арифметикой в Go и не зависел от разбора dialect-таймстампов.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Статусы строки идемпотентности _intake_log.
const (
	IntakeStatusReceived    = "received"    // строка занята, обработчик ещё не завершён
	IntakeStatusProcessed   = "processed"   // бизнес-объект создан, результат сохранён
	IntakeStatusQuarantined = "quarantined" // обработчик упал, событие в карантине
)

// Состояния записи карантина _intake_dlq.
const (
	DLQStateOpen      = "open"      // ждёт разбора/повтора
	DLQStateReplayed  = "replayed"  // успешно переигран
	DLQStateDiscarded = "discarded" // отброшен вручную
)

// IntakeLogRow — строка стора идемпотентности.
type IntakeLogRow struct {
	Intake         string
	Scope          string
	Key            string
	PayloadHash    string
	Status         string
	ResultRef      string
	BusinessResult string
	CorrelationID  string
	ReceivedAt     int64
	TTLExpiresAt   int64 // 0 — бессрочно
}

// IntakeDLQEntry — запись карантина.
type IntakeDLQEntry struct {
	ID            string
	Intake        string
	Key           string
	Scope         string
	Payload       string // сырое тело конверта (JSON)
	Reason        string
	Error         string
	Attempts      int
	CorrelationID string
	QuarantinedAt int64
	ReplayState   string
}

// EnsureIntakeSchema создаёт служебные таблицы приёмки. Идемпотентно.
func (db *DB) EnsureIntakeSchema(ctx context.Context) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _intake_log (
			intake          TEXT   NOT NULL,
			scope           TEXT   NOT NULL DEFAULT '',
			key             TEXT   NOT NULL,
			payload_hash    TEXT   NOT NULL DEFAULT '',
			status          TEXT   NOT NULL DEFAULT 'received',
			result_ref      TEXT   NOT NULL DEFAULT '',
			business_result TEXT   NOT NULL DEFAULT '',
			correlation_id  TEXT   NOT NULL DEFAULT '',
			received_at     BIGINT NOT NULL DEFAULT 0,
			ttl_expires_at  BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (intake, scope, key)
		)`); err != nil {
		return fmt.Errorf("intake: create _intake_log: %w", err)
	}
	if _, err := db.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS idx_intake_log_ttl ON _intake_log (ttl_expires_at)`); err != nil {
		return fmt.Errorf("intake: index _intake_log ttl: %w", err)
	}
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _intake_dlq (
			id             %s PRIMARY KEY,
			intake         TEXT   NOT NULL,
			key            TEXT   NOT NULL DEFAULT '',
			scope          TEXT   NOT NULL DEFAULT '',
			payload        TEXT   NOT NULL DEFAULT '',
			reason         TEXT   NOT NULL DEFAULT '',
			error          TEXT   NOT NULL DEFAULT '',
			attempts       INTEGER NOT NULL DEFAULT 0,
			correlation_id TEXT   NOT NULL DEFAULT '',
			quarantined_at BIGINT NOT NULL DEFAULT 0,
			replay_state   TEXT   NOT NULL DEFAULT 'open'
		)`, d.TypeUUID())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("intake: create _intake_dlq: %w", err)
	}
	if _, err := db.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS idx_intake_dlq_open ON _intake_dlq (intake, replay_state)`); err != nil {
		return fmt.Errorf("intake: index _intake_dlq open: %w", err)
	}
	return nil
}

// InsertIntakeLogIfNew атомарно вставляет строку идемпотентности, если её ещё
// нет. inserted=true — строка новая (вызывающий владеет обработкой); false —
// ключ уже занят (дубль или конфликт), актуальную строку читай GetIntakeLog.
//
// Гарантия опирается на UNIQUE(intake, scope, key) + условный UPSERT:
// под конкуренцией уникальный индекс сериализует вставки, ровно одна побеждает.
// Истёкшая processed-строка атомарно заменяется новой, поэтому настроенное окно
// идемпотентности действительно освобождает ключ без отдельной TOCTOU-очистки.
// received/quarantined не вытесняются: незавершённое событие нельзя потерять
// только потому, что истёк срок дедупликации.
func (db *DB) InsertIntakeLogIfNew(ctx context.Context, row IntakeLogRow) (bool, error) {
	if row.Status == "" {
		row.Status = IntakeStatusReceived
	}
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _intake_log
			(intake, scope, key, payload_hash, status, result_ref, business_result, correlation_id, received_at, ttl_expires_at)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
		ON CONFLICT (intake, scope, key) DO UPDATE SET
			payload_hash = excluded.payload_hash,
			status = excluded.status,
			result_ref = excluded.result_ref,
			business_result = excluded.business_result,
			correlation_id = excluded.correlation_id,
			received_at = excluded.received_at,
			ttl_expires_at = excluded.ttl_expires_at
		WHERE _intake_log.ttl_expires_at > 0
		  AND _intake_log.ttl_expires_at <= excluded.received_at
		  AND _intake_log.status = 'processed'`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10))
	tag, err := db.Exec(ctx, q,
		row.Intake, row.Scope, row.Key, row.PayloadHash, row.Status,
		row.ResultRef, row.BusinessResult, row.CorrelationID, row.ReceivedAt, row.TTLExpiresAt)
	if err != nil {
		return false, fmt.Errorf("intake: insert-if-new: %w", err)
	}
	return tag.RowsAffected == 1, nil
}

// GetIntakeLog читает строку идемпотентности. found=false — строки нет.
func (db *DB) GetIntakeLog(ctx context.Context, intake, scope, key string) (IntakeLogRow, bool, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT intake, scope, key, payload_hash, status, result_ref, business_result, correlation_id, received_at, ttl_expires_at
		FROM _intake_log WHERE intake = %s AND scope = %s AND key = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	var r IntakeLogRow
	err := db.QueryRow(ctx, q, intake, scope, key).Scan(
		&r.Intake, &r.Scope, &r.Key, &r.PayloadHash, &r.Status,
		&r.ResultRef, &r.BusinessResult, &r.CorrelationID, &r.ReceivedAt, &r.TTLExpiresAt)
	if IsNotFound(err) {
		return IntakeLogRow{}, false, nil
	}
	if err != nil {
		return IntakeLogRow{}, false, fmt.Errorf("intake: get log: %w", err)
	}
	return r, true, nil
}

// ClaimIntakeLogForReplay атомарно переводит подходящую quarantined-строку в
// received и тем самым блокирует её до конца транзакции replay. Это не даёт
// параллельной доставке или второму replay запустить обработчик одновременно.
func (db *DB) ClaimIntakeLogForReplay(ctx context.Context, intake, scope, key, payloadHash string) (bool, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		UPDATE _intake_log SET status = %s
		WHERE intake = %s AND scope = %s AND key = %s
		  AND payload_hash = %s AND status = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3),
		d.Placeholder(4), d.Placeholder(5), d.Placeholder(6))
	tag, err := db.Exec(ctx, q,
		IntakeStatusReceived, intake, scope, key, payloadHash, IntakeStatusQuarantined)
	if err != nil {
		return false, fmt.Errorf("intake: claim log replay: %w", err)
	}
	return tag.RowsAffected == 1, nil
}

// SetIntakeLogProcessed помечает строку обработанной и сохраняет результат.
// Вызывать в той же транзакции, что и создание бизнес-объекта (storage.WithTx).
func (db *DB) SetIntakeLogProcessed(ctx context.Context, intake, scope, key, resultRef, businessResult string) error {
	d := db.dialect
	q := fmt.Sprintf(`
		UPDATE _intake_log SET status = %s, result_ref = %s, business_result = %s
		WHERE intake = %s AND scope = %s AND key = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3),
		d.Placeholder(4), d.Placeholder(5), d.Placeholder(6))
	tag, err := db.Exec(ctx, q, IntakeStatusProcessed, resultRef, businessResult, intake, scope, key)
	if err != nil {
		return fmt.Errorf("intake: mark processed: %w", err)
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("intake: mark processed: строка идемпотентности не найдена")
	}
	return nil
}

// DeleteIntakeLog удаляет строку идемпотентности. Нужен, когда причина сбоя не
// входит в dlq.on: событие отклоняется без карантина, а осиротевшую received-
// строку надо снять, чтобы корректный повтор обработался заново.
func (db *DB) DeleteIntakeLog(ctx context.Context, intake, scope, key string) error {
	d := db.dialect
	q := fmt.Sprintf(`DELETE FROM _intake_log WHERE intake = %s AND scope = %s AND key = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	if _, err := db.Exec(ctx, q, intake, scope, key); err != nil {
		return fmt.Errorf("intake: delete log: %w", err)
	}
	return nil
}

// SetIntakeLogStatus меняет статус строки идемпотентности.
func (db *DB) SetIntakeLogStatus(ctx context.Context, intake, scope, key, status string) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _intake_log SET status = %s WHERE intake = %s AND scope = %s AND key = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4))
	if _, err := db.Exec(ctx, q, status, intake, scope, key); err != nil {
		return fmt.Errorf("intake: set status: %w", err)
	}
	return nil
}

// IntakeStats — сводка по шлюзу для монитора/сверки (CC-INT-007): счётчики строк
// идемпотентности по статусам + число открытых записей карантина.
type IntakeStats struct {
	Received    int
	Processed   int
	Quarantined int
	OpenDLQ     int
}

// IntakeLogStats считает строки _intake_log шлюза по статусам и открытые записи
// карантина. Это «своя сторона» сверки: оператор сопоставляет числа с источником.
func (db *DB) IntakeLogStats(ctx context.Context, intake string) (IntakeStats, error) {
	d := db.dialect
	var st IntakeStats
	rows, err := db.Query(ctx,
		fmt.Sprintf(`SELECT status, COUNT(*) FROM _intake_log WHERE intake = %s GROUP BY status`, d.Placeholder(1)),
		intake)
	if err != nil {
		return st, fmt.Errorf("intake: stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return st, err
		}
		switch status {
		case IntakeStatusReceived:
			st.Received = n
		case IntakeStatusProcessed:
			st.Processed = n
		case IntakeStatusQuarantined:
			st.Quarantined = n
		}
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	if err := db.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM _intake_dlq WHERE intake = %s AND replay_state = %s`,
			d.Placeholder(1), d.Placeholder(2)), intake, DLQStateOpen).Scan(&st.OpenDLQ); err != nil {
		return st, fmt.Errorf("intake: stats dlq: %w", err)
	}
	return st, nil
}

// InsertIntakeDLQ добавляет запись карантина. Возвращает её id.
func (db *DB) InsertIntakeDLQ(ctx context.Context, e IntakeDLQEntry) (string, error) {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.ReplayState == "" {
		e.ReplayState = DLQStateOpen
	}
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _intake_dlq
			(id, intake, key, scope, payload, reason, error, attempts, correlation_id, quarantined_at, replay_state)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6),
		d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10), d.Placeholder(11))
	if _, err := db.Exec(ctx, q,
		e.ID, e.Intake, e.Key, e.Scope, e.Payload, e.Reason, e.Error,
		e.Attempts, e.CorrelationID, e.QuarantinedAt, e.ReplayState); err != nil {
		return "", fmt.Errorf("intake: insert dlq: %w", err)
	}
	return e.ID, nil
}

// ListIntakeDLQ возвращает записи карантина шлюза (новые первыми). onlyOpen —
// только неразобранные (replay_state = open).
func (db *DB) ListIntakeDLQ(ctx context.Context, intake string, onlyOpen bool, limit int) ([]IntakeDLQEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	d := db.dialect
	where := fmt.Sprintf("intake = %s", d.Placeholder(1))
	args := []any{intake}
	if onlyOpen {
		where += fmt.Sprintf(" AND replay_state = %s", d.Placeholder(2))
		args = append(args, DLQStateOpen)
	}
	q := fmt.Sprintf(`
		SELECT id, intake, key, scope, payload, reason, error, attempts, correlation_id, quarantined_at, replay_state
		FROM _intake_dlq WHERE %s ORDER BY quarantined_at DESC, id LIMIT %d`, where, limit)
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("intake: list dlq: %w", err)
	}
	defer rows.Close()
	var out []IntakeDLQEntry
	for rows.Next() {
		var e IntakeDLQEntry
		if err := rows.Scan(&e.ID, &e.Intake, &e.Key, &e.Scope, &e.Payload, &e.Reason,
			&e.Error, &e.Attempts, &e.CorrelationID, &e.QuarantinedAt, &e.ReplayState); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetIntakeDLQ возвращает запись карантина по её уникальному ID в пределах
// шлюза. В отличие от поиска по ключу однозначен при scope и повторных
// конфликтах одного event_id.
func (db *DB) GetIntakeDLQ(ctx context.Context, intake, id string) (IntakeDLQEntry, bool, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, intake, key, scope, payload, reason, error, attempts, correlation_id, quarantined_at, replay_state
		FROM _intake_dlq WHERE intake = %s AND id = %s`,
		d.Placeholder(1), d.Placeholder(2))
	var e IntakeDLQEntry
	err := db.QueryRow(ctx, q, intake, id).Scan(
		&e.ID, &e.Intake, &e.Key, &e.Scope, &e.Payload, &e.Reason,
		&e.Error, &e.Attempts, &e.CorrelationID, &e.QuarantinedAt, &e.ReplayState)
	if IsNotFound(err) {
		return IntakeDLQEntry{}, false, nil
	}
	if err != nil {
		return IntakeDLQEntry{}, false, fmt.Errorf("intake: get dlq: %w", err)
	}
	return e, true, nil
}

// FindOpenIntakeDLQ возвращает самую свежую открытую запись карантина ключа с
// заданной причиной. Нужна повтору отправителя (dlq.retry_by_sender): он
// приносит тот же ключ, а не ID записи.
func (db *DB) FindOpenIntakeDLQ(ctx context.Context, intake, scope, key, reason string) (IntakeDLQEntry, bool, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, intake, key, scope, payload, reason, error, attempts, correlation_id, quarantined_at, replay_state
		FROM _intake_dlq
		WHERE intake = %s AND scope = %s AND key = %s AND reason = %s AND replay_state = %s
		ORDER BY quarantined_at DESC, id DESC LIMIT 1`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5))
	var e IntakeDLQEntry
	err := db.QueryRow(ctx, q, intake, scope, key, reason, DLQStateOpen).Scan(
		&e.ID, &e.Intake, &e.Key, &e.Scope, &e.Payload, &e.Reason,
		&e.Error, &e.Attempts, &e.CorrelationID, &e.QuarantinedAt, &e.ReplayState)
	if IsNotFound(err) {
		return IntakeDLQEntry{}, false, nil
	}
	if err != nil {
		return IntakeDLQEntry{}, false, fmt.Errorf("intake: find open dlq: %w", err)
	}
	return e, true, nil
}

// MarkIntakeDLQReplayedIfOpen атомарно захватывает открытую запись для replay.
// Вызывается внутри той же транзакции, что обработчик и отметка processed:
// при любом сбое переход откатывается вместе с бизнес-записями.
func (db *DB) MarkIntakeDLQReplayedIfOpen(ctx context.Context, id string) (bool, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		UPDATE _intake_dlq SET replay_state = %s
		WHERE id = %s AND replay_state = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	tag, err := db.Exec(ctx, q, DLQStateReplayed, id, DLQStateOpen)
	if err != nil {
		return false, fmt.Errorf("intake: claim dlq replay: %w", err)
	}
	return tag.RowsAffected == 1, nil
}

// SetIntakeDLQState меняет состояние записи карантина (open|replayed|discarded).
func (db *DB) SetIntakeDLQState(ctx context.Context, id, state string) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _intake_dlq SET replay_state = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, state, id); err != nil {
		return fmt.Errorf("intake: set dlq state: %w", err)
	}
	return nil
}

// BumpIntakeDLQAttempts увеличивает счётчик попыток и записывает последнюю ошибку.
func (db *DB) BumpIntakeDLQAttempts(ctx context.Context, id, lastErr string) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _intake_dlq SET attempts = attempts + 1, error = %s WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2))
	if _, err := db.Exec(ctx, q, lastErr, id); err != nil {
		return fmt.Errorf("intake: bump dlq attempts: %w", err)
	}
	return nil
}
