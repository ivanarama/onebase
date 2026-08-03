package configdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Version is one persisted snapshot of _onebase_config.
type Version struct {
	ID          string
	CreatedAt   time.Time
	AuthorID    string
	AuthorLogin string
	Message     string
}

// VersionOptions describes metadata for a new configuration version.
type VersionOptions struct {
	AuthorID    string
	AuthorLogin string
	Message     string
}

// versionTimeLayout keeps SQLite TEXT timestamps lexicographically sortable.
const versionTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// versionClock выдаёт строго возрастающие created_at внутри процесса. Системный
// таймер (особенно на Windows) может вернуть одинаковый time.Now() для
// нескольких подряд созданных версий; тогда порядок решал тай-брейк по
// СЛУЧАЙНОМУ UUID — история конфигурации могла показать «последней» не ту
// версию, а откат взять не тот снимок (флак TestRepoVersions_SaveDiffRollback).
var versionClock struct {
	mu   sync.Mutex
	last time.Time
}

func nextVersionTime() time.Time {
	versionClock.mu.Lock()
	defer versionClock.mu.Unlock()
	now := time.Now().UTC()
	if !now.After(versionClock.last) {
		now = versionClock.last.Add(time.Nanosecond)
	}
	versionClock.last = now
	return now
}

// DiffKind describes how a file differs between two versions.
type DiffKind string

const (
	DiffAdded    DiffKind = "added"
	DiffDeleted  DiffKind = "deleted"
	DiffModified DiffKind = "modified"
)

// DiffEntry is one file-level difference between two config versions.
type DiffEntry struct {
	Path   string
	Kind   DiffKind
	Before []byte
	After  []byte
}

type configSnapshot struct {
	Version int          `json:"version"`
	Files   []ConfigFile `json:"files"`
}

// EnsureVersionSchema creates the configuration history table.
func (r *Repo) EnsureVersionSchema(ctx context.Context) error {
	d := r.db.Dialect()
	if _, err := r.db.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _config_versions (
			id           %s PRIMARY KEY,
			created_at   %s NOT NULL,
			author_id    TEXT,
			author_login TEXT,
			message      TEXT,
			snapshot     %s NOT NULL
		)`, d.TypeUUID(), d.TypeTimestamp(), d.TypeBytes())); err != nil {
		return fmt.Errorf("configdb: create versions table: %w", err)
	}
	if _, err := r.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_config_versions_created ON _config_versions (created_at DESC)`); err != nil {
		return fmt.Errorf("configdb: create versions index: %w", err)
	}
	return nil
}

// CreateVersion stores a compressed snapshot of the current _onebase_config.
func (r *Repo) CreateVersion(ctx context.Context, opts VersionOptions) (*Version, error) {
	files, err := r.ListByPrefix(ctx, "")
	if err != nil {
		return nil, err
	}
	snapshot, err := encodeSnapshot(files)
	if err != nil {
		return nil, err
	}
	v := &Version{
		ID:          uuid.NewString(),
		CreatedAt:   nextVersionTime(),
		AuthorID:    opts.AuthorID,
		AuthorLogin: opts.AuthorLogin,
		Message:     opts.Message,
	}
	d := r.db.Dialect()
	_, err = r.db.Exec(ctx, fmt.Sprintf(`
		INSERT INTO _config_versions (id, created_at, author_id, author_login, message, snapshot)
		VALUES (%s, %s, %s, %s, %s, %s)`,
		d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4), d.Placeholder(5), d.Placeholder(6)),
		v.ID, v.CreatedAt.Format(versionTimeLayout), emptyToNil(v.AuthorID), emptyToNil(v.AuthorLogin), v.Message, snapshot)
	if err != nil {
		return nil, fmt.Errorf("configdb: create version: %w", err)
	}
	return v, nil
}

