package cli

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
)

func TestCheckListFilterSystemUserID(t *testing.T) {
	for _, tc := range []struct{ field, target, code string }{
		{"Author.id", "_users", ""},
		{"Author.ID", "_users", ""},
		{"Author.Missing", "_users", "form.list-filter"},
		{"Author.login", "_users", "form.list-filter"},
		{"Author.password_hash", "_users", "form.list-filter"},
		{"Author.id.More", "_users", "form.list-filter"},
		// Unknown references already fail at project loading, before form checks.
		{"Author.id", "_unknown_system", "project.load-failed"},
	} {
		t.Run(tc.field+"/"+tc.target, func(t *testing.T) {
			dir := checkFixture(t, false)
			writeProcrunFixture(t, dir, "catalogs/Tasks.yaml", fmt.Sprintf("name: Tasks\nfields:\n  - name: Наименование\n    type: string\n  - name: Author\n    type: reference:%s\n", tc.target))
			writeProcrunFixture(t, dir, "forms/tasks/list.form.yaml", fmt.Sprintf("schema: onebase.form/v1\nform:\n  name: TaskList\n  kind: list\n  entity: Tasks\n  filter:\n    - field: %s\n      value: '@ТекущийПользователь'\n", tc.field))
			out, err := runCheckCmd(t, runCheck, dir, map[string]string{"json": "true"})
			var result configcheck.Result
			if parseErr := json.Unmarshal([]byte(out), &result); parseErr != nil {
				t.Fatalf("%v: %s", parseErr, out)
			}
			if tc.code == "" {
				if err != nil || !result.OK {
					t.Fatalf("valid condition rejected: %v %s", err, out)
				}
				return
			}
			if err == nil || result.OK {
				t.Fatalf("unsupported condition accepted: %s", out)
			}
			for _, issue := range result.Issues {
				if issue.Code == tc.code {
					return
				}
			}
			t.Fatalf("missing %s: %s", tc.code, out)
		})
	}
}
