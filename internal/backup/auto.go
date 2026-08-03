package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/objstore"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/scheduler"
)

const (
	defaultAutoBackupSchedule = "0 2 * * *"
	defaultAutoBackupKeepLast = 7
)

// AutoTarget describes the database that automatic backup should dump.
type AutoTarget struct {
	DBType     string
	DSN        string
	SQLitePath string
	ProjectDir string
}

type autoDumper func(context.Context, AutoTarget, string) (string, error)

// RegisterAutoBackup registers the configured automatic backup as a scheduler
// Go job. Disabled or nil config is a no-op.
func RegisterAutoBackup(cfg *project.BackupConfig, target AutoTarget, sched *scheduler.Scheduler) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if sched == nil {
		return fmt.Errorf("auto backup: scheduler is nil")
	}
	schedule := strings.TrimSpace(cfg.Schedule)
	if schedule == "" {
		schedule = defaultAutoBackupSchedule
	}
	return sched.RegisterGoJob("AutoBackup", "Автоматический бэкап", schedule, func(ctx context.Context) error {
		_, err := CreateAutoBackup(ctx, cfg, target)
		return err
	})
}

// CreateAutoBackup creates one backup file and rotates older files according to
// cfg.KeepLast. It returns the created backup path.
func CreateAutoBackup(ctx context.Context, cfg *project.BackupConfig, target AutoTarget) (string, error) {
	return createAutoBackup(ctx, cfg, target, dumpAutoTarget, newObjectStore)
}

func createAutoBackup(ctx context.Context, cfg *project.BackupConfig, target AutoTarget, dumper autoDumper, mkStore storeFactory) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("auto backup: config is nil")
	}
	dir := AutoBackupDir(cfg, target.ProjectDir)
	path, err := dumper(ctx, target, dir)
	if err != nil {
		return "", err
	}
	// Копия снимается с базы целиком — секреты, лежащие в _settings значением,
	// уезжают вместе с ней (план 83). Предупреждение в журнал: ночной бэкап
	// некому показать на экране, а файл может уехать ещё и off-site в S3.
	WarnPlaintextSecrets(ctx, target.DBType, target.DSN, target.SQLitePath)
	keepLast := cfg.KeepLast
	if keepLast <= 0 {
		keepLast = defaultAutoBackupKeepLast
	}
	if err := RotateBackups(dir, keepLast); err != nil {
		return path, err
	}
	// Опциональная off-site выгрузка. Локальная копия уже создана — ошибка S3
	// возвращается наверх (планировщик её залогирует), но path остаётся валидным.
	if cfg.S3 != nil {
		if err := uploadToS3(ctx, cfg.S3, path, mkStore); err != nil {
			return path, err
		}
	}
	return path, nil
}

// ObjectStore is the minimal S3 surface auto-backup needs; injected for tests.
type ObjectStore interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	ListKeys(ctx context.Context, prefix string) ([]string, error)
	DeleteObject(ctx context.Context, key string) error
}

// storeFactory builds an ObjectStore from S3 config; overridable in tests.
type storeFactory func(*project.S3Config) (ObjectStore, error)

func newObjectStore(cfg *project.S3Config) (ObjectStore, error) {
	// Креды разыменовываются здесь, при создании клиента (план 83): в app.yaml
	// лежит ссылка env:/file:/enc:, а не сам ключ.
	c, err := cfg.ResolveSecrets()
	if err != nil {
		return nil, fmt.Errorf("auto backup: s3: %w", err)
	}
	return objstore.New(objstore.Config{
		Endpoint:  c.Endpoint,
		Region:    c.Region,
		Bucket:    c.Bucket,
		AccessKey: c.AccessKey,
		SecretKey: c.SecretKey,
		UseSSL:    c.UseSSL,
		PathStyle: c.PathStyle,
	})
}

