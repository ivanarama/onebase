package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/onec_forms"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func executeMatrix(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	root := &cobra.Command{Use: "onebase", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newMigrateMatrixCmd())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"migrate-matrix"}, args...))
	err := root.Execute()
	return out.Bytes(), err
}

func matrixJSON(t *testing.T, args ...string) ([]byte, onec_forms.CompatibilityMatrix) {
	t.Helper()
	out, err := executeMatrix(t, append(args, "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	var matrix onec_forms.CompatibilityMatrix
	if err := json.Unmarshal(out, &matrix); err != nil {
		t.Fatal(err)
	}
	return out, matrix
}

func TestMigrateMatrix_PublicCommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"migrate-matrix"})
	if err != nil || cmd.Name() != "migrate-matrix" {
		t.Fatalf("public compatibility matrix command is missing: %v", err)
	}
}

func TestMigrateMatrix_MeasurementIsNotCompatibility(t *testing.T) {
	first, unmeasured := matrixJSON(t)
	second, _ := matrixJSON(t)
	if !bytes.Equal(first, second) || unmeasured.Measured || unmeasured.SchemaVersion != 1 {
		t.Fatal("unmeasured registry is not stable")
	}
	for _, row := range unmeasured.Contracts {
		if row.Frequency != nil {
			t.Fatalf("invented frequency: %+v", row)
		}
	}
	_, measured := matrixJSON(t, filepath.Join("testdata", "migrate-scan"))
	if !measured.Measured {
		t.Fatal("fixture was not measured")
	}
	for _, row := range measured.Contracts {
		if row.Method != "" && (row.Frequency == nil || row.Frequency.Name != row.Method || row.Frequency.Count != 0) {
			t.Fatalf("module-wide raw count assigned to an absent exact method: %+v", row)
		}
		if row.RawPattern == "НСтр(" && (row.Frequency == nil || row.Frequency.Count != 3 || row.Frequency.Files != 1) {
			t.Fatalf("wrong slice 1 frequency: %+v", row)
		}
		if row.RawPattern == "Новый Массив" && (row.Frequency == nil || row.Frequency.Count != 2) {
			t.Fatalf("wrong array frequency: %+v", row)
		}
	}
	if !reflect.DeepEqual(measured.UnassessedMethods, []onec_forms.ScanFrequency{{Name: "Утилиты.Помощь", Count: 4, Files: 1}, {Name: "Редкий.Вход", Count: 1, Files: 1}}) {
		t.Fatalf("unknown methods received implied verdicts: %+v", measured.UnassessedMethods)
	}
	if len(measured.Contracts) != len(unmeasured.Contracts) {
		t.Fatal("counts created contracts")
	}
	for i := range measured.Contracts {
		measured.Contracts[i].Frequency = nil
		if !reflect.DeepEqual(measured.Contracts[i], unmeasured.Contracts[i]) {
			t.Fatal("counts changed verdicts")
		}
	}
	// An unreadable export must not emit a partial, apparently valid matrix.
	for _, args := range [][]string{{filepath.Join(t.TempDir(), "missing")}, {"testdata/migrate-scan", "--encoding", "invalid"}, {"one", "two"}} {
		out, err := executeMatrix(t, args...)
		if err == nil || bytes.Contains(out, []byte("schema_version")) || bytes.Contains(out, []byte("Матрица переноса")) {
			t.Fatalf("invalid input emitted a matrix: %q, %v", out, err)
		}
	}
}

