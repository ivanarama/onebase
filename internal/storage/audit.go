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
	_, err := db.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _audit (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID,
			user_login TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			entity_kind TEXT NOT NULL DEFAULT '',
			entity_name TEXT NOT NULL DEFAULT '',
			record_id UUID,
			field TEXT NOT NULL DEFAULT '',
			old_value JSONB,
			new_value JSONB,
			ip TEXT NOT NULL DEFAULT '',
			at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("audit: create _audit: %w", err)
	}
	_, _ = db.pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_record ON _audit (entity_name, record_id)`)
	_, _ = db.pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_user ON _audit (user_id, at DESC)`)
	_, _ = db.pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_at ON _audit (at DESC)`)
	return nil
}

// Log writes a single audit entry.
func (db *DB) Log(ctx context.Context, e *AuditEntry) error {
	var userID *uuid.UUID
	if e.UserID != "" {
		if id, err := uuid.Parse(e.UserID); err == nil {
			userID = &id
		}
	}
	var recordID *uuid.UUID
	if e.RecordID != "" {
		if id, err := uuid.Parse(e.RecordID); err == nil {
			recordID = &id
		}
	}
	// Use NULL for empty/nil values
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

	err := db.execAudit(ctx, `
		INSERT INTO _audit (user_id, user_login, action, entity_kind, entity_name, record_id, field, old_value, new_value, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10)`,
		userID, e.UserLogin, e.Action, e.EntityKind, e.EntityName, recordID, e.Field,
		oldVal, newVal, e.IP)
	return err
}

// AuditByRecord returns all audit entries for a specific record, newest first.
func (db *DB) AuditByRecord(ctx context.Context, entityName string, recordID uuid.UUID) ([]*AuditEntry, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, user_id, user_login, action, entity_kind, entity_name, record_id, field, old_value, new_value, ip, at
		FROM _audit
		WHERE entity_name = $1 AND record_id = $2
		ORDER BY at DESC`, entityName, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// AuditSearch returns audit entries matching the filter, newest first.
func (db *DB) AuditSearch(ctx context.Context, filter AuditFilter, limit, offset int) ([]*AuditEntry, error) {
	var where []string
	var args []any
	idx := 1
	if filter.UserID != "" {
		where = append(where, fmt.Sprintf("user_id = $%d", idx))
		args = append(args, filter.UserID)
		idx++
	}
	if filter.UserLogin != "" {
		where = append(where, fmt.Sprintf("user_login ILIKE $%d", idx))
		args = append(args, "%"+filter.UserLogin+"%")
		idx++
	}
	if filter.Action != "" {
		where = append(where, fmt.Sprintf("action = $%d", idx))
		args = append(args, filter.Action)
		idx++
	}
	if filter.EntityName != "" {
		where = append(where, fmt.Sprintf("entity_name = $%d", idx))
		args = append(args, filter.EntityName)
		idx++
	}
	if filter.DateFrom != nil {
		where = append(where, fmt.Sprintf("at >= $%d", idx))
		args = append(args, *filter.DateFrom)
		idx++
	}
	if filter.DateTo != nil {
		where = append(where, fmt.Sprintf("at <= $%d", idx))
		args = append(args, *filter.DateTo)
		idx++
	}

	q := `SELECT id, user_id, user_login, action, entity_kind, entity_name, record_id, field, old_value, new_value, ip, at FROM _audit`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY at DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	rows, err := db.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

// AuditDiff compares old and new field values and returns changed fields.
func AuditDiff(old, new map[string]any, entity *metadata.Entity) []FieldChange {
	var changes []FieldChange
	for _, f := range entity.Fields {
		ov := old[f.Name]
		nv := new[f.Name]
		if fmt.Sprintf("%v", ov) != fmt.Sprintf("%v", nv) {
			changes = append(changes, FieldChange{Field: f.Name, Old: ov, New: nv})
		}
	}
	return changes
}

// FieldChange represents one changed field.
type FieldChange struct {
	Field string
	Old   any
	New   any
}

// logCreate writes a "create" audit entry from context.
func (db *DB) logCreate(ctx context.Context, kind, entityName string, id uuid.UUID) {
	u, ok := auditUserFromCtx(ctx)
	if !ok {
		return
	}
	_ = db.Log(ctx, &AuditEntry{
		UserID:     u.UserID,
		UserLogin:  u.UserLogin,
		Action:     "create",
		EntityKind: kind,
		EntityName: entityName,
		RecordID:   id.String(),
	})
}

// logUpdate writes "update" audit entries (one per changed field) from context.
func (db *DB) logUpdate(ctx context.Context, kind, entityName string, id uuid.UUID, changes []FieldChange) {
	u, ok := auditUserFromCtx(ctx)
	if !ok {
		return
	}
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
func (db *DB) LogAction(ctx context.Context, action, kind, entityName, recordID, userID, userLogin, ip string) {
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

type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

func scanAuditRows(rows pgxRows) ([]*AuditEntry, error) {
	defer rows.Close()
	var entries []*AuditEntry
	for rows.Next() {
		e := &AuditEntry{}
		var userID, recordID *uuid.UUID
		var auditID uuid.UUID
		var oldVal, newVal []byte
		if err := rows.Scan(
			&auditID, &userID, &e.UserLogin, &e.Action,
			&e.EntityKind, &e.EntityName, &recordID,
			&e.Field, &oldVal, &newVal, &e.IP, &e.At,
		); err != nil {
			return nil, err
		}
		e.ID = auditID.String()
		if userID != nil {
			e.UserID = userID.String()
		}
		if recordID != nil {
			e.RecordID = recordID.String()
		}
		if len(oldVal) > 0 && string(oldVal) != "null" {
			json.Unmarshal(oldVal, &e.OldValue)
		}
		if len(newVal) > 0 && string(newVal) != "null" {
			json.Unmarshal(newVal, &e.NewValue)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// execAudit runs a statement on the pool directly (audit inserts bypass tx).
func (db *DB) execAudit(ctx context.Context, sql string, args ...any) error {
	_, err := db.pool.Exec(ctx, sql, args...)
	return err
}
