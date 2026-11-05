package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/storage"
)

type attachmentTempStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	get     func(context.Context, []byte) (io.ReadCloser, int64, error)
	putErr  error
}

func (s *attachmentTempStore) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if s.putErr != nil {
		return s.putErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}

func (s *attachmentTempStore) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	b := bytes.Clone(s.objects[key])
	s.mu.Unlock()
	if s.get != nil {
		return s.get(ctx, b)
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (s *attachmentTempStore) DeleteObject(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *attachmentTempStore) OpenReadSeeker(_ context.Context, key string, _ int64) io.ReadSeekCloser {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &attachmentTempReader{bytes.NewReader(bytes.Clone(s.objects[key]))}
}

type attachmentTempReader struct{ *bytes.Reader }

func (*attachmentTempReader) Close() error { return nil }

func attachmentTempDB(t *testing.T) (*storage.DB, *attachmentTempStore) {
	t.Helper()
	db, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "attachments.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureAttachmentTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &attachmentTempStore{objects: make(map[string][]byte)}
	db.SetBlobStore(s, "temp-test")
	if err := db.SaveFileStorageMode(context.Background(), storage.FileStorageS3); err != nil {
		t.Fatal(err)
	}
	return db, s
}

func attachmentTempUpload(t *testing.T, db *storage.DB) storage.Attachment {
	t.Helper()
	a, err := db.UploadAttachment(context.Background(), "document", "order", uuid.New(), "sample.txt", "text/plain", "test", bytes.NewBufferString("attachment bytes"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The durable root is a sentinel file: even probing it as a directory fails.
// Both public read paths must work and leave its bytes and the cwd unchanged.
func TestS3AttachmentTempIndependentRoot(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	db, _ := attachmentTempDB(t)
	root := filepath.Join(t.TempDir(), "durable-sentinel")
	if err := os.WriteFile(root, []byte("durable"), 0o600); err != nil {
		t.Fatal(err)
	}
	db.SetFilesDir(root)
	a := attachmentTempUpload(t, db)
	path, cleanup, _, err := db.MaterializeAttachment(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got, err := os.ReadFile(path); err != nil || string(got) != "attachment bytes" {
		t.Fatalf("materialize: %q, %v", got, err)
	}
	for _, stream := range []bool{false, true} {
		db.SetBlobStreaming(stream)
		r, _, err := db.OpenAttachment(context.Background(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil || string(got) != "attachment bytes" {
			t.Fatalf("stream=%v: %q, %v, %v", stream, got, readErr, closeErr)
		}
	}
	if got, err := os.ReadFile(root); err != nil || string(got) != "durable" {
		t.Fatalf("durable root changed: %q, %v", got, err)
	}
	if entries, err := os.ReadDir(cwd); err != nil || len(entries) != 0 {
		t.Fatalf("cwd changed: %v, %v", entries, err)
	}
}

func attachmentTempSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	return dir
}

func attachmentTempNoCopies(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && (!e.IsDir() || filepath.Dir(path) != root) {
			t.Errorf("leaked temporary copy: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestS3AttachmentTempHandleLifetime(t *testing.T) {
	temp := attachmentTempSandbox(t)
	db1, _ := attachmentTempDB(t)
	db2, _ := attachmentTempDB(t)
	// Two handles share the same durable root, including a legacy temporary
	// tree which neither startup nor Close is allowed to sweep.
	root := t.TempDir()
	legacy := filepath.Join(root, "_attach_tmp", "legacy")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	db1.SetFilesDir(root)
	db2.SetFilesDir(root)
	a1, a2 := attachmentTempUpload(t, db1), attachmentTempUpload(t, db2)
	p1, cleanup1, _, err := db1.MaterializeAttachment(context.Background(), a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup1()
	r1, _, err := db1.OpenAttachment(context.Background(), a1.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r1.Close() }()
	r2, _, err := db2.OpenAttachment(context.Background(), a2.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Close() }()
	p2 := r2.(interface{ Name() string }).Name()
	root1, root2 := filepath.Dir(filepath.Dir(p1)), filepath.Dir(filepath.Dir(p2))
	if root1 == root2 || filepath.Dir(root1) != temp || filepath.Dir(root2) != temp {
		t.Fatalf("not private handle roots: %s, %s", root1, root2)
	}
	if info, err := os.Stat(root1); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("root must be private: %v, %v", info, err)
	}
	db1.Close()
	if got, err := os.ReadFile(p1); err != nil || string(got) != "attachment bytes" {
		t.Fatalf("Close removed live materialization: %q %v", got, err)
	}
	cleanup1()
	cleanup1()
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove own copy: %v", err)
	}
	for _, r := range []io.Reader{r1, r2} {
		if got, err := io.ReadAll(r); err != nil || string(got) != "attachment bytes" {
			t.Fatalf("another consumer was removed: %q %v", got, err)
		}
	}
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root1); !os.IsNotExist(err) {
		t.Fatalf("closed handle root leaked: %v", err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatalf("other handle affected: %v", err)
	}
	// Concurrent, idempotent DB/reader close must also work on Windows, where
	// removing an open file is forbidden.
	var wg sync.WaitGroup
	for _, close := range []func(){db2.Close, func() { _ = r2.Close() }, func() { _ = r2.Close() }} {
		wg.Add(1)
		go func() { defer wg.Done(); close() }()
	}
	wg.Wait()
	if _, err := os.Stat(root2); !os.IsNotExist(err) {
		t.Fatalf("second handle root leaked: %v", err)
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != "sentinel" {
		t.Fatalf("legacy durable tree changed: %q %v", got, err)
	}
}

type attachmentCancelReader struct {
	cancel context.CancelFunc
	read   bool
}

func (r *attachmentCancelReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	n := copy(p, "x")
	r.cancel()
	return n, nil
}

func TestS3AttachmentTempFailureCleanup(t *testing.T) {
	for _, fault := range []string{"short", "extra", "reported-size", "read-error", "cancel", "get-error"} {
		t.Run(fault, func(t *testing.T) {
			temp := attachmentTempSandbox(t)
			db, store := attachmentTempDB(t)
			a := attachmentTempUpload(t, db)
			for _, materialize := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				store.get = func(_ context.Context, b []byte) (io.ReadCloser, int64, error) {
					switch fault {
					case "short":
						return io.NopCloser(bytes.NewReader(b[:1])), -1, nil
					case "extra":
						return io.NopCloser(bytes.NewReader(append(b, 'x'))), -1, nil
					case "reported-size":
						return io.NopCloser(bytes.NewReader(b)), int64(len(b) + 1), nil
					case "read-error":
						return io.NopCloser(attachmentErrorReader{}), -1, nil
					case "cancel":
						return io.NopCloser(&attachmentCancelReader{cancel: cancel}), -1, nil
					default:
						return nil, 0, io.ErrUnexpectedEOF
					}
				}
				var err error
				if materialize {
					_, cleanup, _, e := db.MaterializeAttachment(ctx, a.ID)
					err = e
					if cleanup != nil {
						cleanup()
						t.Error("failed materialization returned ownership")
					}
				} else {
					r, _, e := db.OpenAttachment(ctx, a.ID)
					err = e
					if r != nil {
						_ = r.Close()
						t.Error("failed download returned reader")
					}
				}
				cancel()
				if err == nil {
					t.Fatalf("%s materialize=%v succeeded", fault, materialize)
				}
				attachmentTempNoCopies(t, temp)
			}
			db.Close()
			if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
				t.Fatalf("closed handle leaked: %v %v", entries, err)
			}
		})
	}
}

type attachmentErrorReader struct{}

func (attachmentErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestS3AttachmentTempUploadFailureCleanup(t *testing.T) {
	for _, fault := range []string{"limit", "read-error", "cancel", "put-error"} {
		t.Run(fault, func(t *testing.T) {
			temp := attachmentTempSandbox(t)
			db, store := attachmentTempDB(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var reader io.Reader = bytes.NewBufferString("payload")
			limit := int64(1024)
			switch fault {
			case "limit":
				limit = 1
			case "read-error":
				reader = attachmentErrorReader{}
			case "cancel":
				reader = &attachmentCancelReader{cancel: cancel}
			case "put-error":
				store.putErr = io.ErrUnexpectedEOF
			}
			owner := uuid.New()
			_, err := db.UploadAttachment(ctx, "document", "order", owner, "f.txt", "text/plain", "test", reader, limit)
			if err == nil {
				t.Fatal("faulty upload succeeded")
			}
			if fault == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if fault == "limit" && !errors.Is(err, storage.ErrAttachmentTooLarge) {
				t.Fatalf("size limit lost: %v", err)
			}
			rows, err := db.ListAttachments(context.Background(), "document", "order", owner)
			if err != nil || len(rows) != 0 || len(store.objects) != 0 {
				t.Fatalf("failed upload changed metadata/store: %v %v", rows, err)
			}
			attachmentTempNoCopies(t, temp)
			db.Close()
			if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
				t.Fatalf("closed handle leaked: %v %v", entries, err)
			}
		})
	}
}
