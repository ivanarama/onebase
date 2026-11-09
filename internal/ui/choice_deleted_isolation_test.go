package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Exercise the actual GET of a saved document, rather than an option helper.
// Keeping row zero's historical value must not offer it in an empty row.
func TestTPDeletedSelectionIsolationHTTP(t *testing.T) {
	for _, mode := range []string{"auto", "dom", "dom-explicit"} {
		t.Run(mode, func(t *testing.T) {
			no := false
			f := newHideDeletedFixture(t, nil, &no)
			f.doc.TableParts = []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Мастер", Type: "reference:Мастера", RefEntity: f.ref.Name, ChoiceShowDeleted: &no}}}}
			if mode != "auto" {
				table := &metadata.FormElement{ID: "rows", Name: "Строки", Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки", NoGrid: true}
				if mode == "dom-explicit" {
					table.Columns = []*metadata.FormElement{{ID: "master", Kind: metadata.FormElementColumn, DataPath: "Объект.Строки.Мастер", ChoiceShowDeleted: &no}}
				}
				f.doc.Forms = []*metadata.FormModule{{Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{table}}}
			}
			ctx := context.Background()
			if err := f.db.Migrate(ctx, []*metadata.Entity{f.ref, f.doc}); err != nil {
				t.Fatal(err)
			}
			id := uuid.New()
			if err := f.db.Upsert(ctx, f.doc.Name, id, map[string]any{}, f.doc); err != nil {
				t.Fatal(err)
			}
			if err := f.db.UpsertTablePartRows(ctx, f.doc.Name, "Строки", id, []map[string]any{{"Мастер": f.deleted.String()}, {"Мастер": ""}}, f.doc.TableParts[0]); err != nil {
				t.Fatal(err)
			}
			body := f.render(t, "/ui/document/Заявка/"+id.String(), map[string]string{"kind": "document", "entity": f.doc.Name, "id": id.String()}, f.s.formEdit)
			doc := deletedChoiceHTML(t, body)
			for row := 0; row < 2; row++ {
				name := fmt.Sprintf("tp.Строки.%d.Мастер", row)
				sel := keyboardFindHTML(doc, func(n *html.Node) bool { return n.Data == "select" && deletedChoiceAttr(n, "name") == name })
				if sel == nil {
					t.Fatalf("missing %s", name)
				}
				option := keyboardFindHTML(sel, func(n *html.Node) bool {
					return n.Data == "option" && deletedChoiceAttr(n, "value") == f.deleted.String()
				})
				if (option != nil) != (row == 0) {
					t.Errorf("row %d: deleted offered=%v", row, option != nil)
				}
				if row == 0 && option != nil && (option.FirstChild == nil || !strings.Contains(option.FirstChild.Data, "Уволенный")) {
					t.Error("selected label lost")
				}
			}
		})
	}
}

func TestTPDeletedSelectionIsolationMatrix(t *testing.T) {
	for _, mode := range []string{"auto", "dom", "dom-false", "dom-true", "grid-false", "grid-true", "two-placements"} {
		t.Run(mode, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				ctx := t.Context()
				yes, no := true, false
				ref := &metadata.Entity{Name: "Мастера", Kind: metadata.KindCatalog, ChoiceShowDeleted: &yes, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
				fieldPolicy := &no
				if strings.HasSuffix(mode, "-false") {
					fieldPolicy = &yes
				}
				doc := &metadata.Entity{Name: "Заявка", Kind: metadata.KindDocument, TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Мастер", Type: "reference:Мастера", RefEntity: ref.Name, ChoiceShowDeleted: fieldPolicy}, {Name: "Примечание", Type: metadata.FieldTypeString}}}}}
				show := strings.HasSuffix(mode, "-true")
				if show {
					ref.ChoiceShowDeleted = &no
				}
				var form *metadata.FormModule
				if mode != "auto" {
					table := &metadata.FormElement{ID: "rows", Name: "Строки", Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки", NoGrid: !strings.HasPrefix(mode, "grid")}
					if mode != "dom" {
						table.Columns = []*metadata.FormElement{{ID: "master", Kind: metadata.FormElementColumn, DataPath: "Объект.Строки.Мастер", ChoiceShowDeleted: &show}}
					}
					form = &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{table, {ID: "refresh", Name: "Обновить", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Обновить"}}}}
					form.ProgramAST = mustParse(t, "Процедура Обновить()\nКонецПроцедуры")
					if mode == "two-placements" {
						form.Elements = append(form.Elements, &metadata.FormElement{ID: "other", Name: "ДругиеСтроки", ReadOnly: true, Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки", NoGrid: true, Columns: []*metadata.FormElement{{ID: "othermaster", Kind: metadata.FormElementColumn, DataPath: "Объект.Строки.Мастер", ChoiceShowDeleted: &yes}}})
					}
					doc.Forms = []*metadata.FormModule{form}
				}
				s := newSubmitTestServerOn(t, db, []*metadata.Entity{ref, doc})
				active, deleted, id := uuid.New(), uuid.New(), uuid.New()
				for key, label := range map[uuid.UUID]string{active: "Действующий", deleted: "Уволенный"} {
					if err := db.Upsert(ctx, ref.Name, key, map[string]any{"Наименование": label}, ref); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.MarkForDeletion(ctx, ref.Name, deleted, true); err != nil {
					t.Fatal(err)
				}
				if err := db.Upsert(ctx, doc.Name, id, map[string]any{}, doc); err != nil {
					t.Fatal(err)
				}
				rows := []map[string]any{{"Мастер": deleted.String()}, {"Мастер": "", "Примечание": "Пустой выбор"}, {"Мастер": active.String()}}
				if err := db.UpsertTablePartRows(ctx, doc.Name, "Строки", id, rows, doc.TableParts[0]); err != nil {
					t.Fatal(err)
				}
				get := func() string {
					r := httptest.NewRecorder()
					s.formEdit(r, reqWithChi(http.MethodGet, "/ui/document/Заявка/"+id.String(), nil, map[string]string{"kind": "document", "entity": doc.Name, "id": id.String()}))
					if r.Code != http.StatusOK {
						t.Fatalf("GET: %d %s", r.Code, r.Body.String())
					}
					return r.Body.String()
				}
				body := get()
				parsed := deletedChoiceHTML(t, body)
				var event formEventResponse
				if form != nil {
					raw, err := json.Marshal(rows)
					if err != nil {
						t.Fatal(err)
					}
					values := url.Values{"_element": {"Обновить"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}, "_form": {form.Name}, "_id": {id.String()}, "tp_json.Строки": {string(raw)}}
					r := httptest.NewRecorder()
					s.handleManagedFormEvent(r, reqWithChi(http.MethodPost, "/ui/document/Заявка/form-event", values, map[string]string{"kind": "document", "entity": doc.Name}))
					event = decodeFormEventResponse(t, r.Body.Bytes())
					if r.Code != http.StatusOK || !event.OK || len(event.TableParts["Строки"]) != 3 {
						t.Fatalf("event: %d %s", r.Code, r.Body.String())
					}
				}
				payload := map[string]any{"mode": mode, "show": show, "selected": deleted.String(), "active": active.String(), "event": event}
				labelsID, choicesID := "ob-tp-ref-opts", "ob-tp-ref-choices"
				if form != nil {
					labelsID, choicesID = "ob-managed-tp-ref-opts", "ob-managed-tp-ref-choices"
				}
				payload["labels"] = deletedChoiceJSON(t, parsed, labelsID)
				payload["choices"] = deletedChoiceJSON(t, parsed, choicesID)
				payload["meta"] = deletedChoiceJSON(t, parsed, "ob-tp-ref-meta")
				var attrs []map[string]string
				var walk func(*html.Node)
				walk = func(n *html.Node) {
					if n.Data == "tbody" && deletedChoiceAttr(n, "id") == "tp-body-Строки" || deletedChoiceAttr(n, "data-sg-tp") == "Строки" {
						m := map[string]string{}
						for _, a := range n.Attr {
							m[a.Key] = a.Val
						}
						attrs = append(attrs, m)
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c)
					}
				}
				walk(parsed)
				if len(attrs) == 0 {
					t.Fatal("no table placements")
				}
				payload["attrs"] = attrs
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				node, err := exec.LookPath("node")
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(node, "static/choice_deleted_isolation_harness.js") //nolint:gosec // test runtime resolved through PATH
				cmd.Stdin = bytes.NewReader(encoded)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("production browser paths: %v\n%s", err, out)
				}
				var requests []struct {
					Marked string
					Show   bool
				}
				if err := json.Unmarshal(out, &requests); err != nil {
					t.Fatal(err, string(out))
				}
				if len(requests) == 0 {
					requests = append(requests, struct {
						Marked string
						Show   bool
					}{map[bool]string{false: "0", true: "1"}[show], show})
				}
				for _, request := range requests {
					response := serveRefOptions(t, s, ref.Name, "marked="+url.QueryEscape(request.Marked)+"&selected_id="+deleted.String(), nil)
					if response.Code != http.StatusOK {
						t.Fatal(response.Code, response.Body.String())
					}
					var result struct {
						Items           []map[string]any `json:"items"`
						SelectedAllowed *bool            `json:"selected_allowed"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, item := range result.Items {
						if item["id"] == deleted.String() {
							found = true
						}
					}
					if found != request.Show {
						t.Fatalf("public search/picker: %s", response.Body.String())
					}
				}
				// Selected historical values survive a normal write and another GET.
				raw, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRecorder()
				s.submitEdit(r, reqWithChi(http.MethodPost, "/ui/document/Заявка/"+id.String(), url.Values{"tp_json.Строки": {string(raw)}, "_action": {""}}, map[string]string{"kind": "document", "entity": doc.Name, "id": id.String()}))
				if r.Code >= 400 {
					t.Fatalf("save: %d %s", r.Code, r.Body.String())
				}
				final := get()
				if !strings.Contains(final, "Уволенный") {
					t.Fatal("saved selected label disappeared")
				}
				finalHTML := deletedChoiceHTML(t, final)
				if strings.HasPrefix(mode, "grid") {
					grid := keyboardFindHTML(finalHTML, func(n *html.Node) bool { return deletedChoiceAttr(n, "data-sg-tp") == "Строки" })
					var saved []map[string]any
					if grid == nil {
						t.Fatal("missing saved grid")
					}
					if err := json.Unmarshal([]byte(deletedChoiceAttr(grid, "data-sg-rows")), &saved); err != nil {
						t.Fatal(err)
					}
					if len(saved) < 2 || saved[0]["Мастер"] != deleted.String() {
						t.Fatalf("saved rows: %#v", saved)
					}
				} else {
					selectNode := keyboardFindHTML(finalHTML, func(n *html.Node) bool {
						return n.Data == "select" && deletedChoiceAttr(n, "name") == "tp.Строки.0.Мастер"
					})
					if selectNode == nil || keyboardFindHTML(selectNode, func(n *html.Node) bool {
						_, selected := keyboardHTMLAttr(n, "selected")
						return n.Data == "option" && selected && deletedChoiceAttr(n, "value") == deleted.String()
					}) == nil {
						t.Fatal("saved row lost its selected value")
					}
				}
			})
		})
	}
}
