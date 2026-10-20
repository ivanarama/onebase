package launcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// The browser creates conditions with production canvas controls, saves via the
// registered launcher routes, reloads the page and then Go reloads persisted YAML.
func TestChoiceFilterConfiguratorBrowserAcceptance(t *testing.T) {
	browseracceptance.Require(t)
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(browseracceptance.Root(), "e2e/fixtures/choice-filter"))); err != nil {
		t.Fatal(err)
	}
	formPath := filepath.Join(dir, "forms/заявка/объекта.form.yaml")
	const initial = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
elements:
  - id: direction
    kind: ПолеВвода
    name: ПолеНаправление
    data_path: Объект.Направление
  - id: fault-picker
    kind: ПолеВвода
    name: ПолеНеисправность
    data_path: Объект.Неисправность
    choice: true # acceptance: preserve neighboring YAML comment
`
	if err := os.WriteFile(formPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "configurator.db")
	db, err := storage.ConnectSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureServiceSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{Path: dir, ConfigSource: "file", DBType: "sqlite", DBPath: dbPath}
	if err := store.Add(base); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() { _ = server.ListenAndServe() }()
	browseracceptance.Run(t, "configurator", map[string]string{
		"OB_ACCEPTANCE_URL": server.URL(), "OB_ACCEPTANCE_BASE": base.ID,
	})
	form, err := loader.NewManagedFormLoader().LoadFormFile(formPath, "Заявка")
	if err != nil {
		t.Fatal(err)
	}
	conditions := form.Elements[1].ChoiceFilter
	if len(conditions) != 2 || conditions[0].Field != "Направление" || conditions[0].Op != metadata.FormChoiceOpInHierarchy || conditions[0].From != "Объект.Направление" || conditions[1].Field != "is_folder" || conditions[1].Op != metadata.FormChoiceOpEqual || conditions[1].Value == nil || *conditions[1].Value {
		t.Fatalf("persisted ordered/typed choice_filter differs: %+v", conditions)
	}
}
