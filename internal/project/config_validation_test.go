package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigMissingUsesProjectName(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != filepath.Base(dir) {
		t.Fatalf("name=%q, want %q", cfg.Name, filepath.Base(dir))
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	dir := writeAppConfig(t, "name: Demo\nlimtis:\n  report_max_rows: 10\n")
	_, err := LoadConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "field limtis not found") {
		t.Fatalf("error=%v, want unknown-field diagnostic", err)
	}
}

func TestLoadConfigRejectsMalformedYAML(t *testing.T) {
	dir := writeAppConfig(t, "name: [broken\n")
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("malformed app.yaml must fail")
	}
}

func TestLoadConfigRejectsMultipleDocuments(t *testing.T) {
	dir := writeAppConfig(t, "name: First\n---\nname: Second\n")
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("error=%v, want multiple-document diagnostic", err)
	}
}

func TestLoadConfigReturnsNonNotExistReadErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config", "app.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("unreadable app.yaml path must fail")
	}
}

func writeAppConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
