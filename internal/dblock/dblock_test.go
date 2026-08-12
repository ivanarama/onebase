package dblock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAcquireSQLiteExclusiveAndReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.db")
	first, err := AcquireSQLite(path)
	if err != nil {
		t.Fatalf("first AcquireSQLite: %v", err)
	}
	second, err := AcquireSQLite(path)
	if !errors.Is(err, ErrLocked) {
		if second != nil {
			_ = second.Close()
		}
		_ = first.Close()
		t.Fatalf("second AcquireSQLite error = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release first lease: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second Close must be harmless: %v", err)
	}
	third, err := AcquireSQLite(path)
	if err != nil {
		t.Fatalf("AcquireSQLite after release: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("release third lease: %v", err)
	}
}

func TestAcquireSQLiteSharedConsumersExcludeRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.db")
	first, err := AcquireSQLiteShared(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close() //nolint:errcheck // test cleanup
	second, err := AcquireSQLiteShared(path)
	if err != nil {
		t.Fatalf("second shared consumer: %v", err)
	}
	defer second.Close() //nolint:errcheck // test cleanup
	exclusive, err := AcquireSQLite(path)
	if exclusive != nil {
		_ = exclusive.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("exclusive with shared consumers error = %v, want ErrLocked", err)
	}
}

func TestSQLiteExclusiveDowngradesToShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.db")
	lease, err := AcquireSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close() //nolint:errcheck // test cleanup
	if err := lease.Downgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	consumer, err := AcquireSQLiteShared(path)
	if err != nil {
		t.Fatalf("shared consumer after downgrade: %v", err)
	}
	defer consumer.Close() //nolint:errcheck // test cleanup
	exclusive, err := AcquireSQLite(path)
	if exclusive != nil {
		_ = exclusive.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("exclusive after downgrade error = %v, want ErrLocked", err)
	}
}

func TestAcquireSQLiteMemoryDoesNotCreateFile(t *testing.T) {
	for _, path := range []string{":memory:", "file::memory:?cache=shared", "file:test?mode=memory&cache=shared"} {
		lease, err := AcquireSQLite(path)
		if err != nil {
			t.Fatalf("AcquireSQLite(%q): %v", path, err)
		}
		if err := lease.Close(); err != nil {
			t.Fatalf("Close(%q): %v", path, err)
		}
	}
}

func TestAcquireSQLiteTargetRejectsFilesystemRoot(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	lease, target, err := AcquireSQLiteTarget(root)
	if lease != nil {
		_ = lease.Close()
	}
	if err == nil {
		t.Fatalf("AcquireSQLiteTarget(%q) = (%T, %q, nil), want unsafe-target error", root, lease, target)
	}
}

