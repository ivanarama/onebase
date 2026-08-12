package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/fsmode"
)

// directorySwap is a prepared, same-volume directory snapshot. stage and
// backup are reserved before a restore intent is persisted, so a fresh process
// can finish or undo every rename after the database outcome is known.
type directorySwap struct {
	dest          string
	stage         string
	backup        string
	stageRetired  string
	backupRetired string
	preserve      []string
	hadDest       bool
	cutpoint      directorySwapCutpoint
}

type directorySwapCutpoint func(string) error

type directorySwapCutpointKey struct{}

func withDirectorySwapCutpoint(ctx context.Context, cutpoint directorySwapCutpoint) context.Context {
	return context.WithValue(ctx, directorySwapCutpointKey{}, cutpoint)
}

func directorySwapCutpointFromContext(ctx context.Context) directorySwapCutpoint {
	cutpoint, _ := ctx.Value(directorySwapCutpointKey{}).(directorySwapCutpoint)
	return cutpoint
}

func (s *directorySwap) hitCutpoint(name string) error {
	if s == nil || s.cutpoint == nil {
		return nil
	}
	if err := s.cutpoint(name); err != nil {
		return fmt.Errorf("restore: directory durability cutpoint %s: %w", name, err)
	}
	return nil
}

const (
	directorySwapAfterStageDurable        = "after_stage_durable"
	directorySwapAfterOldMoved            = "after_old_moved"
	directorySwapAfterNewPublished        = "after_new_published"
	directorySwapAfterPreservedPublished  = "after_preserved_published"
	directorySwapAfterUnpublishedRetired  = "after_unpublished_retired"
	directorySwapAfterUnpublishedRemoved  = "after_unpublished_removed"
	directorySwapAfterPreservedRestored   = "after_preserved_restored"
	directorySwapAfterNewMovedAside       = "after_new_moved_aside"
	directorySwapAfterOldRestored         = "after_old_restored"
	directorySwapAfterPublishedRetired    = "after_published_retired"
	directorySwapAfterPublishedRemoved    = "after_published_removed"
	directorySwapAfterPreviousTreeRetired = "after_previous_tree_retired"
	directorySwapAfterPreviousTreeRemoved = "after_previous_tree_removed"
)

type directorySwapRecord struct {
	Dest          string   `json:"dest"`
	Stage         string   `json:"stage"`
	Backup        string   `json:"backup,omitempty"`
	StageRetired  string   `json:"stage_retired"`
	BackupRetired string   `json:"backup_retired,omitempty"`
	Preserve      []string `json:"preserve,omitempty"`
	HadDest       bool     `json:"had_dest"`
}

func (s *directorySwap) record() directorySwapRecord {
	return directorySwapRecord{
		Dest: s.dest, Stage: s.stage, Backup: s.backup,
		StageRetired: s.stageRetired, BackupRetired: s.backupRetired,
		Preserve: append([]string(nil), s.preserve...), HadDest: s.hadDest,
	}
}

func swapFromRecord(record directorySwapRecord) *directorySwap {
	return &directorySwap{
		dest: record.Dest, stage: record.Stage, backup: record.Backup,
		stageRetired: record.StageRetired, backupRetired: record.BackupRetired,
		preserve: append([]string(nil), record.Preserve...), hadDest: record.HadDest,
	}
}

