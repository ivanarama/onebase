package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLintYAML_RequiredKnownForEntitiesAndTablePartsOnly(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "required.yaml"), `name: RequiredDocument
fields:
  - name: Header
    type: string
    required: true
tableparts:
  - name: Lines
    fields:
      - name: RowValue
        type: string
        required: true
`)
	mkFile(t, filepath.Join(dir, "registers", "unsupported.yaml"), `name: UnsupportedRequired
dimensions:
  - name: Key
    type: string
    required: true
resources:
  - name: Value
    type: number
`)

	var registerWarning bool
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code != "metadata.unvalidated-key" {
			continue
		}
		if strings.Contains(issue.File, "documents") {
			t.Fatalf("entity/table-part required was reported as unknown: %+v", issue)
		}
		if strings.Contains(issue.File, "registers") && strings.Contains(issue.Message, "required") {
			registerWarning = true
		}
	}
	if !registerWarning {
		t.Fatal("register required declaration was silently accepted although register writers do not enforce it")
	}
}
