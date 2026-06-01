package configdb

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/storage"
)

type Repo struct {
	db *storage.DB
}

func New(db *storage.DB) *Repo {
	return &Repo{db: db}
}

func (r *Repo) EnsureSchema(ctx context.Context) error {
	d := r.db.Dialect()
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS _onebase_config (
			path TEXT PRIMARY KEY,
			content %s NOT NULL,
			updated_at %s NOT NULL DEFAULT %s
		)`, d.TypeBytes(), d.TypeTimestamp(), d.CurrentTimestampTZ()))
	if err != nil {
		return fmt.Errorf("configdb: create table: %w", err)
	}
	return nil
}

func (r *Repo) ImportFromDir(ctx context.Context, dir string) error {
	d := r.db.Dialect()
	if _, err := r.db.Exec(ctx, `DELETE FROM _onebase_config`); err != nil {
		return fmt.Errorf("configdb: clear: %w", err)
	}

	return filepath.WalkDir(dir, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
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

		_, err = r.db.Exec(ctx, fmt.Sprintf(`
			INSERT INTO _onebase_config (path, content, updated_at)
			VALUES (%s, %s, %s)
			ON CONFLICT (path) DO UPDATE SET content = EXCLUDED.content, updated_at = %s
		`, d.Placeholder(1), d.Placeholder(2), d.Now(), d.Now()), rel, content)
		return err
	})
}

// SaveFile upserts a single config file entry.
func (r *Repo) SaveFile(ctx context.Context, path string, content []byte) error {
	d := r.db.Dialect()
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		INSERT INTO _onebase_config (path, content, updated_at)
		VALUES (%s, %s, %s)
		ON CONFLICT (path) DO UPDATE SET content = EXCLUDED.content, updated_at = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Now(), d.Now()),
		path, content)
	return err
}

// ReadFile возвращает содержимое одного файла конфигурации. Второе значение
// false — записи нет (это не ошибка для опциональных файлов вроде tree_order.yaml).
func (r *Repo) ReadFile(ctx context.Context, path string) ([]byte, bool, error) {
	ph := r.db.Dialect().Placeholder(1)
	var content []byte
	err := r.db.QueryRow(ctx, `SELECT content FROM _onebase_config WHERE path = `+ph, path).Scan(&content)
	if err != nil {
		// Запись отсутствует (или таблица ещё пуста) — трактуем как «нет файла».
		return nil, false, nil
	}
	return content, true, nil
}

func (r *Repo) DeleteFile(ctx context.Context, path string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM _onebase_config WHERE path = `+r.db.Dialect().Placeholder(1), path)
	return err
}

func (r *Repo) ExportToDir(ctx context.Context, dir string) error {
	rows, err := r.db.Query(ctx, `SELECT path, content FROM _onebase_config ORDER BY path`)
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

// ConfigFile — путь и содержимое одной записи в _onebase_config.
// Возвращается ListByPrefix; используется, например, редактором
// управляемых форм (план 37, этап 4) для пакетной выборки forms/<entity>/*.
type ConfigFile struct {
	Path    string
	Content []byte
}

// ListByPrefix возвращает все файлы конфигурации, чьи path начинаются
// с указанного префикса. Префикс может быть пустым — тогда возвращается
// всё содержимое.
func (r *Repo) ListByPrefix(ctx context.Context, prefix string) ([]ConfigFile, error) {
	ph := r.db.Dialect().Placeholder(1)
	rows, err := r.db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path LIKE `+ph+` ORDER BY path`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("configdb: list: %w", err)
	}
	defer rows.Close()
	var out []ConfigFile
	for rows.Next() {
		var cf ConfigFile
		if err := rows.Scan(&cf.Path, &cf.Content); err != nil {
			return nil, err
		}
		out = append(out, cf)
	}
	return out, rows.Err()
}

func (r *Repo) IsEmpty(ctx context.Context) (bool, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM _onebase_config`).Scan(&count)
	return count == 0, err
}

// MigrateContent fixes known content issues in stored YAML files.
func (r *Repo) MigrateContent(ctx context.Context) error {
	d := r.db.Dialect()
	rows, err := r.db.Query(ctx,
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
		if strings.Contains(text, "тип_контрагента") {
			text = strings.ReplaceAll(text, "тип_контрагента", "ТипКонтрагента")
			updates = append(updates, update{path, []byte(text)})
		}
	}
	rows.Close()

	for _, u := range updates {
		if _, err := r.db.Exec(ctx, fmt.Sprintf(
			`UPDATE _onebase_config SET content=%s, updated_at=%s WHERE path=%s`,
			d.Placeholder(1), d.Now(), d.Placeholder(2),
		), u.content, u.path); err != nil {
			return fmt.Errorf("configdb: fix content %s: %w", u.path, err)
		}
	}
	return nil
}
