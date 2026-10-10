package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
)

func TestCheckChoiceFilterReportedOnce(t *testing.T) {
	dir := checkFixture(t, false)
	writeProcrunFixture(t, dir, "catalogs/Товар.yaml", `name: Товар
fields:
  - {name: Наименование, type: string}
  - {name: Выбор, type: "reference:Товар"}
`)
	writeProcrunFixture(t, dir, "forms/товар/основная.form.yaml", `schema: onebase.form/v1
form:
  name: Основная
  kind: object
  entity: Товар
elements:
  - id: selection
    kind: ПолеВвода
    data_path: Объект.Выбор
    choice_filter:
      - {field: ОтсутствующийРеквизит, op: eq, value: false}
    choice_context:
      Параметр: НеверныйПуть
`)

	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			defer resetCheckFlags(t)
			args := []string{"check", "--project", dir}
			if format == "json" {
				args = append(args, "--json")
			}
			rootCmd.SetArgs(args)
			out, err := captureStdout(t, rootCmd.Execute)
			if !errors.Is(err, errSilentExit) {
				t.Fatalf("check returned %v, want errSilentExit; output:\n%s", err, out)
			}

			if format == "json" {
				var result configcheck.Result
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, out)
				}
				if result.OK || result.Total != 2 {
					t.Fatalf("want two distinct errors, got %+v", result)
				}
			}
			for _, code := range []string{"form.choice-filter", "form.choice-context"} {
				if count := strings.Count(out, code); count != 1 {
					t.Errorf("%s reported %d times, want 1; output:\n%s", code, count, out)
				}
			}
			if !strings.Contains(out, "ОтсутствующийРеквизит") {
				t.Errorf("missing choice_filter error details:\n%s", out)
			}
		})
	}
}
