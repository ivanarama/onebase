package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUIRolesAndSwitches(t *testing.T) {
	for _, tc := range []struct {
		name, yaml  string
		active, bad bool
	}{
		{"legacy", "name: Demo\nui:\n  list_density: compact\n", false, false},
		{"all-explicit", "name: Demo\nui:\n  roles: []\n", true, false},
		{"scoped", "name: Demo\nui:\n  roles: [OTK]\n  except_roles: [Operator, Messenger]\n  list_detail_panel: false\n  list_row_actions: false\n  attachments: false\n  form_density: dense\n", true, false},
		{"bad-density", "name: Demo\nui:\n  form_density: denes\n", false, true},
		{"date-only", "name: Demo\nui:\n  short_date_input: true\n", false, false},
		{"report-only", "name: Demo\nui:\n  report_density: compact\n  report_roles: []\n  report_except_roles: [Operator, Messenger]\n", false, false},
		{"bad-report-density", "name: Demo\nui:\n  report_density: typo\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config", "app.yaml"), []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(dir)
			if (err != nil) != tc.bad {
				t.Fatalf("err=%v", err)
			}
			if err == nil && cfg.UIEnhancementsConfigured() != tc.active {
				t.Fatal("opt-in scope differs")
			}
		})
	}
}
