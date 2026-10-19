package cli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/spf13/pflag"
)

func TestEvalCommandStructProperty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	runInitInto(t, dir, "")
	for _, tc := range []struct {
		name, snippet string
		wantError     bool
	}{
		{"value", `С = Новый Структура("А", 7); Возврат С.Property("а");`, false},
		{"extra-argument", `С = Новый Структура("А", 7); Выход = Неопределено; Возврат С.Свойство("А", Выход);`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evalCmd.Flags().VisitAll(func(f *pflag.Flag) {
				value, changed := f.Value.String(), f.Changed
				t.Cleanup(func() {
					if err := f.Value.Set(value); err != nil {
						t.Errorf("restore --%s: %v", f.Name, err)
					}
					f.Changed = changed
				})
			})
			rootCmd.SetArgs([]string{"eval", "--project", dir, "--snippet", "--json", tc.snippet})
			t.Cleanup(func() { rootCmd.SetArgs(nil) })
			out, err := captureStdout(t, rootCmd.Execute)
			if tc.wantError {
				var dslErr *interpreter.DSLError
				if !errors.As(err, &dslErr) || !strings.Contains(dslErr.Msg, "выходной параметр не поддерживается") {
					t.Fatalf("expected actionable DSL error, got %v (stdout %q)", err, out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var payload struct{ Result json.Number }
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Result.String() != "7" {
				t.Fatalf("expected numeric value 7, got %s", out)
			}
		})
	}
}

func TestAIGuideCommandStructPropertyContract(t *testing.T) {
	guide := runAIGuideCommand(t)
	for _, want := range []string{
		"Структура.Свойство(Ключ)", "Property", "а не Булево",
		"Выходной параметр не поддерживается", "второй и последующие аргументы вызывают",
		`Значение = С.Ключ;`, "Отсутствующий ключ и существующее",
		"Индексное чтение", "не поддерживается",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("ai-guide is missing %q", want)
		}
	}
}
