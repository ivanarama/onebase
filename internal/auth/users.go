package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/ivantit66/onebase/internal/storage"
)

type User struct {
	ID               string
	Login            string
	FullName         string
	IsAdmin          bool
	DenyPasswdChange bool
	ShowInList       bool   // appears in reference pickers when true
	AIDataAccess     bool   // can use AI chat data tools without being admin
	Lang             string // preferred UI language ("" = use base default)
	CreatedAt        time.Time
	Roles            []*Role // loaded by middleware after session lookup
}

type Repo struct {
	db *storage.DB
}

// NewRepo wires the auth repository to the storage layer. Internally Exec/
// Query/QueryRow are routed to PostgreSQL or SQLite via the DB abstraction.
func NewRepo(db *storage.DB) *Repo {
	return &Repo{db: db}
}

func (r *Repo) EnsureSchema(ctx context.Context) error {
	d := r.db.Dialect()
	usersDDL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _users (
			id %s PRIMARY KEY,
			login TEXT UNIQUE NOT NULL,
			password_hash %s NOT NULL,
			full_name TEXT NOT NULL DEFAULT '',
			is_admin %s NOT NULL DEFAULT %s,
			created_at %s NOT NULL DEFAULT %s
		)`, d.TypeUUID(), d.TypeBytes(), d.TypeBool(), boolFalseFor(d), d.TypeTimestamp(), d.CurrentTimestampTZ())
	if _, err := r.db.Exec(ctx, usersDDL); err != nil {
		return fmt.Errorf("auth: create _users: %w", err)
	}
	sessionsDDL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _sessions (
			token TEXT PRIMARY KEY,
			user_id %s NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
			expires_at %s NOT NULL
		)`, d.TypeUUID(), d.TypeTimestamp())
	if _, err := r.db.Exec(ctx, sessionsDDL); err != nil {
		return fmt.Errorf("auth: create _sessions: %w", err)
	}
	if err := r.EnsureRolesSchema(ctx); err != nil {
		return err
	}
	if err := r.EnsureAPITokenSchema(ctx); err != nil {
		return err
	}
	// idempotent migrations: add columns if missing
	r.db.Exec(ctx, fmt.Sprintf(`ALTER TABLE _users ADD COLUMN deny_passwd_change %s NOT NULL DEFAULT %s`, d.TypeBool(), boolFalseFor(d)))
	r.db.Exec(ctx, fmt.Sprintf(`ALTER TABLE _users ADD COLUMN show_in_list %s NOT NULL DEFAULT %s`, d.TypeBool(), boolFalseFor(d)))
	r.db.Exec(ctx, `ALTER TABLE _users ADD COLUMN lang TEXT NOT NULL DEFAULT ''`)
	r.db.Exec(ctx, fmt.Sprintf(`ALTER TABLE _users ADD COLUMN ai_data_access %s NOT NULL DEFAULT %s`, d.TypeBool(), boolFalseFor(d)))
	return nil
}

// boolFalseFor returns "FALSE" for PG and "0" for SQLite, used in DEFAULT clauses.
func boolFalseFor(d storage.Dialect) string {
	if d.Name() == "sqlite" {
		return "0"
	}
	return "FALSE"
}

