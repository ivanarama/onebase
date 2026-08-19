package storage

// Очередь фоновых заданий (план 130, issue #848).
//
// Таблица `_job_queue` — durable-очередь задач: строка живёт от постановки до
// терминального статуса и несёт всё состояние исполнения (попытки, аренда,
// отмена, результат). Журнал прогонов `_scheduled_runs` тут не участвует
// намеренно: 360 задач на круг в режиме 24/7 переполнили бы его на порядок, а
// вся та же информация уже лежит в самой очереди (см. план 130, решение 2).
//
// Захват работы — один `UPDATE … WHERE id IN (SELECT … ) RETURNING`. На
// PostgreSQL подзапрос берёт `FOR UPDATE SKIP LOCKED`, поэтому конкурирующие
// исполнители расходятся по разным строкам вместо того, чтобы ждать друг друга.
// На SQLite писатель один по определению (`SetMaxOpenConns(1)`), и файловая
// блокировка сериализует захват сама.
//
// Время — эпоха в МИЛЛИСЕКУНДАХ (у приёмки — секунды): арифметика считается в
// Go и не зависит от разбора dialect-таймстампов, а миллисекунды нужны потому,
// что длительность задачи — рабочая величина этой подсистемы.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Статусы задачи. Терминальных три; отдельного «error» нет намеренно: упавшая
// задача возвращается в pending с отложенным available_at, а в dead уходит,
// только когда попытки исчерпаны. Иначе один статус означал бы то «будет
// повтор», то «конец», и монитор очереди не смог бы показать разницу.
const (
	JobTaskPending   = "pending"
	JobTaskRunning   = "running"
	JobTaskDone      = "done"
	JobTaskDead      = "dead"
	JobTaskCancelled = "cancelled"
)

