package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/fsmode"
)

// Plain-SQL restores use the same expanded-data budget as universal backups.
// Keeping one limit prevents the CLI restore path from accepting a payload
// that the universal restore path would reject as a decompression bomb.
const maxRestoreSQLBytes = int64(maxUniversalArchiveExpanded)

// Dump creates a gzipped plain-SQL backup of the database at connStr.
// It writes the file to outDir and returns the full path of the created file.
// Requires pg_dump in PATH.
func Dump(ctx context.Context, connStr, outDir string) (string, error) {
	pgDump, err := findPgTool("pg_dump")
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(outDir) == "" {
		return "", errors.New("postgres backup: output directory is empty")
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

	dbName := dbNameFromDSN(connStr)
	// Nanoseconds keep concurrent/manual dumps from publishing to the same
	// final name. In particular, never remove a successful backup merely
	// because another dump of the same database finished in the same second.
	stamp := time.Now()
	filename := postgresBackupFilename(dbName, stamp)
	tmp, err := os.CreateTemp(outDir, "."+filename+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer func() {
		// Unix publication uses a no-replace hard link and therefore retains
		// the staging name until after the directory durability barrier.
		_ = os.Remove(tmpPath)
	}()

	// pg_dump → stdout → gzip → file
	cmd := exec.CommandContext(ctx, pgDump, "--format=plain", "--no-owner", "--no-acl", "--clean", "--if-exists", connStr) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	r, err := cmd.StdoutPipe()
	if err != nil {
		_ = tmp.Close()
		return "", err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("pg_dump: %w", err)
	}

	gz := gzip.NewWriter(tmp)
	if _, err := io.Copy(gz, r); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = gz.Close()
		_ = tmp.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", err
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("pg_dump завершился с ошибкой: %w", err)
	}
	return publishPostgresBackup(ctx, tmpPath, outDir, dbName, stamp)
}

func postgresBackupFilename(dbName string, stamp time.Time) string {
	return fmt.Sprintf("backup_%s_%s.sql.gz", dbName, stamp.Format("2006-01-02_15-04-05.000000000"))
}

// publishPostgresBackup atomically claims a collision-free name without ever
// replacing a previously successful backup. The staged file has already been
// flushed; the final directory barrier makes the claimed name durable.
func publishPostgresBackup(ctx context.Context, stagedPath, outDir, dbName string, stamp time.Time) (string, error) {
	return publishPostgresBackupWithSync(ctx, stagedPath, outDir, dbName, stamp, syncDirectoryMetadata)
}

func publishPostgresBackupWithSync(
	ctx context.Context,
	stagedPath, outDir, dbName string,
	stamp time.Time,
	syncDirectory func(string) error,
) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		outPath := filepath.Join(outDir, postgresBackupFilename(dbName, stamp))
		err := publishSQLiteFileNoReplace(stagedPath, outPath)
		switch {
		case err == nil:
			if syncErr := syncDirectory(outDir); syncErr != nil {
				// Publication is already visible and must not be erased or reported
				// as nonexistent merely because its durability barrier failed.
				return outPath, fmt.Errorf("postgres backup: sync output directory: %w", syncErr)
			}
			return outPath, nil
		case errors.Is(err, os.ErrExist):
			stamp = stamp.Add(time.Nanosecond)
		default:
			return "", fmt.Errorf("postgres backup: publish: %w", err)
		}
	}
}

