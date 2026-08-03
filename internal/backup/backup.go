package backup

import (
	"compress/gzip"
	"context"
	"fmt"
	"github.com/ivantit66/onebase/internal/fsmode"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Dump creates a gzipped plain-SQL backup of the database at connStr.
// It writes the file to outDir and returns the full path of the created file.
// Requires pg_dump in PATH.
func Dump(ctx context.Context, connStr, outDir string) (string, error) {
	pgDump, err := findPgTool("pg_dump")
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(outDir, fsmode.Dir); err != nil {
		return "", err
	}

	dbName := dbNameFromDSN(connStr)
	ts := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("backup_%s_%s.sql.gz", dbName, ts)
	outPath := filepath.Join(outDir, filename)
	tmp, err := os.CreateTemp(outDir, "."+filename+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
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
	if err := tmp.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", err
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("pg_dump завершился с ошибкой: %w", err)
	}
	_ = os.Remove(outPath)
	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", err
	}
	committed = true
	return outPath, nil
}

// Restore restores a gzipped plain-SQL backup created by Dump into the database.
// Requires psql in PATH. Drops all existing tables before restoring.
func Restore(ctx context.Context, connStr, filePath string) error {
	psql, err := findPgTool("psql")
	if err != nil {
		return err
	}

	// Drop all user tables before restoring to ensure clean state
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
	dropCmd := exec.CommandContext(ctx, psql, "--no-password", connStr) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	dropCmd.Stdin = strings.NewReader(dropSQL)
	dropCmd.Stdout = os.Stdout
	dropCmd.Stderr = os.Stderr
	if err := dropCmd.Run(); err != nil {
		return fmt.Errorf("psql drop tables: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer closeRead("файл резервной копии", f)

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("не удалось прочитать gzip-архив: %w", err)
	}
	defer closeRead("gzip-поток дампа", gz)

	cmd := exec.CommandContext(ctx, psql, "--no-password", connStr) //nolint:gosec // G204: имя программы фиксировано, аргументы — из флагов CLI администратора на его же машине; shell не запускается
	cmd.Stdin = gz
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("psql завершился с ошибкой: %w", err)
	}
	return nil
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