func prepareDirectorySwap(ctx context.Context, srcDir, destDir string, perm os.FileMode, preserve []string) (*directorySwap, error) {
	if strings.TrimSpace(destDir) == "" {
		return nil, errors.New("restore: destination directory is empty")
	}
	dest, err := filepath.Abs(destDir)
	if err != nil {
		return nil, err
	}
	dest = canonicalDirectoryPath(filepath.Clean(dest))
	parent := filepath.Dir(dest)
	base := filepath.Base(dest)
	if base == "." || base == string(filepath.Separator) || parent == dest {
		return nil, fmt.Errorf("restore: unsafe destination directory %q", dest)
	}
	if err := ensureDirectoryDurable(parent); err != nil {
		return nil, err
	}

	hadDest := false
	if info, statErr := os.Lstat(dest); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("restore: destination is not a real directory: %s", dest)
		}
		hadDest = true
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}

	stage, err := os.MkdirTemp(parent, "."+base+".restore-stage-")
	if err != nil {
		return nil, err
	}
	stageRetired, err := reserveAbsentDirectoryPath(parent, "."+base+".restore-retired-stage-")
	if err != nil {
		removeTemp(stage)
		return nil, err
	}
	swap := &directorySwap{
		dest: dest, stage: stage, stageRetired: stageRetired,
		preserve: append([]string(nil), preserve...), hadDest: hadDest,
		cutpoint: directorySwapCutpointFromContext(ctx),
	}
	// Persist both the stage name and its reserved-absent tombstone before an
	// intent can record either path.
	if err := syncDirectoryMetadata(parent); err != nil {
		removeTemp(stage)
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			removeTemp(stage)
		}
	}()
	if hadDest {
		backup, reserveErr := reserveAbsentDirectoryPath(parent, "."+base+".restore-old-")
		if reserveErr != nil {
			return nil, reserveErr
		}
		backupRetired, reserveErr := reserveAbsentDirectoryPath(parent, "."+base+".restore-retired-old-")
		if reserveErr != nil {
			return nil, reserveErr
		}
		swap.backup = backup
		swap.backupRetired = backupRetired
		if err := syncDirectoryMetadata(parent); err != nil {
			return nil, err
		}
	}
	if srcDir != "" {
		if err := copyDirectoryTree(ctx, srcDir, stage, perm); err != nil {
			return nil, err
		}
	}
	// Every copied file has already been fsynced. Flush directory metadata
	// bottom-up; the stage's parent entry was persisted when the protocol paths
	// were reserved, before a recovery intent is allowed to refer to it.
	if err := syncDirectoryTree(stage); err != nil {
		return nil, fmt.Errorf("restore: persist staged directory tree: %w", err)
	}
	if err := swap.hitCutpoint(directorySwapAfterStageDurable); err != nil {
		return nil, err
	}
	if err := validateDirectorySwapRecord(swap.record()); err != nil {
		return nil, err
	}
	ok = true
	return swap, nil
}

func reserveAbsentDirectoryPath(parent, pattern string) (string, error) {
	// Never create then remove a name which the recovery record requires to be
	// absent. On Windows that deletion has no portable directory-fsync barrier
	// and could resurrect after power loss. UUID names are checked for absence
	// and are claimed later only by a no-replace rename.
	prefix := strings.TrimSuffix(pattern, "-") + "-"
	for range 8 {
		reserved := filepath.Join(parent, prefix+uuid.NewString())
		if _, err := os.Lstat(reserved); os.IsNotExist(err) {
			return reserved, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("restore: could not reserve an absent path in %s", parent)
}

func copyDirectoryTree(ctx context.Context, srcDir, destDir string, perm os.FileMode) error {
	return filepath.WalkDir(srcDir, func(src string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, src)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(destDir, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore: symbolic link is not allowed: %s", src)
		}
		if entry.IsDir() {
			return os.MkdirAll(dst, fsmode.Dir)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("restore: unsupported file type: %s", src)
		}
		if err := os.MkdirAll(filepath.Dir(dst), fsmode.Dir); err != nil {
			return err
		}
		return copyFileDurable(ctx, src, dst, perm)
	})
}

func copyFileDurable(ctx context.Context, srcPath, dstPath string, perm os.FileMode) error {
	src, err := os.Open(srcPath) //nolint:gosec // path comes from our extracted archive tree
	if err != nil {
		return err
	}
	defer closeRead("source snapshot file", src)
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = dst.Close()
		if !complete {
			_ = os.Remove(dstPath)
		}
	}()
	if _, err := io.Copy(dst, contextReader{ctx: ctx, r: src}); err != nil {
		return err
	}
	if err := dst.Sync(); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

// syncDirectoryTree persists directory entries from the leaves to root. File
// contents are synced by copyFileDurable before this metadata barrier.
func syncDirectoryTree(root string) error {
	return syncDirectoryTreeWith(root, syncDirectoryMetadata)
}

func syncDirectoryTreeWith(root string, syncDirectory func(string) error) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore: symbolic link is not allowed in staged tree: %s", path)
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if !entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("restore: unsupported file type in staged tree: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := syncDirectory(directories[i]); err != nil {
			return fmt.Errorf("sync directory %s: %w", directories[i], err)
		}
	}
	return nil
}

