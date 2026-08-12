//go:build !windows

package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedWritableTargetIsExplicitlyRejected(t *testing.T) {
	target := filepath.Join(t.TempDir(), "shared-bin")
	if err := os.Mkdir(target, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o775); err != nil {
		t.Fatal(err)
	}
	if _, err := targetCoordinationPermissions(target); err == nil || !strings.Contains(err.Error(), "shared") {
		t.Fatalf("shared writable target error = %v, want explicit rejection", err)
	}
}

func TestPublicReadOnlySystemStyleTargetCannotSelfUpdate(t *testing.T) {
	target, err := os.MkdirTemp(os.TempDir(), "onebase-public-install-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(target) })
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := targetCoordinationPermissions(target); err == nil || !strings.Contains(err.Error(), "system installations") {
		t.Fatalf("public system-style target error = %v, want safe self-update rejection", err)
	}
}
