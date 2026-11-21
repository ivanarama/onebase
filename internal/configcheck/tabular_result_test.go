package configcheck

import (
	"path/filepath"
	"testing"
)

func TestTabularReportExampleNativeLoader(t *testing.T) {
	issues, _ := CheckDir(filepath.Join("..", "..", "examples", "tabular-result"))
	if len(issues) != 0 {
		t.Fatalf("native example check: %+v", issues)
	}
}
