package metadata_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestLoadReportPIIIsIndependentFromCardPII(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := os.WriteFile(path, []byte(`name: ReportSource
numerator:
  field:
    report_pii: true
fields:
  - name: House
    type: string
    report_pii: true
  - name: City
    type: string
`), 0600); err != nil {
		t.Fatal(err)
	}
	entity, err := metadata.LoadFile(path, metadata.KindDocument)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range entity.Fields {
		if field.PII {
			t.Fatalf("ordinary PII changed: %+v", field)
		}
		want := field.Name == "Номер" || field.Name == "House"
		if field.ReportPII != want {
			t.Fatalf("report PII not loaded: %+v", field)
		}
	}
}
