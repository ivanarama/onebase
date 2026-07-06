package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const apiTokenPrefix = "ob_"

// APIToken describes an integration token without exposing the raw secret.
type APIToken struct {
	ID         string
	Name       string
	UserID     string
	UserLogin  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	Expired    bool
}

func (r *Repo) EnsureAPITokenSchema(ctx context.Context) error {
	d := r.db.Dialect()
	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _api_tokens (
			id %s PRIMARY KEY,
			name TEXT NOT NULL,
			token_hash TEXT UNIQUE NOT NULL,
			user_id %s NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
			created_at %s NOT NULL DEFAULT %s,
			last_used_at %s,
			expires_at %s,
			revoked_at %s
		)`, d.TypeUUID(), d.TypeUUID(), d.TypeTimestamp(), d.CurrentTimestampTZ(), d.TypeTimestamp(), d.TypeTimestamp(), d.TypeTimestamp())
	if _, err := r.db.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("auth: create _api_tokens: %w", err)
	}
	return nil
}

func (r *Repo) CreateAPIToken(ctx context.Context, name, userID string, expiresAt *time.Time) (*APIToken, string, error) {
	name = strings.TrimSpace(name)
	userID = strings.TrimSpace(userID)
	if name == "" {
		return nil, "", errors.New("auth: api token name is required")
	}
	if userID == "" {
		return nil, "", errors.New("auth: api token user is required")
	}
	raw, err := generateAPITokenSecret()
	if err != nil {
		return nil, "", err
	}
	id := uuid.New().String()
	d := r.db.Dialect()
	q := fmt.Sprintf(`INSERT INTO _api_tokens (id, name, token_hash, user_id, expires_at) VALUES (%s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5))
	var exp any
	if expiresAt != nil {
		exp = *expiresAt
	}
	if _, err := r.db.Exec(ctx, q, id, name, hashAPIToken(raw), userID, exp); err != nil {
		return nil, "", fmt.Errorf("auth: create api token: %w", err)
	}
	token := &APIToken{
		ID:        id,
		Name:      name,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}
	return token, raw, nil
}

func (r *Repo) LookupAPIToken(ctx context.Context, raw string) (*User, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("auth: invalid api token")
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`
		SELECT t.id, t.expires_at, t.revoked_at, u.id, u.login, u.full_name, u.is_admin, u.deny_passwd_change, u.ai_data_access, u.lang
		FROM _api_tokens t
		JOIN _users u ON u.id = t.user_id
		WHERE t.token_hash = %s
	`, d.Placeholder(1))
	var tokenID string
	var expiresRaw, revokedRaw any
	u := &User{}
	var isAdmin, denyPasswd, aiData any
	if err := r.db.QueryRow(ctx, q, hashAPIToken(raw)).Scan(&tokenID, &expiresRaw, &revokedRaw, &u.ID, &u.Login, &u.FullName, &isAdmin, &denyPasswd, &aiData, &u.Lang); err != nil {
		return nil, errors.New("auth: invalid api token")
	}
	expiresAt := scanLocalTimePtr(expiresRaw)
	revokedAt := scanTimePtr(revokedRaw)
	if revokedAt != nil || (expiresAt != nil && !expiresAt.After(time.Now())) {
		return nil, errors.New("auth: invalid api token")
	}
	u.IsAdmin = scanBool(isAdmin)
	u.DenyPasswdChange = scanBool(denyPasswd)
	u.AIDataAccess = scanBool(aiData)
	if roles, err := r.GetRolesForUser(ctx, u.ID); err == nil {
		u.Roles = roles
	}

	upd := fmt.Sprintf(`UPDATE _api_tokens SET last_used_at = %s WHERE id = %s`, d.Now(), d.Placeholder(1))
	_, _ = r.db.Exec(ctx, upd, tokenID)
	return u, nil
}

func (r *Repo) ListAPITokens(ctx context.Context) ([]*APIToken, error) {
	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.name, t.user_id, u.login, t.created_at, t.last_used_at, t.expires_at, t.revoked_at
		FROM _api_tokens t
		JOIN _users u ON u.id = t.user_id
		ORDER BY t.created_at DESC, t.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []*APIToken
	for rows.Next() {
		tok := &APIToken{}
		var createdRaw, lastRaw, expiresRaw, revokedRaw any
		if err := rows.Scan(&tok.ID, &tok.Name, &tok.UserID, &tok.UserLogin, &createdRaw, &lastRaw, &expiresRaw, &revokedRaw); err != nil {
			return nil, err
		}
		tok.CreatedAt = scanTime(createdRaw)
		tok.LastUsedAt = scanTimePtr(lastRaw)
		tok.ExpiresAt = scanLocalTimePtr(expiresRaw)
		tok.RevokedAt = scanTimePtr(revokedRaw)
		tok.Expired = tok.RevokedAt == nil && tok.ExpiresAt != nil && !tok.ExpiresAt.After(time.Now())
		tokens = append(tokens, tok)
	}
	return tokens, rows.Err()
}

func (r *Repo) RevokeAPIToken(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("auth: api token id is required")
	}
	d := r.db.Dialect()
	q := fmt.Sprintf(`UPDATE _api_tokens SET revoked_at = %s WHERE id = %s AND revoked_at IS NULL`, d.Now(), d.Placeholder(1))
	_, err := r.db.Exec(ctx, q, id)
	return err
}

func generateAPITokenSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return apiTokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func scanTimePtr(v any) *time.Time {
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return nil
	}
	t := scanTime(v)
	if t.IsZero() {
		return nil
	}
	return &t
}

func scanLocalTimePtr(v any) *time.Time {
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return &t
			}
		}
	}
	return scanTimePtr(v)
}
