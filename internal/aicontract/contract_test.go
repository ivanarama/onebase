package aicontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
)

func TestBuildContractAndCompactText(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("catalogs/клиент.yaml", "name: Клиент\nfields:\n  - {name: Наименование, type: string}\n")
	mustWrite("documents/заказ.yaml", "name: Заказ\nposting: true\nfields:\n  - {name: Клиент, type: reference:Клиент}\n")
	mustWrite("roles/basic.yaml", "name: Оператор\npermissions:\n  catalogs:\n    Клиент: [read]\n")
	mustWrite("src/заказ.posting.os", "Процедура Проведение() Экспорт\nКонецПроцедуры\n")

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	defer proj.Close()
	c, err := Build(dir, proj)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.SchemaVersion != 2 {
		t.Fatalf("schemaVersion=%d", c.SchemaVersion)
	}
	if len(c.Catalogs) != 1 || c.Catalogs[0].Source.File != "catalogs/клиент.yaml" {
		t.Fatalf("catalog source not filled: %+v", c.Catalogs)
	}
	if len(c.Roles) != 1 || c.Roles[0].Permissions.Catalogs["Клиент"][0] != "read" {
		t.Fatalf("roles not filled: %+v", c.Roles)
	}
	var hasPosting bool
	for _, m := range c.Modules {
		for _, p := range m.Procedures {
			if p.Name == "Проведение" && p.Export && p.Source.File == "src/заказ.posting.os" {
				hasPosting = true
			}
		}
	}
	if !hasPosting {
		t.Fatalf("posting module not described: %+v", c.Modules)
	}
	if txt := ProjectSchemaText(proj); !strings.Contains(txt, "Заказ (проводится)") || !strings.Contains(txt, "Клиент") {
		t.Fatalf("compact text missing objects:\n%s", txt)
	}
}
