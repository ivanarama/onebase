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
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

// The actual GET contexts of both TP renderers feed production browser
// snapshots and the public HTTP picker on both database dialects. Empty eq
// closes the picker; eq_or_empty preserves only common records under RLS.
func TestTPChoiceEmptySourcePublicHTTPMatrix(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for browser choice snapshots")
	}
	for _, grid := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "grid"}[grid], func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				f := newChoiceHTTPFixtureWithStore(t, db, false)
				addTPChoiceFixture(t, f, grid)
				common, hiddenCommon := choiceHTTPUUID(0x20, 55), choiceHTTPUUID(0x20, 56)
				for id, audience := range map[uuid.UUID]string{common: "anna", hiddenCommon: "bob"} {
					if err := db.Upsert(context.Background(), f.target.Name, id, map[string]any{
						"Наименование": "needle common", "Аудитория": audience,
					}, f.target); err != nil {
						t.Fatal(err)
					}
				}
				// An empty new row must use the same condition during initial rendering.
				if err := db.UpsertTablePartRows(context.Background(), f.owner.Name, "Строки", f.ownerID,
					[]map[string]any{{"Направление": "", "Локальная": ""}}, f.owner.TableParts[0]); err != nil {
					t.Fatal(err)
				}
				table := f.owner.Forms[0].Elements[2]
				for _, op := range []metadata.FormChoiceOperator{metadata.FormChoiceOpEqual, metadata.FormChoiceOpEqualOrEmpty} {
					t.Run(string(op), func(t *testing.T) {
						table.Columns[2].ChoiceFilter[0].Op = op
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
						var walk func(*html.Node)
						walk = func(n *html.Node) {
							if grid {
								if contexts, ok := htmlAttribute(n, "data-sg-choice"); ok {
									var choices map[string]string
									if err := json.Unmarshal([]byte(contexts), &choices); err != nil {
										t.Fatal(err)
									}
									raw = choices["Локальная"]
								}
							} else if name, _ := htmlAttribute(n, "name"); name == "tp.Строки.0.Локальная" {
								raw, _ = htmlAttribute(n, "data-ref-choice-context")
								var options []string
								for o := n.FirstChild; o != nil; o = o.NextSibling {
									if id, _ := htmlAttribute(o, "value"); id != "" {
										options = append(options, id)
									}
								}
								want := []string{}
								if op == metadata.FormChoiceOpEqualOrEmpty {
									want = []string{common.String()}
								}
								if !slices.Equal(options, want) {
									t.Fatalf("initial empty row: options=%v want=%v", options, want)
								}
							}
							for c := n.FirstChild; c != nil; c = c.NextSibling {
								walk(c)
							}
						}
						walk(doc)
						if raw == "" {
							t.Fatal("rendered row-local choice context missing")
						}
						for _, source := range []string{"", f.rootA.String(), f.rootB.String()} {
							want := []uuid.UUID{}
							if source == f.rootA.String() {
								want = append(want, choiceHTTPUUID(0x20, 1))
							} else if source == f.rootB.String() {
								want = append(want, f.legacySelected, f.foreignOther)
							}
							if op == metadata.FormChoiceOpEqualOrEmpty {
								want = append(want, common)
							}
							for _, selected := range []uuid.UUID{common, hiddenCommon, choiceHTTPUUID(0x20, 1), f.legacySelected, f.hidden} {
								payload, err := json.Marshal(map[string]any{
									"context": raw, "grid": grid, "column": "Локальная", "selected": selected.String(),
									"row":      map[string]string{"Направление": source, "Локальная": selected.String()},
									"controls": []map[string]string{{"name": "tp.Строки.0.Направление", "value": source}},
								})
								if err != nil {
									t.Fatal(err)
								}
								cmd := exec.Command(node, "static/tp_choice_global_source_probe.js") //nolint:gosec // test executable resolved by LookPath
								cmd.Stdin = bytes.NewReader(payload)
								out, err := cmd.CombinedOutput()
								if err != nil {
									t.Fatalf("browser snapshot: %v\n%s", err, out)
								}
								q, err := url.ParseQuery(strings.TrimPrefix(string(out), "&"))
								if err != nil {
									t.Fatal(err)
								}
								q.Set("q", "needle")
								q.Set("limit", "100")
								r := f.serveRefOptions(t, f.target, q)
								if r.Code != http.StatusOK {
									t.Fatalf("picker: %d %s", r.Code, r.Body.String())
								}
								got := decodeChoiceHTTP(t, r)
								allowed := slices.Contains(want, selected)
								if !slices.Equal(choiceItemIDs(got), sortedIDs(want...)) || got.Total != len(want) ||
									got.SelectedAllowed == nil || *got.SelectedAllowed != allowed {
									t.Fatalf("%s source=%q selected=%s: %#v, want ids=%v allowed=%v", op, source, selected, got, want, allowed)
								}
							}
						}
					})
				}
			})
		})
	}
}