func TestCanonicalSQLitePathResolvesExistingSymlinkParent(t *testing.T) {
	realDir := t.TempDir()
	aliasRoot := t.TempDir()
	aliasDir := filepath.Join(aliasRoot, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	got, err := CanonicalSQLitePath(filepath.Join(aliasDir, "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realDir, "new.db")
	if got != want {
		t.Fatalf("CanonicalSQLitePath = %q, want %q", got, want)
	}
}

func TestAcquireSQLiteTargetPinsResolvedSymlinkTarget(t *testing.T) {
	realDir := t.TempDir()
	otherDir := t.TempDir()
	aliasRoot := t.TempDir()
	aliasDir := filepath.Join(aliasRoot, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	lease, target, err := AcquireSQLiteTarget(filepath.Join(aliasDir, "base.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close() //nolint:errcheck // test cleanup
	if want := filepath.Join(realDir, "base.db"); target != want {
		t.Fatalf("protected target = %q, want %q", target, want)
	}

	// Retargeting the caller's symlink after lock acquisition must not change
	// the path returned for the database operation itself.
	if err := os.Remove(aliasDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(otherDir, aliasDir); err != nil {
		t.Fatal(err)
	}
	if got := target; got != filepath.Join(realDir, "base.db") {
		t.Fatalf("protected target changed after symlink retarget: %q", got)
	}

	contender, err := AcquireSQLite(target)
	if contender != nil {
		_ = contender.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("canonical target was not protected after alias retarget: %v", err)
	}
}

func TestAcquireSQLiteAcrossProcesses(t *testing.T) {
	if os.Getenv("ONEBASE_DBLOCK_HELPER") == "1" {
		lease, err := AcquireSQLite(os.Getenv("ONEBASE_DBLOCK_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("ONEBASE_DBLOCK_READY")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "shared.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAcquireSQLiteAcrossProcesses$") //nolint:gosec // G204: launches this test binary with a fixed test selector; no shell or user-controlled executable/argument is involved
	cmd.Env = append(os.Environ(),
		"ONEBASE_DBLOCK_HELPER=1",
		"ONEBASE_DBLOCK_PATH="+path,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ONEBASE_DBLOCK_READY" {
		t.Fatalf("lock helper did not become ready: stdout=%q stderr=%q err=%v", scanner.Text(), stderr.String(), scanner.Err())
	}
	contender, err := AcquireSQLite(path)
	if contender != nil {
		_ = contender.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("cross-process contender error = %v, want ErrLocked", err)
	}
	if _, err := fmt.Fprintln(stdin, "release"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("lock helper failed: %v; stderr=%q", err, stderr.String())
	}
	finished = true

	lease, err := AcquireSQLite(path)
	if err != nil {
		t.Fatalf("AcquireSQLite after helper exit: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireSQLiteSharedAcrossProcesses(t *testing.T) {
	if os.Getenv("ONEBASE_DBLOCK_SHARED_HELPER") == "1" {
		lease, err := AcquireSQLiteShared(os.Getenv("ONEBASE_DBLOCK_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("ONEBASE_DBLOCK_SHARED_READY")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "shared.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAcquireSQLiteSharedAcrossProcesses$") //nolint:gosec // G204: launches this test binary with a fixed test selector
	cmd.Env = append(os.Environ(),
		"ONEBASE_DBLOCK_SHARED_HELPER=1",
		"ONEBASE_DBLOCK_PATH="+path,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ONEBASE_DBLOCK_SHARED_READY" {
		t.Fatalf("shared-lock helper did not become ready: stdout=%q stderr=%q err=%v", scanner.Text(), stderr.String(), scanner.Err())
	}
	consumer, err := AcquireSQLiteShared(path)
	if err != nil {
		t.Fatalf("second cross-process shared consumer: %v", err)
	}
	if err := consumer.Close(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := AcquireSQLite(path)
	if exclusive != nil {
		_ = exclusive.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("exclusive contender error = %v, want ErrLocked", err)
	}
	if _, err := fmt.Fprintln(stdin, "release"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("shared-lock helper failed: %v; stderr=%q", err, stderr.String())
	}
	finished = true
}

func TestCanonicalPostgresIdentityIgnoresSecretsAndOptionOrder(t *testing.T) {
	a, err := CanonicalPostgresIdentity("postgres://alice:first@LOCALHOST:5432/app?sslmode=disable&application_name=one")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalPostgresIdentity("host=localhost port=5432 dbname=app user=alice password=second application_name=two sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("equivalent database identities differ:\n%s\n%s", a, b)
	}
	c, err := CanonicalPostgresIdentity("postgres://bob:first@localhost:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	if a != c {
		t.Fatal("different PostgreSQL users for one database must be treated as aliases")
	}
	d, err := CanonicalPostgresIdentity("postgres://alice:first@localhost:5432/other")
	if err != nil {
		t.Fatal(err)
	}
	if a == d {
		t.Fatal("different PostgreSQL databases unexpectedly share an identity")
	}
}

func TestPostgresAdvisoryKeyStableAndDatabaseScoped(t *testing.T) {
	if got, want := postgresAdvisoryKey("app"), postgresAdvisoryKey("app"); got != want {
		t.Fatalf("key is not stable: %d != %d", got, want)
	}
	if postgresAdvisoryKey("app") == postgresAdvisoryKey("other") {
		t.Fatal("different databases unexpectedly share an advisory key")
	}
}
