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
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

// Adding a filtered placement must preserve the ordinary dropdown. Carry the
// actual GET metadata and form-event response through the shipped renderers,
// then validate their row contexts through the public picker endpoint.
func TestTPChoiceMixedPlacementsPublicRoundTrip(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the mixed-placement round-trip")
	}
	for _, grid := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			for _, filled := range []bool{false, true} {
				t.Run(fmt.Sprintf("grid=%t/reverse=%t/filled=%t", grid, reverse, filled), func(t *testing.T) {
					dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
						f := newChoiceHTTPFixtureWithStore(t, db, false)
						addTPChoiceFixture(t, f, grid)
						form := f.owner.Forms[0]
						plain := form.Elements[2]
						filtered := *plain
						filtered.ID, filtered.Name = "rows-filtered", "СтрокиСОтбором"
						filtered.Columns = nil
						// Only one placement may own writes; the ordinary view is a summary.
						plain.ReadOnly = true
						for _, column := range plain.Columns {
							copied := *column
							copied.ID += "_filtered"
							copied.ChoiceFilter = append([]metadata.FormChoiceCondition(nil), column.ChoiceFilter...)
							filtered.Columns = append(filtered.Columns, &copied)
							column.ChoiceFilter = nil
						}
						selected := ""
						if filled {
							selected = f.pageTwo.String()
						}
						rows := []map[string]any{{"Направление": f.rootA.String(), "Глобальная": selected, "Локальная": selected}}
						if err := db.UpsertTablePartRows(context.Background(), f.owner.Name, "Строки", f.ownerID, rows, f.owner.TableParts[0]); err != nil {
							t.Fatal(err)
						}
						router := chi.NewRouter()
						f.server.Mount(router)
						request := func(method, path string, body url.Values) *httptest.ResponseRecorder {
							req := httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
							req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
							req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
							rec := httptest.NewRecorder()
							router.ServeHTTP(rec, req)
							if rec.Code != http.StatusOK {
								t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
							}
							return rec
						}
						read := func() (map[string]map[string]string, map[string]any) {
							rec := request(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
							doc, err := html.Parse(strings.NewReader(rec.Body.String()))
							if err != nil {
								t.Fatal(err)
							}
							hosts := map[string]map[string]string{}
							payload := map[string]any{}
							var walk func(*html.Node)
							walk = func(n *html.Node) {
								attr := "data-ob-element"
								if grid {
									attr = "data-sg-el"
								}
								if name, ok := htmlAttribute(n, attr); ok && (grid || n.Data == "table") {
									attrs := map[string]string{}
									target := n
									if !grid {
										var find func(*html.Node)
										find = func(child *html.Node) {
											if child.Data == "tbody" {
												target = child
											}
											for c := child.FirstChild; c != nil; c = c.NextSibling {
												find(c)
											}
										}
										find(n)
									}
									for _, a := range target.Attr {
										attrs[a.Key] = a.Val
									}
									// Keep the actual initial dropdown values for the no-grid host.
									if !grid {
										sel := findSelectByName(n, "tp.Строки.0.Глобальная")
										if sel == nil {
											t.Fatal("ordinary selector missing")
										}
										var options []map[string]any
										for c := sel.FirstChild; c != nil; c = c.NextSibling {
											if id, _ := htmlAttribute(c, "value"); id != "" {
												outside, _ := htmlAttribute(c, "data-ob-choice-outside-filter")
												options = append(options, map[string]any{"id": id, "_choice_outside_filter": outside == "1"})
											}
										}
										b, _ := json.Marshal(options)
										attrs["test-options"] = string(b)
									}
									hosts[name] = attrs
								}
								if id, _ := htmlAttribute(n, "id"); (id == "ob-tp-ref-meta" || id == "ob-managed-tp-ref-opts") && n.FirstChild != nil {
									var value any
									if err := json.Unmarshal([]byte(n.FirstChild.Data), &value); err != nil {
										t.Fatal(err)
									}
									if id == "ob-tp-ref-meta" {
										payload["refMeta"] = value
									} else {
										payload["initialRefOptions"] = value
									}
								}
								for c := n.FirstChild; c != nil; c = c.NextSibling {
									walk(c)
								}
							}
							walk(doc)
							return hosts, payload
						}
						options := func(attrs map[string]string, filtered bool) []string {
							var values []map[string]any
							if grid {
								key := "data-sg-ref"
								if filtered {
									var seeds map[string][][]map[string]any
									if err := json.Unmarshal([]byte(attrs["data-sg-choice-rows"]), &seeds); err != nil {
										t.Fatal(err)
									}
									if len(seeds["Глобальная"]) != 1 {
										t.Fatal("filtered row seeds missing")
									}
									values = seeds["Глобальная"][0]
								} else {
									var fields map[string][]map[string]any
									if err := json.Unmarshal([]byte(attrs[key]), &fields); err != nil {
										t.Fatal(err)
									}
									values = fields["Глобальная"]
								}
							} else {
								if err := json.Unmarshal([]byte(attrs["test-options"]), &values); err != nil {
									t.Fatal(err)
								}
							}
							ids := []string{}
							for _, value := range values {
								id := refValueString(value["id"])
								if id == f.hidden.String() {
									t.Fatal("RLS-hidden reference leaked")
								}
								ids = append(ids, id)
							}
							return ids
						}
						hosts, _ := read()
						baseline := options(hosts[plain.Name], false)
						if len(baseline) < 50 || filled && !slices.Contains(baseline, selected) {
							t.Fatalf("invalid baseline: %v", baseline)
						}
						if reverse {
							form.Elements[2] = &filtered
							form.Elements = append(form.Elements, plain)
						} else {
							form.Elements = append(form.Elements, &filtered)
						}
						hosts, payload := read()
						if actual := options(hosts[plain.Name], false); !slices.Equal(baseline, actual) {
							t.Fatalf("filtered placement changed ordinary dropdown: before=%d after=%d", len(baseline), len(actual))
						}
						filteredIDs := options(hosts[filtered.Name], true)
						if slices.Contains(filteredIDs, f.foreignOther.String()) || filled && !slices.Contains(filteredIDs, selected) {
							t.Fatalf("wrong placement seeds: %v", filteredIDs)
						}

						// Use the rendered placement context for a picker request before
						// the event as well, including a selection beyond the first page.
						contextAttr := "data-tp-choice"
						if grid {
							contextAttr = "data-sg-choice"
						}
						var contexts map[string]string
						if err := json.Unmarshal([]byte(hosts[filtered.Name][contextAttr]), &contexts); err != nil {
							t.Fatal(err)
						}
						initialInput, err := json.Marshal(map[string]any{"context": contexts["Глобальная"], "grid": grid, "selected": selected,
							"row": rows[0], "controls": []map[string]string{{"name": "Направление", "value": f.rootA.String()}}})
						if err != nil {
							t.Fatal(err)
						}
						initialCmd := exec.Command(node, "static/tp_choice_global_source_probe.js") //nolint:gosec // executable resolved with LookPath
						initialCmd.Stdin = bytes.NewReader(initialInput)
						initialOut, err := initialCmd.CombinedOutput()
						if err != nil {
							t.Fatalf("initial browser snapshot: %v\n%s", err, initialOut)
						}
						initialQuery, err := url.ParseQuery(strings.TrimPrefix(string(initialOut), "&"))
						if err != nil {
							t.Fatal(err)
						}
						initialQuery.Set("q", "needle")
						initialResult := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, initialQuery))
						if initialResult.Total != 2 || len(initialResult.Items) != 2 || filled && (initialResult.SelectedAllowed == nil || !*initialResult.SelectedAllowed) {
							t.Fatalf("initial picker: %#v query=%v", initialResult, initialQuery)
						}

						// A real event changes both the row source and selection. The ordinary
						// placement retains all options; the filtered one may seed only its
						// selected label until the server validates the new row context.
						filtered.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "ИзменитьСтроки"}
						form.ProgramAST = mustParse(t, `Процедура ИзменитьСтроки()
       Для Каждого Стр Из Объект.Строки Цикл
        Стр.Направление = Объект.Неисправность.Направление;
        Стр.Глобальная = Объект.Неисправность;
       КонецЦикла;
      КонецПроцедуры`)
						f.server.interp = interpreter.New()
						f.server.interp.LookupProc = f.server.reg.GetModuleProc
						f.server.lockMgr = runtime.NewLockManager()
						f.server.messages = NewMessageStore()
						encoded, _ := json.Marshal(rows)
						body := url.Values{"_element": {filtered.Name}, "_event": {string(metadata.FormEventOnChange)}, "_kind": {"object"}, "_form": {form.Name}, "_id": {f.ownerID.String()},
							"_tp": {"Строки"}, "_tp_row": {"0"}, "_tp_row_number": {"1"}, "_tp_col": {"Направление"}, "_tp_col_index": {"0"},
							"Направление": {f.rootA.String()}, "Неисправность": {f.legacySelected.String()}, "tp_json.Строки": {string(encoded)}}
						if !grid {
							for field, value := range rows[0] {
								body.Set("tp.Строки.0."+field, refValueString(value))
							}
						}
						rec := request(http.MethodPost, "/ui/document/"+url.PathEscape(f.owner.Name)+"/form-event", body)
						resp := decodeFormEventResponse(t, rec.Body.Bytes())
						if !resp.OK || len(resp.TableParts["Строки"]) != 1 || resp.TableParts["Строки"][0]["Глобальная"] != f.legacySelected.String() || resp.TableParts["Строки"][0]["Направление"] != f.rootB.String() {
							t.Fatalf("event: %s", rec.Body.String())
						}
						placements := []map[string]any{}
						for _, table := range form.Elements[2:] {
							placements = append(placements, map[string]any{"name": table.Name, "attrs": hosts[table.Name]})
						}
						payload["placements"], payload["grid"] = placements, grid
						payload["controls"] = []map[string]string{{"name": "Направление", "value": f.rootA.String()}}
						payload["tableparts"], payload["refOptions"] = resp.TableParts, resp.TPRefOptions
						input, _ := json.Marshal(payload)
						cmd := exec.Command(node, "static/tp_choice_mixed_roundtrip_probe.js") //nolint:gosec // executable resolved with LookPath
						cmd.Stdin = bytes.NewReader(input)
						out, err := cmd.CombinedOutput()
						if err != nil {
							t.Fatalf("browser redraw: %v\n%s", err, out)
						}
						var snapshots []struct {
							Name, Query string
							IDs         []string
						}
						if err := json.Unmarshal(out, &snapshots); err != nil {
							t.Fatalf("%v: %s", err, out)
						}
						if len(snapshots) != 2 {
							t.Fatalf("missing placement redraw: %s", out)
						}
						expected := []string{}
						for _, option := range resp.TPRefOptions["Строки"]["Глобальная"] {
							expected = append(expected, refValueString(option["id"]))
						}
						if len(expected) < 50 {
							t.Fatalf("event lost ordinary reference options: %d", len(expected))
						}
						for _, snapshot := range snapshots {
							if snapshot.Name == plain.Name {
								if snapshot.Query != "" || !slices.Equal(snapshot.IDs, expected) {
									t.Fatalf("ordinary redraw: %#v", snapshot)
								}
								continue
							}
							if snapshot.Name != filtered.Name || len(snapshot.IDs) > 1 || len(snapshot.IDs) == 1 && snapshot.IDs[0] != f.legacySelected.String() {
								t.Fatalf("filtered redraw used ordinary fallback: %#v", snapshot)
							}
							q, err := url.ParseQuery(strings.TrimPrefix(snapshot.Query, "&"))
							if err != nil {
								t.Fatal(err)
							}
							q.Set("q", "needle")
							got := decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
							if got.Total != 2 || len(got.Items) != 2 || got.SelectedAllowed == nil || *got.SelectedAllowed {
								t.Fatalf("redrawn picker: %#v query=%v", got, q)
							}
							for _, item := range got.Items {
								if item["id"] == f.hidden.String() || item["id"] == f.foreignOther.String() {
									t.Fatal("picker leaked hidden or unrelated row")
								}
							}
							q.Set("element", "Строки.local_filtered")
							q.Set("sources", `{"Строки.Направление":"`+f.rootB.String()+`"}`)
							got = decodeChoiceHTTP(t, f.serveRefOptions(t, f.target, q))
							if got.Total != 2 || got.SelectedAllowed == nil || !*got.SelectedAllowed {
								t.Fatalf("row-local picker: %#v", got)
							}
							q.Set("row_id", "../0")
							if f.serveRefOptions(t, f.target, q).Code != http.StatusBadRequest {
								t.Fatal("forged row-context accepted")
							}
						}
					})
				})
			}
		}
	}
}
