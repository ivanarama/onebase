package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func addTPChoiceFixture(t *testing.T, f choiceHTTPFixture, grid bool) {
	t.Helper()
	f.owner.TableParts = []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
		{Name: "Направление", Type: "reference:Направление", RefEntity: f.direction.Name},
		{Name: "Глобальная", Type: "reference:Неисправность", RefEntity: f.target.Name},
		{Name: "Локальная", Type: "reference:Неисправность", RefEntity: f.target.Name},
	}}}
	table := &metadata.FormElement{ID: "rows", Name: "Строки", Kind: metadata.FormElementTablePart, DataPath: "Объект.Строки", NoGrid: !grid,
		Columns: []*metadata.FormElement{
			{ID: "direction", Kind: metadata.FormElementField, DataPath: "Объект.Строки.Направление"},
			{ID: "global", Kind: metadata.FormElementField, DataPath: "Объект.Строки.Глобальная", ChoiceFilter: []metadata.FormChoiceCondition{{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.Направление"}}},
			{ID: "local", Kind: metadata.FormElementField, DataPath: "Объект.Строки.Локальная", ChoiceFilter: []metadata.FormChoiceCondition{{Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Строки.Направление"}}},
		}}
	f.owner.Forms[0].Elements = append(f.owner.Forms[0].Elements, table)
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.direction, f.target, f.owner}); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"Направление": f.rootA.String(), "Глобальная": f.pageTwo.String(), "Локальная": f.pageTwo.String()},
		{"Направление": f.rootB.String(), "Глобальная": f.pageTwo.String(), "Локальная": f.legacySelected.String()},
	}
	if err := f.server.store.UpsertTablePartRows(context.Background(), f.owner.Name, "Строки", f.ownerID, rows, f.owner.TableParts[0]); err != nil {
		t.Fatal(err)
	}
}

func tpChoiceQuery(f choiceHTTPFixture, column, path, value string) url.Values {
	sources, _ := json.Marshal(map[string]string{path: value})
	return url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {"Строки." + column}, "row_id": {"0"}, "sources": {string(sources)}, "q": {"needle"}, "limit": {"100"}}
}

func TestTPChoicePublicHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceHTTPFixtureWithStore(t, db, false)
		addTPChoiceFixture(t, f, false)
		for _, column := range []string{"global", "local"} {
			path := "Объект.Направление"
			if column == "local" {
				path = "Строки.Направление"
			}
			q := tpChoiceQuery(f, column, path, f.rootA.String())
			q.Set("selected_id", f.pageTwo.String())
			r := f.serveRefOptions(t, f.target, q)
			if r.Code != 200 {
				t.Fatalf("%s %d %s", column, r.Code, r.Body.String())
			}
			got := decodeChoiceHTTP(t, r)
			if got.Total != 2 || len(got.Items) != 2 || got.SelectedAllowed == nil || !*got.SelectedAllowed {
				t.Fatalf("%s: %#v", column, got)
			}
			for _, item := range got.Items {
				if strings.Contains(item["_label"].(string), "hidden") {
					t.Fatal("RLS leaked")
				}
			}
			q.Set("row_id", "1")
			q.Set("sources", `{"`+path+`":"`+f.rootB.String()+`"}`)
			got = decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
			if got.Total != 2 || got.SelectedAllowed == nil || *got.SelectedAllowed {
				t.Fatalf("other row: %#v", got)
			}
			q.Set("sources", `{"`+path+`":""}`)
			got = decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
			if got.Total != 0 || len(got.Items) != 0 {
				t.Fatalf("empty source: %#v", got)
			}
		}

		// Equality and hierarchy use the same public endpoint and List/CountList predicates.
		for _, column := range f.owner.Forms[0].Elements[len(f.owner.Forms[0].Elements)-1].Columns[1:] {
			column.ChoiceFilter[0].Op = metadata.FormChoiceOpEqual
		}
		for _, column := range []string{"global", "local"} {
			path := "Объект.Направление"
			if column == "local" {
				path = "Строки.Направление"
			}
			got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, tpChoiceQuery(f, column, path, f.rootA.String())))
			if got.Total != 1 || len(got.Items) != 1 {
				t.Fatalf("eq %s: %#v", column, got)
			}
		}
		for name, mutate := range map[string]func(url.Values){
			"unscoped":           func(q url.Values) { q.Set("element", "local") },
			"foreign column":     func(q url.Values) { q.Set("element", "Чужая.local") },
			"no row":             func(q url.Values) { q.Del("row_id") },
			"duplicate row":      func(q url.Values) { q.Add("row_id", "1") },
			"invalid row":        func(q url.Values) { q.Set("row_id", "../0") },
			"invalid UUID":       func(q url.Values) { q.Set("sources", `{"Строки.Направление":"O'Reilly"}`) },
			"foreign row source": func(q url.Values) { q.Set("sources", `{"Чужая.Направление":"`+f.rootA.String()+`"}`) },
			"extra source": func(q url.Values) {
				q.Set("sources", `{"Строки.Направление":"`+f.rootA.String()+`","sql":"true"}`)
			},
		} {
			t.Run(name, func(t *testing.T) {
				q := tpChoiceQuery(f, "local", "Строки.Направление", f.rootA.String())
				mutate(q)
				r := f.serveRefOptions(t, f.target, q)
				if r.Code != 400 {
					t.Fatalf("%d %s", r.Code, r.Body.String())
				}
			})
		}
		// A forged route cannot redirect a column's filter to another catalog.
		r := f.serveRefOptions(t, f.direction, tpChoiceQuery(f, "local", "Строки.Направление", f.rootA.String()))
		if r.Code != 400 {
			t.Fatalf("wrong target %d", r.Code)
		}
	})
}

