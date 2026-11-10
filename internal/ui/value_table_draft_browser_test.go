package ui

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/testutil/browseracceptance"
)

// Each fixture is opened through a mounted GET and observed through real
// FormData, the production event queue, HTTP and DSL on the matrix database.
func testValueTableDraftBrowser(t *testing.T, db *storage.DB) {
	t.Helper()
	var entities []*metadata.Entity
	for _, name := range []string{"Draft", "Empty", "Scalars", "Protected", "Readonly"} {
		columns := []*metadata.FormAttributeColumn{{Name: "Name", TypeRef: "string"}}
		children := []*metadata.FormElement{{Kind: metadata.FormElementColumn, Name: "Name", DataPath: "Name"}}
		seed := `Стр = Results.Добавить(); Стр.Name = "seed";`
		if name == "Empty" {
			seed = ""
		}
		if name == "Scalars" || name == "Protected" {
			columns = append(columns, &metadata.FormAttributeColumn{Name: "Flag", TypeRef: "bool"})
			children = append(children, &metadata.FormElement{Kind: metadata.FormElementColumn, Name: "Flag", DataPath: "Flag", ReadOnly: name == "Protected"})
			if name == "Protected" {
				columns = append(columns, &metadata.FormAttributeColumn{Name: "ID", TypeRef: "string"}, &metadata.FormAttributeColumn{Name: "Locked", TypeRef: "number"})
				children = append(children, &metadata.FormElement{Kind: metadata.FormElementColumn, Name: "Locked", DataPath: "Locked", ReadOnly: true})
				seed += ` Стр.ID = "hidden"; Стр.Locked = 0; Стр.Flag = Ложь;`
			}
		}
		table := func(name string, readonly bool) *metadata.FormElement {
			return &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: name, DataPath: "Форма.Results", ReadOnly: readonly, Children: children,
				Handlers: map[metadata.FormEventType]string{metadata.FormEventOnRowActivated: "SelectRow"}}
		}
		form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", EntityName: name, LayoutKind: metadata.FormLayoutManaged,
			Elements:   []*metadata.FormElement{table("ReadonlyResults", true), table("WritableResults", false)},
			Attributes: []*metadata.FormAttribute{{Name: "Results", TypeRef: "ValueTable", Columns: columns}},
			Handlers:   map[metadata.FormEventType]string{metadata.FormEventOnOpen: "OpenForm"},
			ProgramAST: mustParse(t, `
Процедура OpenForm()
 Results.Очистить(); `+seed+`
КонецПроцедуры
Процедура SelectRow()
 Сообщить(ТекущаяСтрока.Name);
 Сообщить(Results.Количество());
КонецПроцедуры`),
		}
		if name == "Readonly" {
			form.Elements = form.Elements[:1]
		}
		entities = append(entities, &metadata.Entity{Name: name, Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{form}})
	}
	s := newSubmitTestServerOn(t, db, entities)
	router := chi.NewRouter()
	s.MountStatic(router)
	router.Get("/ui/{kind}/{entity}/new", s.form)
	router.Post("/ui/{kind}/{entity}/form-event", s.handleManagedFormEvent)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config", "acceptance.config.js", "vt-drafts.spec.js")
	cmd.Dir = filepath.Join(browseracceptance.Root(), "e2e")
	cmd.Env = append(os.Environ(), "OB_ACCEPTANCE_SUITE=vt-drafts", "OB_ACCEPTANCE_URL="+server.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("ValueTable drafts browser: %v", err)
	}
}
