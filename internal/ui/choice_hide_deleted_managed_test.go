package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"golang.org/x/net/html"
)

func assertManagedDeletedChoice(t *testing.T, body, name, deleted string, want bool, marked string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	sel := keyboardFindHTML(doc, func(n *html.Node) bool {
		got, _ := keyboardHTMLAttr(n, "name")
		return n.Type == html.ElementNode && n.Data == "select" && got == name
	})
	if sel == nil {
		t.Fatalf("no select %s", name)
	}
	found := keyboardFindHTML(sel, func(n *html.Node) bool {
		v, _ := keyboardHTMLAttr(n, "value")
		return n.Data == "option" && v == deleted
	}) != nil
	if found != want {
		t.Errorf("deleted option=%v, want %v", found, want)
	}
	if got, _ := keyboardHTMLAttr(sel, "data-ref-marked"); got != marked {
		t.Errorf("marked=%q, want %q", got, marked)
	}
}

func TestManagedForm_DeletedChoiceOverrides(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name                    string
		catalog, field, element *bool
		folders, want           bool
		marked                  string
	}{
		{"element-show", nil, nil, &yes, false, true, "1"},
		{"element-hide", &yes, nil, &no, false, false, "0"},
		{"element-over-field-show", nil, &no, &yes, false, true, "1"},
		{"element-over-field-hide", &yes, &yes, &no, false, false, "0"},
		{"folders-field-show", nil, &yes, nil, true, true, "1"},
		{"folders-field-hide", &yes, &no, nil, true, false, "0"},
		{"catalog-show", &yes, nil, nil, false, true, ""},
		{"default-hide", nil, nil, nil, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHideDeletedFixture(t, tc.catalog, tc.field)
			el := &metadata.FormElement{ID: "master", Name: "ПолеМастер", Kind: metadata.FormElementField, DataPath: "Объект.Мастер", ChoiceShowDeleted: tc.element, ChoiceFolders: tc.folders}
			form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", EntityName: f.doc.Name, LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{el}}
			f.doc.Forms = []*metadata.FormModule{form}
			body := f.render(t, "/ui/document/Заявка/new", map[string]string{"kind": "document", "entity": f.doc.Name}, f.s.form)
			assertManagedDeletedChoice(t, body, "Мастер", f.deleted.String(), tc.want, tc.marked)
			// The later picker uses the same effective policy as the initial select.
			q := url.Values{}
			if tc.marked != "" {
				q.Set("marked", tc.marked)
			}
			if tc.folders {
				q.Set("form_entity", f.doc.Name)
				q.Set("form", form.Name)
				q.Set("element", el.ID)
			}
			_, shown := f.options(t, q.Encode())[f.deleted.String()]
			if shown != tc.want {
				t.Errorf("picker deleted option=%v, want %v", shown, tc.want)
			}
		})
	}
}

func TestManagedProcessor_DeletedChoiceParam(t *testing.T) {
	for _, show := range []bool{true, false} {
		t.Run(fmt.Sprint(show), func(t *testing.T) {
			catalog := !show
			f := newHideDeletedFixture(t, &catalog, nil)
			proc, err := processor.ParseBytes([]byte(fmt.Sprintf("name: Подбор\nparams:\n  - {name: Мастер, type: 'reference:Мастера', choice_show_deleted: %v}\n", show)))
			if err != nil {
				t.Fatal(err)
			}
			proc.Forms = []*metadata.FormModule{{Name: "ФормаОбработки", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{ID: "master", Name: "ПолеМастер", Kind: metadata.FormElementField, DataPath: "Объект.Мастер"}}}}
			f.s.reg.LoadProcessors([]*processor.Processor{proc})
			body := f.render(t, "/ui/processor/Подбор", map[string]string{"name": proc.Name}, f.s.processorForm)
			marked := "0"
			if show {
				marked = "1"
			}
			assertManagedDeletedChoice(t, body, "Мастер", f.deleted.String(), show, marked)
			rec := serveRefOptions(t, f.s, f.ref.Name, "marked="+marked, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("picker: %d", rec.Code)
			}
			var got struct{ Items []map[string]any }
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, it := range got.Items {
				found = found || it["id"] == f.deleted.String()
			}
			if found != show {
				t.Errorf("picker deleted=%v, want %v", found, show)
			}
		})
	}
}
