package configcheck

import (
	"path/filepath"
	"testing"
)

func TestRunFullReportChoiceMetadataIsBlocking(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "choice.yaml"), `name: Choice
params:
  - name: Direction
    type: string
    choice_label_suffix: Branch
`)
	result := RunFull(dir)
	for _, issue := range result.Issues {
		if issue.Code == "report.choice" {
			if result.OK {
				t.Fatal("invalid choice metadata accepted")
			}
			return
		}
	}
	t.Fatalf("missing blocking choice issue: %+v", result)
}
