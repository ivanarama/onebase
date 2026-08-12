//go:build !windows

package backup

import (
	"errors"
	"os"
	"path/filepath"
)

func syncDirectoryMetadata(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

// durableRenamePath persists the removal of the source name and addition of
// the destination name. Both parents matter when preserved trees move between
// the previous and replacement snapshots.
func durableRenamePath(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	sourceParent := filepath.Dir(source)
	destinationParent := filepath.Dir(destination)
	if sameDirectoryPath(sourceParent, destinationParent) {
		return syncDirectoryMetadata(sourceParent)
	}
	return errors.Join(
		syncDirectoryMetadata(sourceParent),
		syncDirectoryMetadata(destinationParent),
	)
}
