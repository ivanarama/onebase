package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

type filesGuardStore struct{ objects map[string][]byte }

func (s *filesGuardStore) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err == nil {
		s.objects[key] = b
	}
	return err
}
func (s *filesGuardStore) GetObject(_ context.Context, key string) (io.ReadCloser, int64, error) {
	b, ok := s.objects[key]
	if !ok {
		return nil, 0, errors.New("missing object")
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (s *filesGuardStore) DeleteObject(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

type filesGuardReader struct{ *bytes.Reader }

func (filesGuardReader) Close() error { return nil }
func (s *filesGuardStore) OpenReadSeeker(_ context.Context, key string, _ int64) io.ReadSeekCloser {
	return filesGuardReader{bytes.NewReader(s.objects[key])}
}

func filesGuardPrepare(t *testing.T, db *storage.DB) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "files")
	db.SetFilesDir(root)
	for _, ensure := range []func(context.Context) error{db.EnsureBlobTable, db.EnsureAttachmentTable} {
		if err := ensure(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func filesGuardExec(t *testing.T, db *storage.DB, ctx context.Context, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}
func filesGuardCount(t *testing.T, db *storage.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func filesGuardRead(t *testing.T, r io.ReadCloser, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Fatalf("content = %q", b)
	}
}
func filesGuardSentinel(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
}
func filesGuardCheckSentinel(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "sentinel" {
		t.Fatalf("sentinel %s changed: %q, %v", path, b, err)
	}
}

func TestFilesGuardModesAndLocationsMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		root := filesGuardPrepare(t, db)
		store := &filesGuardStore{objects: map[string][]byte{}}
		db.SetBlobStore(store, "guard")
		// Old disk records must use their loc after a switch to db/S3.
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDisk); err != nil {
			t.Fatal(err)
		}
		disk, err := db.PutBlob(ctx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		att, err := db.UploadAttachment(ctx, "catalog", "Items", uuid.New(), "disk.txt", "text/plain", "", strings.NewReader("payload"), 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{storage.FileStorageDB, storage.FileStorageS3} {
			if err := db.SaveFileStorageMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			_, r, err := db.OpenBlob(ctx, disk.ID)
			filesGuardRead(t, r, err)
			ar, _, err := db.OpenAttachment(ctx, att.ID)
			filesGuardRead(t, ar, err)
		}
		// Unknown loc must neither delete metadata nor touch an arbitrary disk file.
		filesGuardExec(t, db, ctx, "UPDATE _attachments SET loc='unknown'")
		if err := db.DeleteAttachment(ctx, att.ID); err == nil {
			t.Fatal("unknown attachment loc deleted")
		}
		filesGuardExec(t, db, ctx, "UPDATE _attachments SET loc=''") // legacy disk
		filesGuardExec(t, db, ctx, "UPDATE _blobs SET loc='unknown'")
		if err := db.DeleteBlob(ctx, disk.ID); err == nil {
			t.Fatal("unknown blob loc deleted")
		}
		filesGuardExec(t, db, ctx, "UPDATE _blobs SET loc=''") // legacy NULL data = disk
		if err := db.DeleteBlob(ctx, disk.ID); err != nil {
			t.Fatal(err)
		}
		if err := db.DeleteAttachment(ctx, att.ID); err != nil {
			t.Fatal(err)
		}
		// db and S3 blobs do not need the disk root; db deletion must not probe cwd.
		cwd := t.TempDir()
		t.Chdir(cwd)
		for _, mode := range []string{storage.FileStorageDB, storage.FileStorageS3} {
			db.SetFilesDir("")
			if err := db.SaveFileStorageMode(ctx, mode); err != nil {
				t.Fatal(err)
			}
			b, err := db.PutBlob(ctx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
			if err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(cwd, "_blobs", b.ID.String())
			filesGuardSentinel(t, sentinel)
			_, r, err := db.OpenBlob(ctx, b.ID)
			filesGuardRead(t, r, err)
			if mode == storage.FileStorageDB {
				filesGuardExec(t, db, ctx, "UPDATE _blobs SET loc=''") // legacy non-NULL data = db
			}
			if err := db.DeleteBlob(ctx, b.ID); err != nil {
				t.Fatal(err)
			}
			filesGuardCheckSentinel(t, sentinel)
		}
		// db attachments still require disk; S3 streaming and deletion do not.
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
			t.Fatal(err)
		}
		if _, err := db.UploadAttachment(ctx, "catalog", "Items", uuid.New(), "db.txt", "text/plain", "", strings.NewReader("payload"), 100); err == nil {
			t.Fatal("db attachment accepted missing root")
		}
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageS3); err != nil {
			t.Fatal(err)
		}
		s3att, err := db.UploadAttachment(ctx, "catalog", "Items", uuid.New(), "s3.txt", "text/plain", "", strings.NewReader("payload"), 100)
		if err != nil {
			t.Fatal(err)
		}
		db.SetBlobStreaming(true)
		ar, _, err := db.OpenAttachment(ctx, s3att.ID)
		filesGuardRead(t, ar, err)
		db.SetBlobStreaming(false)
		if _, _, err := db.OpenAttachment(ctx, s3att.ID); err == nil {
			t.Fatal("slice A materialization accepted missing root")
		}
		db.SetFilesDir(root)
		ar, _, err = db.OpenAttachment(ctx, s3att.ID)
		filesGuardRead(t, ar, err)
		db.SetFilesDir("")
		if err := db.DeleteAttachment(ctx, s3att.ID); err != nil {
			t.Fatal(err)
		}
		if len(store.objects) != 0 {
			t.Fatalf("objects left: %v", store.objects)
		}
	})
}

func TestFilesGuardRootFailurePreservesDataMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		root := filesGuardPrepare(t, db)
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDisk); err != nil {
			t.Fatal(err)
		}
		b, err := db.PutBlob(ctx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		a, err := db.UploadAttachment(ctx, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
		if err != nil {
			t.Fatal(err)
		}
		cwd := t.TempDir()
		t.Chdir(cwd)
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		t.Setenv("TMP", tmp)
		t.Setenv("TEMP", tmp)
		sentinels := []string{filepath.Join(cwd, "_blobs", b.ID.String()), filepath.Join(cwd, "Items", a.ID.String()), filepath.Join(tmp, "sentinel")}
		for _, s := range sentinels {
			filesGuardSentinel(t, s)
		}
		blocker := filepath.Join(t.TempDir(), "file")
		filesGuardSentinel(t, blocker)
		invalid := []string{"", "relative", blocker, filepath.Join(blocker, "child")}
		dangling := filepath.Join(t.TempDir(), "dangling")
		if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dangling); err == nil {
			invalid = append(invalid, dangling, filepath.Join(dangling, "child"))
		}

		if runtime.GOOS != "windows" {
			denied := t.TempDir()
			if err := os.Chmod(denied, 0000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				// The fixture is a directory: owner execute permission is required for cleanup.
				_ = os.Chmod(denied, 0700) //nolint:gosec // G302: restore private directory access, not executable file permissions.
			})
			if f, err := os.CreateTemp(denied, "probe"); err != nil {
				invalid = append(invalid, denied)
			} else {
				_ = f.Close()
				_ = os.Remove(f.Name())
			}
		}
		for _, bad := range invalid {
			t.Run(fmt.Sprintf("root=%q", bad), func(t *testing.T) {
				db.SetFilesDir(bad)
				if _, err := db.PutBlob(ctx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{}); err == nil {
					t.Fatal("PutBlob succeeded")
				}
				if _, err := db.UploadAttachment(ctx, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100); err == nil {
					t.Fatal("upload succeeded")
				}
				if _, r, err := db.OpenBlob(ctx, b.ID); err == nil {
					_ = r.Close()
					t.Fatal("open blob succeeded")
				}
				if r, _, err := db.OpenAttachment(ctx, a.ID); err == nil {
					_ = r.Close()
					t.Fatal("open attachment succeeded")
				}
				if _, _, _, err := db.MaterializeAttachment(ctx, a.ID); err == nil {
					t.Fatal("materialize succeeded")
				}
				if err := db.DeleteBlob(ctx, b.ID); err == nil {
					t.Fatal("delete blob succeeded")
				}
				if err := db.DeleteAttachment(ctx, a.ID); err == nil {
					t.Fatal("delete attachment succeeded")
				}
				if err := db.SaveFileStorageMode(ctx, storage.FileStorageDisk); err == nil {
					t.Fatal("save disk succeeded")
				}
				if filesGuardCount(t, db, "_blobs") != 1 || filesGuardCount(t, db, "_attachments") != 1 {
					t.Fatal("metadata changed")
				}
				for _, s := range sentinels {
					filesGuardCheckSentinel(t, s)
				}
			})
		}
		db.SetFilesDir(root)
		_, r, err := db.OpenBlob(ctx, b.ID)
		filesGuardRead(t, r, err)
		ar, _, err := db.OpenAttachment(ctx, a.ID)
		filesGuardRead(t, ar, err)
		mode, err := db.ReadFileStorageMode(ctx)
		if err != nil || mode != storage.FileStorageDisk {
			t.Fatalf("mode changed: %s %v", mode, err)
		}
		// A disk setting failure preserves a previous db setting as well.
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
			t.Fatal(err)
		}
		db.SetFilesDir("")
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDisk); err == nil {
			t.Fatal("save disk succeeded")
		}
		mode, err = db.ReadFileStorageMode(ctx)
		if err != nil || mode != storage.FileStorageDB {
			t.Fatalf("mode changed: %s %v", mode, err)
		}
	})
}

