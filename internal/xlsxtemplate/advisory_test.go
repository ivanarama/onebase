package xlsxtemplate

import (
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/testutil/xlsxadvisory"
	"testing"
)

func TestRenderBytes_PublicAdvisories(t *testing.T) {
	for _, scenario := range xlsxadvisory.Cases {
		t.Run(scenario, func(t *testing.T) {
			xlsxadvisory.Run(t, func() {
				out, err := RenderBytes(xlsxadvisory.Book(t, scenario), &printform.RenderContext{})
				xlsxadvisory.NoRecoveredPanic(t, err)
				if scenario == "cyclic-array" {
					// Rendering uses cached values, never formula evaluation. A cycle must
					// neither recurse nor alter that existing contract.
					if err != nil || len(out) == 0 {
						t.Fatalf("cached formula rendering: %v", err)
					}
				} else if err == nil || len(out) != 0 {
					t.Fatalf("bad template returned data: %d, %v", len(out), err)
				}
			})
		})
	}
}
