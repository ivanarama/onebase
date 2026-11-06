package xlsximport

import (
	"github.com/ivantit66/onebase/internal/testutil/xlsxadvisory"
	"testing"
)

func TestImportBytes_PublicAdvisories(t *testing.T) {
	for _, scenario := range xlsxadvisory.Cases {
		t.Run(scenario, func(t *testing.T) {
			xlsxadvisory.Run(t, func() {
				out, err := ImportBytes(xlsxadvisory.Book(t, scenario), Options{})
				xlsxadvisory.NoRecoveredPanic(t, err)
				if scenario == "cyclic-array" {
					if err != nil || out == nil || out.Layout == nil {
						t.Fatalf("cached formula import: %v", err)
					}
				} else if err == nil || out != nil {
					t.Fatalf("bad workbook returned layout: %v, %v", out, err)
				}
			})
		})
	}
}
