package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/spf13/pflag"
)

// Exercise Cobra's real command dispatch, including fmt/check flags, instead
// of calling the numeric-type guard directly.
func executeNumericTypeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	type flagState struct {
		flag    *pflag.Flag
		value   string
		changed bool
	}
	var flags []flagState
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		flags = append(flags, flagState{f, f.Value.String(), f.Changed})
	})
	defer func() {
		rootCmd.SetArgs(nil)
		for _, f := range flags {
			if err := f.flag.Value.Set(f.value); err != nil {
				t.Error(err)
			}
			f.flag.Changed = f.changed
		}
	}()
	rootCmd.SetArgs(args)
	return captureStdout(t, rootCmd.Execute)
}

func numericTypeFixture(t *testing.T, field string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	writeProcrunFixture(t, dir, "config/app.yaml", "name: numeric-types\nversion: '1.0'\n")
	path = filepath.Join(dir, "catalogs", "Товар.yaml")
	writeProcrunFixture(t, dir, "catalogs/Товар.yaml", "name: Товар\nfields:\n  - "+field+"\n")
	return dir, path
}

func TestFmtCLI_TruncatedNumericTypePreservesFile(t *testing.T) {
	for _, field := range []string{
		"{name: Ставка, type: number(15,4), title: Ставка}",
		"{name: Ставка, type: decimal(10,2), title: Ставка}",
		"name: Ставка\n    type: number(15",
		"{name: Ставка, type: 'decimal(10'}",
		"{4): '', name: Ставка, type: number(15}", // already damaged: no guessing
		"name: Ставка\n    format: &spec number(15\n    type: *spec",
	} {
		t.Run(field, func(t *testing.T) {
			dir, path := numericTypeFixture(t, field)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []bool{false, true} {
				args := []string{"fmt", "--project", dir}
				if check {
					args = append(args, "--check")
				}
				_, err := executeNumericTypeCLI(t, args...)
				if err == nil || !strings.Contains(err.Error(), "оборванная числовая спецификация") ||
					!strings.Contains(err.Error(), path+":") || !strings.Contains(err.Error(), "'number(15,4)'") {
					t.Errorf("fmt check=%v: expected filename, location and quote guidance, got %v", check, err)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatalf("fmt check=%v changed the broken file: read=%v\nbefore=%s\nafter=%s", check, readErr, before, after)
				}
			}
			out, err := executeNumericTypeCLI(t, "check", "--project", dir, "--json")
			if !errors.Is(err, errSilentExit) {
				t.Fatalf("check must fail, got %v\n%s", err, out)
			}
			var result configcheck.Result
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			for _, issue := range result.Issues {
				if issue.Code == "field.type-truncated" && issue.File == "catalogs/Товар.yaml" && issue.Line > 0 && issue.Column > 0 {
					return
				}
			}
			t.Fatalf("missing structured numeric-type error: %s", out)
		})
	}
}

func TestCheckCLI_TruncatedNumericTypeIsError(t *testing.T) {
	for path, body := range map[string]string{
		"catalogs/Товар.yaml":            "name: Товар\nfields:\n  - {name: Ставка, type: number(15,4)}\n",
		"documents/Заказ.yaml":           "name: Заказ\ntableparts:\n  - name: Строки\n    fields:\n      - {name: Сумма, type: decimal(15,2)}\n",
		"inforegs/Цены.yaml":             "name: Цены\nresources:\n  - {name: Цена, type: number(15,4)}\n",
		"constants/Константы.yaml":       "constants:\n  - {name: Ставка, type: number(15,4)}\n",
		"forms/товар/элемента.form.yaml": "name: Элемента\nattributes:\n  - {name: Цена, type: number(15,4)}\n",
	} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: numeric-types\n")
			writeProcrunFixture(t, dir, path, body)
			out, err := executeNumericTypeCLI(t, "check", "--project", dir, "--json")
			if !errors.Is(err, errSilentExit) {
				t.Fatalf("check must reject truncated numeric type: %v\n%s", err, out)
			}
			var result configcheck.Result
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			for _, issue := range result.Issues {
				if issue.Code == "field.type-truncated" && issue.File == path && issue.Line > 0 && issue.Column > 0 {
					return
				}
			}
			t.Fatalf("missing numeric-type error for %s: %s", path, out)
		})
	}
}

func TestFmtCLI_ValidNumericTypeRoundTrip(t *testing.T) {
	for _, field := range []string{
		"{name: Ставка, type: 'number(15,4)', title: Ставка}",
		"{name: Ставка, type: \"decimal(15,4)\", title: Ставка}",
		"name: Ставка\n    type: number(15,4)",
		"name: Ставка\n    type: decimal(15,4)",
	} {
		t.Run(field, func(t *testing.T) {
			dir, path := numericTypeFixture(t, field)
			for _, args := range [][]string{
				{"fmt", "--project", dir},
				{"fmt", "--project", dir, "--check"},
				{"check", "--project", dir},
			} {
				out, err := executeNumericTypeCLI(t, args...)
				if err != nil {
					t.Fatalf("%v: %v\n%s", args, err, out)
				}
			}
			entity, err := metadata.LoadFile(path, metadata.KindCatalog)
			if err != nil {
				t.Fatal(err)
			}
			f := entity.Fields[0]
			if f.Type != metadata.FieldTypeNumber || f.Length != 15 || f.Scale != 4 {
				t.Fatalf("numeric precision/scale changed after fmt: %+v", f)
			}
		})
	}
}

func TestFmtCLI_UnknownTypeRemainsWarning(t *testing.T) {
	dir, _ := numericTypeFixture(t, "{name: Ставка, type: nubmer, title: 'number(15'} # decimal(10")
	if out, err := executeNumericTypeCLI(t, "fmt", "--project", dir); err != nil {
		t.Fatalf("unrelated type/title/comment rejected: %v\n%s", err, out)
	}
	out, err := executeNumericTypeCLI(t, "check", "--project", dir, "--json")
	if err != nil {
		t.Fatalf("unknown type must remain advisory: %v\n%s", err, out)
	}
	var result configcheck.Result
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	for _, warning := range result.Warnings {
		if warning.Code == "field.type" {
			return
		}
	}
	t.Fatalf("unknown-type warning lost: %s", out)
}