// choice_filter по колонке табличной части выбираемого справочника (#1822)
// через публичный маршрут подбора /ui/_ref-options. Бренд подходит, если в
// его ТЧ «Направления» есть строка с выбранным направлением (EXISTS): без
// дублей в выдаче и total, с RLS цели, пустой источник — пустой подбор, а
// колонка под полевой политикой или ПДн закрывает условие.
//
// Бренды: Холодильный (ХД), Швейный (ШМ), Универсальный (ХД, ШМ, ХД — две
// строки с одним направлением), Закрытый (ХД, RLS: только bob), Без
// направлений.

type tablePartChoiceFixture struct {
	server         *Server
	brand, owner   *metadata.Entity
	fridge, sewing uuid.UUID
}

func newTablePartChoiceFixture(t *testing.T, db *storage.DB, pii bool) tablePartChoiceFixture {
	t.Helper()
	ctx := context.Background()
	f := tablePartChoiceFixture{fridge: choiceHTTPUUID(0x80, 1), sewing: choiceHTTPUUID(0x80, 2)}
	direction := &metadata.Entity{
		Name: "Направление", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	brand := &metadata.Entity{
		Name: "Бренд", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Аудитория", Type: metadata.FieldTypeString},
		},
		TableParts: []metadata.TablePart{{
			Name: "Направления",
			Fields: []metadata.Field{{
				Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name, PII: pii,
			}},
		}},
	}
	condition := func(c metadata.FormChoiceCondition) []metadata.FormChoiceCondition {
		return []metadata.FormChoiceCondition{c}
	}
	form := &metadata.FormModule{
		Name: "ФормаОбъекта", EntityName: "Заявка", Kind: "object", LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{ID: "direction", Name: "ПолеНаправление", Kind: metadata.FormElementField, DataPath: "Объект.Направление"},
			{ID: "brand-from", Name: "ПолеБренд", Kind: metadata.FormElementField, DataPath: "Объект.Бренд",
				ChoiceFilter: condition(metadata.FormChoiceCondition{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, From: "Объект.Направление"})},
			{ID: "brand-ref", Name: "ПолеБрендХД", Kind: metadata.FormElementField, DataPath: "Объект.БрендХД",
				ChoiceFilter: condition(metadata.FormChoiceCondition{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Ref: f.fridge.String()})},
			// Записи постоянной ссылки в базе нет — подбор пуст, а не весь справочник.
			{ID: "brand-missing", Name: "ПолеБрендНет", Kind: metadata.FormElementField, DataPath: "Объект.БрендНет",
				ChoiceFilter: condition(metadata.FormChoiceCondition{Field: "Направления.Направление", Op: metadata.FormChoiceOpEqual, Ref: choiceHTTPUUID(0x8f, 1).String()})},
		},
	}
	owner := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Направление", Type: metadata.FieldType("reference:" + direction.Name), RefEntity: direction.Name},
			{Name: "Бренд", Type: metadata.FieldType("reference:" + brand.Name), RefEntity: brand.Name},
			{Name: "БрендХД", Type: metadata.FieldType("reference:" + brand.Name), RefEntity: brand.Name},
			{Name: "БрендНет", Type: metadata.FieldType("reference:" + brand.Name), RefEntity: brand.Name},
		},
		Forms: []*metadata.FormModule{form},
	}
	entities := []*metadata.Entity{direction, brand, owner}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[uuid.UUID]string{f.fridge: "ХД", f.sewing: "ШМ"} {
		if err := db.Upsert(ctx, direction.Name, id, map[string]any{"Наименование": name}, direction); err != nil {
			t.Fatalf("seed direction: %v", err)
		}
	}
	for i, row := range []struct {
		name, audience string
		dirs           []uuid.UUID
	}{
		{"Холодильный", "all", []uuid.UUID{f.fridge}},
		{"Швейный", "all", []uuid.UUID{f.sewing}},
		{"Универсальный", "all", []uuid.UUID{f.fridge, f.sewing, f.fridge}},
		{"Закрытый", "bob", []uuid.UUID{f.fridge}},
		{"Без направлений", "all", nil},
	} {
		id := choiceHTTPUUID(0x81, i+1)
		if err := db.Upsert(ctx, brand.Name, id, map[string]any{"Наименование": row.name, "Аудитория": row.audience}, brand); err != nil {
			t.Fatalf("seed brand: %v", err)
		}
		lines := make([]map[string]any, 0, len(row.dirs))
		for _, d := range row.dirs {
			lines = append(lines, map[string]any{"Направление": d.String()})
		}
		if len(lines) > 0 {
			if err := db.UpsertTablePartRows(ctx, brand.Name, "Направления", id, lines, brand.TableParts[0]); err != nil {
				t.Fatalf("seed brand table part: %v", err)
			}
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	f.server = &Server{reg: reg, store: db}
	f.server.entitySvc = f.server.newEntityService(nil)
	f.brand, f.owner = brand, owner
	return f
}

// tablePartChoiceUser — оператор: RLS брендов по аудитории; policies — полевые
// политики Бренда.
func tablePartChoiceUser(policies auth.FieldPolicies) *auth.User {
	permission := auth.Permission{
		Catalogs:  map[string][]string{"Бренд": {"read"}, "Направление": {"read"}},
		Documents: map[string][]string{"Заявка": {"read", "write"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			"Бренд": {"read": {Any: []auth.RowPolicy{
				{Field: "Аудитория", Op: "eq", Value: auth.RowValue{Literal: "all"}},
				{Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}},
			}}},
		}},
	}
	if policies != nil {
		permission.FieldAccess = auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{"Бренд": policies}}
	}
	return &auth.User{Login: "anna", Roles: []*auth.Role{{Permissions: permission}}}
}