// ensureDirectoryDurable creates an absolute directory path and flushes every
// newly-created component bottom-up through the first pre-existing ancestor.
func ensureDirectoryDurable(path string) error {
	path = filepath.Clean(path)
	existing := path
	for {
		info, err := os.Lstat(existing)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("restore: expected a real directory: %s", existing)
			}
			goto create
		case os.IsNotExist(err):
			parent := filepath.Dir(existing)
			if parent == existing {
				return err
			}
			existing = parent
		default:
			return err
		}
	}

create:
	if err := os.MkdirAll(path, fsmode.Dir); err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		if err := syncDirectoryMetadata(current); err != nil {
			return err
		}
		if sameDirectoryPath(current, existing) {
			return nil
		}
	}
}

// mkdirAllDurable persists a newly-created descendant chain bottom-up through
// root. This is required before publishing a preserved path into the deepest
// directory: syncing only that immediate parent would not persist its own name.
func mkdirAllDurable(path, root string) error {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if !directoryPathWithin(path, root) {
		return fmt.Errorf("restore: durable directory %q escapes root %q", path, root)
	}
	if err := os.MkdirAll(path, fsmode.Dir); err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		if err := syncDirectoryMetadata(current); err != nil {
			return err
		}
		if sameDirectoryPath(current, root) {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("restore: durable directory %q escapes root %q", path, root)
		}
	}
}

func directoryPathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *directorySwap) renameDurable(source, destination, cutpoint string) error {
	if err := durableRenamePath(source, destination); err != nil {
		return err
	}
	return s.hitCutpoint(cutpoint)
}

func (s *directorySwap) retireTreeDurable(path, retiredPath, retiredCutpoint, removedCutpoint string) error {
	if err := retireRestoreTreeDurable(path, retiredPath, func() error {
		return s.hitCutpoint(retiredCutpoint)
	}); err != nil {
		return err
	}
	return s.hitCutpoint(removedCutpoint)
}

func (s *directorySwap) Publish() error {
	if s == nil {
		return nil
	}
	if err := validateDirectorySwapRecord(s.record()); err != nil {
		return err
	}
	return s.ensurePublished(false)
}

