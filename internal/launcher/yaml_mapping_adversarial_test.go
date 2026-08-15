package launcher

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSetYAMLMapField_DoesNotLeaveDanglingNestedAnchor(t *testing.T) {
	const raw = `icon:
  nested: &shared-icon cart
mirror: *shared-icon
contents: {}
`

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatal(err)
	}
	err := setYAMLMapField(root.Content[0], "icon", nil)
	if err != nil {
		return // rejecting an unsafe edit is acceptable
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := yaml.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("edit emitted YAML with a dangling alias: %v\n%s", err, out)
	}
}

func TestSetYAMLMapField_DoesNotLeaveDanglingKeyAnchor(t *testing.T) {
	const raw = `&icon-key icon: cart
mirror: *icon-key
contents: {}
`

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatal(err)
	}
	err := setYAMLMapField(root.Content[0], "icon", nil)
	if err != nil {
		return // rejecting an unsafe edit is acceptable
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := yaml.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("edit emitted YAML with a dangling key alias: %v\n%s", err, out)
	}
}

func TestSetYAMLMapField_DoesNotReplaceNestedAnchorDefinition(t *testing.T) {
	const raw = `composition:
  nested: &shared-value old
mirror: *shared-value
`
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatal(err)
	}
	err := setYAMLMapField(root.Content[0], "composition", map[string]any{"new": true})
	if err != nil {
		return // rejecting an unsafe edit is acceptable
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := yaml.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("replacement emitted YAML with a dangling alias: %v\n%s", err, out)
	}
}

func TestSaveSubsystem_ClearsValuesInheritedThroughMergeKeys(t *testing.T) {
	h, cfgDir := newFileBaseHandler(t)
	h.runner = NewRunner()
	if err := os.MkdirAll(filepath.Join(cfgDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config", "app.yaml"), []byte("name: Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := writeCfgFileRv(t, cfgDir, "subsystems", "sales.yaml", `defaults: &defaults
  icon: cart
contents_defaults: &contents_defaults
  catalogs: [Old]
name: Sales
title: Old title
order: 1
<<: *defaults
contents:
  <<: *contents_defaults
`)

	form := url.Values{
		"subsystem_name": {"Sales"},
		"title":          {"New title"},
		"order":          {"2"},
	}
	rec := postCfgRv(t, "test", "/bases/test/configurator/subsystem", form, h.configuratorSaveSubsystem)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Icon     string `yaml:"icon"`
		Contents struct {
			Catalogs []string `yaml:"catalogs"`
		} `yaml:"contents"`
	}
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("saved YAML is invalid: %v\n%s", err, raw)
	}
	if got.Icon != "" || len(got.Contents.Catalogs) != 0 {
		t.Fatalf("unchecked form values survived via YAML merge: icon=%q catalogs=%v\n%s", got.Icon, got.Contents.Catalogs, raw)
	}
}

func TestUpdateYAMLMapping_RejectsEditingWholeInheritedMapping(t *testing.T) {
	const raw = `defaults: &defaults
  contents:
    catalogs: [Old]
    pages: [Keep]
<<: *defaults
`

	out, err := updateYAMLMapping([]byte(raw), "subsystems/test.yaml", func(doc *yaml.Node) error {
		_, err := yamlSubMap(doc, "contents")
		return err
	})
	if err == nil {
		t.Fatalf("expected an unsafe inherited mapping edit to be rejected:\n%s", out)
	}
	if out != nil {
		t.Fatalf("rejected edit returned replacement bytes: %q", out)
	}
}
