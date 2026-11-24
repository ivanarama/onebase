package project

import (
	"os"
	"path/filepath"
	"testing"
)

// app.yaml ui.list_density: compact включает плотные списки; без ключа —
// прежний вид; неизвестный ключ в ui по-прежнему отвергается строгим разбором.
func TestLoadConfigUIListDensity(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		compact    bool
		wantErr    bool
	}{
		{"без ui", "name: КЦ\n", false, false},
		{"compact", "name: КЦ\nui:\n  list_density: compact\n", true, false},
		{"другое значение", "name: КЦ\nui:\n  list_density: normal\n", false, false},
		{"неизвестный ключ ui", "name: КЦ\nui:\n  list_dencity: compact\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(dir)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%t", err, tc.wantErr)
			}
			if err == nil && cfg.CompactLists() != tc.compact {
				t.Fatalf("CompactLists=%t, want %t", cfg.CompactLists(), tc.compact)
			}
		})
	}
}
