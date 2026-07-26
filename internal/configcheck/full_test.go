package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFullIncludesNameCollisionCheck(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "счёт.yaml"), `name: Счёт
fields:
  - name: Наименование
    type: string
`)
	mkFile(t, filepath.Join(dir, "documents", "счёт.yaml"), `name: Счёт
fields:
  - name: Дата
    type: date
`)

	res := RunFull(dir)

	if res.OK {
		t.Fatalf("RunFull returned OK, want name collision issue")
	}
	for _, is := range res.Issues {
		if is.Kind == "Имя таблицы" && strings.Contains(is.Message, "коллизия имён") {
			if is.Code != "metadata.name-collision" {
				t.Fatalf("unexpected issue code: %+v", is)
			}
			if is.SuggestedFix == "" {
				t.Fatalf("expected suggested fix: %+v", is)
			}
			return
		}
	}
	t.Fatalf("name collision issue not found: %+v", res.Issues)
}

func TestRunFullReportsUnknownAppConfigField(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Demo\nlimtis:\n  report_max_rows: 10\n")

	res := RunFull(dir)
	if res.OK {
		t.Fatal("RunFull returned OK for misspelled app config field")
	}
	for _, issue := range res.Issues {
		if strings.Contains(issue.Message, "field limtis not found") {
			return
		}
	}
	t.Fatalf("unknown app config field was not reported: %+v", res.Issues)
}

func TestRunFullReportsMalformedRole(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Demo\n")
	mkFile(t, filepath.Join(dir, "roles", "broken.yaml"), "name: [broken\n")

	res := RunFull(dir)
	if res.OK {
		t.Fatal("RunFull returned OK for malformed role")
	}
	for _, issue := range res.Issues {
		if strings.Contains(issue.Message, "parse role broken.yaml") {
			return
		}
	}
	t.Fatalf("malformed role was not reported: %+v", res.Issues)
}
