//go:build !windows

package backup

import (
	"errors"
	"os"
)

func moveSQLiteFile(source, destination string, replace bool) error {
	if replace {
		return os.Rename(source, destination)
	}
	// link(2) claims the destination atomically without replacing it. If the
	// unlink fails, remove the new name again so callers retain the source.
	if err := os.Link(source, destination); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return errors.Join(err, os.Remove(destination))
	}
	return nil
}

func publishSQLiteFileNoReplace(source, destination string) error {
	// Keep the staging name until the caller has fsynced the directory. A hard
	// link is an atomic, portable Unix no-replace publication primitive.
	return os.Link(source, destination)
}

func syncSQLiteDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