func (f tablePartChoiceFixture) fetch(t *testing.T, user *auth.User, element, direction string) choiceHTTPResponse {
	t.Helper()
	query := url.Values{"form_entity": {f.owner.Name}, "form": {"ФормаОбъекта"}, "element": {element}, "limit": {"100"}}
	if element == "brand-from" {
		sources, _ := json.Marshal(map[string]string{"Объект.Направление": direction})
		query.Set("sources", string(sources))
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	request := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+url.PathEscape(f.brand.Name)+"?"+query.Encode(), nil)
	request = request.WithContext(auth.ContextWithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("element %s: status=%d body=%s", element, recorder.Code, recorder.Body.String())
	}
	return decodeChoiceHTTP(t, recorder)
}

func TestRefOptionsChoiceTablePart(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTablePartChoiceFixture(t, db, false)
		operator := tablePartChoiceUser(nil)
		for _, tc := range []struct {
			name, element, direction, want string
		}{
			// «Закрытый» тоже холодильный, но RLS цели его прячет; «Универсальный»
			// с двумя строками ХД приходит один раз.
			{"from ХД", "brand-from", f.fridge.String(), "Универсальный,Холодильный"},
			{"from ШМ", "brand-from", f.sewing.String(), "Универсальный,Швейный"},
			{"ref ХД", "brand-ref", "", "Универсальный,Холодильный"},
			{"пустой источник", "brand-from", "", ""},
			{"ref на несуществующую запись", "brand-missing", "", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetch(t, operator, tc.element, tc.direction)
				if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
					t.Fatalf("total=%d items=%q, ожидалось %q", got.Total, labels, tc.want)
				}
			})
		}
	})
}

