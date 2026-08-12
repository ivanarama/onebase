//go:build !windows

package selfupdate

import (
	"os"
	"testing"
)

func unsupportedRecoveryTarget(t *testing.T) string {
	t.Helper()
	target := t.TempDir()
	if err := os.Chmod(target, 0o777); err != nil { //nolint:gosec // intentionally model a shared installation
		t.Fatal(err)
	}
	return target
}
