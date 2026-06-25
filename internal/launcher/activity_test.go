package launcher

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyFieldEdits_Activity(t *testing.T) {
	ent := &saveEntity{
		Name:   "Номенклатура",
		Fields: []saveField{{Name: "Наименование", Type: "string"}, {Name: "Активный", Type: "bool"}},
	}
	hide := true
	target := &saveActivity{Field: "Активный", DefaultScope: "active", HideFromChoice: &hide}
	activity := &target

	applyFieldEdits(ent, ent.Fields, nil, nil, nil, nil, activity)
	if ent.Activity == nil {
		t.Fatal("Activity = nil after explicit set")
	}
	if ent.Activity.Field != "Активный" || ent.Activity.DefaultScope != "active" || ent.Activity.HideFromChoice == nil || !*ent.Activity.HideFromChoice {
		t.Fatalf("Activity = %+v, want Активный/active/hide", ent.Activity)
	}

	target = nil
	applyFieldEdits(ent, ent.Fields, nil, nil, nil, nil, activity)
	if ent.Activity != nil {
		t.Fatalf("Activity was not cleared: %+v", ent.Activity)
	}
}

func TestConfiguratorTree_RendersActivitySettings(t *testing.T) {
	hide := true
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "X", ConfigSource: "file"},
		Lang: "ru",
		Tab:  "tree",
		Catalogs: []cfgEntity{{
			Name: "Номенклатура",
			Kind: "Справочник",
			Fields: []cfgField{
				{Name: "Наименование", Type: "string"},
				{Name: "Активный", Type: "bool"},
			},
			Activity: &cfgActivity{Field: "Активный", DefaultScope: "active", HideFromChoice: hide},
		}},
		AllEntityNames: []string{"Номенклатура"},
	}
	html := renderCfgTree(t, data)
	for _, want := range []string{
		`name="activity_present" value="1"`,
		`name="activity_enabled" value="1" checked`,
		`name="activity_field"`,
		`option value="Активный" selected`,
		`name="activity_default_scope"`,
		`name="activity_hide_from_choice" value="1" checked`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("configurator activity UI does not contain %q", want)
		}
	}
}

func TestConfiguratorSaveFields_Activity(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	if err := os.MkdirAll(filepath.Join(cfgDir, "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(cfgDir, "catalogs", "номенклатура.yaml")
	initial := `name: Номенклатура
fields:
  - name: Наименование
    type: string
  - name: Активный
    type: bool
`
	if err := os.WriteFile(yamlPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("entity", "Номенклатура")
	form.Set("entity_kind", "Справочник")
	form.Set("field.0.name", "Наименование")
	form.Set("field.0.type", "string")
	form.Set("field.1.name", "Активный")
	form.Set("field.1.type", "bool")
	form.Set("activity_present", "1")
	form.Set("activity_enabled", "1")
	form.Set("activity_field", "Активный")
	form.Set("activity_default_scope", "active")
	form.Set("activity_hide_from_choice", "1")

	rec := saveFieldsForm(t, h, "test", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("код ответа %d: %s", rec.Code, rec.Body.String())
	}

	out, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"activity:",
		"field: Активный",
		"default_scope: active",
		"hide_from_choice: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("saved YAML does not contain %q\n%s", want, got)
		}
	}
}
