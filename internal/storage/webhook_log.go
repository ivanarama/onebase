package storage

// Журнал исходящих веб-хуков _webhook_log (план 29): каждый вызов — запись
// с результатом (код/ошибка/длительность/попытки) для отладки интеграций.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

var webhookURLScrubbed sync.Map // map[*DB]struct{}, once per opened database

// WebhookLogEntry — одна запись журнала веб-хуков.
type WebhookLogEntry struct {
	ID         string
	Webhook    string
	Event      string
	Entity     string
	RecordID   string
	URL        string
	StatusCode int
	Error      string
	Duration   time.Duration // вход при записи
	DurationMs int           // выход при чтении
	Attempts   int
	At         time.Time
}

// EnsureWebhookLogSchema создаёт таблицу _webhook_log (идемпотентно).
func (db *DB) EnsureWebhookLogSchema(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _webhook_log (
			id %s PRIMARY KEY,
			webhook_name TEXT NOT NULL DEFAULT '',
			event TEXT NOT NULL DEFAULT '',
			entity TEXT NOT NULL DEFAULT '',
			record_id TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			fired_at %s NOT NULL DEFAULT %s
		)`, d.TypeUUID(), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("webhook_log: create: %w", err)
	}
	// Older versions stored the fully expanded endpoint, including Telegram bot
	// tokens and query credentials. URLs are not shown by the admin UI and the
	// webhook name is the stable diagnostic identifier, so scrub legacy values
	// and never persist endpoint URLs again.
	if _, ok := webhookURLScrubbed.Load(db); !ok {
		if _, err := db.Exec(ctx, `UPDATE _webhook_log
			SET url = '', error = CASE WHEN error <> '' THEN 'legacy error redacted' ELSE '' END
			WHERE url <> ''`); err != nil {
			return fmt.Errorf("webhook_log: scrub legacy urls: %w", err)
		}
		webhookURLScrubbed.Store(db, struct{}{})
	}
	return nil
}

// LogWebhook пишет запись журнала. Best-effort: ошибки записи не должны
// влиять на доставку хука, поэтому проглатываются (таблица создаётся лениво).
func (db *DB) LogWebhook(ctx context.Context, e WebhookLogEntry) {
	if err := db.EnsureWebhookLogSchema(ctx); err != nil {
		return
	}
	d := db.dialect
	q := fmt.Sprintf(`INSERT INTO _webhook_log
		(id, webhook_name, event, entity, record_id, url, status_code, error, duration_ms, attempts)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5),
		d.Placeholder(6), d.Placeholder(7), d.Placeholder(8), d.Placeholder(9), d.Placeholder(10))
	_, _ = db.Exec(ctx, q,
		uuid.NewString(), e.Webhook, e.Event, e.Entity, e.RecordID, "",
		e.StatusCode, e.Error, int(e.Duration.Milliseconds()), e.Attempts)
}

// ListWebhookLog возвращает последние записи журнала (новые первыми).
func (db *DB) ListWebhookLog(ctx context.Context, limit int) ([]WebhookLogEntry, error) {
	if err := db.EnsureWebhookLogSchema(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.Query(ctx, fmt.Sprintf(`SELECT id, webhook_name, event, entity, record_id,
		url, status_code, error, duration_ms, attempts, fired_at
		FROM _webhook_log ORDER BY fired_at DESC, id LIMIT %d`, limit))
	if err != nil {
		return nil, fmt.Errorf("webhook_log: list: %w", err)
	}
	defer rows.Close()
	var out []WebhookLogEntry
	for rows.Next() {
		var e WebhookLogEntry
		var at any
		if err := rows.Scan(&e.ID, &e.Webhook, &e.Event, &e.Entity, &e.RecordID,
			&e.URL, &e.StatusCode, &e.Error, &e.DurationMs, &e.Attempts, &at); err != nil {
			return nil, err
		}
		e.At = parseAuditTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
