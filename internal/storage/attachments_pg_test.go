//go:build integration

package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestAttachments_PostgresUUIDLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	db.SetFilesDir(filepath.Join(t.TempDir(), "files"))
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}

	ownerID := uuid.New()
	ownerName := "AttachmentPG" + uuid.NewString()
	attachment, err := db.UploadAttachment(ctx, "catalog", ownerName, ownerID,
		"uuid.txt", "text/plain", "integration", bytes.NewBufferString("postgres"), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	t.Cleanup(func() {
		_ = db.DeleteAttachment(context.Background(), attachment.ID)
	})

	list, err := db.ListAttachments(ctx, "catalog", ownerName, ownerID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(list) != 1 || list[0].ID != attachment.ID || list[0].OwnerID != ownerID {
		t.Fatalf("ListAttachments = %+v", list)
	}
	got, err := db.GetAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if got.OwnerID != ownerID || got.Filename != "uuid.txt" {
		t.Fatalf("GetAttachment = %+v", got)
	}
	if err := db.DeleteAttachment(ctx, attachment.ID); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	if _, err := db.GetAttachment(ctx, attachment.ID); !IsNotFound(err) {
		t.Fatalf("deleted attachment lookup = %v, want not found", err)
	}
}
