package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

type auditCtxKey struct{}

// AuditUserInfo is stored in context by the auth middleware.
type AuditUserInfo struct {
	UserID    string
	UserLogin string
}

// WithAuditUser stores audit user info in the context (called by auth middleware).
func WithAuditUser(ctx context.Context, userID, login string) context.Context {
	return context.WithValue(ctx, auditCtxKey{}, AuditUserInfo{UserID: userID, UserLogin: login})
}

// auditUserFromCtx extracts audit user info from context.
func auditUserFromCtx(ctx context.Context) (AuditUserInfo, bool) {
	v, ok := ctx.Value(auditCtxKey{}).(AuditUserInfo)
	return v, ok
}

// AuditUserLogin возвращает логин пользователя запроса из контекста аудита
// («» если аутентификация не настроена). Используется веб-хуками (план 29)
// для подстановки {{user}} в тело уведомления.
func AuditUserLogin(ctx context.Context) string {
	u, _ := auditUserFromCtx(ctx)
	return u.UserLogin
}

// AuditEntry is one audit log record.
type AuditEntry struct {
	ID         string
	UserID     string
	UserLogin  string
	Action     string
	EntityKind string
	EntityName string
	RecordID   string
	Field      string
	OldValue   any
	NewValue   any
	IP         string
	// Reason — обоснование действия (план 88): заполняется при action=disclose
	// (раскрытие замаскированного поля ПДн). Для остальных действий пустое.
	Reason     string
	DecisionID string
	At         time.Time
}

// AuditFilter for querying audit log.
type AuditFilter struct {
	UserID     string
	UserLogin  string
	Action     string
	EntityName string
	DateFrom   *time.Time
	DateTo     *time.Time
}

// EnsureAuditSchema creates _audit table if it does not exist.
func (db *DB) EnsureAuditSchema(ctx context.Context) error {
	d := db.dialect
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _audit (
			id %s PRIMARY KEY,
			user_id %s,
			user_login TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			entity_kind TEXT NOT NULL DEFAULT '',
			entity_name TEXT NOT NULL DEFAULT '',
			record_id %s,
			field TEXT NOT NULL DEFAULT '',
			old_value %s,
			new_value %s,
			ip TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			decision_id TEXT NOT NULL DEFAULT '',
			at %s NOT NULL DEFAULT %s
		)`, d.TypeUUID(), d.TypeUUID(), d.TypeUUID(), d.TypeJSON(), d.TypeJSON(),
		d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("audit: create _audit: %w", err)
	}
	// reason — колонка добавлена планом 88; для баз, созданных раньше, дотягиваем
	// её ALTER-ом (existing rows → NULL, читаем через COALESCE).
	_ = db.AddColumnIfMissing(ctx, "_audit", "reason", "TEXT")
	// decision_id связывает привилегированное lifecycle-событие с решением,
	// которым оно санкционировано (CC-RULE-005).
	_ = db.AddColumnIfMissing(ctx, "_audit", "decision_id", "TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_record ON _audit (entity_name, record_id)`)
	_, _ = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_user ON _audit (user_id, at DESC)`)
	_, _ = db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_at ON _audit (at DESC)`)
	// Настройки журнала регистрации живут рядом — создаём заодно.
	return db.EnsureSettingsSchema(ctx)
}

