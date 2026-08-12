package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
	sqlite "modernc.org/sqlite"
)

// DumpSQLite creates a backup of the SQLite database via `VACUUM INTO`.
// This is an atomic online backup that does not block readers.
// The output file is plain SQLite (.db), not compressed — SQLite is already
// compact and many users want random-restore by file copy.
//
// Returns the full path of the created file.
func DumpSQLite(ctx context.Context, dbPath, outDir string) (string, error) {
	if strings.TrimSpace(outDir) == "" {
		return "", errors.New("sqlite backup: output directory is empty")
	}
	absOutDir, err := filepath.Abs(outDir)
	if err != nil {
		return "", err
	}
	absOutDir = canonicalDirectoryPath(absOutDir)
	if err := ensureDirectoryDurable(absOutDir); err != nil {
		return "", err
	}
	outDir = absOutDir
	base := filepath.Base(dbPath)
	for _, ext := range []string{".db", ".sqlite", ".sqlite3"} {
		if len(base) > len(ext) && base[len(base)-len(ext):] == ext {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	stamp := time.Now()
	filename := sqliteBackupFilename(base, stamp)
	tmp, err := os.CreateTemp(outDir, "."+filename+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	// VACUUM INTO fails if the file exists. Reserve a unique name with
	// CreateTemp, then remove it before SQLite writes the actual backup.
	_ = os.Remove(tmpPath)
	defer func() {
		// On Unix publication uses an atomic hard link so the temporary name
		// can still exist after the final backup is already committed. Removing
		// it never affects the published inode.
		_ = os.Remove(tmpPath)
	}()

	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		return "", fmt.Errorf("sqlite backup: open source: %w", err)
	}
	defer db.Close()

	// VACUUM INTO 'path' — atomic, no locks held longer than necessary.
	// We can't use parameters (it's not a query but a meta-command); embed
	// the path with simple single-quote escaping.
	escaped := ""
	for _, c := range tmpPath {
		if c == '\'' {
			escaped += "''"
			continue
		}
		escaped += string(c)
	}
	if _, err := db.Exec(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return "", fmt.Errorf("sqlite VACUUM INTO: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return "", fmt.Errorf("sqlite backup: chmod: %w", err)
	}
	if err := syncSQLiteFile(tmpPath); err != nil {
		return "", fmt.Errorf("sqlite backup: sync staged database: %w", err)
	}
	outPath, err := publishSQLiteBackup(ctx, tmpPath, outDir, base, stamp)
	if err != nil {
		return outPath, err
	}
	return outPath, nil
}

func sqliteBackupFilename(base string, stamp time.Time) string {
	return fmt.Sprintf("backup_%s_%s.db", base, stamp.Format("2006-01-02_15-04-05.000000000"))
}

// publishSQLiteBackup claims the final name without replacement. If two dumps
// observe the same clock value, later publishers advance the timestamp by one
// nanosecond until they claim a free, still rotation-compatible name.
func publishSQLiteBackup(ctx context.Context, stagedPath, outDir, base string, stamp time.Time) (string, error) {
	return publishSQLiteBackupWithSync(ctx, stagedPath, outDir, base, stamp, syncSQLiteDirectory)
}

func publishSQLiteBackupWithSync(
	ctx context.Context,
	stagedPath, outDir, base string,
	stamp time.Time,
	syncDirectory func(string) error,
) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		outPath := filepath.Join(outDir, sqliteBackupFilename(base, stamp))
		err := publishSQLiteFileNoReplace(stagedPath, outPath)
		switch {
		case err == nil:
			if syncErr := syncDirectory(outDir); syncErr != nil {
				// Publication already happened. Return its path so callers and logs
				// do not mistake a durability warning for a missing backup.
				return outPath, fmt.Errorf("sqlite backup: sync output directory: %w", syncErr)
			}
			return outPath, nil
		case errors.Is(err, os.ErrExist):
			stamp = stamp.Add(time.Nanosecond)
		default:
			return "", fmt.Errorf("sqlite backup: publish: %w", err)
		}
	}
}

const sqliteHeader = "SQLite format 3\x00"

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

func sqliteFileURI(path string, query string) string {
	// A Windows path "C:\..." must be encoded as file:///C:/..., otherwise C:
	// becomes the URI authority and modernc/sqlite rejects the DSN.
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p, RawQuery: query}).String()
}

