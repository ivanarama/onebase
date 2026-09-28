package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectPostgresMissingHomeFailsClosedBeforeDial(t *testing.T) {
	missingHome := filepath.Join(t.TempDir(), "missing-home")
	t.Setenv(filesDirEnv, "")
	t.Setenv("HOME", missingHome)
	t.Setenv("USERPROFILE", missingHome)

	_, err := Connect(context.Background(), "postgres://onebase:secret@127.0.0.1:1/app")
	if err == nil {
		t.Fatal("Connect succeeded with an unavailable home and no explicit files directory")
	}
	if !strings.Contains(err.Error(), filesDirEnv) {
		t.Fatalf("Connect error = %q, want instruction to set %s", err, filesDirEnv)
	}
}

func TestDefaultFilesDirUsesExplicitAbsolutePath(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "persistent-files")
	t.Setenv(filesDirEnv, configured)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "missing-home"))
	t.Setenv("USERPROFILE", filepath.Join(t.TempDir(), "missing-profile"))

	got, err := defaultFilesDir("postgres://onebase:secret@db.example/app")
	if err != nil {
		t.Fatalf("defaultFilesDir: %v", err)
	}
	if got != filepath.Clean(configured) {
		t.Fatalf("defaultFilesDir = %q, want %q", got, filepath.Clean(configured))
	}
}

func TestDefaultFilesDirKeepsExistingHomeLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv(filesDirEnv, "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got, err := defaultFilesDir("postgres://onebase:secret@db.example/app")
	if err != nil {
		t.Fatalf("defaultFilesDir: %v", err)
	}
	want := filepath.Join(home, ".onebase", "files", "app")
	if got != want {
		t.Fatalf("defaultFilesDir = %q, want %q", got, want)
	}
}

func TestDefaultFilesDirRejectsRelativeOverride(t *testing.T) {
	t.Setenv(filesDirEnv, "relative/files")

	if _, err := defaultFilesDir("postgres://onebase:secret@db.example/app"); err == nil {
		t.Fatal("defaultFilesDir accepted a relative explicit path")
	}
}
