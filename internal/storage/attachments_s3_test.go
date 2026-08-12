package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func newS3AttachDB(t *testing.T) (*DB, *memBlobStore) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "att.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureAttachmentTable(ctx); err != nil {
		t.Fatalf("EnsureAttachmentTable: %v", err)
	}
	store := newMemBlobStore()
	db.SetBlobStore(store, "px/")
	if err := db.SaveFileStorageMode(ctx, FileStorageS3); err != nil {
		t.Fatalf("SaveFileStorageMode: %v", err)
	}
	return db, store
}

func TestAttachmentRoundtrip_S3(t *testing.T) {
	ctx := context.Background()
	db, store := newS3AttachDB(t)
	owner := uuid.New()
	payload := []byte("вложение в S3, произвольные байты \x00\x01")

	att, err := db.UploadAttachment(ctx, "document", "order", owner, "file.txt", "text/plain", "ivan", bytes.NewReader(payload), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if att.Loc != FileStorageS3 {
		t.Fatalf("loc = %q, want s3", att.Loc)
	}
	key := "px/attachments/order/" + att.ID.String()
	if got := store.objs[key]; !bytes.Equal(got, payload) {
		t.Fatalf("object at %q не совпал (%d байт)", key, len(got))
	}
	// На диске вложения быть не должно.
	if _, err := os.Stat(filepath.Join(db.filesDir, "order", att.ID.String())); !os.IsNotExist(err) {
		t.Fatalf("в s3-режиме файла на диске быть не должно: %v", err)
	}

	// OpenAttachment → seekable reader с содержимым.
	rsc, meta, err := db.OpenAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("OpenAttachment: %v", err)
	}
	if meta.Filename != "file.txt" {
		t.Errorf("filename = %q", meta.Filename)
	}
	got, _ := io.ReadAll(rsc)
	// Проверим, что ридер seekable (ServeContent так делает).
	if _, err := rsc.Seek(0, io.SeekStart); err != nil {
		t.Errorf("reader must be seekable: %v", err)
	}
	_ = rsc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("OpenAttachment содержимое не совпало: %q", got)
	}

	// MaterializeAttachment → путь с basename = ИД, cleanup удаляет.
	path, cleanup, _, err := db.MaterializeAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("MaterializeAttachment: %v", err)
	}
	if filepath.Base(path) != att.ID.String() {
		t.Errorf("materialized basename = %q, want %s", filepath.Base(path), att.ID)
	}
	if content, _ := os.ReadFile(path); !bytes.Equal(content, payload) {
		t.Errorf("materialized content не совпал")
	}
	if cleanup == nil {
		t.Fatal("s3 materialize должен вернуть непустой cleanup")
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup должен удалить temp: %v", err)
	}

	// DeleteAttachment убирает объект.
	if err := db.DeleteAttachment(ctx, att.ID); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	if _, ok := store.objs[key]; ok {
		t.Fatal("объект S3 не удалён при DeleteAttachment")
	}
}

func TestAttachmentRejectsS3SizeMismatch(t *testing.T) {
	ctx := context.Background()
	db, store := newS3AttachDB(t)
	att, err := db.UploadAttachment(ctx, "document", "order", uuid.New(), "file.txt", "text/plain", "ivan", bytes.NewReader([]byte("original")), 1024)
	if err != nil {
		t.Fatal(err)
	}
	store.objs["px/attachments/order/"+att.ID.String()] = []byte("x")
	if _, _, err := db.OpenAttachment(ctx, att.ID); err == nil {
		t.Fatal("OpenAttachment must reject an S3 object whose size disagrees with metadata")
	}
	if _, _, _, err := db.MaterializeAttachment(ctx, att.ID); err == nil {
		t.Fatal("MaterializeAttachment must reject an S3 object whose size disagrees with metadata")
	}
}

