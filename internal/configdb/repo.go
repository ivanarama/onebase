package configdb

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
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
	return r.EnsureVersionSchema(ctx)
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
		if err := ValidatePath(rel); err != nil {
			return fmt.Errorf("configdb: unsafe import path %q: %w", rel, err)
		}

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
	save := func(txCtx context.Context) error {
		if err := r.saveFileNoVersion(txCtx, path, content); err != nil {
			return err
		}
		_, err := r.CreateVersion(txCtx, VersionOptions{Message: "save " + path})
		return err
	}
	if storage.HasTx(ctx) {
		return save(ctx)
	}
	return r.db.WithTx(ctx, save)
}

// SaveFiles upserts several config files and creates one configuration version
// after all writes. Use it for UI actions that logically save one object backed
// by multiple files, e.g. form YAML + form module or AI apply with several YAMLs.
func (r *Repo) SaveFiles(ctx context.Context, files []ConfigFile, opts VersionOptions) error {
	if len(files) == 0 {
		return nil
	}
	save := func(txCtx context.Context) error {
		for _, f := range files {
			if err := r.saveFileNoVersion(txCtx, f.Path, f.Content); err != nil {
				return err
			}
		}
		if opts.Message == "" {
			opts.Message = saveFilesMessage(files)
		}
		_, err := r.CreateVersion(txCtx, opts)
		return err
	}
	if storage.HasTx(ctx) {
		return save(ctx)
	}
	return r.db.WithTx(ctx, save)
}

// ApplyFiles removes and upserts config files in one transaction, then creates
// at most one configuration version. Missing delete paths are ignored.
func (r *Repo) ApplyFiles(ctx context.Context, saves []ConfigFile, deletes []string, opts VersionOptions) error {
	if len(saves) == 0 && len(deletes) == 0 {
		return nil
	}
	apply := func(txCtx context.Context) error {
		var deleted []string
		for _, p := range deletes {
			ok, err := r.deleteFileNoVersion(txCtx, p)
			if err != nil {
				return err
			}
			if ok {
				deleted = append(deleted, p)
			}
		}
		for _, f := range saves {
			if err := r.saveFileNoVersion(txCtx, f.Path, f.Content); err != nil {
				return err
			}
		}
		if len(saves) == 0 && len(deleted) == 0 {
			return nil
		}
		if opts.Message == "" {
			opts.Message = applyFilesMessage(saves, deleted)
		}
		_, err := r.CreateVersion(txCtx, opts)
		return err
	}
	if storage.HasTx(ctx) {
		return apply(ctx)
	}
	return r.db.WithTx(ctx, apply)
}

func (r *Repo) saveFileNoVersion(ctx context.Context, path string, content []byte) error {
	if err := ValidatePath(path); err != nil {
		return fmt.Errorf("configdb: unsafe path %q: %w", path, err)
	}
	d := r.db.Dialect()
	if _, err := r.db.Exec(ctx, fmt.Sprintf(`
			INSERT INTO _onebase_config (path, content, updated_at)
			VALUES (%s, %s, %s)
			ON CONFLICT (path) DO UPDATE SET content = EXCLUDED.content, updated_at = %s`,
		d.Placeholder(1), d.Placeholder(2), d.Now(), d.Now()),
		path, content); err != nil {
		return err
	}
	return nil
}

// ReadFile возвращает содержимое одного файла конфигурации. Второе значение
// false — записи нет (это не ошибка для опциональных файлов вроде tree_order.yaml).
func (r *Repo) ReadFile(ctx context.Context, path string) ([]byte, bool, error) {
	if err := ValidatePath(path); err != nil {
		return nil, false, fmt.Errorf("configdb: unsafe path %q: %w", path, err)
	}
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
	del := func(txCtx context.Context) error {
		deleted, err := r.deleteFileNoVersion(txCtx, path)
		if err != nil || !deleted {
			return err
		}
		_, err = r.CreateVersion(txCtx, VersionOptions{Message: "delete " + path})
		return err
	}
	if storage.HasTx(ctx) {
		return del(ctx)
	}
	return r.db.WithTx(ctx, del)
}

