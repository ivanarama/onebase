package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRestoreRejectsCorruptGzipBeforeLookingForPSQL(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write([]byte("CREATE TABLE must_not_run(id int);")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := compressed.Bytes()
	data = data[:len(data)-4] // corrupt the checksum/trailer after a valid header
	path := filepath.Join(t.TempDir(), "corrupt.sql.gz")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	err := Restore(context.Background(), "postgres://invalid/unused", path)
	if err == nil || !strings.Contains(err.Error(), "gzip") {
		t.Fatalf("corrupt dump reached psql/tool lookup instead of safe preflight: %v", err)
	}
}

func TestCopyBoundedRestoreSQL(t *testing.T) {
	compress := func(t *testing.T, payload []byte) *gzip.Reader {
		t.Helper()
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		if _, err := zw.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		return zr
	}

	t.Run("exact limit", func(t *testing.T) {
		payload := bytes.Repeat([]byte("x"), 32)
		zr := compress(t, payload)
		defer zr.Close() //nolint:errcheck // test cleanup
		var got bytes.Buffer
		written, err := copyBoundedRestoreSQL(&got, zr, int64(len(payload)))
		if err != nil {
			t.Fatalf("copy exact-size SQL: %v", err)
		}
		if written != int64(len(payload)) || !bytes.Equal(got.Bytes(), payload) {
			t.Fatalf("copied %d bytes (%q), want %d bytes", written, got.Bytes(), len(payload))
		}
	})

	t.Run("one byte over limit", func(t *testing.T) {
		payload := bytes.Repeat([]byte("x"), 64)
		zr := compress(t, payload)
		defer zr.Close() //nolint:errcheck // test cleanup
		var got bytes.Buffer
		written, err := copyBoundedRestoreSQL(&got, zr, 32)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized SQL error = %v, want size-limit error", err)
		}
		if written != 33 || got.Len() != 33 {
			t.Fatalf("oversized SQL copied %d bytes (buffer=%d), want 33-byte probe", written, got.Len())
		}
	})
}

func TestPostgresRestoreDisablesPSQLRCAndForcesDurableFinalCommit(t *testing.T) {
	args := postgresRestoreArgs("postgres://example/db")
	if !slices.Contains(args, "-X") {
		t.Fatalf("psql args = %q, want -X", args)
	}
	if !slices.Contains(args, "--single-transaction") || !slices.Contains(args, "--set=ON_ERROR_STOP=1") {
		t.Fatalf("psql args lost atomic/fail-fast flags: %q", args)
	}

	input, err := io.ReadAll(postgresRestoreInput("DROP SQL", strings.NewReader("DUMP SQL")))
	if err != nil {
		t.Fatal(err)
	}
	got := string(input)
	if !strings.HasPrefix(got, "DROP SQL\nDUMP SQL") {
		t.Fatalf("restore input order = %q", got)
	}
	if !strings.HasSuffix(got, postgresRestoreDurableCommitSQL) {
		t.Fatalf("restore input does not end with durable commit guard: %q", got)
	}
}

func TestPublishPostgresBackupNeverReplacesCollision(t *testing.T) {
	dir := t.TempDir()
	stamp := time.Date(2026, 8, 12, 1, 2, 3, 4, time.UTC)
	first := filepath.Join(dir, postgresBackupFilename("prod", stamp))
	if err := os.WriteFile(first, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".staged.sql.gz")
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := publishPostgresBackupWithSync(
		context.Background(), staged, dir, "prod", stamp, func(string) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if published == first {
		t.Fatal("publisher replaced the colliding backup")
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "existing" {
		t.Fatalf("existing backup changed: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(published); err != nil || string(got) != "new" {
		t.Fatalf("published backup = %q err=%v", got, err)
	}
}

func TestPublishPostgresBackupSyncFailureKeepsPublishedPath(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, ".staged.sql.gz")
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("injected directory sync failure")
	published, err := publishPostgresBackupWithSync(
		context.Background(), staged, dir, "prod", time.Now(), func(string) error { return want },
	)
	if !errors.Is(err, want) || published == "" {
		t.Fatalf("publish result = (%q, %v), want path plus injected error", published, err)
	}
	if got, readErr := os.ReadFile(published); readErr != nil || string(got) != "new" {
		t.Fatalf("published backup lost after sync error: %q err=%v", got, readErr)
	}
}