// JobTask — задача очереди.
type JobTask struct {
	ID          uuid.UUID      `json:"id"`
	JobName     string         `json:"job_name"`
	Params      map[string]any `json:"params,omitempty"`
	Key         string         `json:"key,omitempty"`
	Status      string         `json:"status"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	AvailableAt int64          `json:"available_at"`
	CreatedAt   int64          `json:"created_at"`
	StartedAt   int64          `json:"started_at,omitempty"`
	FinishedAt  int64          `json:"finished_at,omitempty"`
	LeaseUntil  int64          `json:"lease_until,omitempty"`
	Worker      string         `json:"worker,omitempty"`
	Cancel      bool           `json:"cancel_requested,omitempty"`
	Error       string         `json:"error,omitempty"`
	Output      string         `json:"output,omitempty"`
}

// DurationMs — длительность последней попытки; 0, пока задача не завершилась.
func (t JobTask) DurationMs() int64 {
	if t.StartedAt == 0 || t.FinishedAt == 0 || t.FinishedAt < t.StartedAt {
		return 0
	}
	return t.FinishedAt - t.StartedAt
}

// Terminal сообщает, что задача больше не будет исполняться.
func (t JobTask) Terminal() bool {
	return t.Status == JobTaskDone || t.Status == JobTaskDead || t.Status == JobTaskCancelled
}

const jobTaskColumns = `id, job_name, params, key, status, attempts, max_attempts,
	available_at, created_at, started_at, finished_at, lease_until, worker,
	cancel_requested, error, output`

// EnsureJobQueueSchema создаёт таблицу очереди. Идемпотентно.
func (db *DB) EnsureJobQueueSchema(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _job_queue (
			id               %s PRIMARY KEY,
			job_name         TEXT    NOT NULL,
			params           TEXT    NOT NULL DEFAULT '',
			key              TEXT    NOT NULL DEFAULT '',
			status           TEXT    NOT NULL DEFAULT 'pending',
			attempts         INTEGER NOT NULL DEFAULT 0,
			max_attempts     INTEGER NOT NULL DEFAULT 1,
			available_at     BIGINT  NOT NULL DEFAULT 0,
			created_at       BIGINT  NOT NULL DEFAULT 0,
			started_at       BIGINT  NOT NULL DEFAULT 0,
			finished_at      BIGINT  NOT NULL DEFAULT 0,
			lease_until      BIGINT  NOT NULL DEFAULT 0,
			worker           TEXT    NOT NULL DEFAULT '',
			cancel_requested %s NOT NULL DEFAULT %s,
			error            TEXT    NOT NULL DEFAULT '',
			output           TEXT    NOT NULL DEFAULT ''
		)`, d.TypeUUID(), d.TypeBool(), boolFalseLit(d))
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("job queue: create _job_queue: %w", err)
	}
	// Драйвер SQLite исполняет по одному стейтменту за Exec, поэтому индексы
	// заводятся отдельными вызовами (как в _scheduled_runs).
	if _, err := db.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS idx_job_queue_ready ON _job_queue (status, available_at)`); err != nil {
		return fmt.Errorf("job queue: index ready: %w", err)
	}
	if _, err := db.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS idx_job_queue_lease ON _job_queue (status, lease_until)`); err != nil {
		return fmt.Errorf("job queue: index lease: %w", err)
	}
	// Ключ идемпотентности занят, только пока задача жива: частичный UNIQUE по
	// pending/running. Терминальные задачи ключ отпускают — иначе повторить ту
	// же работу завтра было бы нельзя.
	if _, err := db.Exec(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_job_queue_key ON _job_queue (key)
		 WHERE key <> '' AND status IN ('pending','running')`); err != nil {
		return fmt.Errorf("job queue: unique index key: %w", err)
	}
	return nil
}

// EnqueueJobTask ставит задачу в очередь. created=false означает, что задача с
// таким ключом идемпотентности уже жива и возвращена как есть — новой не
// создано.
//
// Гонку двух постановок с одним ключом разруливает частичный UNIQUE: проигравший
// получает ошибку вставки, после чего перечитывает живую задачу и отдаёт её
// вызывающему. Ошибку глотаем ТОЛЬКО если живая задача с этим ключом
// действительно нашлась — иначе это настоящий сбой, и молчать о нём нельзя.
//
// Исключение — постановка внутри транзакции вызывающего на PostgreSQL: там
// конфликт уникальности рушит всю транзакцию, и перечитать живую задачу уже
// нечем. Вызывающий получит ошибку — это правильно: его транзакция всё равно
// обречена, и делать вид, что задача поставлена, было бы враньём.
func (db *DB) EnqueueJobTask(ctx context.Context, task JobTask) (JobTask, bool, error) {
	if strings.TrimSpace(task.JobName) == "" {
		return JobTask{}, false, fmt.Errorf("job queue: имя задания обязательно")
	}
	if task.ID == uuid.Nil {
		task.ID = uuid.New()
	}
	if task.MaxAttempts < 1 {
		task.MaxAttempts = 1
	}
	if task.Status == "" {
		task.Status = JobTaskPending
	}
	task.Key = strings.TrimSpace(task.Key)

	if task.Key != "" {
		if live, err := db.liveJobTaskByKey(ctx, task.Key); err != nil {
			return JobTask{}, false, err
		} else if live != nil {
			return *live, false, nil
		}
	}

	paramsJSON := ""
	if len(task.Params) > 0 {
		raw, err := json.Marshal(task.Params)
		if err != nil {
			return JobTask{}, false, fmt.Errorf("job queue: параметры задачи не сериализуются: %w", err)
		}
		paramsJSON = string(raw)
	}

	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _job_queue
			(id, job_name, params, key, status, attempts, max_attempts, available_at, created_at)
		VALUES (%s, %s, %s, %s, %s, 0, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8))
	_, err := db.Exec(ctx, q, task.ID.String(), task.JobName, paramsJSON, task.Key,
		task.Status, task.MaxAttempts, task.AvailableAt, task.CreatedAt)
	if err != nil {
		if task.Key != "" {
			if live, lookupErr := db.liveJobTaskByKey(ctx, task.Key); lookupErr == nil && live != nil {
				return *live, false, nil
			}
		}
		return JobTask{}, false, fmt.Errorf("job queue: постановка задачи: %w", err)
	}
	return task, true, nil
}

func (db *DB) liveJobTaskByKey(ctx context.Context, key string) (*JobTask, error) {
	d := db.dialect
	q := fmt.Sprintf(`SELECT %s FROM _job_queue
		WHERE key = %s AND status IN ('pending','running') LIMIT 1`, jobTaskColumns, d.Placeholder(1))
	rows, err := db.Query(ctx, q, key)
	if err != nil {
		return nil, fmt.Errorf("job queue: поиск задачи по ключу: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	task, err := scanJobTask(rows)
	if err != nil {
		return nil, err
	}
	return &task, rows.Err()
}

// ClaimJobTasks захватывает до limit готовых задач и переводит их в running.
//
// Один стейтмент, а не «выбрать, потом обновить»: между SELECT и UPDATE другой
// исполнитель успел бы взять ту же строку. На PostgreSQL подзапрос идёт с
// FOR UPDATE SKIP LOCKED — конкуренты пропускают занятые строки вместо ожидания;
// на SQLite писатель один, и его же файловая блокировка даёт ту же гарантию.
func (db *DB) ClaimJobTasks(ctx context.Context, worker string, limit int, nowMs, leaseUntilMs int64) ([]JobTask, error) {
	if limit <= 0 {
		return nil, nil
	}
	d := db.dialect
	skipLocked := ""
	if d.Name() == "postgres" {
		skipLocked = " FOR UPDATE SKIP LOCKED"
	}
	q := fmt.Sprintf(`
		UPDATE _job_queue SET
			status = 'running',
			attempts = attempts + 1,
			started_at = %s,
			finished_at = 0,
			lease_until = %s,
			worker = %s,
			cancel_requested = %s
		WHERE id IN (
			SELECT id FROM _job_queue
			WHERE status = 'pending' AND available_at <= %s
			ORDER BY available_at, created_at
			LIMIT %s%s
		)
		RETURNING %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), boolFalseLit(d),
		d.Placeholder(4), d.Placeholder(5), skipLocked, jobTaskColumns)
	rows, err := db.Query(ctx, q, nowMs, leaseUntilMs, worker, nowMs, limit)
	if err != nil {
		return nil, fmt.Errorf("job queue: захват задач: %w", err)
	}
	defer rows.Close()
	var out []JobTask
	for rows.Next() {
		task, err := scanJobTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

// RenewJobLeases продлевает аренду взятых задач и возвращает те из них, для
// которых запрошена отмена. Обе вещи делает один запрос намеренно: heartbeat —
// единственный момент, когда исполнитель гарантированно смотрит в базу, и
// разносить их значило бы либо второй запрос на том же такте, либо задержку
// отмены на целый цикл.
func (db *DB) RenewJobLeases(ctx context.Context, ids []uuid.UUID, leaseUntilMs int64) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	d := db.dialect
	args := make([]any, 0, len(ids)+1)
	args = append(args, leaseUntilMs)
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = d.Placeholder(i + 2)
		args = append(args, id.String())
	}
	q := fmt.Sprintf(`UPDATE _job_queue SET lease_until = %s
		WHERE status = 'running' AND id IN (%s)
		RETURNING id, cancel_requested`,
		d.Placeholder(1), strings.Join(placeholders, ", "))
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("job queue: продление аренды: %w", err)
	}
	defer rows.Close()
	var cancelled []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var cancel bool
		if err := rows.Scan(&id, &cancel); err != nil {
			return nil, fmt.Errorf("job queue: продление аренды: %w", err)
		}
		if cancel {
			cancelled = append(cancelled, id)
		}
	}
	return cancelled, rows.Err()
}