// DeleteFiles removes several config files and creates at most one version.
// Missing files are ignored; when none are deleted, no version is created.
func (r *Repo) DeleteFiles(ctx context.Context, paths []string, opts VersionOptions) error {
	if len(paths) == 0 {
		return nil
	}
	del := func(txCtx context.Context) error {
		var deleted []string
		for _, p := range paths {
			ok, err := r.deleteFileNoVersion(txCtx, p)
			if err != nil {
				return err
			}
			if ok {
				deleted = append(deleted, p)
			}
		}
		if len(deleted) == 0 {
			return nil
		}
		if opts.Message == "" {
			opts.Message = deleteFilesMessage(deleted)
		}
		_, err := r.CreateVersion(txCtx, opts)
		return err
	}
	if storage.HasTx(ctx) {
		return del(ctx)
	}
	return r.db.WithTx(ctx, del)
}

func (r *Repo) deleteFileNoVersion(ctx context.Context, path string) (bool, error) {
	if err := ValidatePath(path); err != nil {
		return false, fmt.Errorf("configdb: unsafe path %q: %w", path, err)
	}
	tag, err := r.db.Exec(ctx, `DELETE FROM _onebase_config WHERE path = `+r.db.Dialect().Placeholder(1), path)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected > 0, nil
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
		osPath, err := SafeJoin(dir, path)
		if err != nil {
			return fmt.Errorf("configdb: unsafe export path %q: %w", path, err)
		}
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

func saveFilesMessage(files []ConfigFile) string {
	if len(files) == 1 {
		return "save " + files[0].Path
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return fmt.Sprintf("save %d files: %s", len(files), strings.Join(paths, ", "))
}

func deleteFilesMessage(paths []string) string {
	if len(paths) == 1 {
		return "delete " + paths[0]
	}
	return fmt.Sprintf("delete %d files: %s", len(paths), strings.Join(paths, ", "))
}

func applyFilesMessage(saves []ConfigFile, deletes []string) string {
	if len(deletes) == 0 {
		return saveFilesMessage(saves)
	}
	if len(saves) == 0 {
		return deleteFilesMessage(deletes)
	}
	savePaths := make([]string, 0, len(saves))
	for _, f := range saves {
		savePaths = append(savePaths, f.Path)
	}
	return fmt.Sprintf("apply %d file changes: save %s; delete %s", len(saves)+len(deletes), strings.Join(savePaths, ", "), strings.Join(deletes, ", "))
}

// ListByPrefix возвращает все файлы конфигурации, чьи path начинаются
// с указанного префикса. Префикс может быть пустым — тогда возвращается
// всё содержимое.
func (r *Repo) ListByPrefix(ctx context.Context, prefix string) ([]ConfigFile, error) {
	if prefix != "" {
		if err := ValidatePath(prefix); err != nil {
			return nil, fmt.Errorf("configdb: unsafe prefix %q: %w", prefix, err)
		}
	}
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

var winReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidatePath checks a slash-separated relative path before it is persisted in
// _onebase_config or resolved against a file-backed project directory.
func ValidatePath(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return fmt.Errorf("empty path")
	}
	if path.IsAbs(rel) || filepath.IsAbs(rel) {
		return fmt.Errorf("absolute path")
	}
	if strings.ContainsRune(rel, '\\') || strings.ContainsRune(rel, 0) {
		return fmt.Errorf("invalid path separator or NUL")
	}
	if rel != path.Clean(rel) {
		return fmt.Errorf("unclean path")
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid path segment %q", part)
		}
		if strings.ContainsAny(part, `:*?"<>|`) {
			return fmt.Errorf("invalid file name %q", part)
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("control character in file name %q", part)
			}
		}
		stem := strings.ToLower(strings.TrimSuffix(part, path.Ext(part)))
		if winReservedNames[stem] {
			return fmt.Errorf("reserved file name %q", part)
		}
	}
	return nil
}

// SafeJoin validates rel and resolves it under base, rejecting paths that would
// escape base after filepath cleaning.
func SafeJoin(base, rel string) (string, error) {
	if err := ValidatePath(rel); err != nil {
		return "", err
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(baseAbs, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	back, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if back == "." || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) || filepath.IsAbs(back) {
		return "", fmt.Errorf("path escapes base")
	}
	return targetAbs, nil
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
