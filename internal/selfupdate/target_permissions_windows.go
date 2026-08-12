//go:build windows

package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var windowsPrivateInstallRoot = func() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Profile, windows.KF_FLAG_DEFAULT)
}

func windowsTestPrivateInstallRoot() string {
	root, _ := windowsPrivateInstallRoot()
	return root
}

func validateTargetCoordinationDirectory(path string, _ os.FileInfo) error {
	// Do not trust USERPROFILE/HOME: both are caller-controlled and could be
	// pointed at Program Files to bypass the private-install boundary.
	home, err := windowsPrivateInstallRoot()
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return err
	}
	if !pathWithinWindowsRoot(home, path) {
		return errors.New("selfupdate: shared/readable system installations cannot be self-updated safely; use a private per-user installation")
	}
	return nil
}

func pathWithinWindowsRoot(root, path string) bool {
	root = strings.ToLower(filepath.Clean(root))
	path = strings.ToLower(filepath.Clean(path))
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// On Windows the file inherits the installation directory DACL. Chmod would
// synthesize DOS attributes, not safely express the parent ACL's principals.
func applyTargetCoordinationPermissions(*os.File, os.FileMode) error { return nil }
