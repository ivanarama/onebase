package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfig_UIStyle читает ui.style из app.yaml — то, чем включается новый
// вид форм.
func TestLoadConfig_UIStyle(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	appYAML := "name: Demo\nui:\n  style: new-style\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte(appYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI == nil {
		t.Fatal("блок ui не разобран")
	}
	if cfg.UI.Style != UIStyleNew {
		t.Fatalf("ui.style = %q, ожидали %q", cfg.UI.Style, UIStyleNew)
	}
	if style, err := cfg.NormalizedUIStyle(); err != nil || style != UIStyleNew {
		t.Fatalf("NormalizedUIStyle() = %q, %v", style, err)
	}
}

// TestLoadConfig_UIStyleUnknownKey — опечатка в ключе блока ловится загрузчиком,
// а не превращается в «стиль задан, но не работает».
func TestLoadConfig_UIStyleUnknownKey(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	appYAML := "name: Demo\nui:\n  style: new-style\n  theme: dark\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "app.yaml"), []byte(appYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("неизвестный ключ ui.theme принят без ошибки")
	}
	if !strings.Contains(err.Error(), "theme") {
		t.Fatalf("ошибка не называет ключ: %v", err)
	}
}