// ListVersions returns newest versions first. limit <= 0 means no explicit limit.
func (r *Repo) ListVersions(ctx context.Context, limit int) ([]Version, error) {
	rows, err := r.db.Query(ctx, `SELECT id, created_at, author_id, author_login, message FROM _config_versions`)
	if err != nil {
		return nil, fmt.Errorf("configdb: list versions: %w", err)
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetVersion returns version metadata and its decoded files.
func (r *Repo) GetVersion(ctx context.Context, id string) (*Version, []ConfigFile, error) {
	v, raw, err := r.getVersionSnapshot(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	files, err := decodeSnapshot(raw)
	if err != nil {
		return nil, nil, err
	}
	return v, files, nil
}

// DiffVersions compares two stored versions by file path and content.
func (r *Repo) DiffVersions(ctx context.Context, beforeID, afterID string) ([]DiffEntry, error) {
	_, beforeFiles, err := r.GetVersion(ctx, beforeID)
	if err != nil {
		return nil, err
	}
	_, afterFiles, err := r.GetVersion(ctx, afterID)
	if err != nil {
		return nil, err
	}
	before := filesByPath(beforeFiles)
	after := filesByPath(afterFiles)
	seen := make(map[string]bool, len(before)+len(after))
	var out []DiffEntry
	for p, b := range before {
		seen[p] = true
		a, ok := after[p]
		if !ok {
			out = append(out, DiffEntry{Path: p, Kind: DiffDeleted, Before: b.Content})
			continue
		}
		if !bytes.Equal(b.Content, a.Content) {
			out = append(out, DiffEntry{Path: p, Kind: DiffModified, Before: b.Content, After: a.Content})
		}
	}
	for p, a := range after {
		if seen[p] {
			continue
		}
		out = append(out, DiffEntry{Path: p, Kind: DiffAdded, After: a.Content})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// RollbackToVersion replaces _onebase_config with the version snapshot and
// creates a new version documenting the rollback.
func (r *Repo) RollbackToVersion(ctx context.Context, id string, opts VersionOptions) (*Version, error) {
	_, files, err := r.GetVersion(ctx, id)
	if err != nil {
		return nil, err
	}
	if opts.Message == "" {
		opts.Message = "rollback to " + id
	}
	var rolledBack *Version
	err = r.db.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := r.db.Exec(txCtx, `DELETE FROM _onebase_config`); err != nil {
			return err
		}
		d := r.db.Dialect()
		for _, f := range files {
			if err := ValidatePath(f.Path); err != nil {
				return fmt.Errorf("configdb: unsafe snapshot path %q: %w", f.Path, err)
			}
			if _, err := r.db.Exec(txCtx, fmt.Sprintf(`
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES (%s, %s, %s)`,
				d.Placeholder(1), d.Placeholder(2), d.Now()),
				f.Path, f.Content); err != nil {
				return err
			}
		}
		v, err := r.CreateVersion(txCtx, opts)
		if err != nil {
			return err
		}
		rolledBack = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rolledBack, nil
}

func (r *Repo) getVersionSnapshot(ctx context.Context, id string) (*Version, []byte, error) {
	if id == "" {
		return nil, nil, fmt.Errorf("configdb: empty version id")
	}
	var (
		v        Version
		created  any
		authorID *string
		login    *string
		message  *string
		raw      []byte
	)
	err := r.db.QueryRow(ctx,
		`SELECT id, created_at, author_id, author_login, message, snapshot FROM _config_versions WHERE id = `+r.db.Dialect().Placeholder(1),
		id).Scan(&v.ID, &created, &authorID, &login, &message, &raw)
	if err != nil {
		return nil, nil, fmt.Errorf("configdb: version %s not found: %w", id, err)
	}
	v.CreatedAt, err = parseDBTime(created)
	if err != nil {
		return nil, nil, err
	}
	if authorID != nil {
		v.AuthorID = *authorID
	}
	if login != nil {
		v.AuthorLogin = *login
	}
	if message != nil {
		v.Message = *message
	}
	return &v, raw, nil
}

func scanVersion(rows interface{ Scan(...any) error }) (Version, error) {
	var (
		v        Version
		created  any
		authorID *string
		login    *string
		message  *string
	)
	if err := rows.Scan(&v.ID, &created, &authorID, &login, &message); err != nil {
		return Version{}, err
	}
	var err error
	v.CreatedAt, err = parseDBTime(created)
	if err != nil {
		return Version{}, err
	}
	if authorID != nil {
		v.AuthorID = *authorID
	}
	if login != nil {
		v.AuthorLogin = *login
	}
	if message != nil {
		v.Message = *message
	}
	return v, nil
}

func encodeSnapshot(files []ConfigFile) ([]byte, error) {
	snap := configSnapshot{Version: 1, Files: files}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	if err := enc.Encode(snap); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeSnapshot(raw []byte) ([]ConfigFile, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer oblog.CloseQuiet("configdb", "gzip-поток версии", gz)
	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	var snap configSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.Version != 1 {
		return nil, fmt.Errorf("configdb: unsupported snapshot version %d", snap.Version)
	}
	return snap.Files, nil
}

func filesByPath(files []ConfigFile) map[string]ConfigFile {
	out := make(map[string]ConfigFile, len(files))
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func parseDBTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		return parseTimeString(t)
	case []byte:
		return parseTimeString(string(t))
	default:
		return time.Time{}, fmt.Errorf("configdb: unsupported timestamp type %T", v)
	}
}

func parseTimeString(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("configdb: cannot parse timestamp %q", s)
}