func validateSQLiteBackup(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	header := make([]byte, len(sqliteHeader))
	_, readErr := io.ReadFull(f, header)
	_ = f.Close()
	if readErr != nil || !bytes.Equal(header, []byte(sqliteHeader)) {
		return fmt.Errorf("sqlite restore: файл не является SQLite database")
	}
	db, err := sql.Open("sqlite", sqliteFileURI(path, "mode=ro&immutable=1"))
	if err != nil {
		return fmt.Errorf("sqlite restore: открыть подготовленную копию: %w", err)
	}
	defer closeRead("проверочное соединение с копией", db)
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("sqlite restore: integrity_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("sqlite restore: integrity_check: %s", result)
	}
	return nil
}

// sqliteInUse сообщает, держит ли файл базы открытым кто-то ещё.
//
// Восстановление подменяет файл целиком и удаляет -wal/-shm. Под работающей
// базой это уничтожает незачекпойнченные транзакции и оставляет процесс с
// удалённым inode (issue #627). Останавливать базу обязан вызывающий, но
// проверка здесь не даёт порче случиться из-за того, что очередной обработчик
// спросил живость не той функцией.
//
// Приём: в WAL-режиме соединение держит shared-блокировку «мёртвой руки» в
// -shm всё время, пока база открыта, поэтому первая же транзакция в
// exclusive-режиме упирается в SQLITE_BUSY, пока базу держит кто-то ещё.
// Ограничение: базу в journal-режиме простаивающее соединение не блокирует —
// такую занятость проверка не увидит.
//
// «Занята» возвращается только по явному SQLITE_BUSY/SQLITE_LOCKED: любая
// другая ошибка означает «проверить не удалось», и восстановление продолжается
// как раньше — иначе проверка запрещала бы восстановление ровно тех баз,
// которые сломаны и восстановления и ждут.
func sqliteInUse(ctx context.Context, dbPath string) bool {
	if _, err := os.Stat(dbPath); err != nil {
		return false // файла нет — некому его и держать
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(dbPath))
	if err != nil {
		return false
	}
	defer closeRead("проверочное соединение с базой", db)
	conn, err := db.Conn(ctx)
	if err != nil {
		return false
	}
	defer closeRead("проверочное соединение с базой", conn)
	// busy_timeout=0: ждать освобождения незачем, ответ нужен сразу.
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return false
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		return false
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return isSQLiteBusy(err)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		backupLog().Debug("не удалось откатить проверочную транзакцию", "err", err)
	}
	return false
}

// isSQLiteBusy распознаёт отказ по занятости: у драйвера modernc код лежит в
// ошибке (5 — SQLITE_BUSY, 6 — SQLITE_LOCKED, старший байт — уточнение),
// строковая проверка оставлена запасной на случай обёрнутой ошибки.
func isSQLiteBusy(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case 5, 6:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy")
}

func copyFileSynced(ctx context.Context, srcPath, dstPath string, perm os.FileMode) (err error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer closeRead("исходный файл", src)
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer func() {
		_ = dst.Close()
		if err != nil {
			_ = os.Remove(dstPath)
		}
	}()
	if _, err = io.Copy(dst, contextReader{ctx: ctx, r: src}); err != nil {
		return err
	}
	if err = dst.Sync(); err != nil {
		return err
	}
	return dst.Close()
}

type sqliteOnlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func reserveSQLitePath(dir, pattern string) (string, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	closeErr := tmp.Close()
	removeErr := os.Remove(path)
	if closeErr != nil {
		return "", closeErr
	}
	if removeErr != nil {
		return "", removeErr
	}
	return path, nil
}

func syncSQLiteFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// snapshotSQLiteDatabase uses SQLite's online-backup API instead of copying
// the main file. The snapshot therefore includes committed pages which still
// live only in a WAL and is a standalone database after Finish returns.
func snapshotSQLiteDatabase(ctx context.Context, sourcePath, destinationPath string) (err error) {
	source, err := sql.Open("sqlite", sqliteFileURI(sourcePath, "mode=ro"))
	if err != nil {
		return err
	}
	source.SetMaxOpenConns(1)
	source.SetMaxIdleConns(1)
	defer func() { err = errors.Join(err, source.Close()) }()

	conn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return err
	}

	removeSnapshot := true
	defer func() {
		if !removeSnapshot {
			return
		}
		for _, path := range []string{destinationPath, destinationPath + "-wal", destinationPath + "-shm", destinationPath + "-journal"} {
			_ = os.Remove(path)
		}
	}()
	err = conn.Raw(func(driverConnection any) (rawErr error) {
		backuper, ok := driverConnection.(sqliteOnlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(sqliteFileURI(destinationPath, "mode=rwc"))
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				rawErr = errors.Join(rawErr, backup.Finish())
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(256)
			if err != nil {
				return err
			}
			if !more {
				break
			}
		}
		finished = true
		return backup.Finish()
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(destinationPath, 0o600); err != nil {
		return err
	}
	if err := syncSQLiteFile(destinationPath); err != nil {
		return err
	}
	if err := validateSQLiteBackup(ctx, destinationPath); err != nil {
		return err
	}
	removeSnapshot = false
	return nil
}

// checkpointSQLiteTarget makes the live main file independently usable before
// any WAL/SHM name is hidden. A crash after this boundary leaves the old
// logical database intact even if the restore itself has not been published.
func checkpointSQLiteTarget(ctx context.Context, path string) (err error) {
	db, err := sql.Open("sqlite", sqliteFileURI(path, "mode=rw"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		_ = conn.Close()
		return err
	}
	var busy, logPages, checkpointedPages int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&busy, &logPages, &checkpointedPages); err != nil {
		_ = conn.Close()
		return err
	}
	if busy != 0 {
		_ = conn.Close()
		return fmt.Errorf("sqlite checkpoint remained busy (%d pages, %d checkpointed)", logPages, checkpointedPages)
	}
	var integrity string
	if err := conn.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		_ = conn.Close()
		return err
	}
	if integrity != "ok" {
		_ = conn.Close()
		return fmt.Errorf("sqlite integrity_check before replacement: %s", integrity)
	}
	if err := conn.Close(); err != nil {
		return err
	}
	return syncSQLiteFile(path)
}

type quarantinedSQLiteFile struct {
	original   string
	quarantine string
}

func quarantineSQLiteSidecars(basePath string) (moved []quarantinedSQLiteFile, err error) {
	dir := filepath.Dir(basePath)
	defer func() {
		if err != nil && len(moved) != 0 {
			err = errors.Join(err, restoreSQLiteSidecars(moved))
		}
	}()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		original := basePath + suffix
		info, err := os.Lstat(original)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return moved, err
		}
		if !info.Mode().IsRegular() {
			return moved, fmt.Errorf("sqlite sidecar is not a regular file: %s", original)
		}
		quarantine, err := reserveSQLitePath(dir, "."+filepath.Base(basePath)+".restore-sidecar-*")
		if err != nil {
			return moved, err
		}
		if err := moveSQLiteFile(original, quarantine, false); err != nil {
			return moved, err
		}
		moved = append(moved, quarantinedSQLiteFile{original: original, quarantine: quarantine})
	}
	if err := syncSQLiteDirectory(dir); err != nil {
		return moved, err
	}
	return moved, nil
}

func restoreSQLiteSidecars(files []quarantinedSQLiteFile) error {
	var result error
	for i := len(files) - 1; i >= 0; i-- {
		result = errors.Join(result, moveSQLiteFile(files[i].quarantine, files[i].original, false))
	}
	if len(files) != 0 {
		result = errors.Join(result, syncSQLiteDirectory(filepath.Dir(files[0].original)))
	}
	return result
}

