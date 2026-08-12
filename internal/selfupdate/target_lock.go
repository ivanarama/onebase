package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
)

func targetCoordinationMode(dirMode os.FileMode) os.FileMode {
	var mode os.FileMode
	if dirMode&0o300 == 0o300 {
		mode |= 0o600
	}
	if dirMode&0o030 == 0o030 {
		mode |= 0o060
	}
	if dirMode&0o003 == 0o003 {
		mode |= 0o006
	}
	return mode
}

func targetCoordinationPermissions(path string) (os.FileMode, error) {
	info, err := os.Stat(path) //nolint:gosec // G703: caller supplies the operator-selected update target; this function validates it before deriving lock permissions
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, errors.New("selfupdate: update target is not a directory")
	}
	if err := validateTargetCoordinationDirectory(path, info); err != nil {
		return 0, err
	}
	mode := targetCoordinationMode(info.Mode().Perm())
	if mode == 0 {
		return 0, errors.New("selfupdate: update target has no writable permission class")
	}
	return mode, nil
}

// openTargetLockFile refuses symlinks and verifies that the path opened is the
// same stable regular inode inspected immediately before/after open. Keeping
// the inode forever avoids the classic unlink-and-recreate lock split.
func openTargetLockFile(path string) (*os.File, error) {
	mode, err := targetCoordinationPermissions(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path) //nolint:gosec // G703: path is the fixed lock filename inside the validated canonical update target
	if os.IsNotExist(err) {
		created, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, mode) //nolint:gosec // G703: exclusive creation is confined to the validated update target
		if createErr == nil {
			if chmodErr := applyTargetCoordinationPermissions(created, mode); chmodErr != nil {
				return nil, errors.Join(chmodErr, created.Close())
			}
			return created, nil
		}
		if !os.IsExist(createErr) {
			return nil, createErr
		}
		before, err = os.Lstat(path) //nolint:gosec // G703: recheck of the same validated lock path after an expected create race
	}
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("selfupdate: target update lock is not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0) //nolint:gosec // G703: identity is checked against Lstat before this handle is accepted
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.Join(errors.New("selfupdate: target update lock identity changed while opening"), f.Close())
	}
	if err := applyTargetCoordinationPermissions(f, mode); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