func (r *Repo) HasUsers(ctx context.Context) (bool, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM _users`).Scan(&count)
	return count > 0, err
}

func (r *Repo) List(ctx context.Context) ([]*User, error) {
	return r.listWhere(ctx, "")
}

// ListForSelection returns only users with show_in_list=true, for reference pickers.
func (r *Repo) ListForSelection(ctx context.Context) ([]*User, error) {
	return r.listWhere(ctx, "WHERE show_in_list")
}

func (r *Repo) listWhere(ctx context.Context, where string) ([]*User, error) {
	q := `SELECT id, login, full_name, is_admin, deny_passwd_change, show_in_list, ai_data_access, lang, created_at FROM _users ` + where + ` ORDER BY login`
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		var isAdmin, denyPasswd, showInList, aiData, createdAt any
		if err := rows.Scan(&u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &showInList, &aiData, &u.Lang, &createdAt); err != nil {
			return nil, err
		}
		u.IsAdmin = scanBool(isAdmin)
		u.DenyPasswdChange = scanBool(denyPasswd)
		u.ShowInList = scanBool(showInList)
		u.AIDataAccess = scanBool(aiData)
		u.CreatedAt = scanTime(createdAt)
		users = append(users, u)
	}
	return users, nil
}

// GetByID returns a single user by ID.
func (r *Repo) GetByID(ctx context.Context, userID string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var isAdmin, denyPasswd, showInList, aiData, createdAt any
	q := fmt.Sprintf(`SELECT id, login, full_name, is_admin, deny_passwd_change, show_in_list, ai_data_access, lang, created_at FROM _users WHERE id = %s`, d.Placeholder(1))
	if err := r.db.QueryRow(ctx, q, userID).Scan(&u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &showInList, &aiData, &u.Lang, &createdAt); err != nil {
		return nil, err
	}
	u.IsAdmin = scanBool(isAdmin)
	u.DenyPasswdChange = scanBool(denyPasswd)
	u.ShowInList = scanBool(showInList)
	u.AIDataAccess = scanBool(aiData)
	u.CreatedAt = scanTime(createdAt)
	return u, nil
}

// Update saves editable fields on a user.
func (r *Repo) Update(ctx context.Context, userID, fullName string, isAdmin, denyPasswdChange, showInList, aiDataAccess bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET full_name=%s, is_admin=%s, deny_passwd_change=%s, show_in_list=%s, ai_data_access=%s WHERE id=%s`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6))
	_, err := r.db.Exec(ctx, q, fullName, isAdmin, denyPasswdChange, showInList, aiDataAccess, userID)
	return err
}

// SetShowInList toggles the show_in_list flag for a user.
func (r *Repo) SetShowInList(ctx context.Context, userID string, show bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET show_in_list = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, show, userID)
	return err
}

// SetAIDataAccess toggles the ai_data_access flag for a user.
func (r *Repo) SetAIDataAccess(ctx context.Context, userID string, allow bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET ai_data_access = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, allow, userID)
	return err
}

// SetUserLang sets the preferred UI language for a user.
func (r *Repo) SetUserLang(ctx context.Context, userID, lang string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET lang = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, lang, userID)
	return err
}