// FinishJobTask переводит задачу в терминальный статус.
func (db *DB) FinishJobTask(ctx context.Context, id uuid.UUID, status, output, errText string, finishedAtMs int64) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _job_queue SET
			status = %s, finished_at = %s, output = %s, error = %s,
			lease_until = 0, cancel_requested = %s
		WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		boolFalseLit(d), d.Placeholder(5))
	tag, err := db.Exec(ctx, q, status, finishedAtMs, output, errText, id.String())
	if err != nil {
		return fmt.Errorf("job queue: завершение задачи: %w", err)
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("job queue: завершение задачи %s: строка не найдена", id)
	}
	return nil
}

// RetryJobTask возвращает упавшую задачу в очередь с отложенным стартом.
// Счётчик попыток не трогается: его нарастил захват.
func (db *DB) RetryJobTask(ctx context.Context, id uuid.UUID, availableAtMs int64, errText string) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _job_queue SET
			status = 'pending', available_at = %s, error = %s,
			lease_until = 0, worker = '', finished_at = 0, cancel_requested = %s
		WHERE id = %s`,
		d.Placeholder(1), d.Placeholder(2), boolFalseLit(d), d.Placeholder(3))
	tag, err := db.Exec(ctx, q, availableAtMs, errText, id.String())
	if err != nil {
		return fmt.Errorf("job queue: повтор задачи: %w", err)
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("job queue: повтор задачи %s: строка не найдена", id)
	}
	return nil
}

// ReclaimExpiredJobTasks изымает задачи, чья аренда истекла: процесс исполнителя
// умер жёстко (питание, OOM, kill -9) и терминального UPDATE не случилось.
// Такая задача либо возвращается в очередь, либо — если попытки исчерпаны —
// уходит в карантин. Без этого она навсегда осталась бы running: ровно тот
// класс дефекта, что и брошенные прогоны из #966.
func (db *DB) ReclaimExpiredJobTasks(ctx context.Context, nowMs int64, reason string) (requeued, dead int64, err error) {
	d := db.dialect
	deadQ := fmt.Sprintf(`UPDATE _job_queue SET
			status = 'dead', finished_at = %s, error = %s, lease_until = 0
		WHERE status = 'running' AND lease_until > 0 AND lease_until < %s
		  AND attempts >= max_attempts`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	deadTag, err := db.Exec(ctx, deadQ, nowMs, reason, nowMs)
	if err != nil {
		return 0, 0, fmt.Errorf("job queue: изъятие зависших (карантин): %w", err)
	}
	requeueQ := fmt.Sprintf(`UPDATE _job_queue SET
			status = 'pending', available_at = %s, error = %s,
			lease_until = 0, worker = ''
		WHERE status = 'running' AND lease_until > 0 AND lease_until < %s
		  AND attempts < max_attempts`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	requeueTag, err := db.Exec(ctx, requeueQ, nowMs, reason, nowMs)
	if err != nil {
		return 0, deadTag.RowsAffected, fmt.Errorf("job queue: изъятие зависших (возврат): %w", err)
	}
	return requeueTag.RowsAffected, deadTag.RowsAffected, nil
}

// RequestJobTaskCancel просит отменить задачу и сообщает, что получилось:
// "cancelled" — снята до начала исполнения, "cancelling" — исполнителю
// выставлена пометка (он увидит её на ближайшем heartbeat), "" — задача
// терминальна или неизвестна.
func (db *DB) RequestJobTaskCancel(ctx context.Context, id uuid.UUID, nowMs int64, reason string) (string, error) {
	d := db.dialect
	pendingQ := fmt.Sprintf(`UPDATE _job_queue SET
			status = 'cancelled', finished_at = %s, error = %s, lease_until = 0
		WHERE id = %s AND status = 'pending'`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	tag, err := db.Exec(ctx, pendingQ, nowMs, reason, id.String())
	if err != nil {
		return "", fmt.Errorf("job queue: отмена задачи: %w", err)
	}
	if tag.RowsAffected == 1 {
		return JobTaskCancelled, nil
	}
	runningQ := fmt.Sprintf(`UPDATE _job_queue SET cancel_requested = %s
		WHERE id = %s AND status = 'running'`, boolTrueLit(d), d.Placeholder(1))
	tag, err = db.Exec(ctx, runningQ, id.String())
	if err != nil {
		return "", fmt.Errorf("job queue: отмена исполняемой задачи: %w", err)
	}
	if tag.RowsAffected == 1 {
		return "cancelling", nil
	}
	return "", nil
}

// ReplayJobTask возвращает задачу из карантина в очередь со сброшенным счётчиком
// попыток. Ключ идемпотентности при этом снова занимается — если его уже держит
// живая задача, повтор честно отказывает вместо создания дубля работы.
func (db *DB) ReplayJobTask(ctx context.Context, id uuid.UUID, availableAtMs int64) error {
	d := db.dialect
	q := fmt.Sprintf(`UPDATE _job_queue SET
			status = 'pending', attempts = 0, available_at = %s,
			error = '', worker = '', started_at = 0, finished_at = 0,
			lease_until = 0, cancel_requested = %s
		WHERE id = %s AND status IN ('dead','cancelled')`,
		d.Placeholder(1), boolFalseLit(d), d.Placeholder(2))
	tag, err := db.Exec(ctx, q, availableAtMs, id.String())
	if err != nil {
		return fmt.Errorf("job queue: повтор задачи из карантина: %w", err)
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("job queue: повтор задачи %s: она не в карантине", id)
	}
	return nil
}

// JobTaskByID читает задачу по идентификатору. Задачи нет — `nil, nil`: для
// вызывающего это не ошибка, а «очередь такой не помнит» (подрезана ретенцией
// или id чужой) — та же семантика, что у ScheduledRunByID.
func (db *DB) JobTaskByID(ctx context.Context, id uuid.UUID) (*JobTask, error) {
	d := db.dialect
	q := fmt.Sprintf(`SELECT %s FROM _job_queue WHERE id = %s`, jobTaskColumns, d.Placeholder(1))
	rows, err := db.Query(ctx, q, id.String())
	if err != nil {
		return nil, fmt.Errorf("job queue: чтение задачи: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	task, err := scanJobTask(rows)
	if err != nil {
		return nil, err
	}
	return &task, rows.Err()
}

// JobTaskFilter — отбор для монитора очереди.
type JobTaskFilter struct {
	Status  string
	JobName string
	Limit   int
}

// ListJobTasks отдаёт задачи для монитора: сначала незавершённые (по времени
// готовности), затем свежие терминальные.
func (db *DB) ListJobTasks(ctx context.Context, filter JobTaskFilter) ([]JobTask, error) {
	d := db.dialect
	var where []string
	var args []any
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf("status = %s", d.Placeholder(len(args))))
	}
	if filter.JobName != "" {
		args = append(args, filter.JobName)
		where = append(where, fmt.Sprintf("job_name = %s", d.Placeholder(len(args))))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	q := fmt.Sprintf(`SELECT %s FROM _job_queue`, jobTaskColumns)
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(` ORDER BY CASE WHEN status IN ('pending','running') THEN 0 ELSE 1 END,
		created_at DESC LIMIT %s`, d.Placeholder(len(args)))
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("job queue: список задач: %w", err)
	}
	defer rows.Close()
	var out []JobTask
	for rows.Next() {
		task, err := scanJobTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

// JobQueueStats — глубина очереди по статусам (ключ = статус).
func (db *DB) JobQueueStats(ctx context.Context) (map[string]int64, error) {
	rows, err := db.Query(ctx, `SELECT status, COUNT(*) FROM _job_queue GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("job queue: глубина очереди: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("job queue: глубина очереди: %w", err)
		}
		out[status] = count
	}
	return out, rows.Err()
}

