package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/navigation"
)

const (
	NavigationAdminPrefix = "ui.navigation.admin."
	NavigationUserPrefix  = "ui.navigation.user."
)

// NavigationSettingsScope is chosen by the server. UI handlers must obtain the
// user login from the authenticated context, and separately enforce admin RBAC.
type NavigationSettingsScope struct {
	Layer   navigation.Layer
	Login   string
	Context string
}

// NavigationSettings preserves raw JSON for revisions and backup/restore. Even
// corrupt JSON has a revision, so an explicit CAS reset can safely remove it.
type NavigationSettings struct {
	Raw      string
	Revision string
	Exists   bool
}

func (s NavigationSettingsScope) key() (string, error) {
	valid := func(value string) bool {
		return value != "" && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
	}
	if !valid(s.Context) {
		return "", fmt.Errorf("navigation settings: invalid context")
	}
	switch s.Layer {
	case navigation.AdminLayer:
		if s.Login != "" {
			return "", fmt.Errorf("navigation settings: unexpected admin login")
		}
		return fmt.Sprintf("%s%d:%s", NavigationAdminPrefix, len(s.Context), s.Context), nil
	case navigation.UserLayer:
		if !valid(s.Login) {
			return "", fmt.Errorf("navigation settings: invalid login")
		}
		return fmt.Sprintf("%s%d:%s.%d:%s", NavigationUserPrefix, len(s.Login), s.Login, len(s.Context), s.Context), nil
	default:
		return "", fmt.Errorf("navigation settings: invalid layer")
	}
}

// IsNavigationSettingsKey recognizes only the two collision-safe portable key
// families, including byte lengths for Unicode logins and subsystem names.
func IsNavigationSettingsKey(key string) bool {
	part := func(input string) (string, bool) {
		colon := strings.IndexByte(input, ':')
		if colon <= 0 {
			return "", false
		}
		length, err := strconv.Atoi(input[:colon])
		if err != nil || length <= 0 || strconv.Itoa(length) != input[:colon] || length > len(input)-colon-1 {
			return "", false
		}
		value := input[colon+1 : colon+1+length]
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return "", false
		}
		return input[colon+1+length:], true
	}
	if tail, ok := strings.CutPrefix(key, NavigationAdminPrefix); ok {
		rest, valid := part(tail)
		return valid && rest == ""
	}
	if tail, ok := strings.CutPrefix(key, NavigationUserPrefix); ok {
		rest, valid := part(tail)
		if !valid || !strings.HasPrefix(rest, ".") {
			return false
		}
		rest, valid = part(rest[1:])
		return valid && rest == ""
	}
	return false
}

func navigationSettingsValue(raw string, exists bool) NavigationSettings {
	result := NavigationSettings{Raw: raw, Exists: exists}
	if exists {
		hash := sha256.Sum256([]byte(raw))
		result.Revision = "sha256:" + hex.EncodeToString(hash[:])
	}
	return result
}

func (db *DB) GetNavigationSettings(ctx context.Context, scope NavigationSettingsScope) (NavigationSettings, error) {
	key, err := scope.key()
	if err != nil {
		return NavigationSettings{}, err
	}
	exists, err := db.TableExists(ctx, "_settings")
	if err != nil || !exists {
		return NavigationSettings{}, err
	}
	var raw string
	if err := db.QueryRow(ctx, "SELECT value FROM _settings WHERE key = "+db.dialect.Placeholder(1), key).Scan(&raw); err != nil {
		if IsNotFound(err) {
			return NavigationSettings{}, nil
		}
		return NavigationSettings{}, fmt.Errorf("navigation settings: read: %w", err)
	}
	return navigationSettingsValue(raw, true), nil
}

// SaveNavigationSettings validates the entire delta and merged tree before SQL.
// The final conditional write compares raw JSON atomically on both dialects.
// A stale revision returns ErrVersionConflict; empty operations delete the key.
func (db *DB) SaveNavigationSettings(ctx context.Context, scope NavigationSettingsScope, base navigation.Tree, delta navigation.Delta, expectedRevision string) (NavigationSettings, error) {
	if base.Context != scope.Context {
		return NavigationSettings{}, fmt.Errorf("navigation settings: context mismatch")
	}
	raw, err := navigation.EncodeDelta(delta, scope.Layer)
	if err != nil {
		return NavigationSettings{}, err
	}
	if _, _, err := navigation.ApplyDelta(base, delta, scope.Layer); err != nil {
		return NavigationSettings{}, err
	}
	if len(delta.Ops) == 0 {
		return db.DeleteNavigationSettings(ctx, scope, expectedRevision)
	}
	return db.writeNavigationSettings(ctx, scope, string(raw), false, expectedRevision)
}

// SaveDesiredNavigation derives server-side changes from the authoritative
// previous layer. It never adopts client-supplied target metadata or SQL keys.
func (db *DB) SaveDesiredNavigation(ctx context.Context, scope NavigationSettingsScope, base, desired navigation.Tree, expectedRevision string) (NavigationSettings, error) {
	delta, err := navigation.Diff(base, desired, scope.Layer)
	if err != nil {
		return NavigationSettings{}, err
	}
	return db.SaveNavigationSettings(ctx, scope, base, delta, expectedRevision)
}

func (db *DB) DeleteNavigationSettings(ctx context.Context, scope NavigationSettingsScope, expectedRevision string) (NavigationSettings, error) {
	return db.writeNavigationSettings(ctx, scope, "", true, expectedRevision)
}

func (db *DB) writeNavigationSettings(ctx context.Context, scope NavigationSettingsScope, raw string, remove bool, expectedRevision string) (NavigationSettings, error) {
	key, err := scope.key()
	if err != nil {
		return NavigationSettings{}, err
	}
	current, err := db.GetNavigationSettings(ctx, scope)
	if err != nil {
		return NavigationSettings{}, err
	}
	if current.Revision != expectedRevision {
		return NavigationSettings{}, ErrVersionConflict
	}
	if remove && !current.Exists {
		return NavigationSettings{}, nil
	}
	d := db.dialect
	var query string
	var args []any
	switch {
	case remove:
		query = "DELETE FROM _settings WHERE key = " + d.Placeholder(1) + " AND value = " + d.Placeholder(2)
		args = []any{key, current.Raw}
	case current.Exists:
		query = "UPDATE _settings SET value = " + d.Placeholder(1) + " WHERE key = " + d.Placeholder(2) + " AND value = " + d.Placeholder(3)
		args = []any{raw, key, current.Raw}
	default:
		if err := db.EnsureSettingsSchema(ctx); err != nil {
			return NavigationSettings{}, err
		}
		query = "INSERT INTO _settings (key, value) VALUES (" + d.Placeholder(1) + ", " + d.Placeholder(2) + ") ON CONFLICT (key) DO NOTHING"
		args = []any{key, raw}
	}
	result, err := db.Exec(ctx, query, args...)
	if err != nil {
		return NavigationSettings{}, fmt.Errorf("navigation settings: CAS: %w", err)
	}
	if result.RowsAffected != 1 {
		return NavigationSettings{}, ErrVersionConflict
	}
	return navigationSettingsValue(raw, !remove), nil
}
