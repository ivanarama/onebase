package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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
	key := s3Key(cfg.Prefix, name)
	if err := store.PutObject(ctx, key, f, info.Size(), contentTypeFor(name)); err != nil {
		return fmt.Errorf("auto backup: s3 upload: %w", err)
	}
	if cfg.KeepLast > 0 {
		if err := rotateS3(ctx, store, cfg.Prefix, key, cfg.KeepLast); err != nil {
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

// backupStampRe вычленяет метку времени в конце имени копии. Имя строится как
// backup_<БД>_<дата>[_<время>], причём имя базы само может содержать «_»,
// поэтому разбираем с хвоста, а не по первому разделителю.
var backupStampRe = regexp.MustCompile(`_(\d{4}-\d{2}-\d{2}(?:_\d{2}-\d{2}(?:-\d{2}(?:\.\d+)?)?)?)$`)

// Форматы метки: SQLite пишет наносекунды (sqlite.go:36), PostgreSQL — до секунд
// (backup.go:32). Формат без секунд и дата без времени остались от старых имён:
// такие файлы могут лежать в каталоге бэкапов с прошлых версий, и не разобрать
// их — значит перестать их ротировать.
var backupStampLayouts = []string{
	"2006-01-02_15-04-05.000000000",
	"2006-01-02_15-04-05",
	"2006-01-02_15-04",
	"2006-01-02",
}

// backupStem отрезает известное расширение копии; ok=false — файл не наш.
func backupStem(name string) (string, bool) {
	base := filepath.Base(name)
	lower := strings.ToLower(base)
	for _, ext := range []string{".sql.gz", ".sql", ".db", ".sqlite3", ".sqlite"} {
		if strings.HasSuffix(lower, ext) {
			return base[:len(base)-len(ext)], true
		}
	}
	return "", false
}

// splitBackupName разбирает имя копии на «семейство» (backup_<БД>_, общее у всех
// копий одной базы) и метку времени. ok=false — имя не по шаблону, семейство
// неизвестно, и трогать такой объект нельзя.
func splitBackupName(name string) (family string, stamp time.Time, ok bool) {
	stem, isBackup := backupStem(name)
	if !isBackup {
		return "", time.Time{}, false
	}
	m := backupStampRe.FindStringSubmatchIndex(stem)
	if m == nil {
		return "", time.Time{}, false
	}
	family = stem[:m[0]+1] // вместе с «_» перед меткой
	raw := stem[m[2]:m[3]]
	for _, layout := range backupStampLayouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return family, ts, true
		}
	}
	return family, time.Time{}, false
}

// rotateS3 оставляет keepLast свежих копий ЭТОЙ базы под prefix и удаляет
// остальные.
//
// Отбор идёт по семейству имени (backup_<БД>_), как локальная ротация отбирает
// по маске backup_* в каталоге базы. Раньше кандидатом был любой объект с
// «бэкапным» расширением под префиксом, а при пустом prefix ListKeys отдаёт весь
// бакет — под нож попадали и посторонние файлы, и копии других баз. Хуже того,
// сортировка была лексической, а дата в имени стоит ПОСЛЕ имени базы, поэтому
// порядок задавало имя базы: база «alpha» в общем бакете с «zeta» удаляла все
// свои копии, включая только что загруженную (issue #628).
//
// freshKey — только что загруженная копия: её не удаляем никогда, чем бы ни
// кончился разбор имён.
func rotateS3(ctx context.Context, store ObjectStore, prefix, freshKey string, keepLast int) error {
	listPrefix := strings.TrimSuffix(prefix, "/")
	if listPrefix != "" {
		listPrefix += "/"
	}
	family, _, ok := splitBackupName(freshKey)
	if !ok {
		// Семейство неизвестно — отбирать «свои» объекты не по чему. Удалять
		// наугад нельзя, поэтому ротация пропускается, но громко.
		backupLog().Warn("ротация off-site копий пропущена: имя копии не по шаблону backup_<БД>_<дата>",
			"key", freshKey)
		return nil
	}
	keys, err := store.ListKeys(ctx, listPrefix)
	if err != nil {
		return err
	}
	mine := make([]string, 0, len(keys))
	skipped := 0
	for _, k := range keys {
		if strings.Contains(strings.TrimPrefix(k, listPrefix), "/") {
			// Объект во вложенной «папке» префикса — чужая инсталляция. Свою копию
			// s3Key всегда кладёт непосредственно под префикс.
			continue
		}
		kFamily, _, kOK := splitBackupName(k)
		if !kOK || kFamily != family {
			if isBackupFile(k) {
				skipped++
			}
			continue
		}
		mine = append(mine, k)
	}
	if skipped > 0 {
		// При пустом префиксе ListKeys отдаёт весь бакет, поэтому «чужие» — это
		// и копии других баз, и посторонние файлы; предупреждаем громко. При
		// заданном префиксе это штатная картина общего бакета — только Debug,
		// иначе ночной джоб будет каждый раз шуметь.
		if listPrefix == "" {
			backupLog().Warn("backup.s3.prefix не задан, в бакете есть чужие объекты — ротация трогает только копии этой базы",
				"family", family, "пропущено", skipped)
		} else {
			backupLog().Debug("ротация off-site копий пропустила копии других баз и посторонние файлы",
				"family", family, "пропущено", skipped)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { // новые в начале
		ti, iOK := stampOf(mine[i])
		tj, jOK := stampOf(mine[j])
		switch {
		case iOK != jOK:
			return iOK // копию без разобранной метки считаем самой старой
		case iOK && !ti.Equal(tj):
			return ti.After(tj)
		default:
			return mine[i] > mine[j]
		}
	})
	for _, k := range mine[min(keepLast, len(mine)):] {
		if k == freshKey {
			// Свежая копия попала в хвост — значит на часах или в именах что-то
			// не так. Молча удалять её нельзя: off-site копии базы не осталось бы
			// вовсе, а джоб завершился бы успехом.
			backupLog().Warn("ротация off-site копий не удаляет только что загруженную копию", "key", k)
			continue
		}
		if err := store.DeleteObject(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

func stampOf(name string) (time.Time, bool) {
	_, ts, ok := splitBackupName(name)
	return ts, ok && !ts.IsZero()
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
