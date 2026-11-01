package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

func TestNoGridDateTimezonesHTTPAndJS(t *testing.T) {
	zone := os.Getenv("ONEBASE_NOGRID_DATE_TEST_ZONE")
	if zone == "" {
		for _, zone := range []string{"UTC", "Etc/GMT-3", "Etc/GMT+5"} {
			t.Run(zone, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestNoGridDateTimezonesHTTPAndJS$", "-test.v") //nolint:gosec // current test executable, fixed argument list
				for _, v := range os.Environ() {
					if !strings.HasPrefix(v, "TZ=") && !strings.HasPrefix(v, "ONEBASE_NOGRID_DATE_TEST_ZONE=") {
						cmd.Env = append(cmd.Env, v)
					}
				}
				cmd.Env = append(cmd.Env, "TZ="+zone, "ONEBASE_NOGRID_DATE_TEST_ZONE="+zone)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("isolated timezone: %v\n%s", err, out)
				} else {
					t.Logf("%s", out)
				}
			})
		}
		return
	}
	// Windows Go does not select time.Local from TZ. Set it once at child
	// startup; no timezone mutation occurs in the parent or another test.
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	time.Local = loc
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node is required for the shipped no_grid renderers")
	}
	for _, managed := range []bool{false, true} {
		for _, readonly := range []bool{false, true} {
			for _, value := range []string{"2026-10-19T09:15:30Z", "2026-10-19T12:15:30+03:00", "2026-10-19T09:15:30"} {
				t.Run(fmt.Sprintf("managed=%t/readonly=%t/%s", managed, readonly, value), func(t *testing.T) {
					tp := metadata.TablePart{Name: "Lines", Fields: []metadata.Field{{Name: "At", Type: metadata.FieldTypeDate}}}
					ent := &metadata.Entity{Name: "DateRecord", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{tp}}
					if managed {
						ent.Forms = []*metadata.FormModule{managedObjectForm(fieldEl("NameField", "Object.Name"), &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "LinesTable", DataPath: "Object.Lines", NoGrid: true})}
					}
					s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
					router := chi.NewRouter()
					s.Mount(router)
					request := func(method, path string, form url.Values, viewer bool) *httptest.ResponseRecorder {
						req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
						if form != nil {
							req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						}
						if viewer {
							u := &auth.User{Login: "viewer", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{ent.Name: {"read"}}}}}}
							req = req.WithContext(auth.ContextWithUser(req.Context(), u))
						}
						rec := httptest.NewRecorder()
						router.ServeHTTP(rec, req)
						return rec
					}
					input := url.Values{"Name": {"date record"}, "tp_present.Lines": {"1"}, "tp.Lines.0.At": {value}}
					if rec := request("POST", "/ui/catalog/DateRecord/new", input, false); rec.Code != http.StatusSeeOther {
						t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
					}
					id := onlyStoredID(t, s, ent)
					instant, err := time.Parse(time.RFC3339, value)
					if err != nil {
						instant, err = time.ParseInLocation("2006-01-02T15:04:05", value, loc)
					}
					if err != nil {
						t.Fatal(err)
					}
					local := instant.In(loc)
					want := local.Format("2006-01-02T15:04:05")
					display := local.Format("02.01.2006 15:04:05")
					assertStored := func(wantRows int) {
						t.Helper()
						rows, err := s.store.GetTablePartRows(ctx, ent.Name, tp.Name, id, tp)
						if err != nil {
							t.Fatal(err)
						}
						if len(rows) != wantRows {
							t.Fatalf("persisted rows=%d, want %d", len(rows), wantRows)
						}
						for _, row := range rows {
							got, ok := row["At"].(time.Time)
							if !ok {
								t.Fatalf("date value has type %T: %v", row["At"], row["At"])
							}
							if !got.Equal(instant) {
								t.Fatalf("persisted moment=%s, want %s", got.Format(time.RFC3339), instant.Format(time.RFC3339))
							}
						}
					}
					assertStored(1)
					if managed {
						ent.Forms[0].Elements[1].ReadOnly = readonly
					}
					rec := request("GET", "/ui/catalog/DateRecord/"+id.String(), nil, readonly && !managed)
					if rec.Code != http.StatusOK {
						t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
					}
					page, err := html.Parse(strings.NewReader(rec.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					var body, control, add *html.Node
					var visit func(*html.Node)
					visit = func(n *html.Node) {
						if v, _ := managedHTMLAttr(n, "id"); v == "tp-body-Lines" {
							body = n
						}
						if v, _ := managedHTMLAttr(n, "name"); v == "tp.Lines.0.At" {
							control = n
						}
						if v, _ := managedHTMLAttr(n, "data-tp-name"); v == "Lines" {
							if _, ok := managedHTMLAttr(n, "data-ob-add-tp-row"); ok {
								add = n
							}
						}
						for c := n.FirstChild; c != nil; c = c.NextSibling {
							visit(c)
						}
					}
					visit(page)
					if body == nil || control == nil {
						t.Fatal("public form missing no_grid date row")
					}
					attrs := func(n *html.Node) map[string]string {
						out := map[string]string{}
						if n != nil {
							for _, a := range n.Attr {
								out[a.Key] = a.Val
							}
						}
						return out
					}
					ca := attrs(control)
					expectedType, expectedValue := "datetime-local", want
					if readonly {
						expectedType, expectedValue = "text", display
					}
					if ca["type"] != expectedType || ca["value"] != expectedValue {
						t.Fatalf("initial date=%v, want %s %s", ca, expectedType, expectedValue)
					}
					if readonly {
						if _, ok := ca["disabled"]; !ok {
							t.Fatal("readonly date is not disabled")
						}
					} else if ca["step"] != "1" {
						t.Fatal("date loses seconds")
					}
					payload, err := json.Marshal(map[string]any{"managed": managed, "readonly": readonly, "attrs": attrs(body), "add": attrs(add), "value": value, "want": want, "display": display, "initial": ca["value"], "offset": local.Format("-07:00")})
					if err != nil {
						t.Fatal(err)
					}
					cmd := exec.Command(node, "static/no_grid_date_timezone_probe.js") //nolint:gosec // fixed fixture, node resolved from PATH
					cmd.Stdin = bytes.NewReader(payload)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("shipped renderers: %v\n%s", err, output)
					}
					var client struct{ Current, Added string }
					if err := json.Unmarshal(output, &client); err != nil {
						t.Fatalf("client result: %v %s", err, output)
					}
					if readonly {
						assertStored(1)
						return
					}
					input.Set("tp.Lines.0.At", client.Current)
					input.Set("tp.Lines.1.At", client.Added)
					if rec := request("POST", "/ui/catalog/DateRecord/"+id.String(), input, false); rec.Code != http.StatusSeeOther {
						t.Fatalf("save rendered values: %d %s", rec.Code, rec.Body.String())
					}
					assertStored(2)
				})
			}
		}
	}
}