// ensurePublished converges the rename protocol without destroying its
// recovery evidence. When allowFinalizedWithoutEvidence is false, the state
// dest=true/stage=false/backup=false is deliberately rejected for a swap that
// replaced an existing tree: it can mean either "old tree already retired" or
// "durable stage metadata was lost before publication". A recorded retired
// backup tombstone disambiguates the completed case.
func (s *directorySwap) ensurePublished(allowFinalizedWithoutEvidence bool) error {
	destExists, err := realDirectoryExists(s.dest)
	if err != nil {
		return err
	}
	stageExists, err := realDirectoryExists(s.stage)
	if err != nil {
		return err
	}
	backupExists := false
	if s.backup != "" {
		backupExists, err = realDirectoryExists(s.backup)
		if err != nil {
			return err
		}
	}
	stageRetiredExists, err := realDirectoryExists(s.stageRetired)
	if err != nil {
		return err
	}
	backupRetiredExists := false
	if s.backupRetired != "" {
		backupRetiredExists, err = realDirectoryExists(s.backupRetired)
		if err != nil {
			return err
		}
	}
	if stageRetiredExists || backupExists && backupRetiredExists {
		return s.stateConflictWithRetired("publish", destExists, stageExists, backupExists,
			stageRetiredExists, backupRetiredExists)
	}

	if s.hadDest {
		if backupRetiredExists {
			if destExists && !stageExists && !backupExists {
				// Finalization durably retired the old tree but intentionally kept
				// the tombstone until the committed database marker is deleted.
				return nil
			}
			return s.stateConflictWithRetired("publish", destExists, stageExists, backupExists,
				stageRetiredExists, backupRetiredExists)
		}
		switch {
		case destExists && stageExists && !backupExists:
			// This barrier must reach stable storage before stage -> dest. Without
			// it, a power loss may retain the second rename while losing the first
			// and thereby strand or replace the old tree.
			if err := s.renameDurable(s.dest, s.backup, directorySwapAfterOldMoved); err != nil {
				return err
			}
		case !destExists && stageExists && backupExists:
			// The old tree was already moved aside.
		case destExists && !stageExists && backupExists:
			return s.finishPreservedPaths()
		case destExists && !stageExists && !backupExists:
			if allowFinalizedWithoutEvidence {
				return nil
			}
			return fmt.Errorf("restore: cannot publish snapshot %q: ambiguous finalized state without retired-tree evidence", s.dest)
		default:
			return s.stateConflict("publish", destExists, stageExists, backupExists)
		}
	} else {
		if backupExists || s.backup != "" {
			return s.stateConflict("publish", destExists, stageExists, backupExists)
		}
		switch {
		case !destExists && stageExists:
		case destExists && !stageExists:
			return nil
		default:
			return s.stateConflict("publish", destExists, stageExists, backupExists)
		}
	}

	if err := s.renameDurable(s.stage, s.dest, directorySwapAfterNewPublished); err != nil {
		return err
	}
	if err := s.finishPreservedPaths(); err != nil {
		return err
	}
	return nil
}

