package configfmt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatBytesCanonicalizesYAML(t *testing.T) {
	in := []byte(`fields:
  - {type: string, name: Наименование}
title: Контрагенты
name: Контрагент
query: "ВЫБРАТЬ\n  1 КАК X\n"
`)

	out, err := FormatBytes(in)
	if err != nil {
		t.Fatalf("FormatBytes: %v", err)
	}
	got := string(out)
	if !strings.HasPrefix(got, "name: Контрагент\ntitle: Контрагенты\nfields:\n") {
		t.Fatalf("unexpected key order:\n%s", got)
	}
	if strings.Contains(got, "{type: string") {
		t.Fatalf("flow mapping was not expanded:\n%s", got)
	}
	if !strings.Contains(got, "query: |\n") {
		t.Fatalf("multiline string should use literal style:\n%s", got)
	}

	again, err := FormatBytes(out)
	if err != nil {
		t.Fatalf("FormatBytes second pass: %v", err)
	}
	if string(again) != got {
		t.Fatalf("formatter is not idempotent:\nfirst:\n%s\nsecond:\n%s", got, string(again))
	}
}

func TestCollectYAMLFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("name: X\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("catalogs/a.yaml")
	mustWrite("src/a.os")
	mustWrite(".git/ignored.yaml")

	files, err := CollectYAMLFiles([]string{dir, filepath.Join(dir, "catalogs", "a.yaml")})
	if err != nil {
		t.Fatalf("CollectYAMLFiles: %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(filepath.ToSlash(files[0]), "catalogs/a.yaml") {
		t.Fatalf("files = %#v", files)
	}
}

func TestFormatConfigContentSkipsNonYAML(t *testing.T) {
	src := []byte("Процедура X()\nКонецПроцедуры\n")
	got, err := FormatConfigContent("src/x.os", src)
	if err != nil {
		t.Fatalf("FormatConfigContent: %v", err)
	}
	if string(got) != string(src) {
		t.Fatalf("non-yaml content changed: %q", got)
	}
}
