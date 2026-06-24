package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunFmtFormatsProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalogs", "клиент.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fields:\n  - {type: string, name: Наименование}\nname: Клиент\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fmtCmd.Flags().Set("project", dir); err != nil {
		t.Fatal(err)
	}
	if err := fmtCmd.Flags().Set("check", "false"); err != nil {
		t.Fatal(err)
	}
	if err := runFmt(fmtCmd, nil); err != nil {
		t.Fatalf("runFmt: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n"
	if string(got) != want {
		t.Fatalf("formatted file:\n%s\nwant:\n%s", got, want)
	}

	if err := fmtCmd.Flags().Set("check", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runFmt(fmtCmd, nil); err != nil {
		t.Fatalf("runFmt --check after format: %v", err)
	}
}
