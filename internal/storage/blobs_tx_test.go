package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Blob metadata lives in the database, while disk/S3 bytes do not. A later
// object-save error must therefore roll both parts back together; otherwise the
// external object has no _blobs row and cannot even be found by gc-blobs.
func TestBlobExternalContentFollowsTransactionOutcome(t *testing.T) {
	ctx := context.Background()
	t.Run("disk", func(t *testing.T) {
		db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "blobs.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		filesDir := filepath.Join(t.TempDir(), "files")
		db.SetFilesDir(filesDir)
		if err := db.EnsureBlobTable(ctx); err != nil {
			t.Fatal(err)
		}

		tx, txCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rolledBack, err := db.PutBlob(txCtx, "image/png", bytes.NewBufferString("rollback"), 1<<20, BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		rolledBackPath := filepath.Join(filesDir, blobsDirName, rolledBack.ID.String())
		if _, err := os.Stat(rolledBackPath); err != nil {
			t.Fatalf("blob file missing before rollback: %v", err)
		}
		if err := tx.Rollback(txCtx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(rolledBackPath); !os.IsNotExist(err) {
			t.Fatalf("rolled-back blob left file on disk: %v", err)
		}
		if _, _, err := db.OpenBlob(ctx, rolledBack.ID); err == nil {
			t.Fatal("rolled-back blob left metadata")
		}

		tx, txCtx, err = db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		kept, err := db.PutBlob(txCtx, "image/png", bytes.NewBufferString("commit"), 1<<20, BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		keptPath := filepath.Join(filesDir, blobsDirName, kept.ID.String())
		if err := tx.Commit(txCtx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(keptPath); err != nil {
			t.Fatalf("commit removed blob file: %v", err)
		}
		if _, rc, err := db.OpenBlob(ctx, kept.ID); err != nil {
			t.Fatalf("committed blob metadata missing: %v", err)
		} else {
			_ = rc.Close()
		}
	})

	t.Run("s3", func(t *testing.T) {
		db, store := newS3TestDB(t)
		tx, txCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		rolledBack, err := db.PutBlob(txCtx, "image/png", bytes.NewBufferString("rollback"), 1<<20, BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		rolledBackKey := "px/blobs/" + rolledBack.ID.String()
		if _, ok := store.objs[rolledBackKey]; !ok {
			t.Fatal("S3 object missing before rollback")
		}
		if err := tx.Rollback(txCtx); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.objs[rolledBackKey]; ok {
			t.Fatal("rolled-back blob left S3 object")
		}
		if _, _, err := db.OpenBlob(ctx, rolledBack.ID); err == nil {
			t.Fatal("rolled-back S3 blob left metadata")
		}

		tx, txCtx, err = db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		kept, err := db.PutBlob(txCtx, "image/png", bytes.NewBufferString("commit"), 1<<20, BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		keptKey := "px/blobs/" + kept.ID.String()
		if err := tx.Commit(txCtx); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.objs[keptKey]; !ok {
			t.Fatal("commit removed S3 object")
		}
	})
}
