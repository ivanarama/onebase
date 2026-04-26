package configdb

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrateContent fixes known content issues in stored YAML files.
// Currently: replaces old snake_case field references in report queries
// (e.g. "тип_контрагента" → "ТипКонтрагента") that no longer match the DB column names.
func (r *Repo) MigrateContent(ctx context.Context) error {
	rows, err := r.pool.Query(ctx,
		`SELECT path, content FROM _onebase_config WHERE path LIKE 'reports/%'`)
	if err != nil {
		return nil // table may not exist yet
	}
	defer rows.Close()

	type update struct {
		path    string
		content []byte
	}
	var updates []update
	for rows.Next() {
		var path string
		var content []byte
		if err := rows.Scan(&path, &content); err != nil {
			return err
		}
		text := string(content)
		// Fix: old report had тип_контрагента (snake_case) in WHERE clause
		// but the DB column is now типконтрагента (after ColumnName normalisation).
		if strings.Contains(text, "тип_контрагента") {
			text = strings.ReplaceAll(text, "тип_контрагента", "ТипКонтрагента")
			updates = append(updates, update{path, []byte(text)})
		}
	}
	rows.Close()

	for _, u := range updates {
		if _, err := r.pool.Exec(ctx,
			`UPDATE _onebase_config SET content=$1, updated_at=now() WHERE path=$2`,
			u.content, u.path); err != nil {
			return fmt.Errorf("configdb: fix content %s: %w", u.path, err)
		}
	}
	return nil
}

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _onebase_config (
			path TEXT PRIMARY KEY,
			content BYTEA NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("configdb: create table: %w", err)
	}
	return nil
}

func (r *Repo) ImportFromDir(ctx context.Context, dir string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM _onebase_config`); err != nil {
		return fmt.Errorf("configdb: clear: %w", err)
	}

	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = strings.ReplaceAll(rel, `\`, `/`)

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("configdb: read %s: %w", rel, err)
		}

		_, err = r.pool.Exec(ctx, `
			INSERT INTO _onebase_config (path, content, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (path) DO UPDATE SET content = EXCLUDED.content, updated_at = now()
		`, rel, content)
		return err
	})
}

func (r *Repo) ExportToDir(ctx context.Context, dir string) error {
	rows, err := r.pool.Query(ctx, `SELECT path, content FROM _onebase_config ORDER BY path`)
	if err != nil {
		return fmt.Errorf("configdb: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		var content []byte
		if err := rows.Scan(&path, &content); err != nil {
			return err
		}
		osPath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(osPath), 0o755); err != nil {
			return fmt.Errorf("configdb: mkdir: %w", err)
		}
		if err := os.WriteFile(osPath, content, 0o644); err != nil {
			return fmt.Errorf("configdb: write %s: %w", osPath, err)
		}
	}
	return rows.Err()
}

func (r *Repo) IsEmpty(ctx context.Context) (bool, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM _onebase_config`).Scan(&count)
	return count == 0, err
}
