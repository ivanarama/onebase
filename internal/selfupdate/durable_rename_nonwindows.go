//go:build !windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
)

func platformDurableRename(source, destination string, _ bool) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	destinationDir := filepath.Dir(destination)
	destinationErr := syncDirectory(destinationDir)
	sourceDir := filepath.Dir(source)
	if filepath.Clean(sourceDir) == filepath.Clean(destinationDir) {
		return destinationErr
	}
	return errors.Join(destinationErr, syncDirectory(sourceDir))
}