func removeQuarantinedSQLiteFiles(files []quarantinedSQLiteFile) {
	removed := false
	for _, file := range files {
		if err := os.Remove(file.quarantine); err != nil && !os.IsNotExist(err) {
			backupLog().Warn("не удалось удалить изолированный SQLite sidecar", "path", file.quarantine, "err", err)
		} else if err == nil {
			removed = true
		}
	}
	if removed {
		if err := syncSQLiteDirectory(filepath.Dir(files[0].quarantine)); err != nil {
			backupLog().Warn("не удалось синхронизировать каталог после удаления SQLite sidecar", "err", err)
		}
	}
}

type sqliteRestoreCutpointKey struct{}

type sqliteRestoreCutpoint func(string) error

func withSQLiteRestoreCutpoint(ctx context.Context, cutpoint sqliteRestoreCutpoint) context.Context {
	return context.WithValue(ctx, sqliteRestoreCutpointKey{}, cutpoint)
}

func hitSQLiteRestoreCutpoint(ctx context.Context, name string) error {
	cutpoint, _ := ctx.Value(sqliteRestoreCutpointKey{}).(sqliteRestoreCutpoint)
	if cutpoint == nil {
		return nil
	}
	if err := cutpoint(name); err != nil {
		return fmt.Errorf("sqlite restore cutpoint %s: %w", name, err)
	}
	return nil
}

const (
	sqliteRestoreAfterStage          = "after_stage"
	sqliteRestoreAfterOldPublished   = "after_old_published"
	sqliteRestoreAfterCheckpoint     = "after_checkpoint"
	sqliteRestoreAfterSidecarsHidden = "after_sidecars_hidden"
	sqliteRestoreAfterTargetPublish  = "after_target_publish"
)

func rollbackSQLiteTarget(oldPath, targetPath string, hadTarget bool) error {
	dir := filepath.Dir(targetPath)
	if !hadTarget {
		removeErr := os.Remove(targetPath)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		return errors.Join(removeErr, syncSQLiteDirectory(dir))
	}
	tmpPath, err := reserveSQLitePath(dir, "."+filepath.Base(targetPath)+".restore-rollback-*")
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := copyFileSynced(context.Background(), oldPath, tmpPath, 0o600); err != nil {
		return err
	}
	if err := moveSQLiteFile(tmpPath, targetPath, true); err != nil {
		return err
	}
	owned = false
	return syncSQLiteDirectory(dir)
}

