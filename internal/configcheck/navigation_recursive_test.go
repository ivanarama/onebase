package configcheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/configcheck"
	"github.com/ivantit66/onebase/internal/project"
)

func TestRecursiveMenuPublicProjectLoadAndCheck(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"config/app.yaml":    "name: RecursiveMenuTest\n",
		"catalogs/Item.yaml": "name: Item\nfields: []\n",
		"subsystems/OTK.yaml": `name: OTK
contents:
  catalogs: [Item]
menu:
  sections:
  - id: reports
    title: Reports
    nodes:
    - id: masters
      title: Masters
      nodes:
      - id: held
        title: Held
        nodes:
        - id: command
          target: catalog:Item
`,
	}
	for name, raw := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if loaded.Subsystems[0].Menu.Sections[0].Nodes[0].Nodes[0].Nodes[0].ID != "command" {
		t.Fatal("public loader lost depth")
	}
	issues, _ := configcheck.CheckDir(dir)
	issues = append(issues, configcheck.CheckCrossRefs(loaded, nil)...)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	raw := strings.Replace(files["subsystems/OTK.yaml"], "target: catalog:Item", "target: catalog:Missing", 1)
	if err := os.WriteFile(filepath.Join(dir, "subsystems", "OTK.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer changed.Close()
	issues = configcheck.CheckCrossRefs(changed, nil)
	found := false
	for _, issue := range issues {
		found = found || issue.Code == "navigation.membership"
	}
	if !found {
		t.Fatalf("public check accepted target outside contents: %v", issues)
	}
}

func TestMenuReportVariantPublicCheck(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"config/app.yaml":     "name: MenuVariantTest\n",
		"reports/Work.yaml":   "name: Work\nquery: ВЫБРАТЬ 1 КАК Amount\nvariants:\n- name: Полный\n  query: ВЫБРАТЬ 2 КАК Amount\n",
		"subsystems/OTK.yaml": "name: OTK\ncontents:\n  reports: [Work]\nmenu:\n  sections:\n  - id: reports\n    title: Reports\n    nodes:\n    - id: full\n      target: report:Work\n      variant: Полный\n",
	}
	for name, raw := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, variant := range []string{"Полный", "Missing"} {
		raw := strings.Replace(files["subsystems/OTK.yaml"], "variant: Полный", "variant: "+variant, 1)
		if err := os.WriteFile(filepath.Join(dir, "subsystems", "OTK.yaml"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		p, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		issues := configcheck.CheckCrossRefs(p, nil)
		p.Close()
		if variant == "Полный" {
			if len(issues) != 0 {
				t.Fatal("existing report variant rejected", issues)
			}
			continue
		}
		found := false
		for _, issue := range issues {
			found = found || issue.Code == "navigation.variant"
		}
		if !found {
			t.Fatal("missing report variant accepted", issues)
		}
	}
}
