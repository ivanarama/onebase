package launcher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dblock"
)

func TestRestoreRefusesDatabaseOwnedByAnotherProcessAndReleasesGates(t *testing.T) {
	h, base, dbPath := adoptedBase(t, "restore-cross-process-lock", false)
	lease, err := dblock.AcquireSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close() //nolint:errcheck // test cleanup

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/restore", nil)
	_, _, _, ok := h.ensureBaseStoppedForRestore(recorder, request, base, "ru")
	if ok {
		t.Fatal("restore acquired its lifecycle lease while another process owned the database")
	}
	if !strings.Contains(recorder.Body.String(), dblock.ErrLocked.Error()) {
		t.Fatalf("restore error does not explain the database lock: %s", recorder.Body.String())
	}

	if err := h.runner.holdStarts(); err != nil {
		t.Fatalf("failed restore leaked the lifecycle gate: %v", err)
	}
	h.runner.AllowStarts()
	contender, err := dblock.AcquireSQLite(dbPath)
	if contender != nil {
		_ = contender.Close()
	}
	if !errors.Is(err, dblock.ErrLocked) {
		t.Fatalf("test owner unexpectedly lost its database lock: %v", err)
	}
}

func TestDatabaseIdentityCanonicalizesPostgresDSN(t *testing.T) {
	a := &Base{DBType: "postgres", DB: "postgres://alice:first@LOCALHOST:5432/app?sslmode=disable&application_name=one"}
	b := &Base{DBType: "postgres", DB: "host=localhost port=5432 dbname=app user=alice password=second application_name=two sslmode=require"}
	if gotA, gotB := databaseIdentity(a), databaseIdentity(b); gotA != gotB {
		t.Fatalf("equivalent PostgreSQL aliases differ:\n%s\n%s", gotA, gotB)
	}
}

func TestLegacySQLiteBaseUsesDeterministicLifetimeLock(t *testing.T) {
	id := filepath.Base(t.TempDir())
	base := &Base{ID: id, DBType: "", DB: "", DBPath: ""}
	path, ok := sqlitePathForBase(base)
	if !ok {
		t.Fatal("legacy empty database fields were not recognized as SQLite")
	}
	want := filepath.Join(os.TempDir(), "onebase_"+id+".db")
	t.Cleanup(func() { _ = os.Remove(want + ".onebase.lock") })
	if path != want {
		t.Fatalf("legacy SQLite path = %q, want %q", path, want)
	}
	lease, err := acquireBaseDatabaseLease(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close() //nolint:errcheck // test cleanup
	contender, err := dblock.AcquireSQLite(want)
	if contender != nil {
		_ = contender.Close()
	}
	if !errors.Is(err, dblock.ErrLocked) {
		t.Fatalf("legacy base lock did not protect its actual SQLite target: %v", err)
	}
}

func TestBasePinnedToOpenDBKeepsResolvedSQLiteAlias(t *testing.T) {
	realDir := t.TempDir()
	otherDir := t.TempDir()
	aliasRoot := t.TempDir()
	aliasDir := filepath.Join(aliasRoot, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	originalPath := filepath.Join(aliasDir, "base.db")
	base := &Base{ID: "backup-pinned-alias", Name: "backup-pinned-alias", DBType: "sqlite", DBPath: originalPath}
	db, err := OpenDB(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pinned := basePinnedToOpenDB(base, db)
	want := filepath.Join(realDir, "base.db")
	if pinned.DBPath != want {
		t.Fatalf("pinned dump path = %q, want %q", pinned.DBPath, want)
	}
	if base.DBPath != originalPath {
		t.Fatalf("pinning mutated registry base path: %q", base.DBPath)
	}

	if err := os.Remove(aliasDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(otherDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	if pinned.DBPath != want {
		t.Fatalf("pinned dump target changed after alias retarget: %q", pinned.DBPath)
	}
}