func scanBool(v any) bool {
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

func scanTime(v any) time.Time {
	if t := storage.ParseDBTime(v); !t.IsZero() {
		return t
	}
	if t, ok := v.(time.Time); ok {
		return t
	}
	if s, ok := v.(string); ok {
		for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05 -0700 MST", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func (r *Repo) Create(ctx context.Context, login, password, fullName string, isAdmin bool) (*User, error) {
	d := r.db.Dialect()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := uuid.New().String()
	q := fmt.Sprintf(`INSERT INTO _users (id, login, password_hash, full_name, is_admin) VALUES (%s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5))
	_, err = r.db.Exec(ctx, q, id, login, hash, fullName, isAdmin)
	if err != nil {
		return nil, fmt.Errorf("auth: create user: %w", err)
	}
	return &User{ID: id, Login: login, FullName: fullName, IsAdmin: isAdmin}, nil
}

func (r *Repo) Delete(ctx context.Context, id string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _users WHERE id = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, id)
	return err
}

func (r *Repo) Authenticate(ctx context.Context, login, password string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var hash []byte
	q := fmt.Sprintf(`SELECT id, login, password_hash, full_name, is_admin FROM _users WHERE login = %s`, d.Placeholder(1))
	err := r.db.QueryRow(ctx, q, login).Scan(&u.ID, &u.Login, &hash, &u.FullName, &u.IsAdmin)
	if err != nil {
		return nil, fmt.Errorf("auth: user not found")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return nil, fmt.Errorf("auth: wrong password")
	}
	return u, nil
}

func (r *Repo) CreateSession(ctx context.Context, userID string) (string, error) {
	d := r.db.Dialect()
	// удаляем все старые сессии пользователя перед созданием новой
	delQ := fmt.Sprintf(`DELETE FROM _sessions WHERE user_id = %s`, d.Placeholder(1))
	r.db.Exec(ctx, delQ, userID)

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	expires := time.Now().Add(24 * time.Hour)
	q := fmt.Sprintf(`INSERT INTO _sessions (token, user_id, expires_at) VALUES (%s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3))
	_, err := r.db.Exec(ctx, q, token, userID, expires)
	return token, err
}

func (r *Repo) LookupSession(ctx context.Context, token string) (*User, error) {
	d := r.db.Dialect()
	u := &User{}
	var aiData any
	q := fmt.Sprintf(`
		SELECT u.id, u.login, u.full_name, u.is_admin, u.deny_passwd_change, u.ai_data_access, u.lang
		FROM _sessions s JOIN _users u ON u.id = s.user_id
		WHERE s.token = %s AND s.expires_at > %s
	`, d.Placeholder(1), d.Now())
	err := r.db.QueryRow(ctx, q, token).Scan(&u.ID, &u.Login, &u.FullName, &u.IsAdmin, &u.DenyPasswdChange, &aiData, &u.Lang)
	if err != nil {
		return nil, err
	}
	u.AIDataAccess = scanBool(aiData)
	return u, nil
}

func (r *Repo) DeleteSession(ctx context.Context, token string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE token = %s`, d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, token)
	return err
}

// SessionInfo describes one active session.
type SessionInfo struct {
	Login     string
	FullName  string
	IsAdmin   bool
	ExpiresAt time.Time
}

// ActiveSessions returns all non-expired sessions with user info.
func (r *Repo) ActiveSessions(ctx context.Context) ([]*SessionInfo, error) {
	d := r.db.Dialect()
	q := fmt.Sprintf(`
		SELECT u.login, u.full_name, u.is_admin, s.expires_at
		FROM _sessions s
		JOIN _users u ON u.id = s.user_id
		WHERE s.expires_at > %s
		ORDER BY u.login, s.expires_at DESC
	`, d.Now())
	rows, err := r.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*SessionInfo
	for rows.Next() {
		si := &SessionInfo{}
		var expiresRaw any
		if err := rows.Scan(&si.Login, &si.FullName, &si.IsAdmin, &expiresRaw); err != nil {
			return nil, err
		}
		si.ExpiresAt = parseSessionTime(expiresRaw)
		sessions = append(sessions, si)
	}
	return sessions, rows.Err()
}

// ActiveSessionCount returns the number of non-expired sessions.
func (r *Repo) ActiveSessionCount(ctx context.Context) (int, error) {
	d := r.db.Dialect()
	q := fmt.Sprintf(`SELECT COUNT(*) FROM _sessions WHERE expires_at > %s`, d.Now())
	var count int
	if err := r.db.QueryRow(ctx, q).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// parseSessionTime normalises an expires_at column value to time.Time.
// PostgreSQL returns time.Time natively; SQLite stores it as TEXT which
// the driver may return as string or []byte in Go's time format.
func parseSessionTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		// Try standard formats first, then Go's own String() format.
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05 -0700 MST",
			"2006-01-02 15:04:05",
		} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed
			}
		}
	case []byte:
		return parseSessionTime(string(t))
	}
	return time.Time{}
}

// SetDenyPasswdChange sets the deny_passwd_change flag for a user.
func (r *Repo) SetDenyPasswdChange(ctx context.Context, userID string, deny bool) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET deny_passwd_change = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err := r.db.Exec(ctx, q, deny, userID)
	return err
}

// UpdatePassword sets a new bcrypt-hashed password for the given user ID.
func (r *Repo) UpdatePassword(ctx context.Context, userID, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _users SET password_hash = %s WHERE id = %s`, d.Placeholder(1), d.Placeholder(2))
	_, err = r.db.Exec(ctx, q, hash, userID)
	return err
}

// KickUser deletes all sessions for the given login (forces re-login).
func (r *Repo) KickUser(ctx context.Context, login string) error {
	d := r.db.Dialect()
	q := fmt.Sprintf(`DELETE FROM _sessions WHERE user_id = (SELECT id FROM _users WHERE login = %s)`,
		d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, login)
	return err
}