// Колонка ТЧ под полевой политикой или ПДн: выдача и total отвечали бы на
// вопрос «есть ли у бренда строка с этим направлением», поэтому условие
// закрывается так же, как у замаскированного реквизита шапки. Явное read: full
// открывает; администратору маска не применяется по умолчанию, при mask_admin=false.
func TestRefOptionsChoiceTablePartProtectedColumnIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			name     string
			pii      bool
			policies auth.FieldPolicies
			user     func(auth.FieldPolicies) *auth.User
			want     string
		}{
			{name: "политика на колонку", policies: auth.FieldPolicies{"Направления.Направление": {Read: "mask_all"}}, user: tablePartChoiceUser},
			{name: "политика на ТЧ целиком", policies: auth.FieldPolicies{"Направления": {Read: "hide"}}, user: tablePartChoiceUser},
			{name: "ПДн колонки, роль молчит", pii: true, user: tablePartChoiceUser},
			{name: "ПДн колонки, явный read: full", pii: true, policies: auth.FieldPolicies{"Направления.Направление": {Read: "full"}},
				user: tablePartChoiceUser, want: "Универсальный,Холодильный"},
			{name: "ПДн колонки, администратор", pii: true,
				user: func(auth.FieldPolicies) *auth.User { return &auth.User{Login: "root", IsAdmin: true} },
				want: "Закрытый,Универсальный,Холодильный"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newTablePartChoiceFixture(t, db, tc.pii)
				for _, element := range []string{"brand-from", "brand-ref"} {
					got := f.fetch(t, tc.user(tc.policies), element, f.fridge.String())
					if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
						t.Fatalf("%s: total=%d items=%q, ожидалось %q", element, got.Total, labels, tc.want)
					}
				}
			})
		}
	})
}

// Закрытый источник from (#1917, ревью круг 1): запись направления, которую
// пользователь не видит, не должна отвечать, у каких брендов она стоит в ТЧ.
// Тот же гейт, что у ref: нет права чтения справочника источника или строку
// закрывает RLS — подбор пуст, а видимое направление работает как обычно.
func TestRefOptionsChoiceTablePartClosedSourceIsClosed(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := newTablePartChoiceFixture(t, db, false)
		noDirectionRead := tablePartChoiceUser(nil)
		delete(noDirectionRead.Roles[0].Permissions.Catalogs, "Направление")
		onlySewing := tablePartChoiceUser(nil)
		onlySewing.Roles[0].Permissions.RowAccess.Catalogs["Направление"] = auth.RowPolicies{
			"read": {Field: "Наименование", Op: "eq", Value: auth.RowValue{Literal: "ШМ"}},
		}
		for _, tc := range []struct {
			name      string
			user      *auth.User
			direction uuid.UUID
			want      string
		}{
			{"нет права чтения направлений", noDirectionRead, f.fridge, ""},
			{"RLS закрывает направление", onlySewing, f.fridge, ""},
			{"RLS открывает направление", onlySewing, f.sewing, "Универсальный,Швейный"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := f.fetch(t, tc.user, "brand-from", tc.direction.String())
				if labels := refChoiceLabels(got); labels != tc.want || got.Total != len(got.Items) {
					t.Fatalf("total=%d items=%q, ожидалось %q", got.Total, labels, tc.want)
				}
			})
		}
	})
}