// Log writes a single audit entry.
func (db *DB) Log(ctx context.Context, e *AuditEntry) error {
	d := db.dialect
	var userID any
	if e.UserID != "" {
		if id, err := uuid.Parse(e.UserID); err == nil {
			userID = id.String()
		}
	}
	var recordID any
	if e.RecordID != "" {
		if id, err := uuid.Parse(e.RecordID); err == nil {
			recordID = id.String()
		}
	}
	oldVal := "null"
	newVal := "null"
	if e.OldValue != nil {
		if b, err := json.Marshal(e.OldValue); err == nil {
			oldVal = string(b)
		}
	}
	if e.NewValue != nil {
		if b, err := json.Marshal(e.NewValue); err == nil {
			newVal = string(b)
		}
	}

	q := fmt.Sprintf(`
		INSERT INTO _audit (id, user_id, user_login, action, entity_kind, entity_name, record_id, field, old_value, new_value, ip, reason, decision_id)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s%s, %s%s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
		d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8),
		d.Placeholder(9), d.JSONCast(), d.Placeholder(10), d.JSONCast(), d.Placeholder(11), d.Placeholder(12),
		d.Placeholder(13))
	err := db.execAudit(ctx, q,
		uuid.NewString(),
		userID, e.UserLogin, e.Action, e.EntityKind, e.EntityName, recordID, e.Field,
		oldVal, newVal, e.IP, e.Reason, e.DecisionID)
	return err
}

// AuditByRecord returns all audit entries for a specific record, newest first.
func (db *DB) AuditByRecord(ctx context.Context, entityName string, recordID uuid.UUID) ([]*AuditEntry, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT id, user_id, user_login, action, entity_kind, entity_name, record_id, field, old_value, new_value, ip, COALESCE(reason,''), COALESCE(decision_id,''), at
		FROM _audit
		WHERE entity_name = %s AND record_id = %s
		ORDER BY at DESC`, d.Placeholder(1), d.Placeholder(2))
	rows, err := db.Query(ctx, q, entityName, recordID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// AuditSearch returns audit entries matching the filter, newest first.
func (db *DB) AuditSearch(ctx context.Context, filter AuditFilter, limit, offset int) ([]*AuditEntry, error) {
	d := db.dialect
	var where []string
	var args []any
	idx := 1
	if filter.UserID != "" {
		where = append(where, fmt.Sprintf("user_id = %s", d.Placeholder(idx)))
		args = append(args, filter.UserID)
		idx++
	}
	if filter.UserLogin != "" {
		where = append(where, fmt.Sprintf("%s %s %s", d.LowerLike("user_login"), "LIKE", d.LowerLike(d.Placeholder(idx))))
		args = append(args, "%"+filter.UserLogin+"%")
		idx++
	}
	if filter.Action != "" {
		where = append(where, fmt.Sprintf("action = %s", d.Placeholder(idx)))
		args = append(args, filter.Action)
		idx++
	}
	if filter.EntityName != "" {
		where = append(where, fmt.Sprintf("entity_name = %s", d.Placeholder(idx)))
		args = append(args, filter.EntityName)
		idx++
	}
	if filter.DateFrom != nil {
		where = append(where, fmt.Sprintf("at >= %s", d.Placeholder(idx)))
		args = append(args, *filter.DateFrom)
		idx++
	}
	if filter.DateTo != nil {
		where = append(where, fmt.Sprintf("at <= %s", d.Placeholder(idx)))
		args = append(args, *filter.DateTo)
		idx++
	}

	q := `SELECT id, user_id, user_login, action, entity_kind, entity_name, record_id, field, old_value, new_value, ip, COALESCE(reason,''), COALESCE(decision_id,''), at FROM _audit`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY at DESC LIMIT %s OFFSET %s", d.Placeholder(idx), d.Placeholder(idx+1))
	args = append(args, limit, offset)

	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// AuditDiff compares old and new field values and returns changed fields.
// Values implementing refUUIDer (e.g. *interpreter.Ref) are normalised to
// their UUID string so the audit log stores plain UUIDs, not struct dumps.
func AuditDiff(old, new map[string]any, entity *metadata.Entity) []FieldChange {
	var changes []FieldChange
	for _, f := range entity.Fields {
		ov := auditVal(old[f.Name])
		nv := auditVal(new[f.Name])
		if fmt.Sprintf("%v", ov) != fmt.Sprintf("%v", nv) {
			changes = append(changes, FieldChange{Field: f.Name, Old: ov, New: nv})
		}
	}
	return changes
}

// refUUIDer is satisfied by *interpreter.Ref. Kept here to avoid
// importing interpreter (which would create a circular dependency).
type refUUIDer interface {
	GetRefUUID() string
}

// auditVal normalises a value for audit storage. Ref objects become their
// UUID string; everything else is passed through unchanged.
func auditVal(v any) any {
	if v == nil {
		return nil
	}
	if r, ok := v.(refUUIDer); ok {
		return r.GetRefUUID()
	}
	return v
}

// FieldChange represents one changed field.
type FieldChange struct {
	Field string
	Old   any
	New   any
}

// logCreate writes a "create" audit entry. Пишется, если журнал включён и
// тип события «создание» разрешён в настройках; пользователь берётся из
// контекста, а при его отсутствии (фоновые операции, анонимный режим без
// пользователей) запись делается с пустым логином.
func (db *DB) logCreate(ctx context.Context, kind, entityName string, id uuid.UUID) {
	if s := db.GetAuditSettings(ctx); !s.Enabled || !s.Create {
		return
	}
	u, _ := auditUserFromCtx(ctx)
	_ = db.Log(ctx, &AuditEntry{
		UserID:     u.UserID,
		UserLogin:  u.UserLogin,
		Action:     "create",
		EntityKind: kind,
		EntityName: entityName,
		RecordID:   id.String(),
	})
}

// logUpdate writes "update" audit entries (one per changed field).
func (db *DB) logUpdate(ctx context.Context, kind, entityName string, id uuid.UUID, changes []FieldChange) {
	if s := db.GetAuditSettings(ctx); !s.Enabled || !s.Update {
		return
	}
	u, _ := auditUserFromCtx(ctx)
	for _, ch := range changes {
		_ = db.Log(ctx, &AuditEntry{
			UserID:     u.UserID,
			UserLogin:  u.UserLogin,
			Action:     "update",
			EntityKind: kind,
			EntityName: entityName,
			RecordID:   id.String(),
			Field:      ch.Field,
			OldValue:   ch.Old,
			NewValue:   ch.New,
		})
	}
}

// LogAction writes an arbitrary audit action (post, unpost, delete, login, logout).
// Уважает настройки журнала: вход/выход пишется только при включённом
// audit.login, остальные действия — при включённом журнале.
func (db *DB) LogAction(ctx context.Context, action, kind, entityName, recordID, userID, userLogin, ip string) {
	s := db.GetAuditSettings(ctx)
	if !s.Enabled {
		return
	}
	if (action == "login" || action == "logout") && !s.Login {
		return
	}
	_ = db.Log(ctx, &AuditEntry{
		UserID:     userID,
		UserLogin:  userLogin,
		Action:     action,
		EntityKind: kind,
		EntityName: entityName,
		RecordID:   recordID,
		IP:         ip,
	})
}

// LogDisclose records a field disclosure (план 88, CC-SEC-004). Пишется
// БЕЗУСЛОВНО, независимо от настроек журнала: раскрытие замаскированного ПДн —
// привилегированное действие, которое должно оставаться прослеживаемым даже у
// администратора. Само раскрытое значение НЕ сохраняется (CC-DATA-111) —
// new_value фиксировано «(раскрыто)», а обоснование пишется в reason.
func (db *DB) LogDisclose(ctx context.Context, kind, entityName, recordID, field, reason string) error {
	u, _ := auditUserFromCtx(ctx)
	return db.Log(ctx, &AuditEntry{
		UserID:     u.UserID,
		UserLogin:  u.UserLogin,
		Action:     "disclose",
		EntityKind: kind,
		EntityName: entityName,
		RecordID:   recordID,
		Field:      field,
		NewValue:   "(раскрыто)",
		Reason:     reason,
	})
}

// LogDecisionAction records an auditable privileged lifecycle decision
// (publish/rollback). It is unconditional, like LogDisclose: disabling the
// general registration journal must not erase governance events.
func (db *DB) LogDecisionAction(ctx context.Context, action, kind, entityName, recordID, decisionID, author string) error {
	u, _ := auditUserFromCtx(ctx)
	if author != "" && author != u.UserLogin {
		u.UserID = ""
		u.UserLogin = author
	}
	return db.Log(ctx, &AuditEntry{
		UserID:     u.UserID,
		UserLogin:  u.UserLogin,
		Action:     action,
		EntityKind: kind,
		EntityName: entityName,
		RecordID:   recordID,
		DecisionID: decisionID,
	})
}

type auditRowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

func scanAuditRows(rows auditRowsScanner) ([]*AuditEntry, error) {
	defer rows.Close()
	var entries []*AuditEntry
	for rows.Next() {
		e := &AuditEntry{}
		var userID, recordID *uuid.UUID
		var auditID uuid.UUID
		var oldVal, newVal []byte
		// at сканируем в any: PostgreSQL (TIMESTAMPTZ) отдаёт time.Time,
		// SQLite хранит timestamp как TEXT (ISO 8601) и драйвер не
		// конвертирует строку → time.Time автоматически. Раньше прямое
		// сканирование в time.Time падало/давало нулевое время на SQLite.
		var at any
		if err := rows.Scan(
			&auditID, &userID, &e.UserLogin, &e.Action,
			&e.EntityKind, &e.EntityName, &recordID,
			&e.Field, &oldVal, &newVal, &e.IP, &e.Reason, &e.DecisionID, &at,
		); err != nil {
			return nil, err
		}
		e.At = parseAuditTime(at)
		e.ID = auditID.String()
		if userID != nil {
			e.UserID = userID.String()
		}
		if recordID != nil {
			e.RecordID = recordID.String()
		}
		// Неразобранное значение оставляло бы в журнале регистрации пустое
		// «было/стало» — запись, по которой изменение выглядит как отсутствие
		// изменения. Для журнала аудита это хуже шумной строки.
		if len(oldVal) > 0 && string(oldVal) != "null" {
			if err := json.Unmarshal(oldVal, &e.OldValue); err != nil {
				storageLog().Warn("значение «было» в журнале регистрации не разобрано", "err", err)
			}
		}
		if len(newVal) > 0 && string(newVal) != "null" {
			if err := json.Unmarshal(newVal, &e.NewValue); err != nil {
				storageLog().Warn("значение «стало» в журнале регистрации не разобрано", "err", err)
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ParseDBTime нормализует значение timestamp-колонки к time.Time независимо
// от диалекта (PostgreSQL → time.Time, SQLite → строка/[]byte). Экспортируется
// для пакетов вне storage, читающих свои таблицы напрямую (например extform).
func ParseDBTime(v any) time.Time { return parseAuditTime(v) }

// parseAuditTime нормализует значение колонки at к time.Time независимо
// от диалекта: PostgreSQL отдаёт time.Time, SQLite — строку (или []byte)
// в формате ISO 8601 / "2006-01-02 15:04:05" (как datetime('now')).
func parseAuditTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		return parseTimeStr(t)
	case []byte:
		return parseTimeStr(string(t))
	}
	return time.Time{}
}

func parseTimeStr(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		// pg text-формат timestamptz: смещение как ±NN (часто) или ±NN:NN
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05", // SQLite datetime('now')
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// execAudit follows the transaction in ctx, so rolled-back business changes do
// not leave committed audit records.
// execAudit пишет строку журнала. Запись необязательная: вызывающие
// отбрасывают её ошибку. Чтобы это было правдой и на PostgreSQL, операция идёт
// в отдельном savepoint — иначе её сбой отравлял бы транзакцию записи объекта
// (issue #826).
func (db *DB) execAudit(ctx context.Context, sql string, args ...any) error {
	return db.bestEffort(ctx, func(ctx context.Context) error {
		_, err := db.Exec(ctx, sql, args...)
		return err
	})
}

// ActiveUserInfo represents a user seen recently in the audit log.
type ActiveUserInfo struct {
	Login    string
	LastSeen time.Time
}

// ActiveUsersFromAudit returns distinct user_logins from _audit in the
// last 24 hours, ordered by most recent activity. Used when no auth
// subsystem is configured (file-based databases).
func (db *DB) ActiveUsersFromAudit(ctx context.Context) ([]ActiveUserInfo, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		SELECT COALESCE(NULLIF(user_login,''),'admin') AS login, MAX(at) AS last_seen
		FROM _audit
		WHERE at >= %s
		GROUP BY login
		ORDER BY last_seen DESC`,
		d.Placeholder(1))
	cutoff := time.Now().Add(-24 * time.Hour)
	// SQLite stores at as TEXT (ISO 8601), direct comparison with
	// time.Time does not work - format as string.
	cutoffArg := any(cutoff)
	if db.IsSQLite() {
		cutoffArg = cutoff.Format("2006-01-02 15:04:05")
	}
	rows, err := db.Query(ctx, q, cutoffArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ActiveUserInfo
	for rows.Next() {
		var login string
		var lastSeen any
		if err := rows.Scan(&login, &lastSeen); err != nil {
			continue
		}
		result = append(result, ActiveUserInfo{
			Login:    login,
			LastSeen: parseAuditTime(lastSeen),
		})
	}
	return result, nil
}
