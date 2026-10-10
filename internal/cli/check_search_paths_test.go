package cli

import (
	"strings"
	"testing"
)

func TestCheckSearchPathsThroughRootCommand(t *testing.T) {
	for _, tc := range []struct {
		key, path string
		valid     bool
	}{
		{"search_fields", "Контакты.Телефон", true},
		{"search_fields", "контакты.телефон", true},
		{"search_fields", "Контакты.Нет", false},
		{"search_fields", "Контакты.Телефон.Нет", false},
		{"fulltext", "Контакты.Телефон", false},
		{"fulltext", "контакты.телефон", false},
	} {
		t.Run(tc.key+tc.path, func(t *testing.T) {
			defer resetCheckFlags(t)
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: search-path-test\nversion: \"1.0\"\n")
			writeProcrunFixture(t, dir, "catalogs/Клиенты.yaml", `name: Клиенты
fields:
  - {name: Наименование, type: string}
tableparts:
  - name: Контакты
    fields:
      - {name: Телефон, type: string}
`+tc.key+": ["+tc.path+"]\n")
			out, err := captureStdout(t, func() error { rootCmd.SetArgs([]string{"check", "--project", dir}); return rootCmd.Execute() })
			if tc.valid {
				if err != nil {
					t.Fatalf("check rejected valid path: %v %s", err, out)
				}
			} else if err == nil || !strings.Contains(out+err.Error(), "Клиенты") || !strings.Contains(out+err.Error(), tc.path) {
				t.Fatalf("check must report entity and original path: %v %s", err, out)
			}
		})
	}
}
