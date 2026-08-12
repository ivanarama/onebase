//go:build !windows

package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func makeUnreadableTestEntry(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Symlink(filepath.Join(dir, "missing-target"), path); err != nil {
		t.Fatalf("create broken symlink: %v", err)
	}
}
