package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

func deletedChoiceHTML(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func deletedChoiceAttr(n *html.Node, name string) string { v, _ := keyboardHTMLAttr(n, name); return v }
func deletedChoiceJSON(t *testing.T, doc *html.Node, id string) map[string]any {
	t.Helper()
	n := keyboardFindHTML(doc, func(n *html.Node) bool { return n.Data == "script" && deletedChoiceAttr(n, "id") == id })
	if n == nil || n.FirstChild == nil {
		t.Fatalf("missing JSON script %s", id)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(n.FirstChild.Data), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeletedChoicePlacementsWithoutID(t *testing.T) {
	for _, show := range []bool{true, false} {
		t.Run(fmt.Sprint(show), func(t *testing.T) {
			catalog := !show
			f := newHideDeletedFixture(t, &catalog, nil)
			// The YAML accepted by the form loader can omit IDs, even for two placements.
			var form metadata.FormModule
			raw := fmt.Sprintf("name: ФормаОбъекта\nkind: object\nlayout_kind: managed\nelements:\n  - {kind: ПолеВвода, name: Выбор, data_path: Объект.Мастер, choice_show_deleted: %v}\n  - {kind: ПолеВвода, name: ДругойВыбор, data_path: Объект.Мастер, choice_show_deleted: %v}\n", show, !show)
			if err := yaml.Unmarshal([]byte(raw), &form); err != nil {
				t.Fatal(err)
			}
			form.LayoutKind = metadata.FormLayoutManaged
			f.doc.Forms = []*metadata.FormModule{&form}
			body := f.render(t, "/ui/document/Заявка/new", map[string]string{"kind": "document", "entity": f.doc.Name}, f.s.form)
			doc := deletedChoiceHTML(t, body)
			var selects []*html.Node
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Data == "select" && deletedChoiceAttr(n, "name") == "Мастер" {
					selects = append(selects, n)
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
			}
			walk(doc)
			if len(selects) != 2 {
				t.Fatalf("selects=%d", len(selects))
			}
			for i, sel := range selects {
				want := show
				if i == 1 {
					want = !show
				}
				found := keyboardFindHTML(sel, func(n *html.Node) bool {
					return n.Data == "option" && deletedChoiceAttr(n, "value") == f.deleted.String()
				}) != nil
				if found != want {
					t.Errorf("placement %d deleted=%v want %v", i, found, want)
				}
			}
		})
	}
}

func TestDeletedChoiceLocalAttributeEmptyCatalog(t *testing.T) {
	for _, show := range []bool{true, false} {
		t.Run(fmt.Sprint(show), func(t *testing.T) {
			f := newHideDeletedFixture(t, nil, nil)
			if err := f.db.MarkForDeletion(context.Background(), f.ref.Name, f.active, true); err != nil {
				t.Fatal(err)
			}
			form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
				Attributes: []*metadata.FormAttribute{{Name: "Локальный", TypeRef: "CatalogRef.Мастера"}},
				Elements:   []*metadata.FormElement{{ID: "local", Name: "ЛокальныйВыбор", Kind: metadata.FormElementField, DataPath: "Форма.Локальный", ChoiceShowDeleted: &show}}}
			f.doc.Forms = []*metadata.FormModule{form}
			body := f.render(t, "/ui/document/Заявка/new", map[string]string{"kind": "document", "entity": f.doc.Name}, f.s.form)
			assertManagedDeletedChoice(t, body, "Локальный", f.deleted.String(), show, map[bool]string{true: "1", false: "0"}[show])
		})
	}
}

func TestDeletedChoiceTablePlacements(t *testing.T) {
	for _, mode := range []string{"auto", "grid", "dom", "grid-short", "dom-short"} {
		for _, show := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%v", mode, show), func(t *testing.T) {
				catalog := !show
				f := newHideDeletedFixture(t, &catalog, nil)
				fieldPolicy := show
				if mode != "auto" {
					fieldPolicy = !show
				}
				f.doc.TableParts = []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Мастер", Type: "reference:Мастера", RefEntity: f.ref.Name, ChoiceShowDeleted: &fieldPolicy}}}}
				if mode != "auto" {
					f.doc.Forms = []*metadata.FormModule{{Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{ID: "rows", Name: "Строки", Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки", NoGrid: strings.HasPrefix(mode, "dom"), Columns: []*metadata.FormElement{{ID: "master", Name: "Выбор", Kind: metadata.FormElementColumn, DataPath: "Объект.Строки.Мастер", ChoiceShowDeleted: &show}}}}}}
				}
				if strings.HasSuffix(mode, "-short") {
					// A valid column can bind by field without data_path or a picker ID.
					column := f.doc.Forms[0].Elements[0].Columns[0]
					column.DataPath, column.ID, column.FieldName = "", "", "Мастер"
				}
				ctx := context.Background()
				if err := f.db.Migrate(ctx, []*metadata.Entity{f.ref, f.doc}); err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				if err := f.db.Upsert(ctx, f.doc.Name, id, map[string]any{}, f.doc); err != nil {
					t.Fatal(err)
				}
				if err := f.db.UpsertTablePartRows(ctx, f.doc.Name, "Строки", id, []map[string]any{{"Мастер": ""}}, f.doc.TableParts[0]); err != nil {
					t.Fatal(err)
				}
				body := f.render(t, "/ui/document/Заявка/"+id.String(), map[string]string{"kind": "document", "entity": f.doc.Name, "id": id.String()}, f.s.formEdit)
				doc := deletedChoiceHTML(t, body)
				marked := map[bool]string{true: "1", false: "0"}[show]
				if strings.HasPrefix(mode, "grid") {
					grid := keyboardFindHTML(doc, func(n *html.Node) bool { return deletedChoiceAttr(n, "data-sg-tp") == "Строки" })
					if grid == nil {
						t.Fatal("missing grid")
					}
					var cols []struct {
						RefMarked string `json:"refMarked"`
					}
					if err := json.Unmarshal([]byte(deletedChoiceAttr(grid, "data-sg-cols")), &cols); err != nil {
						t.Fatal(err)
					}
					if len(cols) != 1 || cols[0].RefMarked != marked {
						t.Errorf("grid columns=%+v want marked=%s", cols, marked)
					}
					var refs map[string][]map[string]any
					if err := json.Unmarshal([]byte(deletedChoiceAttr(grid, "data-sg-ref")), &refs); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, row := range refs["Мастер"] {
						if row["id"] == f.deleted.String() {
							found = true
						}
					}
					if found != show {
						t.Errorf("grid initial deleted=%v want %v", found, show)
					}
				} else {
					assertManagedDeletedChoice(t, body, "tp.Строки.0.Мастер", f.deleted.String(), show, marked)
				}
				// Actual HTTP metadata is consumed by production addTpRow and round-trip redraw.
				tbody := keyboardFindHTML(doc, func(n *html.Node) bool {
					return n.Data == "tbody" && deletedChoiceAttr(n, "id") == "tp-body-Строки"
				})
				if tbody == nil {
					return
				} // SlickGrid has its own editor/refresh coverage.
				attrs := map[string]string{}
				for _, a := range tbody.Attr {
					attrs[a.Key] = a.Val
				}
				optionsID := "ob-tp-ref-opts"
				if strings.HasPrefix(mode, "dom") {
					optionsID = "ob-managed-tp-ref-opts"
				}
				payload := map[string]any{"attrs": attrs, "meta": deletedChoiceJSON(t, doc, "ob-tp-ref-meta"), "options": deletedChoiceJSON(t, doc, optionsID), "managed": strings.HasPrefix(mode, "dom"), "selected": f.deleted.String()}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				node, err := exec.LookPath("node")
				if err != nil {
					t.Fatal("node is required for the HTTP-to-browser regression")
				}
				cmd := exec.Command(node, "static/choice_deleted_placement_harness.js") //nolint:gosec // test runtime resolved by LookPath
				cmd.Stdin = bytes.NewReader(raw)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("browser: %v\n%s", err, out)
				}
				var results []struct {
					Marked   string   `json:"marked"`
					Query    string   `json:"query"`
					Context  string   `json:"context"`
					IDs      []string `json:"ids"`
					Selected bool     `json:"selected"`
				}
				if err := json.Unmarshal(out, &results); err != nil {
					t.Fatal(err, string(out))
				}
				if len(results) == 0 {
					t.Fatal("no browser checks executed")
				}
				for i, result := range results {
					foundInitial := false
					for _, id := range result.IDs {
						if id == f.deleted.String() {
							foundInitial = true
						}
					}
					wantInitial := show || result.Selected
					if foundInitial != wantInitial {
						t.Errorf("browser step %d initial deleted=%v want %v", i, foundInitial, wantInitial)
					}
					if result.Marked != marked {
						t.Errorf("browser step %d marked=%q want %q", i, result.Marked, marked)
					}
					q, err := url.ParseQuery(strings.TrimPrefix(result.Query, "&"))
					if err != nil {
						t.Fatal(err)
					}
					if result.Context != "" {
						var context map[string]any
						if err := json.Unmarshal([]byte(result.Context), &context); err != nil {
							t.Fatal(err)
						}
						for _, key := range []string{"form_entity", "form", "element"} {
							q.Set(key, fmt.Sprint(context[key]))
						}
						q.Set("row_id", "0")
						q.Set("sources", "{}")
					}
					_, found := f.options(t, q.Encode())[f.deleted.String()]
					if found != show {
						t.Errorf("browser step %d HTTP deleted=%v want %v", i, found, show)
					}
				}
			})
		}
	}
}
