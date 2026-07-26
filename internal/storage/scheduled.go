package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ScheduledRun struct {
	ID         uuid.UUID  `json:"id"`
	JobName    string     `json:"job_name"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Status     string     `json:"status"`
	Output     string     `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
	DurationMs int64      `json:"duration_ms,omitempty"`
}

func (db *DB) EnsureScheduledRunsTable(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _scheduled_runs (
			id           %s PRIMARY KEY,
			job_name     TEXT NOT NULL,
			started_at   %s NOT NULL,
			finished_at  %s,
			status       TEXT NOT NULL,
			output       TEXT,
			error        TEXT,
			duration_ms  INTEGER
		)`, d.TypeUUID(), d.TypeTimestamp(), d.TypeTimestamp())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("scheduled runs DDL: %w", err)
	}
	// SQLite drivers (modernc.org/sqlite) execute one statement per Exec, so
	// index creation needs to be split out anyway.
	if _, err := db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_scheduled_runs_job ON _scheduled_runs (job_name, started_at DESC)`); err != nil {
		return fmt.Errorf("scheduled runs DDL idx job: %w", err)
	}
	if _, err := db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_scheduled_runs_at ON _scheduled_runs (started_at DESC)`); err != nil {
		return fmt.Errorf("scheduled runs DDL idx at: %w", err)
	}
	return nil
}

func (db *DB) InsertScheduledRun(ctx context.Context, jobName string, startedAt time.Time) (uuid.UUID, error) {
	d := db.dialect
	id := uuid.New()
	q := fmt.Sprintf(
		`INSERT INTO _scheduled_runs (id, job_name, started_at, status) VALUES (%s, %s, %s, 'running')`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	if _, err := db.Exec(ctx, q, id.String(), jobName, startedAt); err != nil {
		return uuid.Nil, fmt.Errorf("insert scheduled run: %w", err)
	}
	return id, nil
}

func (db *DB) UpdateScheduledRun(ctx context.Context, id uuid.UUID, status, output, errText string, durationMs int64) error {
	d := db.dialect
	now := time.Now()
	q := fmt.Sprintf(
		`UPDATE _scheduled_runs SET finished_at=%s, status=%s, output=%s, error=%s, duration_ms=%s WHERE id=%s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6))
	tag, err := db.Exec(ctx, q, now, status, output, errText, durationMs, id.String())
	if err != nil {
		return err
	}
	if tag.RowsAffected != 1 {
		return fmt.Errorf("update scheduled run %s: run not found", id)
	}
	return nil
}

func (db *DB) ScheduledRuns(ctx context.Context, jobName string, limit int) ([]ScheduledRun, error) {
	d := db.dialect
	var query string
	var args []any
	if jobName != "" {
		query = fmt.Sprintf(`SELECT id, job_name, started_at, finished_at, status, output, error, duration_ms
			 FROM _scheduled_runs WHERE job_name=%s ORDER BY started_at DESC LIMIT %s`,
			d.Placeholder(1), d.Placeholder(2))
		args = []any{jobName, limit}
	} else {
		query = fmt.Sprintf(`SELECT id, job_name, started_at, finished_at, status, output, error, duration_ms
			 FROM _scheduled_runs ORDER BY started_at DESC LIMIT %s`, d.Placeholder(1))
		args = []any{limit}
	}
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("scheduled runs query: %w", err)
	}
	defer rows.Close()

	var result []ScheduledRun
	for rows.Next() {
		var r ScheduledRun
		var output, errText *string
		var startedAtRaw, finishedAtRaw any
		var durationMs *int64
		if err := rows.Scan(&r.ID, &r.JobName, &startedAtRaw, &finishedAtRaw, &r.Status, &output, &errText, &durationMs); err != nil {
			return nil, err
		}
		r.StartedAt = parseAuditTime(startedAtRaw)
		if output != nil {
			r.Output = *output
		}
		if errText != nil {
			r.Error = *errText
		}
		if finishedAtRaw != nil {
			finishedAt := parseAuditTime(finishedAtRaw)
			if !finishedAt.IsZero() {
				r.FinishedAt = &finishedAt
			}
		}
		if durationMs != nil {
			r.DurationMs = *durationMs
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
