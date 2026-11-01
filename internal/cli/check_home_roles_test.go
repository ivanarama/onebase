package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/spf13/cobra"
)

func TestCheckHomeNavByRoleContracts(t *testing.T) {
	for _, tc := range []struct{ name, contents, warning, missing string }{
		{"valid", "catalogs: [Classes]\n    pages: [RolePage]", "", ""},
		{"unknown-content", "catalogz: [Classes]", "nav_by_role.Teacher.catalogz", ""},
		{"missing-page", "pages: [MissingPage]", "", "MissingPage"},
		{"case-insensitive-page", "pages: [rolepage]", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: role-menu-check\nversion: \"1.0\"\n")
			writeProcrunFixture(t, dir, "catalogs/Classes.yaml", "name: Classes\nfields:\n  - name: Name\n    type: string\n")
			writeProcrunFixture(t, dir, "pages/RolePage.yaml", "name: RolePage\ntitle: Role page\n")
			writeProcrunFixture(t, dir, "src/rolepage.page.os", "Процедура ПриФормировании(Страница)\nКонецПроцедуры\n")
			writeProcrunFixture(t, dir, "roles/Teacher.yaml", "name: Teacher\npermissions:\n  processors: {}\n  catalogs:\n    Classes: [read]\n")
			writeProcrunFixture(t, dir, "config/home_page.yaml", "nav_by_role:\n  Teacher:\n    "+tc.contents+"\n")
			out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true", "lint": "true"})
			var result configcheck.Result
			if e := json.Unmarshal([]byte(out), &result); e != nil {
				t.Fatal(e, out)
			}
			if tc.missing != "" {
				if !errors.Is(err, errSilentExit) || result.OK || !strings.Contains(out, tc.missing) || !strings.Contains(out, "home_page.nav_by_role.Teacher") {
					t.Fatalf("missing page accepted: %v %s", err, out)
				}
			} else if err != nil || !result.OK {
				t.Fatalf("valid config failed: %v %s", err, out)
			}
			var warnings []string
			for _, issue := range result.Warnings {
				if issue.Code == "metadata.unvalidated-key" {
					warnings = append(warnings, issue.Message)
				}
			}
			if tc.warning == "" && len(warnings) != 0 || tc.warning != "" && (len(warnings) != 1 || !strings.Contains(warnings[0], tc.warning)) {
				t.Fatalf("unexpected lint warnings: %+v", warnings)
			}
		})
	}
}

// Роли — только roles/*.yaml: несуществующая роль nav_by_role — ошибка и при
// отсутствии каталога ролей; ключи, совпадающие без учёта регистра, — тоже.
func TestCheckHomeNavByRoleRoleNames(t *testing.T) {
	for _, tc := range []struct{ name, teacherRole, navByRole, want string }{
		{"no-roles-dir", "", "  MissingRole:\n    catalogs: [Classes]\n", `роль \"MissingRole\" не найдена`},
		{"empty-role-no-roles-dir", "", "  MissingRole: {}\n", `роль \"MissingRole\" не найдена`},
		{"case-duplicate", "name: Teacher\npermissions:\n  processors: {}\n  catalogs:\n    Classes: [read]\n",
			"  Teacher:\n    catalogs: [Classes]\n  teacher:\n    catalogs: [Classes]\n", "имена ролей — без учёта регистра"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: role-menu-names\nversion: \"1.0\"\n")
			writeProcrunFixture(t, dir, "catalogs/Classes.yaml", "name: Classes\nfields:\n  - name: Name\n    type: string\n")
			if tc.teacherRole != "" {
				writeProcrunFixture(t, dir, "roles/Teacher.yaml", tc.teacherRole)
			}
			writeProcrunFixture(t, dir, "config/home_page.yaml", "nav_by_role:\n"+tc.navByRole)
			out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
			var result configcheck.Result
			if e := json.Unmarshal([]byte(out), &result); e != nil {
				t.Fatal(e, out)
			}
			if !errors.Is(err, errSilentExit) || result.OK || !strings.Contains(out, tc.want) {
				t.Fatalf("want error %q, got %v %s", tc.want, err, out)
			}
		})
	}
}

func TestSchemaHomeNavByRole(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("list", false, "")
	cmd.Flags().String("kind", "home-page", "")
	out, err := captureStdout(t, func() error { return runSchema(cmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(out), &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	role, ok := props["nav_by_role"].(map[string]any)
	if !ok || role["type"] != "object" {
		t.Fatal("schema lacks role map")
	}
	contents, ok := role["additionalProperties"].(map[string]any)
	if !ok || contents["type"] != "object" || contents["additionalProperties"] != false {
		t.Fatal("schema lacks typed closed contents")
	}
	for _, kind := range []string{"catalogs", "documents", "registers", "inforegs", "reports", "processors", "journals", "pages"} {
		p, ok := contents["properties"].(map[string]any)[kind].(map[string]any)
		if !ok || p["type"] != "array" || p["items"].(map[string]any)["type"] != "string" {
			t.Fatalf("invalid contents %s", kind)
		}
	}
}

func TestCheckSubsystemNavByRoleOutsideContentsIsError(t *testing.T) {
	dir := semanticCheckFixture(t, "home_page:\n  nav_by_role:\n    Teacher:\n      catalogs: [Товар, Outside]\n")
	writeProcrunFixture(t, dir, "catalogs/Outside.yaml", "name: Outside\nfields:\n  - name: Title\n    type: string\n")
	writeProcrunFixture(t, dir, "roles/Teacher.yaml", "name: Teacher\npermissions:\n  processors: {}\n  catalogs:\n    Товар: [read]\n    Outside: [read]\n")
	out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true", "lint": "true"})
	var result configcheck.Result
	if parseErr := json.Unmarshal([]byte(out), &result); parseErr != nil {
		t.Fatalf("%v: %s", parseErr, out)
	}
	if !errors.Is(err, errSilentExit) || result.OK {
		t.Fatalf("outside role member must fail check: %v %s", err, out)
	}
	matches := func(issue configcheck.Issue) bool {
		return issue.Object == "Education.home_page.nav_by_role.Teacher" && strings.Contains(issue.Message, `справочник "Outside" не входит в contents`)
	}
	for _, warning := range result.Warnings {
		if matches(warning) {
			t.Fatalf("outside role member reported as warning: %+v", warning)
		}
	}
	for _, issue := range result.Issues {
		if matches(issue) {
			return
		}
	}
	t.Fatalf("missing blocking outside-contents diagnostic: %s", out)
}
