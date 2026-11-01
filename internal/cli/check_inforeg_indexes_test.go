package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
)

// Проверяем зарегистрированную CLI-команду: поддерживаемые indexes не должны
// давать ложный совет удалить блок, а опечатки внутри индекса остаются видимы.
func TestCheckLintInfoRegisterIndexes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		periodic   bool
		index      string
		unknownKey string
	}{
		{name: "dimension", index: "fields: [Адрес]"},
		{name: "period_and_resource", periodic: true, index: "fields: [Адрес, period, Значение]\n    unique: false"},
		{name: "unknown_nested_key", periodic: true, index: "fields: [Адрес, period]\n    unqiue: false", unknownKey: "unqiue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { resetCheckFlags(t) })
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: lint-index-test\nversion: \"1.0\"\n")
			writeProcrunFixture(t, dir, "inforegs/Адреса.yaml", fmt.Sprintf(`name: Адреса
periodic: %t
dimensions:
  - name: Адрес
    type: string
resources:
  - name: Значение
    type: string
indexes:
  - %s
`, tc.periodic, tc.index))
			rootCmd.SetArgs([]string{"check", "--project", dir, "--lint", "--json"})
			out, err := captureStdout(t, rootCmd.Execute)
			if err != nil {
				t.Fatalf("check --lint --json: %v\n%s", err, out)
			}
			var res configcheck.Result
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("невалидный JSON: %v\n%s", err, out)
			}
			if !res.OK || len(res.Issues) != 0 {
				t.Fatalf("конфигурация должна быть валидной: %+v", res)
			}
			var unknown []configcheck.Issue
			for _, w := range res.Warnings {
				if w.Code == "metadata.unvalidated-key" {
					unknown = append(unknown, w)
				}
			}
			if tc.unknownKey == "" {
				if len(unknown) != 0 {
					t.Fatalf("поддерживаемый индекс помечен неизвестным: %+v", unknown)
				}
			} else if len(unknown) != 1 || !strings.Contains(unknown[0].Message, `"indexes[].`+tc.unknownKey+`"`) || unknown[0].File != "inforegs/Адреса.yaml" {
				t.Fatalf("ожидалась только диагностика вложенного ключа %q: %+v", tc.unknownKey, unknown)
			}
		})
	}
}
