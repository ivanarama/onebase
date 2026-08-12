package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "onebase.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestExportConfigRejectsEmpty(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		err := exportConfig(context.Background(), nil, "file", t.TempDir(), zw)
		_ = zw.Close()
		if !errors.Is(err, errNoConfigEntries) {
			t.Fatalf("exportConfig error = %v, want %v", err, errNoConfigEntries)
		}
	})

	t.Run("database", func(t *testing.T) {
		db := newSQLite(t, "empty-config")
		if _, err := db.Exec(context.Background(), `CREATE TABLE _onebase_config (path TEXT, content BLOB)`); err != nil {
			t.Fatal(err)
		}

		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		err := exportConfig(context.Background(), db, "database", "", zw)
		_ = zw.Close()
		if !errors.Is(err, errNoConfigEntries) {
			t.Fatalf("exportConfig error = %v, want %v", err, errNoConfigEntries)
		}
	})
}

func TestExportConfigWalkErrorIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := exportConfig(context.Background(), nil, "file", missing, zw)
	_ = zw.Close()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("exportConfig error = %v, want filesystem not-exist error", err)
	}
}

func TestExportConfigReadErrorIsReported(t *testing.T) {
	dir := t.TempDir()
	makeUnreadableTestEntry(t, dir, "broken.yaml")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := exportConfig(context.Background(), nil, "file", dir, zw)
	_ = zw.Close()
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("exportConfig error = %v, want config file context", err)
	}
}

func TestExportConfigRejectsUnsafeDatabasePath(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "unsafe-config-path")
	if _, err := db.Exec(ctx, `CREATE TABLE _onebase_config (path TEXT, content BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _onebase_config(path,content) VALUES (?,?)`, "../secret", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := exportConfig(ctx, db, "database", "", zw)
	_ = zw.Close()
	if err == nil || !strings.Contains(err.Error(), "unsafe config path") {
		t.Fatalf("exportConfig error = %v, want unsafe-path rejection", err)
	}
}

func TestExportUniversalRejectsNonPortableTableName(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "unsafe-table-name")
	if _, err := db.Exec(ctx, `CREATE TABLE "bad/name" (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := ExportUniversal(ctx, db, "file", testConfigDir(t), "", "test", &buf)
	if err == nil || !strings.Contains(err.Error(), "non-portable application table name") {
		t.Fatalf("ExportUniversal error = %v, want table-name rejection", err)
	}
}

func TestExportAttachmentsRejectsUnreadableOrLinkedEntry(t *testing.T) {
	dir := t.TempDir()
	makeUnreadableTestEntry(t, dir, "outside.bin")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, err := exportAttachments(dir, zw)
	_ = zw.Close()
	if err == nil {
		t.Fatal("exportAttachments accepted an unreadable or linked entry")
	}
}

func TestExportConfigScanErrorIsReported(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t, "scan-config")
	if _, err := db.Exec(ctx, `CREATE TABLE _onebase_config (path TEXT, content BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO _onebase_config (path, content) VALUES (NULL, x'01')`); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := exportConfig(ctx, db, "database", "", zw)
	_ = zw.Close()
	if err == nil || !strings.Contains(err.Error(), "scan config row") {
		t.Fatalf("exportConfig error = %v, want row scan error", err)
	}
}

func TestExportUniversalAttachmentErrorIsReported(t *testing.T) {
	attachmentsDir := t.TempDir()
	content := make([]byte, 1024*1024)
	state := uint32(1)
	for i := range content {
		state = state*1664525 + 1013904223
		content[i] = byte(state >> 24)
	}
	ctx := context.Background()
	db := newSQLite(t, "attachment-error")
	configDir := testConfigDir(t)
	var baseline countingWriter
	if err := ExportUniversal(ctx, db, "file", configDir, "", "test", &baseline); err != nil {
		t.Fatalf("baseline export: %v", err)
	}
	createDiskAttachmentForBackup(t, db, attachmentsDir, content)

	w := &errAfterWriter{limit: baseline.n + 32*1024}
	err := ExportUniversal(
		ctx, db, "file", configDir, attachmentsDir, "test", w,
	)
	if !errors.Is(err, errDiskFull) {
		t.Fatalf("ExportUniversal error = %v, want attachment write error", err)
	}
	if !strings.Contains(err.Error(), "export attachments") {
		t.Fatalf("ExportUniversal error = %v, want attachment context", err)
	}
}
