package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestAttachmentFilesFollowTransactionOutcome(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	filesDir := filepath.Join(t.TempDir(), "files")
	db.SetFilesDir(filesDir)
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}

	ownerID := uuid.New()
	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := db.UploadAttachment(txCtx, "catalog", "Товары", ownerID,
		"rollback.txt", "text/plain", "", bytes.NewBufferString("rollback"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	rolledBackPath := filepath.Join(filesDir, "Товары", rolledBack.ID.String())
	if _, err := os.Stat(rolledBackPath); err != nil {
		t.Fatalf("uploaded file missing before rollback: %v", err)
	}
	if err := tx.Rollback(txCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rolledBackPath); !os.IsNotExist(err) {
		t.Fatalf("rolled-back upload left file on disk: %v", err)
	}
	if _, err := db.GetAttachment(ctx, rolledBack.ID); !IsNotFound(err) {
		t.Fatalf("rolled-back upload left metadata: %v", err)
	}

	kept, err := db.UploadAttachment(ctx, "catalog", "Товары", ownerID,
		"keep.txt", "text/plain", "", bytes.NewBufferString("keep"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	keptPath := filepath.Join(filesDir, "Товары", kept.ID.String())
	tx, txCtx, err = db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAttachment(txCtx, kept.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("file removed before transaction outcome: %v", err)
	}
	if err := tx.Rollback(txCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAttachment(ctx, kept.ID); err != nil {
		t.Fatalf("rollback did not restore attachment metadata: %v", err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("rollback did not preserve attachment file: %v", err)
	}

	tx, txCtx, err = db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAttachment(txCtx, kept.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(txCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keptPath); !os.IsNotExist(err) {
		t.Fatalf("committed delete left file on disk: %v", err)
	}
	if _, err := db.GetAttachment(ctx, kept.ID); !IsNotFound(err) {
		t.Fatalf("committed delete left metadata: %v", err)
	}
}

func TestQueuedAttachmentDeletionSurvivesFailure(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	filesDir := filepath.Join(t.TempDir(), "files")
	db.SetFilesDir(filesDir)
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatal(err)
	}
	const owner = "Товары"
	const queue = "expired-scans"
	att, err := db.UploadAttachment(ctx, "catalog", owner, uuid.New(), "test.txt",
		"text/plain", "", bytes.NewBufferString("test"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, txCtx, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueueAttachmentDeletion(txCtx, att.ID, queue); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FlushAttachmentDeletionQueue(txCtx, owner, queue); err == nil {
		t.Fatal("physical delete ran inside a transaction")
	}
	if err := tx.Rollback(txCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAttachment(ctx, att.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := db.FlushAttachmentDeletionQueue(ctx, owner, queue); err != nil || result.Deleted != 0 {
		t.Fatalf("rollback left a pending job: %+v, %v", result, err)
	}

	if err := db.QueueAttachmentDeletion(ctx, att.ID, queue); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAttachment(ctx, att.ID); !IsNotFound(err) {
		t.Fatalf("queued attachment is still listed: %v", err)
	}
	path := filepath.Join(filesDir, owner, att.ID.String())
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "test.txt")
	if err := os.WriteFile(marker, []byte("block deletion"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := db.FlushAttachmentDeletionQueue(ctx, owner, queue)
	if err != nil || result.Deleted != 0 || result.Failed != 1 || result.LastError == "" {
		t.Fatalf("failed deletion reported incorrectly: %+v, %v", result, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	result, err = db.FlushAttachmentDeletionQueue(ctx, owner, queue)
	if err != nil || result.Deleted != 1 || result.Failed != 0 {
		t.Fatalf("queued retry: %+v, %v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("retry left the file: %v", err)
	}
}