// PruneJobTasks удаляет завершённые задачи старше maxAgeMs. Карантин (dead) не
// трогается: он ждёт человека, и подрезать его по времени значило бы тихо
// терять невыполненную работу. Нулевой или отрицательный maxAgeMs отключает
// уборку.
func (db *DB) PruneJobTasks(ctx context.Context, nowMs, maxAgeMs int64) (int64, error) {
	if maxAgeMs <= 0 {
		return 0, nil
	}
	d := db.dialect
	q := fmt.Sprintf(`DELETE FROM _job_queue
		WHERE status IN ('done','cancelled') AND finished_at > 0 AND finished_at < %s`,
		d.Placeholder(1))
	tag, err := db.Exec(ctx, q, nowMs-maxAgeMs)
	if err != nil {
		return 0, fmt.Errorf("job queue: уборка очереди: %w", err)
	}
	return tag.RowsAffected, nil
}

func scanJobTask(rows Rows) (JobTask, error) {
	var t JobTask
	var params, key, worker, errText, output *string
	if err := rows.Scan(&t.ID, &t.JobName, &params, &key, &t.Status, &t.Attempts, &t.MaxAttempts,
		&t.AvailableAt, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.LeaseUntil, &worker,
		&t.Cancel, &errText, &output); err != nil {
		return JobTask{}, fmt.Errorf("job queue: разбор строки задачи: %w", err)
	}
	if params != nil && strings.TrimSpace(*params) != "" {
		if err := json.Unmarshal([]byte(*params), &t.Params); err != nil {
			return JobTask{}, fmt.Errorf("job queue: параметры задачи %s не читаются: %w", t.ID, err)
		}
	}
	if key != nil {
		t.Key = *key
	}
	if worker != nil {
		t.Worker = *worker
	}
	if errText != nil {
		t.Error = *errText
	}
	if output != nil {
		t.Output = *output
	}
	return t, nil
}
