package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
	"github.com/spf13/cobra"
)

func TestRequireOneDBTarget(t *testing.T) {
	cases := []struct {
		name, db, sqlite string
		wantErr          bool
	}{
		{"ни одного", "", "", true},
		{"оба", "postgres://x", "a.db", true},
		{"только db", "postgres://x", "", false},
		{"только sqlite", "", "a.db", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := requireOneDBTarget(c.db, c.sqlite)
			if (err != nil) != c.wantErr {
				t.Fatalf("requireOneDBTarget(%q,%q): err=%v, ждали wantErr=%v", c.db, c.sqlite, err, c.wantErr)
			}
		})
	}
}

// TestRunBackupSQLite гоняет CLI-путь --sqlite до конца: создаётся файл .db.
func TestRunBackupSQLite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "docflow.db")
	db, err := storage.ConnectSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	outDir := filepath.Join(dir, "backups")
	backupSQLite, backupDB, backupOut = dbPath, "", outDir
	t.Cleanup(func() { backupSQLite, backupDB, backupOut = "", "", "." })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background()) // cobra ставит ctx только в Execute
	if err := runBackup(cmd, nil); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read outDir: %v", err)
	}
	var found string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db") {
			found = e.Name()
		}
	}
	if found == "" {
		t.Fatalf("бэкап .db не создан в %s (файлы: %v)", outDir, entries)
	}
}
