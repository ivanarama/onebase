package storage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/userdata"
)

func TestPortableSQLiteAndDiskBlobMoveTogether(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first bundle")
	second := filepath.Join(t.TempDir(), "second bundle")
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(userdata.PortableRootEnv, first)
	db, err := ConnectSQLite(t.Context(), filepath.Join(first, "sample.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureServiceSchema(t.Context()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	payload := []byte("portable attachment")
	blob, err := db.PutBlob(t.Context(), "text/plain", bytes.NewReader(payload), 1024, BlobOwner{})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first, ".onebase", "files", "sample", "_blobs", blob.ID.String())); err != nil {
		db.Close()
		t.Fatalf("blob not saved in portable filesystem: %v", err)
	}
	db.Close()
	if err := os.Rename(first, second); err != nil {
		t.Fatal(err)
	}
	t.Setenv(userdata.PortableRootEnv, second)
	db, err = ConnectSQLite(t.Context(), filepath.Join(second, "sample.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, reader, err := db.OpenBlob(t.Context(), blob.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	actual, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("moved blob=%q err=%v", actual, err)
	}
}