func TestFilesGuardModeErrorsAndMissingTableMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		root := filesGuardPrepare(t, db)
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
			t.Fatal(err)
		}
		filesGuardExec(t, db, ctx, "UPDATE _settings SET value='corrupt' WHERE key='ui.file_storage'")
		for _, badctx := range []context.Context{ctx, func() context.Context { c, cancel := context.WithCancel(ctx); cancel(); return c }()} {
			if _, err := db.PutBlob(badctx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{}); err == nil {
				t.Fatal("mode error allowed blob")
			}
			if _, err := db.UploadAttachment(badctx, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100); err == nil {
				t.Fatal("mode error allowed attachment")
			}
		}
		if filesGuardCount(t, db, "_blobs") != 0 || filesGuardCount(t, db, "_attachments") != 0 {
			t.Fatal("mode error wrote metadata")
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("mode error created root: %v", err)
		}
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageDB); err != nil {
			t.Fatal(err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, cancelErr := db.PutBlob(canceled, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
		if !errors.Is(cancelErr, context.Canceled) {
			t.Fatalf("cancellation masked: %v", cancelErr)
		}
		_, cancelErr = db.UploadAttachment(canceled, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
		if !errors.Is(cancelErr, context.Canceled) {
			t.Fatalf("attachment cancellation masked: %v", cancelErr)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("cancellation created root: %v", err)
		}
		if err := db.SaveFileStorageMode(ctx, "bad"); err == nil {
			t.Fatal("invalid save normalized to disk")
		}
		filesGuardExec(t, db, ctx, "DELETE FROM _settings WHERE key='ui.file_storage'")
		mode, err := db.ReadFileStorageMode(ctx)
		if err != nil || mode != storage.FileStorageDisk {
			t.Fatalf("missing key: %s %v", mode, err)
		}
		filesGuardExec(t, db, ctx, "DROP TABLE _settings")
		tx, txCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(txCtx) }()
		mode, err = db.ReadFileStorageMode(txCtx)
		if err != nil || mode != storage.FileStorageDisk {
			t.Fatalf("missing settings: %s %v", mode, err)
		}
		b, err := db.PutBlob(txCtx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		a, err := db.UploadAttachment(txCtx, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
		if err != nil {
			t.Fatal(err)
		}
		var one int
		if err := db.QueryRow(txCtx, "SELECT 1").Scan(&one); err != nil {
			t.Fatalf("transaction aborted: %v", err)
		}
		if err := tx.Rollback(txCtx); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{filepath.Join(root, "_blobs", b.ID.String()), filepath.Join(root, "Items", a.ID.String())} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("rollback left file: %s, %v", path, err)
			}
		}
	})
}

