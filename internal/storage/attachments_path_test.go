package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestUploadAttachmentRejectsUnsafeOwnerNameBeforeWriting(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.SetFilesDir(filepath.Join(dir, "files"))
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatalf("EnsureAttachmentTable: %v", err)
	}

	for _, ownerName := range []string{"../escape", `..\\escape`, "/absolute", `C:\\escape`, "_blobs", "_ATTACH_TMP", "CON", "LPT1.txt", "trailing."} {
		t.Run(ownerName, func(t *testing.T) {
			_, err := db.UploadAttachment(ctx, "document", ownerName, uuid.New(), "x.txt", "text/plain", "tester", bytes.NewReader([]byte("x")), 1024)
			if err == nil {
				t.Fatal("unsafe owner_name was accepted")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatalf("unsafe upload wrote outside files root: %v", err)
	}
}

func TestAttachmentOperationsRejectCorruptOwnerName(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.SetFilesDir(filepath.Join(dir, "files"))
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatalf("EnsureAttachmentTable: %v", err)
	}

	a, err := db.UploadAttachment(ctx, "document", "Orders", uuid.New(), "x.txt", "text/plain", "tester", bytes.NewReader([]byte("x")), 1024)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if _, err := db.Exec(ctx, "UPDATE _attachments SET owner_name='../escape' WHERE id=?", a.ID.String()); err != nil {
		t.Fatalf("corrupt owner_name: %v", err)
	}
	if _, _, err := db.OpenAttachment(ctx, a.ID); err == nil {
		t.Fatal("OpenAttachment accepted corrupt owner_name")
	}
	if err := db.DeleteAttachment(ctx, a.ID); err == nil {
		t.Fatal("DeleteAttachment accepted corrupt owner_name")
	}
}