// Restore restores a gzipped plain-SQL backup created by Dump into the database.
// Requires psql in PATH. Drops all existing tables before restoring.
func Restore(ctx context.Context, connStr, filePath string) error {
	// Fully decode and checksum-validate gzip before issuing any destructive SQL.
	// A truncated/corrupt upload must not empty a healthy target database.
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("не удалось прочитать gzip-архив: %w", err)
	}
	staged, err := os.CreateTemp("", "onebase-restore-*.sql")
	if err != nil {
		_ = gz.Close()
		_ = f.Close()
		return err
	}
	stagedPath := staged.Name()
	defer func() {
		_ = staged.Close()
		_ = os.Remove(stagedPath)
	}()
	if err := staged.Chmod(fsmode.SecretFile); err != nil {
		_ = gz.Close()
		_ = f.Close()
		return err
	}
	_, copyErr := copyBoundedRestoreSQL(staged, gz, maxRestoreSQLBytes)
	gzipCloseErr := gz.Close()
	fileCloseErr := f.Close()
	if err := errors.Join(copyErr, gzipCloseErr, fileCloseErr); err != nil {
		return fmt.Errorf("не удалось прочитать gzip-архив: %w", err)
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return err
	}
	psql, err := findPgTool("psql")
	if err != nil {
		return err
	}

	// Drop and restore are one psql transaction with fail-fast SQL semantics.
	// Any statement failure rolls the entire target back to its prior state.
	dropSQL := `
DO $$ DECLARE
  r RECORD;
BEGIN
  FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
    EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
  END LOOP;
  FOR r IN (SELECT sequencename FROM pg_sequences WHERE schemaname = 'public') LOOP
    EXECUTE 'DROP SEQUENCE IF EXISTS public.' || quote_ident(r.sequencename) || ' CASCADE';
  END LOOP;
  FOR r IN (SELECT typname FROM pg_type t JOIN pg_namespace n ON t.typnamespace=n.oid WHERE n.nspname='public' AND t.typtype='e') LOOP
    EXECUTE 'DROP TYPE IF EXISTS public.' || quote_ident(r.typname) || ' CASCADE';
  END LOOP;
END $$;`
	cmd := exec.CommandContext(ctx, psql, postgresRestoreArgs(connStr)...) //nolint:gosec // G204: fixed tool, operator DSN argument, no shell
	cmd.Stdin = postgresRestoreInput(dropSQL, staged)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql завершился с ошибкой: %w", err)
	}
	return nil
}

const postgresRestoreDurableCommitSQL = "\nSET LOCAL synchronous_commit=on;\n"

func postgresRestoreArgs(connStr string) []string {
	// -X prevents ~/.psqlrc from executing commands or changing restore
	// semantics before the validated dump is processed.
	return []string{"-X", "--no-password", "--single-transaction", "--set=ON_ERROR_STOP=1", connStr}
}

func postgresRestoreInput(dropSQL string, staged io.Reader) io.Reader {
	// psql --single-transaction emits COMMIT after consuming stdin. Placing SET
	// LOCAL last prevents a database/user default (or a dump SET) from making
	// that commit return before its WAL record is durable.
	return io.MultiReader(
		strings.NewReader(dropSQL+"\n"),
		staged,
		strings.NewReader(postgresRestoreDurableCommitSQL),
	)
}

func copyBoundedRestoreSQL(dst io.Writer, src io.Reader, maxBytes int64) (int64, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	if maxBytes <= 0 || maxBytes == maxInt64 {
		return 0, fmt.Errorf("restore: invalid uncompressed SQL size limit %d", maxBytes)
	}

	written, err := io.Copy(dst, io.LimitReader(src, maxBytes+1))
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, fmt.Errorf("restore: uncompressed SQL exceeds the %d-byte limit", maxBytes)
	}
	return written, nil
}

// dbNameFromDSN extracts the database name from a connection string.
// Supports both URL (postgres://host/dbname) and DSN (dbname=foo) formats.
func dbNameFromDSN(connStr string) string {
	if strings.HasPrefix(connStr, "postgres://") || strings.HasPrefix(connStr, "postgresql://") {
		if u, err := url.Parse(connStr); err == nil {
			name := strings.TrimPrefix(u.Path, "/")
			if name != "" {
				return sanitize(name)
			}
		}
	}
	// DSN key=value format
	for _, part := range strings.Fields(connStr) {
		if strings.HasPrefix(part, "dbname=") {
			return sanitize(strings.TrimPrefix(part, "dbname="))
		}
	}
	return "db"
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// findPgTool locates a PostgreSQL tool (pg_dump, psql) by first checking PATH,
// then searching common Windows install directories.
func findPgTool(name string) (string, error) {
	// Try PATH first
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		// Search standard PostgreSQL install dirs on Windows
		pgDirs := []string{
			`C:\Program Files\PostgreSQL`,
			`C:\Program Files (x86)\PostgreSQL`,
		}
		for _, base := range pgDirs {
			entries, err := os.ReadDir(base)
			if err != nil {
				continue
			}
			// Iterate version dirs in reverse (newest first)
			for i := len(entries) - 1; i >= 0; i-- {
				binPath := filepath.Join(base, entries[i].Name(), "bin", name+".exe")
				if _, err := os.Stat(binPath); err == nil {
					return binPath, nil
				}
			}
		}
	}
	return "", fmt.Errorf("%s не найден; установите PostgreSQL и добавьте в PATH", name)
}
