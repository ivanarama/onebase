package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectUniversalExportS3References(t *testing.T) {
	for _, tableName := range universalExternalObjectTables {
		t.Run(tableName, func(t *testing.T) {
			ctx := context.Background()
			db := newSQLite(t, "s3-export-"+strings.TrimPrefix(tableName, "_"))
			if _, err := db.Exec(ctx, `CREATE TABLE `+sqliteQuote(tableName)+` (id TEXT PRIMARY KEY, loc TEXT)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `INSERT INTO `+sqliteQuote(tableName)+` (id,loc) VALUES ('external',' S3 ')`); err != nil {
				t.Fatal(err)
			}
			if err := rejectUniversalExportS3References(ctx, db); err == nil || !strings.Contains(err.Error(), tableName) {
				t.Fatalf("rejectUniversalExportS3References() = %v, want %s rejection", err, tableName)
			}
		})
	}
}

func TestRejectUniversalExportS3ReferencesAllowsModeWithoutExternalRows(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "s3-export-empty")
	if _, err := db.Exec(ctx, `CREATE TABLE _settings (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _settings(key,value) VALUES ('ui.file_storage','s3')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE _blobs (id TEXT PRIMARY KEY, loc TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _blobs(id,loc) VALUES ('inline','db')`); err != nil {
		t.Fatal(err)
	}
	if err := rejectUniversalExportS3References(ctx, db); err != nil {
		t.Fatalf("empty S3 mode was rejected: %v", err)
	}
}

func TestRejectUniversalArchiveS3References(t *testing.T) {
	for _, tableName := range universalExternalObjectTables {
		t.Run(tableName, func(t *testing.T) {
			root := t.TempDir()
			filePath := filepath.Join(root, "system", tableName+".jsonl")
			if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
				t.Fatal(err)
			}
			body := "{\"_schema\":1}\n{\"id\":\"external\",\"loc\":\"s3\"}\n"
			if err := os.WriteFile(filePath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := rejectUniversalArchiveS3References(root); err == nil || !strings.Contains(err.Error(), tableName) {
				t.Fatalf("rejectUniversalArchiveS3References() = %v, want %s rejection", err, tableName)
			}
		})
	}
}