func (s *directorySwap) finishPreservedPaths() error {
	if !s.hadDest {
		return nil
	}
	for _, rel := range s.preserve {
		oldPath := filepath.Join(s.backup, rel)
		newPath := filepath.Join(s.dest, rel)
		oldExists, err := anyPathExists(oldPath)
		if err != nil {
			return err
		}
		newExists, err := anyPathExists(newPath)
		if err != nil {
			return err
		}
		switch {
		case oldExists && newExists:
			return fmt.Errorf("restore: preserved path exists in both snapshots: %q", rel)
		case oldExists:
			if err := mkdirAllDurable(filepath.Dir(newPath), s.dest); err != nil {
				return err
			}
			if err := s.renameDurable(oldPath, newPath, directorySwapAfterPreservedPublished); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *directorySwap) Rollback() error {
	if s == nil {
		return nil
	}
	if err := validateDirectorySwapRecord(s.record()); err != nil {
		return err
	}
	return s.recoverPending()
}

func (s *directorySwap) recoverPending() error {
	backupRetiredExists := false
	if s.backupRetired != "" {
		var err error
		backupRetiredExists, err = realDirectoryExists(s.backupRetired)
		if err != nil {
			return err
		}
	}
	if backupRetiredExists {
		return fmt.Errorf("restore: cannot roll back snapshot %q: previous tree was already retired", s.dest)
	}
	if err := cleanupRetiredRestoreTree(s.stage, s.stageRetired); err != nil {
		return err
	}
	destExists, err := realDirectoryExists(s.dest)
	if err != nil {
		return err
	}
	stageExists, err := realDirectoryExists(s.stage)
	if err != nil {
		return err
	}
	backupExists := false
	if s.backup != "" {
		backupExists, err = realDirectoryExists(s.backup)
		if err != nil {
			return err
		}
	}

	if !s.hadDest {
		if backupExists || s.backup != "" {
			return s.stateConflict("rollback", destExists, stageExists, backupExists)
		}
		switch {
		case !destExists && !stageExists:
			return nil
		case !destExists && stageExists:
			return s.retireTreeDurable(s.stage, s.stageRetired,
				directorySwapAfterUnpublishedRetired, directorySwapAfterUnpublishedRemoved)
		case destExists && !stageExists:
			if err := s.renameDurable(s.dest, s.stage, directorySwapAfterNewMovedAside); err != nil {
				return err
			}
			if err := s.retireTreeDurable(s.stage, s.stageRetired,
				directorySwapAfterPublishedRetired, directorySwapAfterPublishedRemoved); err != nil {
				return err
			}
			return nil
		default:
			return s.stateConflict("rollback", destExists, stageExists, backupExists)
		}
	}

	switch {
	case destExists && stageExists && !backupExists:
		// Nothing was published.
		return s.retireTreeDurable(s.stage, s.stageRetired,
			directorySwapAfterUnpublishedRetired, directorySwapAfterUnpublishedRemoved)
	case destExists && !stageExists && !backupExists:
		// Rollback already completed and only intent cleanup remained.
		return nil
	case !destExists && stageExists && backupExists:
		if err := s.renameDurable(s.backup, s.dest, directorySwapAfterOldRestored); err != nil {
			return err
		}
		if err := s.retireTreeDurable(s.stage, s.stageRetired,
			directorySwapAfterUnpublishedRetired, directorySwapAfterUnpublishedRemoved); err != nil {
			return err
		}
		return nil
	case destExists && !stageExists && backupExists:
		if err := s.restorePreservedPaths(); err != nil {
			return err
		}
		if err := s.renameDurable(s.dest, s.stage, directorySwapAfterNewMovedAside); err != nil {
			return err
		}
		if err := s.renameDurable(s.backup, s.dest, directorySwapAfterOldRestored); err != nil {
			// Put the new snapshot back at its live name if restoring the old tree failed.
			return errors.Join(err, durableRenamePath(s.stage, s.dest))
		}
		if err := s.retireTreeDurable(s.stage, s.stageRetired,
			directorySwapAfterPublishedRetired, directorySwapAfterPublishedRemoved); err != nil {
			return err
		}
		return nil
	default:
		return s.stateConflict("rollback", destExists, stageExists, backupExists)
	}
}

func (s *directorySwap) restorePreservedPaths() error {
	for i := len(s.preserve) - 1; i >= 0; i-- {
		rel := s.preserve[i]
		oldPath := filepath.Join(s.backup, rel)
		newPath := filepath.Join(s.dest, rel)
		oldExists, err := anyPathExists(oldPath)
		if err != nil {
			return err
		}
		newExists, err := anyPathExists(newPath)
		if err != nil {
			return err
		}
		switch {
		case oldExists && newExists:
			return fmt.Errorf("restore: preserved path exists in both snapshots: %q", rel)
		case newExists:
			if err := mkdirAllDurable(filepath.Dir(oldPath), s.backup); err != nil {
				return err
			}
			if err := s.renameDurable(newPath, oldPath, directorySwapAfterPreservedRestored); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *directorySwap) Commit() error {
	if s == nil {
		return nil
	}
	if err := validateDirectorySwapRecord(s.record()); err != nil {
		return err
	}
	// Commit is also used as an idempotent low-level primitive without a
	// database intent, so it accepts a fully-cleaned state on retry. Restore
	// intents use sealCommit(false) below and therefore fail closed when the
	// state is ambiguous.
	if err := s.sealCommit(true); err != nil {
		return err
	}
	return s.cleanupCommittedTree()
}

// sealCommit retires the previous tree with a durable rename but keeps the
// tombstone as evidence until the committed database marker is durably
// deleted. Deleting the tree first would make a missing stage indistinguishable
// from a completely finalized restore after a power loss.
func (s *directorySwap) sealCommit(allowFinalizedWithoutEvidence bool) error {
	if err := s.ensurePublished(allowFinalizedWithoutEvidence); err != nil {
		return err
	}
	if s.backup == "" {
		return nil
	}
	backupExists, err := realDirectoryExists(s.backup)
	if err != nil {
		return err
	}
	retiredExists, err := realDirectoryExists(s.backupRetired)
	if err != nil {
		return err
	}
	if backupExists && retiredExists {
		return fmt.Errorf("restore: active and retired previous trees both exist: %s", s.backup)
	}
	if backupExists {
		return s.renameDurable(s.backup, s.backupRetired, directorySwapAfterPreviousTreeRetired)
	}
	if retiredExists || allowFinalizedWithoutEvidence {
		return nil
	}
	return fmt.Errorf("restore: previous tree and its retirement evidence are both missing: %s", s.backup)
}

func (s *directorySwap) cleanupCommittedTree() error {
	if s == nil || s.backupRetired == "" {
		return nil
	}
	if err := removeRetiredRestoreTree(s.backupRetired); err != nil {
		return err
	}
	return s.hitCutpoint(directorySwapAfterPreviousTreeRemoved)
}

func rollbackDirectorySwaps(swaps []*directorySwap) error {
	var errs []error
	for i := len(swaps) - 1; i >= 0; i-- {
		if err := swaps[i].Rollback(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func commitDirectorySwaps(swaps []*directorySwap) error {
	for _, swap := range swaps {
		if err := swap.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func sealDirectorySwaps(swaps []*directorySwap) error {
	for _, swap := range swaps {
		if err := swap.sealCommit(false); err != nil {
			return err
		}
	}
	return nil
}

func cleanupCommittedDirectorySwaps(swaps []*directorySwap) error {
	var errs []error
	for _, swap := range swaps {
		if err := swap.cleanupCommittedTree(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateDirectorySwapRecord(record directorySwapRecord) error {
	if record.Dest == "" || !filepath.IsAbs(record.Dest) || filepath.Clean(record.Dest) != record.Dest {
		return fmt.Errorf("restore: invalid snapshot destination %q", record.Dest)
	}
	parent := filepath.Dir(record.Dest)
	base := filepath.Base(record.Dest)
	if parent == record.Dest || base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("restore: unsafe snapshot destination %q", record.Dest)
	}
	if !validReservedSwapPath(record.Stage, parent, "."+base+".restore-stage-") {
		return fmt.Errorf("restore: invalid snapshot stage %q", record.Stage)
	}
	if !validReservedSwapPath(record.StageRetired, parent, "."+base+".restore-retired-stage-") ||
		sameDirectoryPath(record.StageRetired, record.Stage) {
		return fmt.Errorf("restore: invalid retired snapshot stage %q", record.StageRetired)
	}
	if record.HadDest {
		if !validReservedSwapPath(record.Backup, parent, "."+base+".restore-old-") || sameDirectoryPath(record.Backup, record.Stage) {
			return fmt.Errorf("restore: invalid previous snapshot path %q", record.Backup)
		}
		if !validReservedSwapPath(record.BackupRetired, parent, "."+base+".restore-retired-old-") ||
			sameDirectoryPath(record.BackupRetired, record.Backup) ||
			sameDirectoryPath(record.BackupRetired, record.Stage) ||
			sameDirectoryPath(record.BackupRetired, record.StageRetired) {
			return fmt.Errorf("restore: invalid retired previous snapshot path %q", record.BackupRetired)
		}
	} else if record.Backup != "" {
		return fmt.Errorf("restore: unexpected previous snapshot path %q", record.Backup)
	} else if record.BackupRetired != "" {
		return fmt.Errorf("restore: unexpected retired previous snapshot path %q", record.BackupRetired)
	}
	seen := make(map[string]bool, len(record.Preserve))
	cleanPreserve := make([]string, 0, len(record.Preserve))
	for _, rel := range record.Preserve {
		clean := filepath.Clean(rel)
		key := clean
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if clean != rel || clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || seen[key] {
			return fmt.Errorf("restore: unsafe preserved path %q", rel)
		}
		seen[key] = true
		for _, previous := range cleanPreserve {
			if directoryPathsOverlap(clean, previous) {
				return fmt.Errorf("restore: overlapping preserved paths %q and %q", previous, rel)
			}
		}
		cleanPreserve = append(cleanPreserve, clean)
	}
	return nil
}

func directoryPathsOverlap(a, b string) bool {
	key := func(value string) string {
		value = filepath.Clean(value)
		if runtime.GOOS == "windows" {
			return strings.ToLower(value)
		}
		return value
	}
	a = key(a)
	b = key(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) ||
		strings.HasPrefix(b, a+string(filepath.Separator))
}

func validReservedSwapPath(candidate, parent, prefix string) bool {
	return candidate != "" && filepath.IsAbs(candidate) && filepath.Clean(candidate) == candidate &&
		sameDirectoryPath(filepath.Dir(candidate), parent) && strings.HasPrefix(filepath.Base(candidate), prefix) &&
		len(filepath.Base(candidate)) > len(prefix)
}

func sameDirectoryPath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func realDirectoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("restore: expected a real directory: %s", path)
	}
	return true, nil
}

func anyPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func cleanupRetiredRestoreTree(activePath, retiredPath string) error {
	activeExists, err := realDirectoryExists(activePath)
	if err != nil {
		return err
	}
	retiredExists, err := realDirectoryExists(retiredPath)
	if err != nil {
		return err
	}
	if activeExists && retiredExists {
		return fmt.Errorf("active and retired trees both exist: %s", activePath)
	}
	if !retiredExists {
		return nil
	}
	return removeRetiredRestoreTree(retiredPath)
}

// retireRestoreTreeDurable first moves the exact protocol path to its recorded
// tombstone with a durable rename. Only then may recursive deletion begin.
// Thus a Windows power loss can at worst resurrect an inert tombstone, never a
// stage/backup name that would change recovery state after its marker is gone.
func retireRestoreTreeDurable(path, retiredPath string, afterRetire func() error) error {
	exists, err := realDirectoryExists(path)
	if err != nil {
		return err
	}
	retiredExists, err := realDirectoryExists(retiredPath)
	if err != nil {
		return err
	}
	switch {
	case exists && retiredExists:
		return fmt.Errorf("restore: active and retired trees both exist: %s", path)
	case exists:
		if err := durableRenamePath(path, retiredPath); err != nil {
			return err
		}
		if afterRetire != nil {
			if err := afterRetire(); err != nil {
				return err
			}
		}
	case !retiredExists:
		return nil
	}
	return removeRetiredRestoreTree(retiredPath)
}

func removeRetiredRestoreTree(path string) error {
	if path == "" {
		return nil
	}
	exists, err := realDirectoryExists(path)
	if err != nil || !exists {
		return err
	}
	removeErr := os.RemoveAll(path)
	// On POSIX this persists the tombstone deletion. On Windows the protocol
	// path was already retired with MOVEFILE_WRITE_THROUGH; a resurrected
	// tombstone is inert and the recorded name is retried while intent exists.
	syncErr := syncDirectoryMetadata(filepath.Dir(path))
	return errors.Join(removeErr, syncErr)
}

func (s *directorySwap) stateConflict(action string, dest, stage, backup bool) error {
	return fmt.Errorf("restore: cannot %s snapshot %q: conflicting state (dest=%t stage=%t backup=%t)",
		action, s.dest, dest, stage, backup)
}

func (s *directorySwap) stateConflictWithRetired(action string, dest, stage, backup, stageRetired, backupRetired bool) error {
	return fmt.Errorf("restore: cannot %s snapshot %q: conflicting state (dest=%t stage=%t backup=%t stage_retired=%t backup_retired=%t)",
		action, s.dest, dest, stage, backup, stageRetired, backupRetired)
}
