package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

func TestDirectorySwapPublishesSnapshotAndPreservesReservedTrees(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "archive-config")
	dest := filepath.Join(parent, "project")
	for _, dir := range []string{src, dest, filepath.Join(dest, ".git"), filepath.Join(dest, "backups")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(src, "app.yaml"), "new")
	write(filepath.Join(dest, "app.yaml"), "old")
	write(filepath.Join(dest, "removed.yaml"), "must disappear")
	write(filepath.Join(dest, ".git", "HEAD"), "keep git")
	write(filepath.Join(dest, "backups", "old.obz"), "keep backup")

	swap, err := prepareDirectorySwap(context.Background(), src, dest, 0o600, []string{".git", "backups"})
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Publish(); err != nil {
		t.Fatal(err)
	}
	if got := readFileForSwapTest(t, filepath.Join(dest, "app.yaml")); got != "new" {
		t.Fatalf("published config=%q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "removed.yaml")); !os.IsNotExist(err) {
		t.Fatalf("file absent from snapshot survived: %v", err)
	}
	if got := readFileForSwapTest(t, filepath.Join(dest, ".git", "HEAD")); got != "keep git" {
		t.Fatalf("reserved VCS tree=%q", got)
	}
	if err := swap.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDirectorySwapRollbackRestoresOldTree(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "archive-files")
	dest := filepath.Join(parent, "live-files")
	for _, dir := range []string{src, dest} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	swap, err := prepareDirectorySwap(context.Background(), src, dest, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := swap.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := swap.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := readFileForSwapTest(t, filepath.Join(dest, "old")); got != "old" {
		t.Fatalf("rollback content=%q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "new")); !os.IsNotExist(err) {
		t.Fatalf("new snapshot survived rollback: %v", err)
	}
}

func readFileForSwapTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test temp path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncDirectoryTreeVisitsDirectoriesBottomUp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	leaf := filepath.Join(root, "one", "two")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "data"), []byte("durable"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	err := syncDirectoryTreeWith(root, func(path string) error {
		got = append(got, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{leaf, filepath.Dir(leaf), root}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sync order = %q, want bottom-up %q", got, want)
	}
}

func TestPrepareDirectorySwapDoesNotReturnBeforeStageDurabilityBarrier(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "archive")
	dest := filepath.Join(root, "live")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "value"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stage durability lost acknowledgement")
	ctx := withDirectorySwapCutpoint(context.Background(), func(name string) error {
		if name == directorySwapAfterStageDurable {
			return stop
		}
		return nil
	})
	if swap, err := prepareDirectorySwap(ctx, src, dest, 0o600, nil); !errors.Is(err, stop) || swap != nil {
		t.Fatalf("prepareDirectorySwap = (%v, %v), want nil and injected error", swap, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".live.restore-stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("failed preparation left stage paths: %v", matches)
	}
}

func TestReserveAbsentDirectoryPathNeverCreatesReservedName(t *testing.T) {
	parent := t.TempDir()
	path, err := reserveAbsentDirectoryPath(parent, ".live.restore-old-")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != parent || !strings.HasPrefix(filepath.Base(path), ".live.restore-old-") {
		t.Fatalf("reserved path = %s", path)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("reserved-absent name was created: %v", err)
	}
}

func TestValidateDirectorySwapRecordRejectsOverlappingPreservedPaths(t *testing.T) {
	swap := prepareDurabilitySwapTest(t, context.Background(), false)
	for _, preserved := range [][]string{
		{"a", filepath.Join("a", "b")},
		{filepath.Join("nested", "child"), "nested"},
	} {
		record := swap.record()
		record.Preserve = preserved
		if err := validateDirectorySwapRecord(record); err == nil {
			t.Fatalf("overlapping preserved paths accepted: %q", preserved)
		}
	}
}

func TestDirectorySwapForwardDurabilityCutpointsRecoverOldTree(t *testing.T) {
	stop := errors.New("simulated power-loss boundary")
	for _, cutpoint := range []string{
		directorySwapAfterOldMoved,
		directorySwapAfterNewPublished,
		directorySwapAfterPreservedPublished,
	} {
		t.Run(cutpoint, func(t *testing.T) {
			ctx := withDirectorySwapCutpoint(context.Background(), func(name string) error {
				if name == cutpoint {
					return stop
				}
				return nil
			})
			swap := prepareDurabilitySwapTest(t, ctx, true)
			if err := swap.Publish(); !errors.Is(err, stop) {
				t.Fatalf("Publish error = %v, want injected error", err)
			}

			recovered := swapFromRecord(swap.record())
			if err := recovered.Rollback(); err != nil {
				t.Fatalf("Rollback after restart: %v", err)
			}
			assertDurabilitySwapOldTree(t, recovered)
		})
	}
}

func TestDirectorySwapRollbackDurabilityCutpointsAreRecoverable(t *testing.T) {
	stop := errors.New("simulated power-loss boundary")
	for _, cutpoint := range []string{
		directorySwapAfterPreservedRestored,
		directorySwapAfterNewMovedAside,
		directorySwapAfterOldRestored,
		directorySwapAfterPublishedRetired,
		directorySwapAfterPublishedRemoved,
	} {
		t.Run(cutpoint, func(t *testing.T) {
			swap := prepareDurabilitySwapTest(t, context.Background(), true)
			if err := swap.Publish(); err != nil {
				t.Fatal(err)
			}
			swap.cutpoint = func(name string) error {
				if name == cutpoint {
					return stop
				}
				return nil
			}
			if err := swap.Rollback(); !errors.Is(err, stop) {
				t.Fatalf("Rollback error = %v, want injected error", err)
			}

			recovered := swapFromRecord(swap.record())
			if err := recovered.Rollback(); err != nil {
				t.Fatalf("Rollback after restart: %v", err)
			}
			assertDurabilitySwapOldTree(t, recovered)
		})
	}
}

func TestDirectorySwapPreparedTreeRetirementCutpointsAreRecoverable(t *testing.T) {
	stop := errors.New("simulated power-loss boundary")
	for _, cutpoint := range []string{
		directorySwapAfterUnpublishedRetired,
		directorySwapAfterUnpublishedRemoved,
	} {
		t.Run(cutpoint, func(t *testing.T) {
			swap := prepareDurabilitySwapTest(t, context.Background(), true)
			swap.cutpoint = func(name string) error {
				if name == cutpoint {
					return stop
				}
				return nil
			}
			if err := swap.Rollback(); !errors.Is(err, stop) {
				t.Fatalf("Rollback error = %v, want injected error", err)
			}

			recovered := swapFromRecord(swap.record())
			if err := recovered.Rollback(); err != nil {
				t.Fatalf("Rollback after restart: %v", err)
			}
			assertDurabilitySwapOldTree(t, recovered)
		})
	}
}

func TestDirectorySwapCommitRetirementCutpointsKeepNewTree(t *testing.T) {
	stop := errors.New("simulated power-loss boundary")
	for _, cutpoint := range []string{
		directorySwapAfterPreviousTreeRetired,
		directorySwapAfterPreviousTreeRemoved,
	} {
		t.Run(cutpoint, func(t *testing.T) {
			swap := prepareDurabilitySwapTest(t, context.Background(), true)
			if err := swap.Publish(); err != nil {
				t.Fatal(err)
			}
			swap.cutpoint = func(name string) error {
				if name == cutpoint {
					return stop
				}
				return nil
			}
			if err := swap.Commit(); !errors.Is(err, stop) {
				t.Fatalf("Commit error = %v, want injected error", err)
			}

			recovered := swapFromRecord(swap.record())
			if err := recovered.Commit(); err != nil {
				t.Fatalf("Commit after restart: %v", err)
			}
			assertDurabilitySwapNewTree(t, recovered)
		})
	}
}

func TestDirectorySwapStrictSealRejectsMissingStageAndRetirementEvidence(t *testing.T) {
	swap := prepareDurabilitySwapTest(t, context.Background(), true)
	if err := os.RemoveAll(swap.stage); err != nil {
		t.Fatal(err)
	}

	err := swap.sealCommit(false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous finalized state") {
		t.Fatalf("sealCommit error = %v, want ambiguous-state failure", err)
	}
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "old")); got != "old" {
		t.Fatalf("ambiguous state mutated old destination: %q", got)
	}
}

func TestRestoreIntentMarkerSurvivesDirectoryDurabilityFailures(t *testing.T) {
	stop := errors.New("simulated durability failure")
	t.Run("pending rollback", func(t *testing.T) {
		db := newSQLite(t, "durable-pending-marker")
		swap := prepareDurabilitySwapTest(t, context.Background(), true)
		intent := beginRestoreIntentForTest(t, db, swap)
		swap.cutpoint = func(name string) error {
			if name == directorySwapAfterUnpublishedRetired {
				return stop
			}
			return nil
		}
		if err := intent.Rollback(context.Background(), []*directorySwap{swap}); !errors.Is(err, stop) {
			t.Fatalf("Rollback error = %v, want durability failure", err)
		}
		assertRestoreIntentState(t, db, "pending")
		swap.cutpoint = nil
		if err := intent.Rollback(context.Background(), []*directorySwap{swap}); err != nil {
			t.Fatal(err)
		}
		assertNoRestoreIntent(t, db)
	})

	t.Run("committed finalize", func(t *testing.T) {
		db := newSQLite(t, "durable-committed-marker")
		swap := prepareDurabilitySwapTest(t, context.Background(), true)
		intent := beginRestoreIntentForTest(t, db, swap)
		if err := intent.MarkCommitted(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := swap.Publish(); err != nil {
			t.Fatal(err)
		}
		swap.cutpoint = func(name string) error {
			if name == directorySwapAfterPreviousTreeRetired {
				return stop
			}
			return nil
		}
		if err := intent.Finalize(context.Background(), []*directorySwap{swap}); !errors.Is(err, stop) {
			t.Fatalf("Finalize error = %v, want durability failure", err)
		}
		assertRestoreIntentState(t, db, "committed")
		if exists, err := realDirectoryExists(swap.backupRetired); err != nil || !exists {
			t.Fatalf("retired-tree evidence missing while marker exists: exists=%t err=%v", exists, err)
		}
		assertPathAbsent(t, swap.backup)
		swap.cutpoint = nil
		if err := intent.Finalize(context.Background(), []*directorySwap{swap}); err != nil {
			t.Fatal(err)
		}
		assertNoRestoreIntent(t, db)
		assertDurabilitySwapNewTree(t, swap)
	})
}

func prepareDurabilitySwapTest(t *testing.T, ctx context.Context, preserve bool) *directorySwap {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "archive")
	dest := filepath.Join(root, "live")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	var preserved []string
	if preserve {
		preserved = []string{filepath.Join("nested", "keep")}
		keep := filepath.Join(dest, preserved[0])
		if err := os.MkdirAll(keep, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(keep, "value"), []byte("kept"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	swap, err := prepareDirectorySwap(ctx, src, dest, 0o600, preserved)
	if err != nil {
		t.Fatal(err)
	}
	return swap
}

func assertDurabilitySwapOldTree(t *testing.T, swap *directorySwap) {
	t.Helper()
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "old")); got != "old" {
		t.Fatalf("old tree value = %q", got)
	}
	if _, err := os.Stat(filepath.Join(swap.dest, "new")); !os.IsNotExist(err) {
		t.Fatalf("new tree survived rollback: %v", err)
	}
	if len(swap.preserve) != 0 {
		if got := readFileForSwapTest(t, filepath.Join(swap.dest, swap.preserve[0], "value")); got != "kept" {
			t.Fatalf("preserved value after rollback = %q", got)
		}
	}
	assertDirectorySwapTemporaryPathsAbsent(t, swap)
}

func assertDurabilitySwapNewTree(t *testing.T, swap *directorySwap) {
	t.Helper()
	if got := readFileForSwapTest(t, filepath.Join(swap.dest, "new")); got != "new" {
		t.Fatalf("new tree value = %q", got)
	}
	if _, err := os.Stat(filepath.Join(swap.dest, "old")); !os.IsNotExist(err) {
		t.Fatalf("old tree survived commit: %v", err)
	}
	if len(swap.preserve) != 0 {
		if got := readFileForSwapTest(t, filepath.Join(swap.dest, swap.preserve[0], "value")); got != "kept" {
			t.Fatalf("preserved value after commit = %q", got)
		}
	}
	assertDirectorySwapTemporaryPathsAbsent(t, swap)
}

func assertDirectorySwapTemporaryPathsAbsent(t *testing.T, swap *directorySwap) {
	t.Helper()
	for _, path := range []string{swap.stage, swap.backup, swap.stageRetired, swap.backupRetired} {
		if path == "" {
			continue
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary path remains %s: %v", path, err)
		}
	}
}

func assertRestoreIntentState(t *testing.T, db *storage.DB, want string) {
	t.Helper()
	raw, ok, err := readRestoreIntent(context.Background(), db)
	if err != nil || !ok {
		t.Fatalf("read restore intent: ok=%t err=%v", ok, err)
	}
	record, err := decodeRestoreIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != want {
		t.Fatalf("restore intent state = %q, want %q", record.State, want)
	}
}
