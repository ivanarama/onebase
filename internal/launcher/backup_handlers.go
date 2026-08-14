package launcher

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/dblock"
	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
	"gopkg.in/yaml.v3"
)

func backupLog() *slog.Logger {
	return oblog.Component("launcher.backup")
}

func (h *handler) backupDir(b *Base) string {
	custom := h.loadBackupDirSetting(b)
	if custom != "" {
		return custom
	}
	if b.Path != "" {
		return filepath.Join(b.Path, "backups")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".onebase", "backups", b.ID)
}

// safeBackupPath joins dir and file, guaranteeing the result stays inside dir.
// Protects against path traversal (../, absolute paths) in the {file} URL param.
func safeBackupPath(dir, file string) (string, error) {
	if file == "" || strings.ContainsRune(file, 0) {
		return "", i18nerr.New("недопустимое имя файла")
	}
	// reject any path separators / traversal — backup files are flat names.
	if strings.ContainsAny(file, `/\`) || file == ".." || strings.Contains(file, "..") {
		return "", i18nerr.Errorf("недопустимое имя файла: %s", file)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fp := filepath.Join(absDir, file)
	rel, err := filepath.Rel(absDir, fp)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", i18nerr.Errorf("недопустимое имя файла: %s", file)
	}
	return fp, nil
}

// safeArchivePath joins dir with a ZIP/OBZ archive entry name, guaranteeing the
// result stays inside dir. Unlike safeBackupPath (flat backup file names), an
// archive entry may legitimately contain subdirectories (config/module.yaml),
// but must never escape dir via "../" or an absolute path — that would be a
// zip-slip (CWE-22/CWE-23), letting a crafted archive overwrite arbitrary files.
func safeArchivePath(dir, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return "", i18nerr.Errorf("недопустимое имя записи архива: %s", name)
	}
	// Записи бывают с «\» вместо «/» — нормализуем, чтобы обратный слэш не
	// проскочил мимо проверок на не-Windows хостах (там «\» — обычный символ).
	norm := strings.ReplaceAll(name, `\`, "/")
	clean := filepath.FromSlash(norm)
	// Абсолютные пути в записях архива недопустимы: ни «/etc/passwd»,
	// ни «C:\Windows\...» (иначе — запись вне каталога распаковки).
	if filepath.IsAbs(clean) || strings.HasPrefix(norm, "/") ||
		(len(norm) >= 2 && ((norm[0] >= 'A' && norm[0] <= 'Z') || (norm[0] >= 'a' && norm[0] <= 'z')) && norm[1] == ':') {
		return "", i18nerr.Errorf("недопустимое имя записи архива: %s", name)
	}
	outPath := filepath.Join(dir, clean)
	// «../» не должен выводить за пределы dir (собственно zip-slip).
	rel, err := filepath.Rel(dir, outPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", i18nerr.Errorf("недопустимое имя записи архива: %s", name)
	}
	return outPath, nil
}

const (
	maxArchiveEntries        = 100_000
	maxConfigArchiveExpanded = 1 << 30 // 1 GiB
	maxFullArchiveExpanded   = backup.MaxUniversalArchiveExpanded
	maxFormArchiveExpanded   = 256 << 20
	// The HTTP body must also fit ZIP/container and multipart framing overhead
	// for an archive whose expanded payload is exactly at the universal limit.
	maxFullArchiveUploadOverhead = int64(64 << 20)
	maxFullArchiveUpload         = int64(maxFullArchiveExpanded) + maxFullArchiveUploadOverhead
	maxConfigArchiveUpload       = int64(maxConfigArchiveExpanded) + (64 << 20)
	maxFormArchiveUpload         = int64(64<<20) + (2 << 20)
)

// validateArchiveEntries validates the complete archive before the first file
// is written. This prevents a malformed entry near the end of an archive from
// turning an import into a silently partial restore.
func validateArchiveEntries(dir string, files []*zip.File, maxExpanded uint64) error {
	if len(files) > maxArchiveEntries {
		return i18nerr.Errorf("слишком много записей в архиве: %d", len(files))
	}
	seen := make(map[string]struct{}, len(files))
	var expanded uint64
	for _, f := range files {
		outPath, err := safeArchivePath(dir, f.Name)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.Clean(outPath))
		if _, ok := seen[key]; ok {
			return i18nerr.Errorf("повторяющаяся запись в архиве: %s", f.Name)
		}
		seen[key] = struct{}{}

		mode := f.Mode()
		if mode&os.ModeType != 0 && !mode.IsDir() {
			return i18nerr.Errorf("недопустимый тип записи архива: %s", f.Name)
		}
		if f.UncompressedSize64 > maxExpanded-expanded {
			return i18nerr.New("распакованный архив превышает допустимый размер")
		}
		expanded += f.UncompressedSize64
	}
	return nil
}

// validateFullImportContents rejects structurally incomplete full backups
// before the launcher stops a running base. Directory placeholders such as
// "config/" are not configuration: at least one actual file must follow it.
func validateFullImportContents(files []*zip.File, archiveFormat string) error {
	var hasConfig, hasDatabase, hasManifest bool
	for _, f := range files {
		if f.FileInfo().IsDir() {
			continue
		}
		// Count only a canonical entry that really resolves below config/. An
		// entry such as config/../README must not make an archive look complete.
		cleanName := path.Clean(f.Name)
		if cleanName == f.Name && strings.HasPrefix(cleanName, "config/") {
			hasConfig = true
		}
		switch f.Name {
		case "database.sql.gz", "database.db":
			hasDatabase = f.UncompressedSize64 > 0 || hasDatabase
		case "manifest.json":
			hasManifest = f.UncompressedSize64 > 0
		}
	}

	if !hasConfig {
		return i18nerr.New("полный бэкап не содержит ни одного файла конфигурации в config/")
	}
	if archiveFormat == "universal" {
		if !hasManifest {
			return i18nerr.New("совместимый полный бэкап не содержит manifest.json")
		}
		return nil
	}
	if !hasDatabase {
		return i18nerr.New("полный бэкап не содержит дамп базы данных (ожидался database.sql.gz или database.db)")
	}
	return nil
}

func extractValidatedArchive(dir string, files []*zip.File) error {
	for _, f := range files {
		outPath, err := safeArchivePath(dir, f.Name)
		if err != nil {
			return err // normally caught by validateArchiveEntries; keep fail-closed
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, fsmode.Dir); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), fsmode.Dir); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			closeRead("запись архива", rc)
			return err
		}
		n, copyErr := io.Copy(out, rc) //nolint:gosec // G110: суммарный распакованный объём проверяется до первой записи (validateArchiveEntries/validateUniversalArchive), а после копирования сверяется с UncompressedSize64
		closeErr := out.Close()
		rcErr := rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if rcErr != nil {
			return rcErr
		}
		if uint64(n) != f.UncompressedSize64 { //nolint:gosec // G115: n — результат io.Copy, он неотрицателен по контракту
			return i18nerr.Errorf("неполная запись архива: %s", f.Name)
		}
	}
	return nil
}

// loadBackupDirSetting читает backup.directory из config/app.yaml базы.
//
// Пустая строка означает «класть копии в каталог по умолчанию», поэтому
// нечитаемый app.yaml здесь молча менял место хранения резервных копий:
// пользователь настроил отдельный диск, а копии уходили в каталог базы.
// Само поведение оставлено прежним — падать из-за этого нельзя, каталог нужен и
// для показа настроек, — но причина теперь видна в журнале (внутри readAppYAML).
func (h *handler) loadBackupDirSetting(b *Base) string {
	var cfg struct {
		Backup struct {
			Directory string `yaml:"directory"`
		} `yaml:"backup"`
	}
	if err := readAppYAML(context.Background(), b, &cfg); err != nil {
		return ""
	}
	return cfg.Backup.Directory
}

func sqlitePathForBase(b *Base) (string, bool) {
	if b == nil || b.DBType != "sqlite" && (b.DBType != "" || b.DB != "") {
		return "", false
	}
	path := b.DBPath
	if path == "" {
		path = filepath.Join(os.TempDir(), "onebase_"+b.ID+".db")
	}
	return path, true
}

// dumpForBase chooses the right backup mechanism based on b.DBType.
func dumpForBase(ctx context.Context, b *Base, dir string) (string, error) {
	if sqlitePath, ok := sqlitePathForBase(b); ok {
		return backup.DumpSQLite(ctx, sqlitePath, dir)
	}
	return backup.Dump(ctx, b.DB, dir)
}

func basePinnedToOpenDB(b *Base, db *storage.DB) *Base {
	if b == nil {
		return nil
	}
	pinned := *b
	if db != nil {
		if pinnedPath := db.SQLitePath(); pinnedPath != "" {
			pinned.DBType = "sqlite"
			pinned.DB = ""
			pinned.DBPath = pinnedPath
		}
	}
	return &pinned
}

// restoreForBase chooses the right restore mechanism based on b.DBType.
func restoreForBase(ctx context.Context, b *Base, fp string) error {
	if sqlitePath, ok := sqlitePathForBase(b); ok {
		return backup.RestoreSQLite(ctx, sqlitePath, fp)
	}
	return backup.Restore(ctx, b.DB, fp)
}

// resetBasePrefixAfterRestore гасит префикс базы после восстановления копии.
//
// Копия, восстановленная в ДРУГУЮ базу, сохраняла префикс оригинала и выдавала
// бы его коды — обмен склеил бы разные объекты. В CLI сброс был с самого
// начала (117D), а восстановление через лаунчер шло мимо: защита работала на
// одном входе из двух (#871).
//
// Вызывается ПОД тем же эксклюзивным лизом, что и само восстановление, поэтому
// база открывается openDBUnchecked — как и импорт конфигурации рядом.
// Возвращает прежний префикс, чтобы сказать об этом человеку: молча снятый
// префикс — это тихое изменение поведения нумерации.
func resetBasePrefixAfterRestore(ctx context.Context, b *Base) (string, error) {
	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		return "", err
	}
	defer db.Close()
	return db.ResetBasePrefixAfterRestore(ctx)
}

// checkRawRestoreAllowed runs under the caller's exclusive database lease.
// Raw engine dumps cannot resolve a universal restore's external directory
// journal, so they must never overwrite the database that contains its marker.
func checkRawRestoreAllowed(ctx context.Context, b *Base) error {
	db, err := openDBUnchecked(ctx, b)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
		return fmt.Errorf("raw database restore refused while universal recovery is pending: %w", err)
	}
	return nil
}

func acquireBaseDatabaseLease(ctx context.Context, b *Base) (dblock.Lease, error) {
	if sqlitePath, ok := sqlitePathForBase(b); ok {
		lease, canonical, err := dblock.AcquireSQLiteTarget(sqlitePath)
		if err == nil {
			// Store.Get returns a request-local decoded value. Pin every operation
			// in this restore to the exact target protected by the acquired lock.
			b.DBType = "sqlite"
			b.DBPath = canonical
		}
		return lease, err
	}
	return dblock.AcquirePostgres(ctx, b.DB)
}

// fullExportSnapshotLease keeps every source of a full backup stable while the
// archive is assembled. The configurator gate excludes launcher-side database
// and configuration writes, the Runner gate excludes start/stop/edit lifecycle
// races, and the cross-process lease excludes another cooperating OneBase
// process opening the same database.
type fullExportSnapshotLease struct {
	h          *handler
	base       *Base
	database   dblock.Lease
	releaseCfg func()
	wasRunning bool
	released   bool
}

func (h *handler) restartBaseAfterFullExport(base *Base, wasRunning bool) error {
	if !wasRunning {
		return nil
	}
	// A failed stop may still have left the original process alive. In that
	// case its pre-export state is already preserved and starting a duplicate
	// would only manufacture a misleading error.
	status := h.runner.RuntimeStatus(base)
	if status.Running {
		return nil
	}
	if status.Occupied {
		return fmt.Errorf("перезапустить базу %q после полной выгрузки: порт %d занят другим процессом", base.Name, base.Port)
	}
	if err := h.runner.startHeld(base); err != nil {
		return fmt.Errorf("перезапустить базу %q после полной выгрузки: %w", base.Name, err)
	}
	if err := h.runner.WaitReady(base, 15*time.Second); err != nil {
		return fmt.Errorf("дождаться запуска базы %q после полной выгрузки: %w", base.Name, err)
	}
	return nil
}

func (s *fullExportSnapshotLease) release() error {
	if s == nil || s.released {
		return nil
	}
	s.released = true
	leaseErr := s.database.Close()
	restartErr := s.h.restartBaseAfterFullExport(s.base, s.wasRunning)
	s.h.invalidateStatus(s.base.ID)
	// Preserve the global lock order: cfg gate -> lifecycle gate. No new
	// lifecycle operation may enter while the cfg gate is still owned here.
	s.releaseCfg()
	s.h.runner.AllowStarts()
	if leaseErr != nil {
		leaseErr = fmt.Errorf("освободить блокировку БД после полной выгрузки: %w", leaseErr)
	}
	return errors.Join(leaseErr, restartErr)
}

// acquireFullExportSnapshot stops a running (including authenticated adopted)
// base and returns a lease whose release always attempts to restore that
// running state. It intentionally does not write an HTTP response so export
// failures and restart failures can be reported through one correct status.
func (h *handler) acquireFullExportSnapshot(ctx context.Context, b *Base) (*fullExportSnapshotLease, error) {
	releaseCfg := func() {}
	if !cfgDBExclusiveLeaseHeld(ctx, b.ID) {
		releaseCfg = acquireCfgDBExclusive(b.ID)
	}
	if err := h.runner.holdStarts(); err != nil {
		releaseCfg()
		return nil, fmt.Errorf("начать согласованную полную выгрузку: %w", err)
	}
	releaseGates := func() {
		releaseCfg()
		h.runner.AllowStarts()
	}
	// The handler read b before taking the lifecycle lease. Refuse a stale
	// request rather than exporting a database/configuration selected by a
	// concurrent edit (or by a remove+add that reused the ID).
	current, err := h.store.Get(b.ID)
	if err != nil {
		releaseGates()
		return nil, fmt.Errorf("повторно прочитать базу перед полной выгрузкой: %w", err)
	}
	if !sameFullExportBaseSnapshot(b, current) {
		releaseGates()
		return nil, errors.New("полная выгрузка отменена: параметры информационной базы изменились; повторите запрос")
	}

	if alias, err := h.databaseAlias(b); err != nil || alias != nil {
		releaseGates()
		if err != nil {
			return nil, fmt.Errorf("проверить реестр баз перед полной выгрузкой: %w", err)
		}
		return nil, fmt.Errorf("полная выгрузка отменена: эта БД зарегистрирована также в информационной базе %q", alias.Name)
	}

	status := h.runner.RuntimeStatus(b)
	wasRunning := status.Running
	if err := h.runner.stopBaseHeld(b); err != nil {
		restartErr := h.restartBaseAfterFullExport(b, wasRunning)
		releaseGates()
		return nil, errors.Join(fmt.Errorf("остановить базу перед полной выгрузкой: %w", err), restartErr)
	}

	databaseLease, err := acquireBaseDatabaseLease(ctx, b)
	if err != nil {
		restartErr := h.restartBaseAfterFullExport(b, wasRunning)
		releaseGates()
		return nil, errors.Join(fmt.Errorf("заблокировать БД для полной выгрузки: %w", err), restartErr)
	}
	h.invalidateStatus(b.ID)
	return &fullExportSnapshotLease{
		h: h, base: b, database: databaseLease, releaseCfg: releaseCfg,
		wasRunning: wasRunning,
	}, nil
}

func (h *handler) withFullExportSnapshot(ctx context.Context, b *Base, export func() error) (resultErr error) {
	lease, err := h.acquireFullExportSnapshot(ctx, b)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lease.release())
	}()
	return export()
}

func sameFullExportBaseSnapshot(a, b *Base) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.ControlToken == b.ControlToken && a.Name == b.Name &&
		!runtimeConfigChanged(a, b)
}

// ensureBaseStoppedForRestore останавливает базу перед перезаписью её данных.
//
// Живость проверяется через baseRunning, а не через runner.IsRunning: лаунчер
// «усыновляет» базу, запущенную прежним экземпляром (см. baseRunning в
// handlers.go), и для такой базы в runner.procs процесса нет. По IsRunning она
// выглядела остановленной, поэтому защита «не восстанавливать работающую базу»
// не срабатывала вовсе: восстановление шло поверх открытого файла БД, унося
// незачекпойнченный WAL и подменяя inode под живым процессом (issue #627).
//
// Усыновлённая новым launcher база останавливает себя через token-protected
// control API. Старый/непроверяемый процесс по одному номеру порта не глушим.
//
// Возвращает (wasRunning, release, ok). release удерживает lifecycle lease до
// полного окончания restore: параллельный Start не сможет снова открыть БД.
func (h *handler) ensureBaseStoppedForRestore(w http.ResponseWriter, r *http.Request, b *Base, lang string) (bool, context.Context, func(), bool) {
	// Lock order is cfg DB gate -> launcher lifecycle gate -> cross-process
	// database lease. holdStarts uses TryLock, so a concurrent operation that
	// already owns the lifecycle gate cannot deadlock while waiting for this cfg
	// gate: this request fails promptly and drops the cfg gate below.
	releaseDB := func() {}
	restoreCtx := r.Context()
	if !cfgDBExclusiveLeaseHeld(r.Context(), b.ID) {
		releaseDB = acquireCfgDBExclusive(b.ID)
		restoreCtx = context.WithValue(restoreCtx, cfgDBExclusiveLeaseKey{}, b.ID)
	}
	if err := h.runner.holdStarts(); err != nil {
		releaseDB()
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "База не остановилась — восстановление отменено, чтобы не повредить данные") + ": " + err.Error()
		renderCfg(w, r, data)
		return false, nil, nil, false
	}
	// The upload/archive preflight may have taken minutes. Re-read the registry
	// only after both gates are held and refuse a stale generation: otherwise a
	// remove+add or edit racing the upload could restore the old DBPath/DSN.
	current, err := h.store.Get(b.ID)
	if err != nil || !sameFullExportBaseSnapshot(b, current) {
		releaseDB()
		h.runner.AllowStarts()
		data := h.loadCfgData(r.Context(), b, "backup")
		if err != nil {
			data.Error = tr(lang, "Восстановление отменено: информационная база была удалена или изменена; повторите запрос") + ": " + err.Error()
		} else {
			data.Error = tr(lang, "Восстановление отменено: параметры информационной базы изменились; повторите запрос")
		}
		renderCfg(w, r, data)
		return false, nil, nil, false
	}
	if alias, err := h.databaseAlias(b); err != nil || alias != nil {
		releaseDB()
		h.runner.AllowStarts()
		data := h.loadCfgData(r.Context(), b, "backup")
		if err != nil {
			data.Error = tr(lang, "Не удалось проверить реестр баз перед восстановлением") + ": " + err.Error()
		} else {
			data.Error = fmt.Sprintf("%s: %s", tr(lang, "Восстановление отменено: эта БД зарегистрирована также в другой информационной базе"), alias.Name)
		}
		renderCfg(w, r, data)
		return false, nil, nil, false
	}
	status := h.runner.RuntimeStatus(b)
	wasRunning := status.Running || status.Occupied
	if err := h.runner.stopBaseHeld(b); err != nil {
		releaseDB()
		h.runner.AllowStarts()
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "База не остановилась — восстановление отменено, чтобы не повредить данные") + ": " + err.Error()
		renderCfg(w, r, data)
		return wasRunning, nil, nil, false
	}
	databaseLease, err := acquireBaseDatabaseLease(r.Context(), b)
	if err != nil {
		restartErr := h.restartBaseAfterFullExport(b, wasRunning)
		releaseDB()
		h.runner.AllowStarts()
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "База данных используется другим процессом — восстановление отменено") + ": " + errors.Join(err, restartErr).Error()
		renderCfg(w, r, data)
		return wasRunning, nil, nil, false
	}
	h.invalidateStatus(b.ID) // индикатор в списке должен погаснуть сразу
	var releaseOnce sync.Once
	return wasRunning, restoreCtx, func() {
		releaseOnce.Do(func() {
			// Keep Start blocked until both the destructive DB operation and the cfg
			// exclusive section are over. A newly started server must be the last
			// participant allowed to reopen the database.
			if err := databaseLease.Close(); err != nil {
				backupLog().Warn("database lifetime lock release failed", "base", b.Name, "err", err)
			}
			releaseDB()
			h.runner.AllowStarts()
		})
	}, true
}

func (h *handler) databaseAlias(target *Base) (*Base, error) {
	bases, _, err := h.store.Snapshot()
	if err != nil {
		return nil, err
	}
	want := databaseIdentity(target)
	if want == "" {
		return nil, nil
	}
	for _, base := range bases {
		if base != nil && base.ID != target.ID && databaseIdentity(base) == want {
			return base, nil
		}
	}
	return nil, nil
}

func databaseIdentity(base *Base) string {
	if base == nil {
		return ""
	}
	sqlitePath, ok := sqlitePathForBase(base)
	if !ok {
		if identity, err := dblock.CanonicalPostgresIdentity(base.DB); err == nil {
			return "postgres:" + identity
		}
		return "postgres:" + strings.TrimSpace(base.DB)
	}
	p, err := dblock.CanonicalSQLitePath(sqlitePath)
	if err != nil {
		return ""
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return "sqlite:" + p
}

// checkBackupFileMismatch returns an error when the backup file engine does not
// match the target base engine (e.g. restoring a .sql.gz PG dump into SQLite).
func checkBackupFileMismatch(b *Base, filename string) error {
	lower := strings.ToLower(filename)
	isPGDump := strings.HasSuffix(lower, ".sql.gz") || strings.HasSuffix(lower, ".sql")
	isSQLiteDump := strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite")
	_, targetSQLite := sqlitePathForBase(b)
	if isPGDump && targetSQLite {
		return i18nerr.Errorf("Нельзя восстановить PostgreSQL-бэкап в SQLite-базу (%s). Создайте базу с типом БД PostgreSQL.", filename)
	}
	if isSQLiteDump && !targetSQLite {
		return i18nerr.Errorf("Нельзя восстановить SQLite-бэкап в PostgreSQL-базу (%s). Создайте базу с типом БД SQLite.", filename)
	}
	return nil
}

func (h *handler) backupCreate(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	dir := h.backupDir(b)
	// Hold a shared cross-process lifetime lease through marker check and dump.
	// A destructive restore needs the exclusive counterpart and therefore cannot
	// publish an intent between the check and the end of the snapshot.
	db, dumpErr := OpenDB(r.Context(), b)
	var outPath string
	if dumpErr == nil {
		// OpenDB resolves and pins SQLite aliases while acquiring its shared
		// lifetime lease. Dump that exact target: using the registry path again
		// would let a symlink retarget escape the lock between open and snapshot.
		outPath, dumpErr = dumpForBase(r.Context(), basePinnedToOpenDB(b, db), dir)
		db.Close()
	}
	data := h.loadCfgData(r.Context(), b, "backup")
	if dumpErr != nil {
		data.Error = tr(lang, "Ошибка бэкапа") + ": " + dumpErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		data.BackupMessage = tr(lang, "Бэкап создан") + ": " + filepath.Base(outPath)
	}
	renderCfg(w, r, data)
}

func (h *handler) backupDownload(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file := chi.URLParam(r, "file")
	dir := h.backupDir(b)
	fp, err := safeBackupPath(dir, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(fp); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", attachmentDisposition(file))
	http.ServeFile(w, r, fp)
}

func (h *handler) backupDelete(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file := chi.URLParam(r, "file")
	if fp, err := safeBackupPath(h.backupDir(b), file); err == nil {
		removeTemp(fp)
	}
	data := h.loadCfgData(r.Context(), b, "backup")
	data.FieldsSaved = true
	data.FieldsSavedEntity = "panel-backup"
	renderCfg(w, r, data)
}

func (h *handler) backupSettings(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if failForm(w, r) {
		return
	}
	keepLast, _ := strconv.Atoi(r.FormValue("backup_keep"))
	// Правим только блок backup. Раньше форма собирала весь app.yaml из name и
	// backup: остальное стиралось — включая сам backup.s3 с ключами доступа, — а
	// name подменялось именем базы из реестра лаунчера (issue #656).
	rawApp, _ := h.readConfigFileRaw(r.Context(), b, appConfigPath)
	out, appErr := updateAppYAML(rawApp, func(doc *yaml.Node) error {
		bk, err := yamlSubMap(doc, "backup")
		if err != nil {
			return err
		}
		return setAppYAMLFields(bk, []appYAMLField{
			{"enabled", r.FormValue("backup_enabled") == "on"},
			{"schedule", strOrNil(strings.TrimSpace(r.FormValue("backup_schedule")))},
			{"keep_last", keepLast},
			{"directory", strOrNil(strings.TrimSpace(r.FormValue("backup_dir")))},
		})
	})
	if appErr != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(resolveLang(r), "Ошибка сохранения") + ": " + appErr.Error()
		renderCfg(w, r, data)
		return
	}
	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, "config/app.yaml", out)
		}
	} else {
		dir := filepath.Join(b.Path, "config")
		// Проверяем отдельно: иначе пользователь увидит «no such file or
		// directory» от WriteFile и пойдёт искать пропавший app.yaml.
		if merr := os.MkdirAll(dir, fsmode.Dir); merr != nil {
			saveErr = merr
		} else {
			saveErr = os.WriteFile(filepath.Join(dir, "app.yaml"), out, fsmode.File) //nolint:gosec // G306: то же
		}
	}
	data := h.loadCfgData(r.Context(), b, "backup")
	if saveErr != nil {
		data.Error = tr(resolveLang(r), "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		data.BackupMessage = tr(resolveLang(r), "Настройки бэкапа сохранены")
	}
	renderCfg(w, r, data)
}

func (h *handler) backupUpload(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dir := h.backupDir(b)
	lang := resolveLang(r)
	if merr := os.MkdirAll(dir, fsmode.Dir); merr != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка загрузки") + ": " + merr.Error()
		renderCfg(w, r, data)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFullArchiveUpload)
	file, header, err := r.FormFile("backup_file")
	if err != nil {
		if requestBodyErrorStatus(err) == http.StatusRequestEntityTooLarge {
			http.Error(w, fmt.Sprintf(tr(lang, "файл превышает максимальный размер %d МБ"), maxFullArchiveUpload>>20), http.StatusRequestEntityTooLarge)
			return
		}
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка загрузки") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer closeRead("загруженный файл", file)

	name := filepath.Base(header.Filename)
	outPath, err := safeBackupPath(dir, name)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Недопустимое имя файла")
		renderCfg(w, r, data)
		return
	}
	f, err := os.CreateTemp(dir, ".backup-upload-*")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	tmpPath := f.Name()
	defer removeTemp(tmpPath)
	if _, err := io.Copy(f, file); err != nil {
		_ = f.Close()
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if err := f.Close(); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	data := h.loadCfgData(r.Context(), b, "backup")
	data.FieldsSaved = true
	data.FieldsSavedEntity = "panel-backup"
	data.BackupMessage = tr(lang, "Файл загружен") + ": " + name
	renderCfg(w, r, data)
}

func (h *handler) backupRestore(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	file := chi.URLParam(r, "file")
	dir := h.backupDir(b)
	fp, err := safeBackupPath(dir, file)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Недопустимое имя файла")
		renderCfg(w, r, data)
		return
	}
	if _, err := os.Stat(fp); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Файл не найден") + ": " + file
		renderCfg(w, r, data)
		return
	}

	if err := checkBackupFileMismatch(b, file); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = errText(r, err)
		renderCfg(w, r, data)
		return
	}

	// Восстанавливать поверх работающей базы нельзя: процесс держит файл БД
	// открытым, и запись дампа поверх него портит данные.
	wasRunning, restoreCtx, release, ok := h.ensureBaseStoppedForRestore(w, r, b, lang)
	if !ok {
		return
	}
	defer release()

	restoreErr := checkRawRestoreAllowed(restoreCtx, b)
	var prevPrefix string
	if restoreErr == nil {
		restoreErr = restoreForBase(restoreCtx, b, fp)
	}
	if restoreErr == nil {
		prevPrefix, restoreErr = resetBasePrefixAfterRestore(restoreCtx, b)
	}
	release()
	data := h.loadCfgData(r.Context(), b, "backup")
	if restoreErr != nil {
		data.Error = tr(lang, "Ошибка восстановления") + ": " + restoreErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		msg := tr(lang, "База данных восстановлена из") + ": " + file
		if prevPrefix != "" {
			msg += ". " + tr(lang, "Префикс базы снят") + " (" + prevPrefix + "): " +
				tr(lang, "копия в другой базе выдавала бы коды оригинала")
		}
		if wasRunning {
			msg += ". " + tr(lang, "База остановлена — запустите её заново для применения изменений.")
		}
		data.BackupMessage = msg
	}
	renderCfg(w, r, data)
}

// backupFullExport creates a single .obz file containing both database dump and configuration.
// If the form field "compatible" is "true", a universal (cross-engine) archive is created.
func (h *handler) backupFullExport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// compatible=true means universal cross-engine format; absent/other = binary.
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	compatible := r.FormValue("compatible") == "true"

	name := sanitizeFileName(b.Name) + "_" + time.Now().Format("2006-01-02_15-04") + ".obz"
	lang := resolveLang(r)
	if !compatible {
		http.Error(w, tr(lang, "Бинарный формат полной выгрузки отключён: он не содержит внешние файлы и не является полной резервной копией. Используйте совместимый формат."), http.StatusBadRequest)
		return
	}

	if compatible {
		configSource := b.ConfigSource
		if configSource == "" {
			configSource = "database"
		}

		// Never stream a ZIP while it is still being built: an exporter failure
		// after the first entry would otherwise leave HTTP 200 and a truncated
		// archive that looks like a successful backup. CreateTemp is private
		// (0600); publish headers only after ExportUniversal, Sync and Close have
		// all succeeded.
		tmp, err := os.CreateTemp("", "onebase-universal-export-*.obz")
		if err != nil {
			http.Error(w, tr(lang, "Ошибка создания временного файла")+": "+errText(r, err), http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		defer removeTemp(tmpPath)
		// If snapshot acquisition itself fails, the callback below is not entered;
		// keep a fallback close for that path. A successful callback nils tmp after
		// checking Sync and Close.
		defer func() {
			if tmp != nil {
				closeRead("временный архив полного бэкапа", tmp)
			}
		}()

		exportErr := h.withFullExportSnapshot(r.Context(), b, func() (resultErr error) {
			defer func() {
				syncErr := tmp.Sync()
				closeErr := tmp.Close()
				tmp = nil
				resultErr = errors.Join(resultErr, syncErr, closeErr)
			}()
			// acquireFullExportSnapshot already holds the exclusive database
			// lifetime lease; opening through OpenDB would self-contend on its
			// shared lease. Use the guarded raw handle inside this exact scope.
			db, err := openDBUnchecked(r.Context(), b)
			if err != nil {
				return fmt.Errorf("подключиться к БД для полной выгрузки: %w", err)
			}
			defer db.Close()
			if err := backup.CheckNoPendingRestore(r.Context(), db); err != nil {
				return fmt.Errorf("полная выгрузка запрещена до восстановления: %w", err)
			}
			return backup.ExportUniversal(
				r.Context(), db,
				configSource, b.Path,
				db.FilesDir(),
				b.Name,
				tmp,
			)
		})
		if exportErr != nil {
			backupLog().Error("backup full export failed", "err", exportErr)
			http.Error(w, tr(lang, "Ошибка выгрузки")+": "+errText(r, exportErr), http.StatusInternalServerError)
			return
		}

		archive, err := os.Open(tmpPath) //nolint:gosec // G304: path is the private temp file created immediately above
		if err != nil {
			http.Error(w, tr(lang, "Ошибка чтения выгрузки")+": "+errText(r, err), http.StatusInternalServerError)
			return
		}
		defer closeRead("временный архив полного бэкапа", archive)
		info, err := archive.Stat()
		if err != nil {
			http.Error(w, tr(lang, "Ошибка чтения выгрузки")+": "+errText(r, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", attachmentDisposition(name))
		http.ServeContent(w, r, name, info.ModTime(), archive)
		return
	}

	// Binary export (fast, same-engine only). The dump and the finished ZIP stay
	// on disk: a production database can be many gigabytes, and buffering both
	// in RAM can kill the launcher midway through its only backup.
	tmpDir, err := os.MkdirTemp("", "onebase-obz-dump-*")
	if err != nil {
		http.Error(w, tr(lang, "Ошибка создания временной папки")+": "+errText(r, err), 500)
		return
	}
	defer removeTemp(tmpDir)

	archiveTmp, err := os.CreateTemp("", "onebase-binary-export-*.obz")
	if err != nil {
		http.Error(w, tr(lang, "Ошибка создания временного файла")+": "+errText(r, err), 500)
		return
	}
	archivePath := archiveTmp.Name()
	defer removeTemp(archivePath)
	defer func() {
		if archiveTmp != nil {
			closeRead("временный бинарный архив", archiveTmp)
		}
	}()

	buildErr := h.withFullExportSnapshot(r.Context(), b, func() (resultErr error) {
		defer func() {
			syncErr := archiveTmp.Sync()
			closeErr := archiveTmp.Close()
			archiveTmp = nil
			resultErr = errors.Join(resultErr, syncErr, closeErr)
		}()

		dumpPath, err := dumpForBase(r.Context(), b, tmpDir)
		if err != nil {
			return fmt.Errorf("выгрузить дамп БД: %w", err)
		}
		return func() (archiveErr error) {
			zw := zip.NewWriter(archiveTmp)
			closed := false
			defer func() {
				if !closed {
					archiveErr = errors.Join(archiveErr, zw.Close())
				}
			}()

			dump, err := os.Open(dumpPath) //nolint:gosec // path returned by our backup implementation
			if err != nil {
				return fmt.Errorf("read dump: %w", err)
			}
			defer closeRead("временный дамп", dump)
			dumpEntryName := "database.sql.gz"
			if _, sqlite := sqlitePathForBase(b); sqlite {
				dumpEntryName = "database.db"
			}
			entry, err := zw.Create(dumpEntryName)
			if err != nil {
				return err
			}
			if _, err := io.Copy(entry, dump); err != nil {
				return fmt.Errorf("copy dump: %w", err)
			}

			// A full backup without configuration is not restorable.
			if err := addConfigToZip(r.Context(), zw, b, "config/"); err != nil {
				return err
			}
			exportDBType := b.DBType
			if _, sqlite := sqlitePathForBase(b); sqlite {
				exportDBType = "sqlite"
			} else if exportDBType == "" {
				exportDBType = "postgres"
			}
			meta := fmt.Sprintf("onebase_full_export\nversion=1.0\nformat=binary\ndate=%s\nbase=%s\nsource=%s\ndb_type=%s\n",
				time.Now().Format("2006-01-02T15:04:05"), b.Name, b.ConfigSource, exportDBType)
			if err := zipAdd(zw, "META.txt", []byte(meta)); err != nil {
				return err
			}
			closeErr := zw.Close()
			closed = true
			return closeErr
		}()
	})
	if buildErr != nil {
		backupLog().Error("binary full export failed", "err", buildErr)
		http.Error(w, tr(lang, "Ошибка выгрузки")+": "+errText(r, buildErr), http.StatusInternalServerError)
		return
	}
	archive, err := os.Open(archivePath) //nolint:gosec // private temp file created above
	if err != nil {
		http.Error(w, tr(lang, "Ошибка чтения выгрузки")+": "+errText(r, err), http.StatusInternalServerError)
		return
	}
	defer closeRead("временный бинарный архив", archive)
	info, err := archive.Stat()
	if err != nil {
		http.Error(w, tr(lang, "Ошибка чтения выгрузки")+": "+errText(r, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", attachmentDisposition(name))
	http.ServeContent(w, r, name, info.ModTime(), archive)
}

func attachmentDisposition(name string) string {
	name = sanitizeFileName(filepath.Base(name))
	if value := mime.FormatMediaType("attachment", map[string]string{"filename": name}); value != "" {
		return value
	}
	return "attachment"
}

// backupFullImport restores both database and configuration from a .obz file.
func (h *handler) backupFullImport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	lang := resolveLang(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxFullArchiveUpload)
	file, _, err := r.FormFile("obz_file")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка загрузки файла") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer closeRead("загруженный файл", file)
	exchangeRestoreMode := backup.ExchangeRestoreDisasterRecovery
	if strings.EqualFold(strings.TrimSpace(r.FormValue("exchange_mode")), string(backup.ExchangeRestoreClone)) {
		exchangeRestoreMode = backup.ExchangeRestoreClone
	}

	archiveSize, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка чтения файла") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка чтения файла") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	reader, err := zip.NewReader(file, archiveSize)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Неверный формат файла .obz") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	tmpDir, err := os.MkdirTemp("", "onebase-obz-import-*")
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = "Temp dir error: " + err.Error()
		renderCfg(w, r, data)
		return
	}
	defer removeTemp(tmpDir)
	if err := validateArchiveEntries(tmpDir, reader.File, maxFullArchiveExpanded); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Неверный формат файла .obz") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Pre-scan META.txt for format and db_type.
	archiveFormat := ""
	archiveDBType := ""
	for _, af := range reader.File {
		if af.Name == "META.txt" {
			rc, metaErr := af.Open()
			if metaErr != nil {
				data := h.loadCfgData(r.Context(), b, "backup")
				data.Error = tr(lang, "Неверный формат файла .obz") + ": " + metaErr.Error()
				renderCfg(w, r, data)
				return
			}
			metaBytes, readErr := io.ReadAll(io.LimitReader(rc, (1<<20)+1))
			closeErr := rc.Close()
			if readErr != nil || closeErr != nil || len(metaBytes) > 1<<20 {
				if readErr == nil {
					readErr = closeErr
				}
				if readErr == nil {
					readErr = fmt.Errorf("META.txt превышает лимит 1 MiB")
				}
				data := h.loadCfgData(r.Context(), b, "backup")
				data.Error = tr(lang, "Неверный формат файла .obz") + ": " + readErr.Error()
				renderCfg(w, r, data)
				return
			}
			for _, line := range strings.Split(string(metaBytes), "\n") {
				if strings.HasPrefix(line, "db_type=") {
					archiveDBType = strings.TrimSpace(strings.TrimPrefix(line, "db_type="))
				}
				if strings.HasPrefix(line, "format=") {
					archiveFormat = strings.TrimSpace(strings.TrimPrefix(line, "format="))
				}
			}
			break
		}
	}
	if err := validateFullImportContents(reader.File, archiveFormat); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Неверный формат файла .obz") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	if archiveFormat != "universal" {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Неподдерживаемый формат полной резервной копии") + ": " +
			tr(lang, "старый бинарный .obz не содержит внешние файлы и не может быть безопасно восстановлен как полный снимок; извлеките database.db/database.sql.gz и восстановите его как обычный бэкап")
		renderCfg(w, r, data)
		return
	}

	// Universal format: cross-engine restore.
	if archiveFormat == "universal" {
		// Перезалив таблиц идёт в БД и требует остановленной базы.
		wasRunning, restoreCtx, release, ok := h.ensureBaseStoppedForRestore(w, r, b, lang)
		if !ok {
			return
		}
		defer release()
		// ImportUniversal performs and resolves durable restore recovery itself.
		// The handler already owns cfg/lifecycle/database exclusive leases here,
		// so this is the only safe launcher path that may open past the marker.
		db, cerr := openDBForRestore(restoreCtx, b)
		if cerr != nil {
			// Never rename an unreadable SQLite file here. ImportUniversal cannot
			// persist its restore intent inside a database it cannot open, so a
			// crash between an eager rename and journal creation would publish a
			// fresh empty database while the original survived only as .old.
			// Requiring the operator to repair/move the file keeps this path
			// fail-closed and leaves the original bytes untouched.
			release()
			data := h.loadCfgData(r.Context(), b, "backup")
			data.Error = tr(lang, "Ошибка подключения к БД") + ": " + cerr.Error()
			renderCfg(w, r, data)
			return
		}
		defer db.Close()

		configDest := b.ConfigSource
		if configDest == "" {
			configDest = "database"
		}
		cfgFileDir := b.Path

		report, importErr := backup.ImportUniversalWithOptions(
			restoreCtx, db,
			configDest, cfgFileDir,
			db.FilesDir(),
			file, archiveSize,
			backup.ImportOptions{ExchangeMode: exchangeRestoreMode},
		)

		db.Close()
		release()
		data := h.loadCfgData(r.Context(), b, "backup")
		if importErr != nil {
			data.Error = tr(lang, "Ошибка восстановления") + ": " + importErr.Error()
		} else {
			data.FieldsSaved = true
			data.FieldsSavedEntity = "panel-backup"
			msg := fmt.Sprintf(tr(lang, "Полное восстановление выполнено: %d таблиц, %d файлов вложений"),
				len(report.Tables), report.Files)
			if len(report.TOTPReset) > 0 {
				// Секрет 2FA этих учёток зашифрован чужим мастер-ключом и текущим не
				// читается — второй фактор погашен, чтобы вход не заперло. Называем
				// их, чтобы владельцам перепривязать приложение-аутентификатор (#611).
				msg += ". " + fmt.Sprintf(tr(lang, "Сброшен второй фактор (перепривяжите): %s"),
					strings.Join(report.TOTPReset, ", "))
			}
			if wasRunning {
				msg += ". " + tr(lang, "База остановлена — запустите её заново.")
			}
			data.BackupMessage = msg
		}
		renderCfg(w, r, data)
		return
	}

	var dumpFile string
	var configDir string

	if err := extractValidatedArchive(tmpDir, reader.File); err != nil {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = tr(lang, "Ошибка восстановления") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	for _, f := range reader.File {
		outPath, _ := safeArchivePath(tmpDir, f.Name) // archive was validated above
		switch f.Name {
		case "database.sql.gz":
			dumpFile = outPath
			if archiveDBType == "" {
				archiveDBType = "postgres"
			}
		case "database.db":
			dumpFile = outPath
			if archiveDBType == "" {
				archiveDBType = "sqlite"
			}
		}
		if strings.HasPrefix(f.Name, "config/") && configDir == "" {
			configDir = filepath.Join(tmpDir, "config")
		}
	}

	// Reject cross-engine restores for binary format.
	targetDBType := b.DBType
	if _, sqlite := sqlitePathForBase(b); sqlite {
		targetDBType = "sqlite"
	} else if targetDBType == "" {
		targetDBType = "postgres"
	}
	if archiveDBType != "" && archiveDBType != targetDBType {
		data := h.loadCfgData(r.Context(), b, "backup")
		data.Error = fmt.Sprintf(
			tr(lang, "Нельзя восстановить %s-бэкап в %s-базу (%s). Создайте новую базу с типом БД %s или используйте совместимый формат (.obz с галочкой)."),
			archiveDBType, targetDBType, filepath.Base(r.FormValue("obz_file")), archiveDBType,
		)
		renderCfg(w, r, data)
		return
	}

	wasRunning, restoreCtx, release, ok := h.ensureBaseStoppedForRestore(w, r, b, lang)
	if !ok {
		return
	}
	defer release()

	// Restore database
	var restoreErr error
	if dumpFile != "" {
		restoreErr = checkRawRestoreAllowed(restoreCtx, b)
		if restoreErr == nil {
			restoreErr = restoreForBase(restoreCtx, b, dumpFile)
		}
	} else {
		restoreErr = fmt.Errorf("database dump not found in archive (expected database.sql.gz or database.db)")
	}
	var prevPrefix string
	if restoreErr == nil {
		prevPrefix, restoreErr = resetBasePrefixAfterRestore(restoreCtx, b)
	}

	// Import configuration
	var configErr error
	if restoreErr == nil && configDir != "" {
		if b.ConfigSource == "database" {
			// The handler already holds the exclusive database lease; OpenDB would
			// self-contend on a shared lease. This raw handle is confined to that
			// exclusive scope and the pending marker was checked above.
			db, cerr := openDBUnchecked(restoreCtx, b)
			if cerr != nil {
				configErr = cerr
			} else {
				repo := configdb.New(db)
				configErr = repo.ImportFromDir(r.Context(), configDir)
				if configErr == nil {
					_, configErr = repo.CreateVersion(r.Context(), configdb.VersionOptions{
						AuthorLogin: cfgLogin(r.Context()),
						Message:     "full backup config import",
					})
				}
				db.Close()
			}
		} else {
			configErr = restoreConfigDir(configDir, b.Path)
		}
	}

	// A child `onebase migrate` would correctly request a shared lifetime lease
	// and therefore self-contend with this restore's exclusive lease. Leave the
	// restored snapshot stopped; its normal next start runs schema migration
	// under the server startup lifecycle protocol.
	var migrateErr error

	release()
	data := h.loadCfgData(r.Context(), b, "backup")
	if restoreErr != nil {
		data.Error = tr(lang, "Ошибка восстановления БД") + ": " + restoreErr.Error()
	} else if configErr != nil {
		data.Error = tr(lang, "Ошибка импорта конфигурации") + ": " + configErr.Error()
	} else if migrateErr != nil {
		data.Error = tr(lang, "Данные восстановлены, но миграция схемы не выполнена") + ": " + migrateErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "panel-backup"
		msg := tr(lang, "Полное восстановление выполнено: база данных + конфигурация")
		if prevPrefix != "" {
			msg += ". " + tr(lang, "Префикс базы снят") + " (" + prevPrefix + "): " +
				tr(lang, "копия в другой базе выдавала бы коды оригинала")
		}
		if wasRunning {
			msg += ". " + tr(lang, "База остановлена — запустите её заново для применения изменений.")
		}
		data.BackupMessage = msg
	}
	renderCfg(w, r, data)
}
