//go:build !windows

package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func validateTargetCoordinationDirectory(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o022 != 0 {
		// Общая установка, а не отказ по правам: писать сюда текущий
		// пользователь как раз может. Класс причины различается через
		// errors.Is — интерфейс на нём выбирает текст (#1065).
		return fmt.Errorf("%w: %s is group/other-writable; update as the owner from an owner-writable installation",
			ErrTargetNotPrivate, path)
	}
	// A read-only shared installation is still unsafe to replace: its ordinary
	// consumers cannot create/open our installation-scoped locks. Require a
	// private traversal boundary (normally the user's 0700 home) somewhere
	// between the target and filesystem root.
	current := filepath.Clean(path)
	for {
		entry, err := os.Stat(current)
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return errors.New("selfupdate: update target ancestor is not a directory")
		}
		if entry.Mode().Perm()&0o011 == 0 {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return fmt.Errorf("%w: %s has no private traversal boundary up to the filesystem root", ErrTargetNotPrivate, path)
}

func applyTargetCoordinationPermissions(file *os.File, mode os.FileMode) error {
	return file.Chmod(mode)
}
