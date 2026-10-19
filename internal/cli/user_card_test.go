package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
)

func cardCheckFixture(t *testing.T, value string, extraAccount bool, targetType string) string {
	t.Helper()
	dir := t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: user-card-test\nversion: '1.0'\n")
	card := "name: Сотрудник\nfields:\n  - name: Учётка\n    type: reference:_users\n  - name: Филиал\n    type: string\n"
	if extraAccount {
		card += "  - name: ДругаяУчётка\n    type: reference:_users\n"
	}
	writeProcrunFixture(t, dir, "catalogs/Сотрудник.yaml", card)
	writeProcrunFixture(t, dir, "documents/Ордер.yaml", "name: Ордер\nfields:\n  - name: Филиал\n    type: "+targetType+"\n")
	writeProcrunFixture(t, dir, "roles/Сотрудник.yaml", "name: Сотрудник\npermissions:\n  processors: {}\n  documents:\n    Ордер: [read]\n  row_access:\n    documents:\n      Ордер:\n        read:\n          field: Филиал\n          op: eq\n          value: "+value+"\n")
	return dir
}
func TestUserCardPublicCheckMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, value, typ string
		extra, valid     bool
	}{
		{"structure", "{user_card: {catalog: Сотрудник, account_field: Учётка, field: Филиал}}", "string", false, true},
		{"shorthand", "{user_card: Сотрудник.Филиал}", "string", false, true},
		{"unknown catalog", "{user_card: {catalog: Missing, account_field: Учётка, field: Филиал}}", "string", false, false},
		{"unknown account", "{user_card: {catalog: Сотрудник, account_field: Missing, field: Филиал}}", "string", false, false},
		{"unknown field", "{user_card: {catalog: Сотрудник, account_field: Учётка, field: Missing}}", "string", false, false},
		{"ambiguous shorthand", "{user_card: Сотрудник.Филиал}", "string", true, false},
		{"type mismatch", "{user_card: Сотрудник.Филиал}", "number", false, false},
		{"two sources", "{user_card: Сотрудник.Филиал, literal: null}", "string", false, false},
		{"unknown value key", "{user_crad: Сотрудник.Филиал}", "string", false, false},
		{"unknown source key", "{user_card: {catalog: Сотрудник, account_field: Учётка, field: Филиал, typo: true}}", "string", false, false},
		{"table part", "{user_card: {catalog: Сотрудник, account_field: Учётка, table_part: Филиалы, column: Филиал}}", "string", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := cardCheckFixture(t, tc.value, tc.extra, tc.typ)
			out, err := runCheckCmd(t, runCheck, dir, map[string]string{"lint": "true", "json": "true"})
			var result configcheck.Result
			if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
				t.Fatalf("check JSON=%s err=%v", out, err)
			}
			if tc.valid && (err != nil || !result.OK) {
				t.Fatalf("valid source rejected: %v %s", err, out)
			}
			if !tc.valid && err == nil {
				t.Fatalf("invalid source silently accepted: %s", out)
			}
		})
	}
}
func TestUserCardPublicDescribeCanonicalizesSource(t *testing.T) {
	dir := cardCheckFixture(t, "{user_card: сотрудник.филиал}", false, "string")
	out, err := captureStdout(t, func() error { _, err := executeRootArgs(t, "describe", "--project", dir); return err })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"account_field": "Учётка"`) || !strings.Contains(out, `"field": "Филиал"`) || strings.Contains(out, `"user_card": "`) {
		t.Fatalf("describe source is not canonical: %s", out)
	}
}

// Check must reject an explicitly missing source without requiring --lint.
func TestUserCardPublicCheckRejectsNullSource(t *testing.T) {
	for _, value := range []string{"{user_card: null}", "{user_card: ~}", "{user_card: }", "{user_card: *missing}", "{user_card: {}}"} {
		for name, policy := range map[string]string{
			"ne":  "{field: Филиал, op: ne, value: " + value + "}",
			"not": "{not: {field: Филиал, op: eq, value: " + value + "}}",
			"any": "{any: [{field: Филиал, op: eq, value: " + value + "}, {field: Филиал, op: eq, value: {literal: A}}]}",
		} {
			t.Run(value+"/"+name, func(t *testing.T) {
				dir := cardCheckFixture(t, "{literal: null}", false, "string")
				role := fmt.Sprintf("missing: &missing null\nname: Сотрудник\npermissions:\n  processors: {}\n  documents: {Ордер: [read]}\n  row_access:\n    documents:\n      Ордер:\n        read: %s\n", policy)
				if err := os.WriteFile(filepath.Join(dir, "roles", "Сотрудник.yaml"), []byte(role), 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
				var result configcheck.Result
				if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
					t.Fatal(jsonErr)
				}
				if err == nil || result.OK {
					t.Fatalf("missing user_card accepted: %s", out)
				}
			})
		}
	}
	t.Run("literal null stays valid", func(t *testing.T) {
		dir := cardCheckFixture(t, "{literal: null}", false, "string")
		out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
		if err != nil {
			t.Fatalf("literal null rejected: %v %s", err, out)
		}
	})
}
