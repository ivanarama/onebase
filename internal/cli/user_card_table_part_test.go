package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tableCardCheckFixture(t *testing.T, selector, op, typ string) string {
	t.Helper()
	dir := cardCheckFixture(t, "{user_card: {catalog: сотрудник, account_field: учётка, "+selector+"}}", false, typ)
	path := filepath.Join(dir, "catalogs", "Сотрудник.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("tableparts:\n  - name: Филиалы\n    fields:\n      - name: Филиал\n        type: string\n")...)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "roles", "Сотрудник.yaml")
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "op: eq", "op: "+op, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUserCardTablePartPublicCheck(t *testing.T) {
	for _, tc := range []struct {
		name, selector, op, typ string
		valid                   bool
	}{
		{"in", "table_part: филиалы, column: филиал", "in", "string", true},
		{"not_in", "table_part: филиалы, column: филиал", "not_in", "string", true},
		{"mixed", "field: Филиал, table_part: Филиалы, column: Филиал", "in", "string", false},
		{"empty mixed", "field: '', table_part: Филиалы, column: Филиал", "in", "string", false},
		{"partial", "table_part: Филиалы", "in", "string", false},
		{"missing part", "column: Филиал", "in", "string", false},
		{"unknown part", "table_part: Missing, column: Филиал", "in", "string", false},
		{"unknown column", "table_part: Филиалы, column: Missing", "in", "string", false},
		{"system column", "table_part: Филиалы, column: id", "in", "string", false},
		{"dot column", "table_part: Филиалы, column: Филиал.Имя", "in", "string", false},
		{"scalar operator", "table_part: Филиалы, column: Филиал", "eq", "string", false},
		{"empty operator", "table_part: Филиалы, column: Филиал", "empty", "string", false},
		{"wrong type", "table_part: Филиалы, column: Филиал", "in", "number", false},
		{"scalar in", "field: Филиал", "in", "string", false},
		{"unknown key", "table_part: Филиалы, column: Филиал, typo: true", "in", "string", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tableCardCheckFixture(t, tc.selector, tc.op, tc.typ)
			out, err := runCheckCmd(t, runCheck, dir, map[string]string{"lint": "true", "json": "true"})
			if tc.valid && err != nil {
				t.Fatalf("valid rejected %v %s", err, out)
			}
			if !tc.valid && err == nil {
				t.Fatalf("invalid accepted %s", out)
			}
			if !tc.valid {
				return
			}
			out, err = captureStdout(t, func() error { _, err := executeRootArgs(t, "describe", "--project", dir); return err })
			if err != nil || !strings.Contains(out, `"table_part": "Филиалы"`) || !strings.Contains(out, `"column": "Филиал"`) || !strings.Contains(out, `"account_field": "Учётка"`) {
				t.Fatalf("canonical describe %v %s", err, out)
			}
		})
	}
}