func TestTPChoiceInitialPublicForm(t *testing.T) {
	for _, grid := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "grid"}[grid], func(t *testing.T) {
			f := newChoiceHTTPFixture(t)
			addTPChoiceFixture(t, f, grid)
			router := chi.NewRouter()
			f.server.Mount(router)
			req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
			req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
			r := httptest.NewRecorder()
			router.ServeHTTP(r, req)
			if r.Code != 200 {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
			doc, err := html.Parse(strings.NewReader(r.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			if grid {
				if !strings.Contains(r.Body.String(), "data-sg-choice=") || !strings.Contains(r.Body.String(), "Строки.local") {
					t.Fatal("grid context missing")
				}
				return
			}
			for i := 0; i < 2; i++ {
				sel := findSelectByName(doc, []string{"tp.Строки.0.Локальная", "tp.Строки.1.Локальная"}[i])
				if sel == nil {
					t.Fatal("column select absent")
				}
				raw, _ := htmlAttribute(sel, "data-ref-choice-context")
				var ctx managedChoiceContext
				if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
					t.Fatal(err)
				}
				if ctx.Element != "Строки.local" || ctx.TablePart != "Строки" {
					t.Fatalf("%#v", ctx)
				}
				values := map[string]bool{}
				for o := sel.FirstChild; o != nil; o = o.NextSibling {
					v, _ := htmlAttribute(o, "value")
					values[v] = true
				}
				if i == 0 && (values[f.legacySelected.String()] || values[f.hidden.String()]) {
					t.Fatal("other row/RLS choice leaked")
				}
				if i == 1 && values[f.pageTwo.String()] {
					t.Fatal("first row choice leaked into second")
				}
			}
		})
	}
}