func TestFilesGuardPostgresSettingsIsolationAndPermissions(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		if !db.IsPostgres() {
			t.Skip("PostgreSQL schema and role contracts")
		}
		ctx := context.Background()
		root := filesGuardPrepare(t, db)
		if err := db.SaveFileStorageMode(ctx, storage.FileStorageS3); err != nil {
			t.Fatal(err)
		}
		var schema string
		if err := db.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		empty := storage.NewEphemeralSchemaName()
		if err := db.CreateSchema(ctx, empty); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.DropSchemaCascade(ctx, empty) }()
		tx, txCtx, err := db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(txCtx) }()
		filesGuardExec(t, db, txCtx, `SET LOCAL search_path TO "`+empty+`", "`+schema+`", public`)
		mode, err := db.ReadFileStorageMode(txCtx)
		if err != nil || mode != storage.FileStorageDisk {
			t.Fatalf("inherited fallback setting: %s, %v", mode, err)
		}
		b, err := db.PutBlob(txCtx, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveFileStorageMode(txCtx, storage.FileStorageDB); err != nil {
			t.Fatal(err)
		}
		mode, err = db.ReadFileStorageMode(txCtx)
		if err != nil || mode != storage.FileStorageDB {
			t.Fatalf("local save: %s, %v", mode, err)
		}
		var inherited string
		if err := db.QueryRow(txCtx, `SELECT value FROM "`+schema+`"._settings WHERE key='ui.file_storage'`).Scan(&inherited); err != nil || inherited != storage.FileStorageS3 {
			t.Fatalf("modified fallback settings: %s %v", inherited, err)
		}
		if err := tx.Rollback(txCtx); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, "_blobs", b.ID.String())); !os.IsNotExist(err) {
			t.Fatalf("rollback left blob: %v", err)
		}
		role := "files_guard_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		filesGuardExec(t, db, ctx, "CREATE ROLE "+role)
		defer func() {
			filesGuardExec(t, db, ctx, "REVOKE ALL ON SCHEMA "+schema+" FROM "+role)
			filesGuardExec(t, db, ctx, "DROP ROLE "+role)
		}()
		filesGuardExec(t, db, ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role)
		for _, op := range []func(context.Context) error{
			func(c context.Context) error {
				_, err := db.PutBlob(c, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
				return err
			},
			func(c context.Context) error {
				_, err := db.UploadAttachment(c, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
				return err
			},
		} {
			tx, tc, err := db.BeginTx(ctx)
			if err != nil {
				t.Fatal(err)
			}
			filesGuardExec(t, db, tc, "SET LOCAL ROLE "+role)
			err = op(tc)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
				t.Fatalf("permission failure masked: %v", err)
			}
			if err := tx.Rollback(tc); err != nil {
				t.Fatal(err)
			}
		}
		if filesGuardCount(t, db, "_blobs") != 0 || filesGuardCount(t, db, "_attachments") != 0 {
			t.Fatal("permission error wrote metadata")
		}
		entries, err := os.ReadDir(filepath.Join(root, "_blobs"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("permission error wrote disk: %v %v", entries, err)
		}
	})
}

func TestFilesGuardNestedTransactionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		root := filesGuardPrepare(t, db)
		store := &filesGuardStore{objects: map[string][]byte{}}
		db.SetBlobStore(store, "guard")
		for _, mode := range []string{storage.FileStorageDisk, storage.FileStorageDB, storage.FileStorageS3} {
			t.Run(mode, func(t *testing.T) {
				if err := db.SaveFileStorageMode(ctx, mode); err != nil {
					t.Fatal(err)
				}
				for _, commitOuter := range []bool{false, true} {
					t.Run(fmt.Sprintf("outerCommit=%t", commitOuter), func(t *testing.T) {
						tx, tc, err := db.BeginTx(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer func() { _ = tx.Rollback(tc) }()
						var kept, rolled storage.Blob
						var ka, ra storage.Attachment
						upload := func(c context.Context, b *storage.Blob, a *storage.Attachment) error {
							var err error
							*b, err = db.PutBlob(c, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
							if err != nil {
								return err
							}
							*a, err = db.UploadAttachment(c, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
							return err
						}
						expected := errors.New("rollback scope")
						if err := db.WithTxScope(tc, func(c context.Context) error {
							if err := upload(c, &rolled, &ra); err != nil {
								return err
							}
							return expected
						}); !errors.Is(err, expected) {
							t.Fatal(err)
						}
						if _, _, err := db.OpenBlob(tc, rolled.ID); !storage.IsNotFound(err) {
							t.Fatalf("nested rollback left blob: %v", err)
						}
						if _, err := db.GetAttachment(tc, ra.ID); !storage.IsNotFound(err) {
							t.Fatalf("nested rollback left attachment: %v", err)
						}
						if err := db.WithTxScope(tc, func(c context.Context) error { return upload(c, &kept, &ka) }); err != nil {
							t.Fatal(err)
						}
						if commitOuter {
							err = tx.Commit(tc)
						} else {
							err = tx.Rollback(tc)
						}
						if err != nil {
							t.Fatal(err)
						}
						_, r, err := db.OpenBlob(ctx, kept.ID)
						if commitOuter {
							filesGuardRead(t, r, err)
							ar, _, err := db.OpenAttachment(ctx, ka.ID)
							filesGuardRead(t, ar, err)
							// A nested deletion does not remove the file on savepoint commit;
							// only the outer transaction outcome controls attachment cleanup.
							delTx, dc, err := db.BeginTx(ctx)
							if err != nil {
								t.Fatal(err)
							}
							if err := db.WithTxScope(dc, func(c context.Context) error { return db.DeleteAttachment(c, ka.ID) }); err != nil {
								t.Fatal(err)
							}
							if err := delTx.Rollback(dc); err != nil {
								t.Fatal(err)
							}
							ar, _, err = db.OpenAttachment(ctx, ka.ID)
							filesGuardRead(t, ar, err)
							delTx, dc, err = db.BeginTx(ctx)
							if err != nil {
								t.Fatal(err)
							}
							if err := db.WithTxScope(dc, func(c context.Context) error { return db.DeleteAttachment(c, ka.ID) }); err != nil {
								t.Fatal(err)
							}
							if err := delTx.Commit(dc); err != nil {
								t.Fatal(err)
							}
							if _, err := db.GetAttachment(ctx, ka.ID); !storage.IsNotFound(err) {
								t.Fatalf("committed delete kept row: %v", err)
							}
							if err := db.DeleteBlob(ctx, kept.ID); err != nil {
								t.Fatal(err)
							}
						} else if !storage.IsNotFound(err) {
							t.Fatalf("outer rollback left blob: %v", err)
						}
						// Check bytes as well as rows, including released child scopes.
						for _, b := range []storage.Blob{kept, rolled} {
							if _, err := os.Stat(filepath.Join(root, "_blobs", b.ID.String())); !os.IsNotExist(err) {
								t.Fatalf("blob content left: %v", err)
							}
						}
						for _, a := range []storage.Attachment{ka, ra} {
							if _, err := os.Stat(filepath.Join(root, "Items", a.ID.String())); !os.IsNotExist(err) {
								t.Fatalf("attachment content left: %v", err)
							}
						}
						if len(store.objects) != 0 {
							t.Fatalf("external objects left: %v", store.objects)
						}
					})
				}
			})
		}
	})
}