func TestAttachmentRejectsDiskSizeMismatch(t *testing.T) {
	ctx := context.Background()
	db, _ := newS3AttachDB(t)
	if err := db.SaveFileStorageMode(ctx, FileStorageDisk); err != nil {
		t.Fatal(err)
	}
	att, err := db.UploadAttachment(ctx, "document", "order", uuid.New(), "file.txt", "text/plain", "ivan", bytes.NewReader([]byte("original")), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(db.filesDir, "order", att.ID.String()), 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.OpenAttachment(ctx, att.ID); err == nil {
		t.Fatal("OpenAttachment must reject a disk file whose size disagrees with metadata")
	}
	if _, _, _, err := db.MaterializeAttachment(ctx, att.ID); err == nil {
		t.Fatal("MaterializeAttachment must reject a disk file whose size disagrees with metadata")
	}
}

func TestAttachment_S3Streaming(t *testing.T) {
	ctx := context.Background()
	db, _ := newS3AttachDB(t)
	db.SetBlobStreaming(true)
	owner := uuid.New()
	payload := []byte("streamed attachment, длинновато чтобы подвигать seek туда-сюда")

	att, err := db.UploadAttachment(ctx, "document", "order", owner, "s.txt", "text/plain", "ivan", bytes.NewReader(payload), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	rsc, _, err := db.OpenAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("OpenAttachment(stream): %v", err)
	}
	defer func() { _ = rsc.Close() }()
	// Seekable (ServeContent так делает): в конец за размером, потом в начало.
	if sz, err := rsc.Seek(0, io.SeekEnd); err != nil || sz != int64(len(payload)) {
		t.Fatalf("Seek end = %d, %v; want %d", sz, err, len(payload))
	}
	if _, err := rsc.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rsc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("streamed content mismatch: %q", got)
	}
	// В streaming-режиме временная копия не создаётся.
	if _, err := os.Stat(filepath.Join(db.filesDir, "_attach_tmp")); !os.IsNotExist(err) {
		t.Errorf("streaming не должен создавать _attach_tmp: %v", err)
	}
}

func TestAttachment_S3ModeSwitchKeepsDisk(t *testing.T) {
	ctx := context.Background()
	db, _ := newS3AttachDB(t)
	// Запишем вложение на диск (временно вернув disk-режим).
	if err := db.SaveFileStorageMode(ctx, FileStorageDisk); err != nil {
		t.Fatal(err)
	}
	owner := uuid.New()
	payload := []byte("disk attachment")
	att, err := db.UploadAttachment(ctx, "document", "order", owner, "d.txt", "text/plain", "ivan", bytes.NewReader(payload), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment(disk): %v", err)
	}
	if att.Loc != FileStorageDisk {
		t.Fatalf("loc = %q, want disk", att.Loc)
	}
	// Переключаемся в s3 — disk-вложение всё ещё открывается и материализуется с диска.
	if err := db.SaveFileStorageMode(ctx, FileStorageS3); err != nil {
		t.Fatal(err)
	}
	rsc, _, err := db.OpenAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("OpenAttachment(disk after switch): %v", err)
	}
	got, _ := io.ReadAll(rsc)
	_ = rsc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("disk-вложение после переключения не прочиталось: %q", got)
	}
	path, cleanup, _, err := db.MaterializeAttachment(ctx, att.ID)
	if err != nil {
		t.Fatalf("MaterializeAttachment(disk): %v", err)
	}
	if cleanup != nil {
		t.Error("disk materialize должен возвращать nil cleanup (это реальный путь)")
	}
	if path != filepath.Join(db.filesDir, "order", att.ID.String()) {
		t.Errorf("disk materialize path = %q", path)
	}
}

func TestAttachment_S3NotConfigured(t *testing.T) {
	ctx := context.Background()
	db, store := newS3AttachDB(t)
	owner := uuid.New()

	db.SetBlobStore(nil, "")
	if _, err := db.UploadAttachment(ctx, "document", "order", owner, "x.txt", "text/plain", "ivan", bytes.NewReader([]byte("x")), 1<<20); err == nil {
		t.Fatal("UploadAttachment в s3-режиме без клиента должен вернуть ошибку")
	}

	// Загрузим с клиентом, затем уберём — Open/Delete должны явно ошибиться.
	db.SetBlobStore(store, "px/")
	att, err := db.UploadAttachment(ctx, "document", "order", owner, "y.txt", "text/plain", "ivan", bytes.NewReader([]byte("y")), 1<<20)
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	db.SetBlobStore(nil, "")
	if _, _, err := db.OpenAttachment(ctx, att.ID); err == nil {
		t.Fatal("OpenAttachment s3-вложения без клиента должен вернуть ошибку")
	}
	if err := db.DeleteAttachment(ctx, att.ID); err == nil {
		t.Fatal("DeleteAttachment s3-вложения без клиента должен вернуть ошибку")
	}
}
