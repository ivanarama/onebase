//go:build windows

package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func unsupportedRecoveryTarget(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	original := windowsPrivateInstallRoot
	windowsPrivateInstallRoot = func() (string, error) {
		return filepath.Join(base, "private-profile"), nil
	}
	t.Cleanup(func() { windowsPrivateInstallRoot = original })
	target := filepath.Join(base, "shared-installation")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}