// RestoreSQLite validates a staged copy and atomically replaces the target.
// The caller must still ensure no process is using the target file.
func RestoreSQLite(ctx context.Context, dbPath, backupPath string) (retErr error) {
	dbAbs, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	backupAbs, err := filepath.Abs(backupPath)
	if err != nil {
		return err
	}
	if filepath.Clean(dbAbs) == filepath.Clean(backupAbs) {
		return fmt.Errorf("sqlite restore: backup and target are the same file")
	}
	srcInfo, err := os.Stat(backupPath)
	if err != nil {
		return fmt.Errorf("sqlite restore: backup not found: %w", err)
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("sqlite restore: backup is not a regular file")
	}
	if dstInfo, statErr := os.Stat(dbPath); statErr == nil && os.SameFile(srcInfo, dstInfo) {
		return fmt.Errorf("sqlite restore: backup and target are the same file")
	}
	targetInfo, targetStatErr := os.Stat(dbAbs)
	if targetStatErr != nil && !os.IsNotExist(targetStatErr) {
		return fmt.Errorf("sqlite restore: inspect target: %w", targetStatErr)
	}
	targetExists := targetStatErr == nil
	if targetExists && !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("sqlite restore: target is not a regular file")
	}
	if sqliteInUse(ctx, dbAbs) {
		return fmt.Errorf("sqlite restore: база %s открыта другим процессом — остановите её и повторите", dbAbs)
	}

	dir := filepath.Dir(dbAbs)
	if err := ensureDirectoryDurable(dir); err != nil {
		return err
	}
	tmpPath, err := reserveSQLitePath(dir, "."+filepath.Base(dbAbs)+".restore-*")
	if err != nil {
		return err
	}
	stagedOwned := true
	defer func() {
		if stagedOwned {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := snapshotSQLiteDatabase(ctx, backupAbs, tmpPath); err != nil {
		return fmt.Errorf("sqlite restore: stage standalone backup: %w", err)
	}
	if err := hitSQLiteRestoreCutpoint(ctx, sqliteRestoreAfterStage); err != nil {
		return err
	}

	oldPath := dbAbs + ".old"
	if targetExists {
		oldTmp, err := reserveSQLitePath(dir, "."+filepath.Base(oldPath)+".restore-*")
		if err != nil {
			return err
		}
		oldTmpOwned := true
		defer func() {
			if oldTmpOwned {
				_ = os.Remove(oldTmp)
			}
		}()
		if err := snapshotSQLiteDatabase(ctx, dbAbs, oldTmp); err != nil {
			return fmt.Errorf("sqlite restore: preserve complete old database: %w", err)
		}
		oldSidecars, err := quarantineSQLiteSidecars(oldPath)
		if err != nil {
			return fmt.Errorf("sqlite restore: isolate old rollback sidecars: %w", err)
		}
		if err := moveSQLiteFile(oldTmp, oldPath, true); err != nil {
			return errors.Join(fmt.Errorf("sqlite restore: publish old database: %w", err), restoreSQLiteSidecars(oldSidecars))
		}
		oldTmpOwned = false
		defer removeQuarantinedSQLiteFiles(oldSidecars)
		if err := syncSQLiteDirectory(dir); err != nil {
			return fmt.Errorf("sqlite restore: persist old database: %w", err)
		}
		if err := hitSQLiteRestoreCutpoint(ctx, sqliteRestoreAfterOldPublished); err != nil {
			return err
		}
		if err := checkpointSQLiteTarget(ctx, dbAbs); err != nil {
			return fmt.Errorf("sqlite restore: checkpoint old database: %w", err)
		}
		if err := hitSQLiteRestoreCutpoint(ctx, sqliteRestoreAfterCheckpoint); err != nil {
			return err
		}
	}

	targetSidecars, err := quarantineSQLiteSidecars(dbAbs)
	if err != nil {
		return fmt.Errorf("sqlite restore: isolate target sidecars: %w", err)
	}
	targetPublished := false
	defer func() {
		if targetPublished {
			removeQuarantinedSQLiteFiles(targetSidecars)
			return
		}
		if err := restoreSQLiteSidecars(targetSidecars); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("sqlite restore: put target sidecars back: %w", err))
		}
	}()
	if targetExists {
		if err := validateSQLiteBackup(ctx, dbAbs); err != nil {
			return fmt.Errorf("sqlite restore: old database is not standalone after checkpoint: %w", err)
		}
	}
	if err := hitSQLiteRestoreCutpoint(ctx, sqliteRestoreAfterSidecarsHidden); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Same-directory rename is the only target publication. On Windows the
	// platform helper uses MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH;
	// there is deliberately no remove(target) fallback and therefore no window
	// in which the configured database path is absent.
	if err := moveSQLiteFile(tmpPath, dbAbs, true); err != nil {
		return fmt.Errorf("sqlite restore: publish staged database: %w", err)
	}
	targetPublished = true
	stagedOwned = false
	if err := syncSQLiteDirectory(dir); err != nil {
		rollbackErr := rollbackSQLiteTarget(oldPath, dbAbs, targetExists)
		return errors.Join(fmt.Errorf("sqlite restore: persist published database: %w", err), rollbackErr)
	}
	if err := hitSQLiteRestoreCutpoint(ctx, sqliteRestoreAfterTargetPublish); err != nil {
		return err
	}
	return nil
}