// Public GET -> form-event -> no-grid redraw -> public choice endpoint. Use
// lower-case YAML paths to cover both scoped TP identity and row source names.
func TestTPChoiceNoGridPublicRoundTrip(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the no-grid public round-trip")
	}
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newChoiceHTTPFixtureWithStore(t, db, true)
		// Replace the fixture's legacy table with explicitly filtered columns.
		f.owner.Forms[0].Elements = f.owner.Forms[0].Elements[:2]
		addTPChoiceFixture(t, f, false)
		form := f.owner.Forms[0]
		table := form.Elements[2]
		table.DataPath = "Объект.строки"
		table.Columns[1].ChoiceFilter[0].From = "Объект.направление"
		table.Columns[2].ChoiceFilter[0].From = "строки.направление"
		table.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ИзменитьСтроки"}
		form.ProgramAST = mustParse(t, `Процедура ИзменитьСтроки()
			Для Каждого Стр Из Объект.Строки Цикл
				Стр.Локальная = Объект.Неисправность;
			КонецЦикла;
		КонецПроцедуры`)
		f.server.interp = interpreter.New()
		f.server.interp.LookupProc = f.server.reg.GetModuleProc
		f.server.lockMgr = runtime.NewLockManager()
		f.server.messages = NewMessageStore()
		rows := []map[string]any{{"Направление": f.rootA.String(), "Локальная": f.pageTwo.String(), "Глобальная": choiceHTTPUUID(0x20, 1).String()}}
		if err := db.UpsertTablePartRows(context.Background(), f.owner.Name, "Строки", f.ownerID, rows, f.owner.TableParts[0]); err != nil {
			t.Fatal(err)
		}

		router := chi.NewRouter()
		f.server.Mount(router)
		request := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
		request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
		r := httptest.NewRecorder()
		router.ServeHTTP(r, request)
		if r.Code != http.StatusOK {
			t.Fatalf("GET form: %d %s", r.Code, r.Body.String())
		}
		doc, err := html.Parse(strings.NewReader(r.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		attrs := map[string]string{}
		var refMeta json.RawMessage
		var controls []map[string]string
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			id, _ := htmlAttribute(n, "id")
			if id == "tp-body-Строки" {
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
			}
			if id == "ob-tp-ref-meta" && n.FirstChild != nil {
				refMeta = json.RawMessage(n.FirstChild.Data)
			}
			if name, ok := htmlAttribute(n, "name"); ok && name == "Направление" {
				controls = append(controls, map[string]string{"name": name, "value": f.rootA.String()})
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if len(attrs) == 0 || len(refMeta) == 0 || len(controls) == 0 {
			t.Fatal("public form is missing table metadata or owner control")
		}
		encodedRows, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		body := url.Values{
			"_element": {table.Name}, "_event": {string(metadata.FormEventOnChange)}, "_kind": {"object"},
			"_tp": {"Строки"}, "_tp_row": {"0"}, "_tp_row_number": {"1"}, "_tp_col": {"Направление"}, "_tp_col_index": {"0"},
			"_form": {form.Name}, "_id": {f.ownerID.String()}, "Направление": {f.rootA.String()},
			"Неисправность": {choiceHTTPUUID(0x20, 1).String()}, "tp_json.Строки": {string(encodedRows)},
			"tp.Строки.0.Направление": {f.rootA.String()}, "tp.Строки.0.Локальная": {f.pageTwo.String()}, "tp.Строки.0.Глобальная": {choiceHTTPUUID(0x20, 1).String()},
		}
		request = httptest.NewRequest(http.MethodPost, "/ui/document/"+url.PathEscape(f.owner.Name)+"/form-event", strings.NewReader(body.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request = request.WithContext(auth.ContextWithUser(request.Context(), f.user))
		r = httptest.NewRecorder()
		router.ServeHTTP(r, request)
		resp := decodeFormEventResponse(t, r.Body.Bytes())
		if r.Code != http.StatusOK || !resp.OK || len(resp.TableParts["Строки"]) != 1 {
			t.Fatalf("form event: %d %s", r.Code, r.Body.String())
		}
		if got := resp.TableParts["Строки"][0]["Локальная"]; got != choiceHTTPUUID(0x20, 1).String() {
			t.Fatalf("event did not change the selected reference: %v", got)
		}
		payload, err := json.Marshal(map[string]any{"attrs": attrs, "refMeta": refMeta, "controls": controls, "tableparts": resp.TableParts, "refOptions": resp.TPRefOptions})
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(node, "static/tp_choice_roundtrip_probe.js") //nolint:gosec // test executable resolved by LookPath
		cmd.Stdin = bytes.NewReader(payload)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("browser redraw: %v\n%s", err, out)
		}
		var queries []struct{ Name, Query, Filter string }
		if err := json.Unmarshal(out, &queries); err != nil {
			t.Fatal(err)
		}
		if len(queries) != 2 {
			t.Fatalf("expected two dependent selectors: %s", out)
		}
		for _, snapshot := range queries {
			q, err := url.ParseQuery(strings.TrimPrefix(snapshot.Query, "&"))
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Filter == "" || q.Get("flt") == "" {
				t.Fatalf("redraw lost owner filter: %s", out)
			}
			q.Set("q", "needle")
			got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
			if got.Total != 1 || len(got.Items) != 1 || got.SelectedAllowed == nil || !*got.SelectedAllowed {
				t.Fatalf("redrawn %s choice: %#v", snapshot.Name, got)
			}
			// Column ids remain exact; metadata name normalization must not
			// make another scoped identity valid.
			q.Set("element", strings.Replace(q.Get("element"), ".local", ".LOCAL", 1))
			if strings.HasSuffix(snapshot.Name, ".Локальная") && f.serveRefOptions(t, f.target, q).Code != http.StatusBadRequest {
				t.Fatal("case-folded column id was accepted")
			}
		}
	})
}

// Metadata paths are case-insensitive; browser control names are canonical.
// Exercise contexts rendered by both table implementations and send the
// browser snapshot to the public endpoint, preserving the declared path key.
func TestTPChoiceGlobalSourceCasePublicHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for browser choice snapshots")
	}
	for _, grid := range []bool{false, true} {
		for _, root := range []string{"Объект", "Форма"} {
			t.Run(map[bool]string{false: "table", true: "grid"}[grid]+"/"+root, func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					f := newChoiceHTTPFixtureWithStore(t, db, false)
					addTPChoiceFixture(t, f, grid)
					form := f.owner.Forms[0]
					table := form.Elements[2]
					name := "Направление"
					if root == "Форма" {
						name = "НаправлениеФормы"
						form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: name, TypeRef: "CatalogRef." + f.direction.Name})
						form.Elements = append(form.Elements, &metadata.FormElement{ID: "form-direction", Name: name, Kind: metadata.FormElementField, DataPath: "Форма." + name})
					}
					path := root + "." + strings.ToLower(name)
					table.Columns[1].ChoiceFilter[0].From = path
					router := chi.NewRouter()
					f.server.Mount(router)
					req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
					req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
					r := httptest.NewRecorder()
					router.ServeHTTP(r, req)
					if r.Code != http.StatusOK {
						t.Fatalf("GET form: %d %s", r.Code, r.Body.String())
					}
					doc, err := html.Parse(strings.NewReader(r.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					var raw string
					var controls []map[string]string
					var walk func(*html.Node)
					walk = func(n *html.Node) {
						if control, ok := htmlAttribute(n, "name"); ok && control == name {
							controls = append(controls, map[string]string{"name": control, "value": f.rootA.String()})
						}
						if grid {
							if contexts, ok := htmlAttribute(n, "data-sg-choice"); ok {
								var choices map[string]string
								if err := json.Unmarshal([]byte(contexts), &choices); err != nil {
									t.Fatal(err)
								}
								raw = choices["Глобальная"]
							}
						} else if control, _ := htmlAttribute(n, "name"); control == "tp.Строки.0.Глобальная" {
							raw, _ = htmlAttribute(n, "data-ref-choice-context")
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							walk(c)
						}
					}
					walk(doc)
					if raw == "" || len(controls) == 0 {
						t.Fatal("rendered choice context or source control missing")
					}
					payload, err := json.Marshal(map[string]any{"context": raw, "controls": controls, "grid": grid, "selected": f.pageTwo.String()})
					if err != nil {
						t.Fatal(err)
					}
					cmd := exec.Command(node, "static/tp_choice_global_source_probe.js") //nolint:gosec // test executable resolved by LookPath
					cmd.Stdin = bytes.NewReader(payload)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("browser choice snapshot: %v\n%s", err, out)
					}
					q, err := url.ParseQuery(strings.TrimPrefix(string(out), "&"))
					if err != nil {
						t.Fatal(err)
					}
					q.Set("q", "needle")
					got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
					if got.Total != 2 || len(got.Items) != 2 || got.SelectedAllowed == nil || !*got.SelectedAllowed {
						t.Fatalf("%s browser source: %#v; query=%v", path, got, q)
					}
				})
			})
		}
	}
}
