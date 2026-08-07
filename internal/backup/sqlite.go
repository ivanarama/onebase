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

	"github.com/ivantit66/onebase/internal/fsmode"
	"github.com/ivantit66/onebase/internal/storage"
)

// DumpSQLite creates a backup of the SQLite database via `VACUUM INTO`.
// This is an atomic online backup that does not block readers.
// The output file is plain SQLite (.db), not compressed — SQLite is already
// compact and many users want random-restore by file copy.
//
// Returns the full path of the created file.
func DumpSQLite(ctx context.Context, dbPath, outDir string) (string, error) {
	if err := os.MkdirAll(outDir, fsmode.Dir); err != nil {
		return "", err
	}
	base := filepath.Base(dbPath)
	for _, ext := range []string{".db", ".sqlite", ".sqlite3"} {
		if len(base) > len(ext) && base[len(base)-len(ext):] == ext {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	ts := time.Now().Format("2006-01-02_15-04-05.000000000")
	filename := fmt.Sprintf("backup_%s_%s.db", base, ts)
	outPath := filepath.Join(outDir, filename)
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
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
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
	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", err
	}
	committed = true
	if d, err := os.Open(outDir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return outPath, nil
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
	// Windows-путь «C:\…» после ToSlash не начинается с «/»: url.URL печатает
	// его как file://C:/… — диск попадает в authority и SQLite отвергает URI
	// («invalid uri authority: C:»). Корректная форма — file:///C:/… .
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	dsn := (&url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
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

// RestoreSQLite validates a staged copy and atomically replaces the target.
// The caller must still ensure no process is using the target file.
func RestoreSQLite(ctx context.Context, dbPath, backupPath string) error {
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
	if srcInfo.IsDir() {
		return fmt.Errorf("sqlite restore: backup path is a directory")
	}
	if dstInfo, statErr := os.Stat(dbPath); statErr == nil && os.SameFile(srcInfo, dstInfo) {
		return fmt.Errorf("sqlite restore: backup and target are the same file")
	}
	if sqliteInUse(ctx, dbAbs) {
		return fmt.Errorf("sqlite restore: база %s открыта другим процессом — остановите её и повторите", dbAbs)
	}

	dir := filepath.Dir(dbAbs)
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dbAbs)+".restore-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Remove(tmpPath)
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := copyFileSynced(ctx, backupAbs, tmpPath, 0o600); err != nil {
		return fmt.Errorf("sqlite restore: stage copy: %w", err)
	}
	if err := validateSQLiteBackup(ctx, tmpPath); err != nil {
		return err
	}

	// Keep one known-good previous image for operator rollback. This copy is
	// completed before touching the live path.
	oldPath := dbAbs + ".old"
	if _, err := os.Stat(dbAbs); err == nil {
		oldTmp := oldPath + ".tmp"
		_ = os.Remove(oldTmp)
		if err := copyFileSynced(ctx, dbAbs, oldTmp, 0o600); err != nil {
			return fmt.Errorf("sqlite restore: preserve old database: %w", err)
		}
		_ = os.Remove(oldPath)
		if err := os.Rename(oldTmp, oldPath); err != nil {
			_ = os.Remove(oldTmp)
			return fmt.Errorf("sqlite restore: publish old database: %w", err)
		}
	}

	_ = os.Remove(dbAbs + "-wal")
	_ = os.Remove(dbAbs + "-shm")
	if err := os.Rename(tmpPath, dbAbs); err != nil {
		// Some platforms do not replace an existing file with Rename. The old
		// image is already durable, so use it as rollback for the fallback swap.
		if rmErr := os.Remove(dbAbs); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("sqlite restore: replace target: %w", err)
		}
		if err := os.Rename(tmpPath, dbAbs); err != nil {
			_ = copyFileSynced(context.Background(), oldPath, dbAbs, 0o600)
			return fmt.Errorf("sqlite restore: publish staged database: %w", err)
		}
	}
	committed = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