// uploadToS3 pushes the freshly created backup file to the configured bucket
// and, when cfg.KeepLast > 0, rotates older objects under the same prefix.
func uploadToS3(ctx context.Context, cfg *project.S3Config, localPath string, mkStore storeFactory) error {
	store, err := mkStore(cfg)
	if err != nil {
		return fmt.Errorf("auto backup: s3 init: %w", err)
	}
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("auto backup: s3 open %s: %w", localPath, err)
	}
	defer closeRead("файл резервной копии", f)
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("auto backup: s3 stat: %w", err)
	}
	name := filepath.Base(localPath)
	if err := store.PutObject(ctx, s3Key(cfg.Prefix, name), f, info.Size(), contentTypeFor(name)); err != nil {
		return fmt.Errorf("auto backup: s3 upload: %w", err)
	}
	if cfg.KeepLast > 0 {
		if err := rotateS3(ctx, store, cfg.Prefix, cfg.KeepLast); err != nil {
			return fmt.Errorf("auto backup: s3 rotate: %w", err)
		}
	}
	return nil
}

// s3Key joins a key prefix and file name with a single "/".
func s3Key(prefix, name string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func contentTypeFor(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".gz") {
		return "application/gzip"
	}
	return "application/octet-stream"
}

// rotateS3 keeps the newest keepLast backup objects under prefix and deletes
// older ones. Backup file names embed a sortable timestamp, so lexical
// descending order is newest-first.
func rotateS3(ctx context.Context, store ObjectStore, prefix string, keepLast int) error {
	listPrefix := strings.TrimSuffix(prefix, "/")
	if listPrefix != "" {
		listPrefix += "/"
	}
	keys, err := store.ListKeys(ctx, listPrefix)
	if err != nil {
		return err
	}
	backups := make([]string, 0, len(keys))
	for _, k := range keys {
		if isBackupFile(k) {
			backups = append(backups, k)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(backups)))
	for _, k := range backups[min(keepLast, len(backups)):] {
		if err := store.DeleteObject(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// AutoBackupDir returns the effective backup directory.
func AutoBackupDir(cfg *project.BackupConfig, projectDir string) string {
	if cfg != nil && strings.TrimSpace(cfg.Directory) != "" {
		return strings.TrimSpace(cfg.Directory)
	}
	if projectDir != "" {
		return filepath.Join(projectDir, "backups")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".onebase", "backups", "default")
}

func dumpAutoTarget(ctx context.Context, target AutoTarget, dir string) (string, error) {
	if strings.EqualFold(target.DBType, "sqlite") || target.SQLitePath != "" {
		if target.SQLitePath == "" {
			return "", fmt.Errorf("auto backup: sqlite path is empty")
		}
		return DumpSQLite(ctx, target.SQLitePath, dir)
	}
	if target.DSN == "" {
		return "", fmt.Errorf("auto backup: PostgreSQL DSN is empty")
	}
	return Dump(ctx, target.DSN, dir)
}

// RotateBackups keeps the newest keepLast backup files and removes older ones.
func RotateBackups(dir string, keepLast int) error {
	if keepLast <= 0 {
		return nil
	}
	files, err := BackupFiles(dir)
	if err != nil {
		return err
	}
	if len(files) <= keepLast {
		return nil
	}
	for _, f := range files[keepLast:] {
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// FileInfo is one backup file discovered in a backup directory.
type FileInfo struct {
	Path string
	Info os.FileInfo
}

// BackupFiles returns backup_* files known to onebase, newest first.
func BackupFiles(dir string) ([]FileInfo, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "backup_*"))
	if err != nil {
		return nil, err
	}
	files := make([]FileInfo, 0, len(matches))
	for _, path := range matches {
		if !isBackupFile(path) {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, FileInfo{Path: path, Info: info})
	}
	sort.Slice(files, func(i, j int) bool {
		ti := files[i].Info.ModTime()
		tj := files[j].Info.ModTime()
		if ti.Equal(tj) {
			return files[i].Path > files[j].Path
		}
		return ti.After(tj)
	})
	return files, nil
}

func isBackupFile(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(name, ".sql.gz") ||
		strings.HasSuffix(name, ".sql") ||
		strings.HasSuffix(name, ".db") ||
		strings.HasSuffix(name, ".sqlite") ||
		strings.HasSuffix(name, ".sqlite3")
}