func TestFilesGuardCleanupUsesCapturedRootAndBackendMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		root := filesGuardPrepare(t, db)
		for _, mode := range []string{storage.FileStorageDisk, storage.FileStorageS3} {
			t.Run(mode, func(t *testing.T) {
				db.SetFilesDir(root)
				oldStore := &filesGuardStore{objects: map[string][]byte{}}
				newStore := &filesGuardStore{objects: map[string][]byte{}}
				db.SetBlobStore(oldStore, "guard")
				if err := db.SaveFileStorageMode(ctx, mode); err != nil {
					t.Fatal(err)
				}
				tx, tc, err := db.BeginTx(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(tc) }()
				b, err := db.PutBlob(tc, "image/png", strings.NewReader("payload"), 100, storage.BlobOwner{})
				if err != nil {
					t.Fatal(err)
				}
				a, err := db.UploadAttachment(tc, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
				if err != nil {
					t.Fatal(err)
				}
				newRoot := t.TempDir()
				sentinels := []string{filepath.Join(newRoot, "_blobs", b.ID.String()), filepath.Join(newRoot, "Items", a.ID.String())}
				for _, s := range sentinels {
					filesGuardSentinel(t, s)
				}
				for key := range oldStore.objects {
					newStore.objects[key] = []byte("sentinel")
				}
				db.SetFilesDir(newRoot)
				db.SetBlobStore(newStore, "other-prefix")
				if err := tx.Rollback(tc); err != nil {
					t.Fatal(err)
				}
				for _, s := range sentinels {
					filesGuardCheckSentinel(t, s)
				}
				if len(oldStore.objects) != 0 {
					t.Fatalf("rollback did not clean captured backend: %v", oldStore.objects)
				}
				for _, b := range newStore.objects {
					if string(b) != "sentinel" {
						t.Fatal("rollback touched new backend")
					}
				}
				for _, path := range []string{filepath.Join(root, "_blobs", b.ID.String()), filepath.Join(root, "Items", a.ID.String())} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("rollback left original content: %s %v", path, err)
					}
				}
				// Attachment deletion defers its captured backend until commit.
				db.SetFilesDir(root)
				db.SetBlobStore(oldStore, "guard")
				a, err = db.UploadAttachment(ctx, "catalog", "Items", uuid.New(), "a.txt", "text/plain", "", strings.NewReader("payload"), 100)
				if err != nil {
					t.Fatal(err)
				}
				tx, tc, err = db.BeginTx(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.DeleteAttachment(tc, a.ID); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(newRoot, "Items", a.ID.String())
				filesGuardSentinel(t, sentinel)
				for key := range oldStore.objects {
					newStore.objects[key] = []byte("sentinel")
				}
				db.SetFilesDir(newRoot)
				db.SetBlobStore(newStore, "guard")
				if err := tx.Commit(tc); err != nil {
					t.Fatal(err)
				}
				filesGuardCheckSentinel(t, sentinel)
				if len(oldStore.objects) != 0 {
					t.Fatal("commit did not clean captured backend")
				}
				for _, b := range newStore.objects {
					if string(b) != "sentinel" {
						t.Fatal("commit touched new backend")
					}
				}
				if _, err := os.Stat(filepath.Join(root, "Items", a.ID.String())); !os.IsNotExist(err) {
					t.Fatalf("commit left original content: %v", err)
				}
			})
		}
	})
}
