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