func TestMigrateMatrix_ExactMethodFrequency(t *testing.T) {
	dir := copyScanFixture(t, false)
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		data = bytes.ReplaceAll(data, []byte("Утилиты"), []byte("ОбщегоНазначения"))
		data = bytes.ReplaceAll(data, []byte("Помощь"), []byte("КодОсновногоЯзыка"))
		data = bytes.ReplaceAll(data, []byte("утилиты"), []byte("общегоназначения"))
		data = bytes.ReplaceAll(data, []byte("помощь"), []byte("кодосновногоязыка"))
		return os.WriteFile(path, data, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "CommonModules", "Утилиты"), filepath.Join(dir, "CommonModules", "ОбщегоНазначения")); err != nil {
		t.Fatal(err)
	}
	_, matrix := matrixJSON(t, dir)
	found := false
	for _, contract := range matrix.Contracts {
		if contract.ID == "bsp-language" {
			found = true
			if contract.Frequency == nil || contract.Frequency.Count != 4 || contract.Frequency.Files != 1 {
				t.Fatalf("exact method count is missing: %+v", contract.Frequency)
			}
			if contract.Verdict != "пробел платформы" {
				t.Fatal("frequency authorized an absent library")
			}
		}
	}
	if !found {
		t.Fatal("method contract disappeared")
	}
	if !reflect.DeepEqual(matrix.UnassessedMethods, []onec_forms.ScanFrequency{{Name: "Редкий.Вход", Count: 1, Files: 1}}) {
		t.Fatalf("unassessed method was lost: %+v", matrix.UnassessedMethods)
	}
}

func TestMigrateMatrix_CheckedExamplesThroughEval(t *testing.T) {
	_, matrix := matrixJSON(t)
	if len(matrix.Contracts) < 10 {
		t.Fatal("plan 179 requires at least ten checked positions")
	}
	dir := filepath.Join(t.TempDir(), "project")
	runInitInto(t, dir, "")
	seen := map[string]bool{}
	for _, contract := range matrix.Contracts {
		if seen[contract.ID] || contract.ID == "" || contract.Signature == "" || contract.Advice == "" || len(contract.Examples) == 0 {
			t.Fatalf("invalid checked contract: %+v", contract)
		}
		seen[contract.ID] = true
		switch contract.Verdict {
		case "поддерживается без изменений", "механически", "нативный аналог":
		case "пробел платформы", "не поддерживается":
			if !strings.Contains(contract.Reference, "https://github.com/ivanarama/onebase/issues/") {
				t.Fatalf("gap has no issue: %s", contract.ID)
			}
		default:
			t.Fatalf("unknown verdict %q", contract.Verdict)
		}
		t.Run(contract.ID, func(t *testing.T) {
			for _, example := range contract.Examples {
				evalCmd.Flags().VisitAll(func(f *pflag.Flag) {
					value, changed := f.Value.String(), f.Changed
					t.Cleanup(func() {
						if err := f.Value.Set(value); err != nil {
							t.Errorf("restore --%s: %v", f.Name, err)
						}
						f.Changed = changed
					})
				})
				args := []string{"eval", "--project", dir, "--snippet", "--json"}
				if example.SQLite {
					args = append(args, "--sqlite", filepath.Join(t.TempDir(), "matrix.db"))
				}
				args = append(args, example.Code)
				rootCmd.SetArgs(args)
				t.Cleanup(func() { rootCmd.SetArgs(nil) })
				out, err := captureStdout(t, rootCmd.Execute)
				if example.ErrorContains != "" {
					if err == nil || !strings.Contains(err.Error(), example.ErrorContains) {
						t.Fatalf("expected specific limitation %q: %s => %s, %v", example.ErrorContains, example.Code, out, err)
					}
					t.Logf("expected limitation: %v", err)
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				var payload struct {
					Result   json.RawMessage
					Messages []string
				}
				if err := json.Unmarshal([]byte(out), &payload); err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal(payload.Result, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(example.Result, &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(payload.Messages, example.Messages) {
					t.Fatalf("%s: expected %s/messages %v, got %s", example.Code, example.Result, example.Messages, out)
				}
			}
		})
	}
}

func TestMigrateMatrix_DocumentAndAIGuideMatchRegistry(t *testing.T) {
	text, err := executeMatrix(t)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "bsl-compatibility.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, text) {
		t.Fatal("regenerate docs/bsl-compatibility.md with onebase migrate-matrix")
	}
	guide := runAIGuideCommand(t)
	if !strings.Contains(guide, string(text)) {
		t.Fatal("AI guide does not contain the same complete checked registry")
	}
}
