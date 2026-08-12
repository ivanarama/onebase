package ui

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
)

func mustBundle(t *testing.T, translation string) *i18n.Bundle {
	t.Helper()
	b, err := i18n.Load(fstest.MapFS{
		"en.json": {Data: []byte(`{"Сохранить":` + translation + `}`)},
	}, "")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	return b
}

func TestTemplateFuncsUseCapturedBundle(t *testing.T) {
	first := templateFuncs(mustBundle(t, `"Save"`))["t"].(func(string, string) string)
	second := templateFuncs(mustBundle(t, `"Store"`))["t"].(func(string, string) string)

	if got := first("en", "Сохранить"); got != "Save" {
		t.Fatalf("first bundle translation = %q", got)
	}
	if got := second("en", "Сохранить"); got != "Store" {
		t.Fatalf("second bundle translation = %q", got)
	}
	if got := first("en", "Сохранить"); got != "Save" {
		t.Fatalf("first bundle was affected by second template: %q", got)
	}
}

func TestInfoRegDetailPanelLocalizesSyntheticPeriodLabel(t *testing.T) {
	bundle, err := i18n.Load(fstest.MapFS{
		"en.json": {Data: []byte(`{"Период":"Period"}`)},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	panelFn := templateFuncs(bundle)["infoRegDetailPanel"].(func(*metadata.InfoRegister, map[string]any, string) string)
	raw := panelFn(&metadata.InfoRegister{Name: "Prices", Periodic: true}, map[string]any{"period": "12.08.2026"}, "en")
	var panel detailPanelData
	if err := json.Unmarshal([]byte(raw), &panel); err != nil {
		t.Fatal(err)
	}
	if got, ok := detailPanelValueByLabel(panel, "Period"); !ok || got != "12.08.2026" {
		t.Fatalf("localized period = %q, %v; payload=%+v", got, ok, panel)
	}
}
