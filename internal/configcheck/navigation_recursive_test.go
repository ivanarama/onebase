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