// Each placement of the same TP must publish its own column context and seeds.
// Consume the actual GET payload with the browser snapshot code, then use the
// public picker endpoint to validate the selected reference under each filter.
func TestTPChoiceRepeatedPlacementPublicHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for browser choice snapshots")
	}
	for _, grid := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("grid=%t/reverse=%t", grid, reverse), func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					f := newChoiceHTTPFixtureWithStore(t, db, false)
					addTPChoiceFixture(t, f, grid)
					form := f.owner.Forms[0]
					first := form.Elements[2]
					second := *first
					second.ID, second.Name = "rows-second", "СтрокиПовтор"
					second.Columns = nil
					for _, column := range first.Columns {
						copy := *column
						copy.ID += "_second"
						copy.ChoiceFilter = append([]metadata.FormChoiceCondition(nil), column.ChoiceFilter...)
						second.Columns = append(second.Columns, &copy)
					}
					second.Columns[1].ChoiceFilter[0].From = "Строки.Направление"
					if reverse {
						form.Elements[2] = &second
						form.Elements = append(form.Elements, first)
					} else {
						form.Elements = append(form.Elements, &second)
					}
					// The object points to A, but the single row points to B. The
					// same stored selection is valid only in the first placement.
					rows := []map[string]any{{"Направление": f.rootB.String(), "Глобальная": f.pageTwo.String()}}
					if err := db.UpsertTablePartRows(context.Background(), f.owner.Name, "Строки", f.ownerID, rows, f.owner.TableParts[0]); err != nil {
						t.Fatal(err)
					}
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
					hosts := map[string]*html.Node{}
					var walk func(*html.Node)
					walk = func(n *html.Node) {
						attr := "data-ob-element"
						if grid {
							attr = "data-sg-el"
						}
						if name, ok := htmlAttribute(n, attr); ok && (grid || n.Data == "table") {
							hosts[name] = n
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							walk(c)
						}
					}
					walk(doc)
					for _, placement := range []struct{ name, element, source string }{
						{first.Name, "Строки.global", "Объект.Направление"},
						{second.Name, "Строки.global_second", "Строки.Направление"},
					} {
						t.Run(placement.name, func(t *testing.T) {
							host := hosts[placement.name]
							if host == nil {
								t.Fatal("placement missing")
							}
							var raw string
							var options []map[string]any
							if grid {
								contexts, _ := htmlAttribute(host, "data-sg-choice")
								var choices map[string]string
								if err := json.Unmarshal([]byte(contexts), &choices); err != nil {
									t.Fatal(err)
								}
								raw = choices["Глобальная"]
								seeds, _ := htmlAttribute(host, "data-sg-choice-rows")
								var choiceRows map[string][][]map[string]any
								if err := json.Unmarshal([]byte(seeds), &choiceRows); err != nil {
									t.Fatal(err)
								}
								if len(choiceRows["Глобальная"]) != 1 {
									t.Fatalf("expected one row of seeds, got %d", len(choiceRows["Глобальная"]))
								}
								options = choiceRows["Глобальная"][0]
							} else {
								sel := findSelectByName(host, "tp.Строки.0.Глобальная")
								if sel == nil {
									t.Fatal("column select missing")
								}
								raw, _ = htmlAttribute(sel, "data-ref-choice-context")
								for o := sel.FirstChild; o != nil; o = o.NextSibling {
									if id, _ := htmlAttribute(o, "value"); id != "" {
										outside, _ := htmlAttribute(o, "data-ob-choice-outside-filter")
										options = append(options, map[string]any{"id": id, "_choice_outside_filter": outside == "1"})
									}
								}
							}
							var context managedChoiceContext
							if err := json.Unmarshal([]byte(raw), &context); err != nil {
								t.Fatal(err)
							}
							if context.Element != placement.element || context.Sources[placement.source] != "Направление" || len(context.Sources) != 1 {
								t.Fatalf("wrong placement context: %#v", context)
							}
							global := placement.source == "Объект.Направление"
							selectedFound, otherFound := false, false
							for _, option := range options {
								id := refValueString(option["id"])
								if id == f.hidden.String() {
									t.Fatal("initial options leaked an RLS-hidden reference")
								}
								if id == f.pageTwo.String() {
									selectedFound = true
									outside, _ := option["_choice_outside_filter"].(bool)
									if outside == global {
										t.Fatal("selected seed validity came from the other placement")
									}
								}
								if id == f.foreignOther.String() {
									otherFound = true
								}
							}
							if !selectedFound || otherFound == global {
								t.Fatalf("wrong initial options: %#v", options)
							}
							payload, err := json.Marshal(map[string]any{"context": raw, "grid": grid, "selected": f.pageTwo.String(), "row": rows[0],
								"controls": []map[string]string{{"name": "Направление", "value": f.rootA.String()}, {"name": "tp.Строки.0.Направление", "value": f.rootB.String()}}})
							if err != nil {
								t.Fatal(err)
							}
							cmd := exec.Command(node, "static/tp_choice_global_source_probe.js") //nolint:gosec // executable resolved by LookPath
							cmd.Stdin = bytes.NewReader(payload)
							out, err := cmd.CombinedOutput()
							if err != nil {
								t.Fatalf("browser snapshot: %v\n%s", err, out)
							}
							q, err := url.ParseQuery(strings.TrimPrefix(string(out), "&"))
							if err != nil {
								t.Fatal(err)
							}
							q.Set("q", "needle")
							response := f.serveRefOptions(t, f.target, q)
							if response.Code != http.StatusOK {
								t.Fatalf("picker: %d %s", response.Code, response.Body.String())
							}
							got := decodeChoiceHTTP(t, response)
							if got.Total != 2 || len(got.Items) != 2 || got.SelectedAllowed == nil || *got.SelectedAllowed != global {
								t.Fatalf("placement picker: %#v; query=%v", got, q)
							}
						})
					}
				})
			})
		}
	}
}
