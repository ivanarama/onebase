package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// TestBlobRoundtrip_DiskAndDB проверяет оба режима хранения бинарников (как тома
// на диске и в БД в 1С): запись, чтение, наличие/отсутствие файла на диске,
// удаление.
func TestBlobRoundtrip_DiskAndDB(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}

	payload := []byte("\x89PNG\r\n\x1a\n притворимся картинкой")

	t.Run("disk", func(t *testing.T) {
		b, err := db.PutBlob(ctx, "image/png", bytes.NewReader(payload), 1<<20, BlobOwner{})
		if err != nil {
			t.Fatalf("PutBlob: %v", err)
		}
		if b.Mime != "image/png" || b.Size != int64(len(payload)) {
			t.Fatalf("метаданные blob: mime=%q size=%d", b.Mime, b.Size)
		}
		fp := filepath.Join(db.filesDir, blobsDirName, b.ID.String())
		if _, err := os.Stat(fp); err != nil {
			t.Fatalf("на диске нет файла бинарника: %v", err)
		}
		if got := readBlobBytes(t, db, b.ID); !bytes.Equal(got, payload) {
			t.Fatalf("содержимое не совпало: %d байт", len(got))
		}
		if err := db.DeleteBlob(ctx, b.ID); err != nil {
			t.Fatalf("DeleteBlob: %v", err)
		}
		if _, _, err := db.OpenBlob(ctx, b.ID); err == nil {
			t.Fatal("OpenBlob после удаления должен вернуть ошибку")
		}
	})

	t.Run("db", func(t *testing.T) {
		if err := db.SaveFileStorageMode(ctx, FileStorageDB); err != nil {
			t.Fatalf("SaveFileStorageMode: %v", err)
		}
		if got := db.GetFileStorageMode(ctx); got != FileStorageDB {
			t.Fatalf("режим = %q, ожидался db", got)
		}
		b, err := db.PutBlob(ctx, "image/jpeg", bytes.NewReader(payload), 1<<20, BlobOwner{})
		if err != nil {
			t.Fatalf("PutBlob(db): %v", err)
		}
		fp := filepath.Join(db.filesDir, blobsDirName, b.ID.String())
		if _, err := os.Stat(fp); !os.IsNotExist(err) {
			t.Fatalf("в db-режиме файла на диске быть не должно, stat err=%v", err)
		}
		if got := readBlobBytes(t, db, b.ID); !bytes.Equal(got, payload) {
			t.Fatalf("содержимое (db) не совпало: %d байт", len(got))
		}
	})
}

func TestOpenBlobZeroByteDBAndCorruptLocations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}
	if err := db.SaveFileStorageMode(ctx, FileStorageDB); err != nil {
		t.Fatalf("SaveFileStorageMode: %v", err)
	}

	empty, err := db.PutBlob(ctx, "application/octet-stream", bytes.NewReader(nil), 1, BlobOwner{})
	if err != nil {
		t.Fatalf("PutBlob(empty): %v", err)
	}
	if got := readBlobBytes(t, db, empty.ID); len(got) != 0 {
		t.Fatalf("empty db blob returned %d bytes", len(got))
	}

	d := db.dialect
	if _, err := db.Exec(ctx, fmt.Sprintf("UPDATE _blobs SET data=NULL WHERE id=%s", d.Placeholder(1)), empty.ID.String()); err != nil {
		t.Fatalf("corrupt data: %v", err)
	}
	if _, _, err := db.OpenBlob(ctx, empty.ID); err == nil {
		t.Fatal("loc=db with NULL data must fail closed")
	}
	if _, err := db.Exec(ctx, fmt.Sprintf("UPDATE _blobs SET loc='unknown', data=NULL WHERE id=%s", d.Placeholder(1)), empty.ID.String()); err != nil {
		t.Fatalf("corrupt location: %v", err)
	}
	if _, _, err := db.OpenBlob(ctx, empty.ID); err == nil {
		t.Fatal("unknown blob location must fail closed")
	}
}

func TestOpenBlobRejectsDiskSizeMismatch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}

	b, err := db.PutBlob(ctx, "application/octet-stream", bytes.NewReader([]byte("original")), 1024, BlobOwner{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(db.filesDir, blobsDirName, b.ID.String()), 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.OpenBlob(ctx, b.ID); err == nil {
		t.Fatal("OpenBlob must reject a disk file whose size disagrees with metadata")
	}
}

// TestBlobOwnerRoundtrip проверяет, что владелец (сущность), указанный при
// PutBlob, сохраняется и возвращается OpenBlob — основа авторизации отдачи
// картинок (IDOR): imageServe проверяет право чтения на сущность-владельца.
func TestBlobOwnerRoundtrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}

	payload := []byte("\x89PNG\r\n\x1a\n картинка с владельцем")
	owner := BlobOwner{Kind: "catalog", Entity: "Контрагенты"}
	b, err := db.PutBlob(ctx, "image/png", bytes.NewReader(payload), 1<<20, owner)
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	if b.OwnerKind != "catalog" || b.OwnerEntity != "Контрагенты" {
		t.Fatalf("PutBlob не вернул владельца: kind=%q entity=%q", b.OwnerKind, b.OwnerEntity)
	}

	got, rc, err := db.OpenBlob(ctx, b.ID)
	if err != nil {
		t.Fatalf("OpenBlob: %v", err)
	}
	if rc != nil {
		_ = rc.Close()
	}
	if got.OwnerKind != "catalog" || got.OwnerEntity != "Контрагенты" {
		t.Fatalf("OpenBlob потерял владельца: kind=%q entity=%q", got.OwnerKind, got.OwnerEntity)
	}

	// Пустой владелец (легаси / DSL СохранитьКартинку) сохраняется как пустые строки.
	b2, err := db.PutBlob(ctx, "image/png", bytes.NewReader(payload), 1<<20, BlobOwner{})
	if err != nil {
		t.Fatalf("PutBlob(без владельца): %v", err)
	}
	got2, rc2, err := db.OpenBlob(ctx, b2.ID)
	if err != nil {
		t.Fatalf("OpenBlob(без владельца): %v", err)
	}
	if rc2 != nil {
		_ = rc2.Close()
	}
	if got2.OwnerKind != "" || got2.OwnerEntity != "" {
		t.Fatalf("ожидался пустой владелец, получено kind=%q entity=%q", got2.OwnerKind, got2.OwnerEntity)
	}
}

func TestPutBlobReportsSizeLimit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{FileStorageDisk, FileStorageDB} {
		t.Run(mode, func(t *testing.T) {
			if err := db.SaveFileStorageMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			_, err := db.PutBlob(ctx, "image/png", bytes.NewReader([]byte("12345")), 4, BlobOwner{})
			if !errors.Is(err, ErrBlobTooLarge) {
				t.Fatalf("error = %v, want ErrBlobTooLarge", err)
			}
		})
	}
}

func readBlobBytes(t *testing.T, db *DB, id uuid.UUID) []byte {
	t.Helper()
	_, rc, err := db.OpenBlob(context.Background(), id)
	if err != nil {
		t.Fatalf("OpenBlob: %v", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return data
}
