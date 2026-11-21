package launcher

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestConfiguratorReportPIIRoundTripHTTP(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	path := writeCfgFile(t, cfgDir, "catalogs", "ReportSource.yaml", `name: ReportSource
fields:
  - name: City
    type: string
  - name: House
    type: string
    report_pii: true
`)
	base, err := h.store.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	data := h.loadCfgData(context.Background(), base, "tree")
	if data.Error != "" {
		t.Fatal(data.Error)
	}
	form := browserSubmitForEntity(t, renderCfgTree(t, data), "ReportSource")
	form.Set("new_field.1.name", "Comment")
	form.Set("new_field.1.type", "string")
	if ok, message := cfgResponse(t, postCfg(t, "test", "/bases/test/configurator/fields", form, h.configuratorSaveFields)); !ok {
		t.Fatal(message)
	}
	entity, err := metadata.LoadFile(path, metadata.KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range entity.Fields {
		if field.PII || field.ReportPII != (field.Name == "House") {
			t.Fatalf("policy lost or card changed: %+v", field)
		}
	}
}
