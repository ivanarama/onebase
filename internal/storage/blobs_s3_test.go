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

// memBlobStore is an in-memory BlobObjectStore for exercising the S3 branch of
// PutBlob/OpenBlob/DeleteBlob without a real bucket.
type memBlobStore struct {
	objs              map[string][]byte
	putErr            error
	deleteErr         error
	deleteHadDeadline bool
}

func newMemBlobStore() *memBlobStore { return &memBlobStore{objs: map[string][]byte{}} }

func (m *memBlobStore) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if m.putErr != nil {
		return m.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.objs[key] = b
	return nil
}

func (m *memBlobStore) GetObject(_ context.Context, key string) (io.ReadCloser, int64, error) {
	b, ok := m.objs[key]
	if !ok {
		return nil, 0, errors.New("memBlobStore: not found")
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (m *memBlobStore) DeleteObject(ctx context.Context, key string) error {
	_, m.deleteHadDeadline = ctx.Deadline()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.objs, key)
	return nil
}

func (m *memBlobStore) OpenReadSeeker(_ context.Context, key string, _ int64) io.ReadSeekCloser {
	return nopSeekCloser{bytes.NewReader(m.objs[key])}
}

// nopSeekCloser adds a no-op Close to a *bytes.Reader (which is a ReadSeeker).
type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

func blobLoc(t *testing.T, db *DB, id uuid.UUID) (loc string, dataLen int) {
	t.Helper()
	var data []byte
	err := db.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT loc, data FROM _blobs WHERE id=%s`, db.dialect.Placeholder(1)),
		id.String()).Scan(&loc, &data)
	if err != nil {
		t.Fatalf("read _blobs loc: %v", err)
	}
	return loc, len(data)
}

func newS3TestDB(t *testing.T) (*DB, *memBlobStore) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := ConnectSQLite(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	db.filesDir = filepath.Join(dir, "files")
	if err := db.EnsureBlobTable(ctx); err != nil {
		t.Fatalf("EnsureBlobTable: %v", err)
	}
	store := newMemBlobStore()
	db.SetBlobStore(store, "px/")
	if err := db.SaveFileStorageMode(ctx, FileStorageS3); err != nil {
		t.Fatalf("SaveFileStorageMode: %v", err)
	}
	if got := db.GetFileStorageMode(ctx); got != FileStorageS3 {
		t.Fatalf("режим = %q, ожидался s3", got)
	}
	return db, store
}

func TestBlobRoundtrip_S3(t *testing.T) {
	ctx := context.Background()
	db, store := newS3TestDB(t)
	payload := []byte("\x89PNG\r\n\x1a\n s3-картинка")

	b, err := db.PutBlob(ctx, "image/png", bytes.NewReader(payload), 1<<20, BlobOwner{})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	key := "px/blobs/" + b.ID.String()
	if got := store.objs[key]; !bytes.Equal(got, payload) {
		t.Fatalf("объект в S3 по ключу %q не совпал (%d байт)", key, len(got))
	}
	// В s3-режиме файла на диске быть не должно и данных в колонке — тоже.
	if _, err := os.Stat(filepath.Join(db.filesDir, blobsDirName, b.ID.String())); !os.IsNotExist(err) {
		t.Fatalf("в s3-режиме файла на диске быть не должно, stat err=%v", err)
	}
	if loc, dataLen := blobLoc(t, db, b.ID); loc != FileStorageS3 || dataLen != 0 {
		t.Fatalf("ожидал loc=s3, пустой data; got loc=%q dataLen=%d", loc, dataLen)
	}
	// OpenBlob отдаёт содержимое из S3.
	if got := readBlobBytes(t, db, b.ID); !bytes.Equal(got, payload) {
		t.Fatalf("OpenBlob(s3) содержимое не совпало: %d байт", len(got))
	}
	// DeleteBlob убирает объект и строку.
	if err := db.DeleteBlob(ctx, b.ID); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}
	if _, ok := store.objs[key]; ok {
		t.Fatal("объект S3 не удалён при DeleteBlob")
	}
	if _, _, err := db.OpenBlob(ctx, b.ID); err == nil {
		t.Fatal("OpenBlob после удаления должен вернуть ошибку")
	}
}

// TestBlob_S3ModeSwitchKeepsDiskBlob: блоб, записанный на диск, остаётся читаемым
// после переключения базы в режим s3 (маршрутизация по loc, не по текущему режиму).
func TestBlob_S3ModeSwitchKeepsDiskBlob(t *testing.T) {
	ctx := context.Background()
	db, _ := newS3TestDB(t)
	// Временно вернём disk-режим и запишем блоб на диск.
	if err := db.SaveFileStorageMode(ctx, FileStorageDisk); err != nil {
		t.Fatal(err)
	}
	payload := []byte("disk-blob")
	b, err := db.PutBlob(ctx, "image/png", bytes.NewReader(payload), 1<<20, BlobOwner{})
	if err != nil {
		t.Fatalf("PutBlob(disk): %v", err)
	}
	if loc, _ := blobLoc(t, db, b.ID); loc != FileStorageDisk {
		t.Fatalf("ожидал loc=disk, got %q", loc)
	}
	// Переключаемся в s3 — старый disk-блоб всё ещё открывается с диска.
	if err := db.SaveFileStorageMode(ctx, FileStorageS3); err != nil {
		t.Fatal(err)
	}
	if got := readBlobBytes(t, db, b.ID); !bytes.Equal(got, payload) {
		t.Fatalf("disk-блоб после переключения в s3 не прочитался: %q", got)
	}
}

func TestBlob_S3NotConfigured(t *testing.T) {
	ctx := context.Background()
	db, store := newS3TestDB(t)

	// Put без клиента → понятная ошибка.
	db.SetBlobStore(nil, "")
	if _, err := db.PutBlob(ctx, "image/png", bytes.NewReader([]byte("x")), 1<<20, BlobOwner{}); err == nil {
		t.Fatal("PutBlob в s3-режиме без клиента должен вернуть ошибку")
	}

	// Запишем s3-блоб с клиентом, затем уберём клиент — Open/Delete должны явно
	// ошибиться, а не тихо потерять объект.
	db.SetBlobStore(store, "px/")
	b, err := db.PutBlob(ctx, "image/png", bytes.NewReader([]byte("y")), 1<<20, BlobOwner{})
	if err != nil {
		t.Fatalf("PutBlob: %v", err)
	}
	db.SetBlobStore(nil, "")
	if _, _, err := db.OpenBlob(ctx, b.ID); err == nil {
		t.Fatal("OpenBlob для s3-блоба без клиента должен вернуть ошибку")
	}
	if err := db.DeleteBlob(ctx, b.ID); err == nil {
		t.Fatal("DeleteBlob для s3-блоба без клиента должен вернуть ошибку")
	}
}

func TestBlob_S3PutErrorNoRow(t *testing.T) {
	ctx := context.Background()
	db, store := newS3TestDB(t)
	store.putErr = errors.New("network down")
	if _, err := db.PutBlob(ctx, "image/png", bytes.NewReader([]byte("z")), 1<<20, BlobOwner{}); err == nil {
		t.Fatal("PutBlob должен вернуть ошибку при сбое S3")
	}
	// При сбое заливки строка _blobs не должна появиться.
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM _blobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("после сбоя S3 в _blobs не должно быть строк, got %d", n)
	}
}
