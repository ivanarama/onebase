package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	obruntime "github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Saved production forms, real ui.js/managed.js/SlickGrid and native browser
// keyboard/mouse events. Count implicit focus targets too, not just [tabindex].
func TestFormKeyboard_ReadonlyTablesBrowser(t *testing.T) {
	browser := newMobileLayoutBrowser(t, managedLayoutTestBrowser(t))
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, tc := range []struct {
			name                   string
			noGrid, readOnly, skip bool
		}{
			{"slick readonly skip", false, true, true},
			{"DOM readonly skip", true, true, true},
			{"slick readonly default", false, true, false},
			{"DOM readonly default", true, true, false},
			{"slick editable skip", false, false, true},
			{"DOM editable skip", true, false, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				table := &metadata.FormElement{Kind: metadata.FormElementTablePart, Name: "History", DataPath: "Object.Lines", NoGrid: tc.noGrid}
				form := managedObjectForm(fieldEl("FirstField", "Object.First"), table, fieldEl("LastField", "Object.Last"))
				form.TabSkipReadonly = tc.skip
				form.Autofocus = "first_empty"
				ent := &metadata.Entity{
					Name: "KeyboardRecord", Kind: metadata.KindCatalog,
					Fields: []metadata.Field{{Name: "First", Type: metadata.FieldTypeString}, {Name: "Last", Type: metadata.FieldTypeString}},
					TableParts: []metadata.TablePart{{Name: "Lines", Fields: []metadata.Field{
						{Name: "Text", Type: metadata.FieldTypeString},
						{Name: "Link", Type: metadata.FieldType("reference:KeyboardRecord"), RefEntity: "KeyboardRecord"},
					}}},
					Forms: []*metadata.FormModule{form},
				}
				if err := db.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
					t.Fatal(err)
				}
				reg := obruntime.NewRegistry()
				reg.Load(obruntime.LoadOptions{Entities: []*metadata.Entity{ent}})
				srv := New(reg, db, interpreter.New(), nil, Config{}, nil)
				t.Cleanup(srv.Close)
				written := httptest.NewRecorder()
				srv.submit(written, reqWithChi(http.MethodPost, "/ui/catalog/KeyboardRecord/new", url.Values{
					"tp_present.Lines": {"1"}, "tp.Lines.0.Text": {"history"},
				}, map[string]string{"entity": ent.Name}))
				if written.Code != http.StatusSeeOther {
					t.Fatalf("create: %d %s", written.Code, written.Body.String())
				}
				// Each matrix case owns its saved object, even when sharing a dialect DB.
				id := written.Header().Get("Location")
				parsed, err := url.Parse(id)
				if err != nil {
					t.Fatal(err)
				}
				// The submit redirect is the ordinary edit route for the created object.
				id = parsed.Path[len("/ui/catalog/KeyboardRecord/"):]
				linked := httptest.NewRecorder()
				srv.submitEdit(linked, reqWithChi(http.MethodPost, "/ui/catalog/KeyboardRecord/"+id, url.Values{
					"tp_present.Lines": {"1"}, "tp.Lines.0.Text": {"history"}, "tp.Lines.0.Link": {id},
				}, map[string]string{"entity": ent.Name, "id": id}))
				if linked.Code != http.StatusSeeOther {
					t.Fatalf("save link: %d %s", linked.Code, linked.Body.String())
				}
				table.ReadOnly = tc.readOnly
				opened := httptest.NewRecorder()
				srv.formEdit(opened, reqWithChi(http.MethodGet, "/ui/catalog/KeyboardRecord/"+id, nil, map[string]string{"entity": ent.Name, "id": id}))
				if opened.Code != http.StatusOK {
					t.Fatalf("open: %d %s", opened.Code, opened.Body.String())
				}

				router := chi.NewRouter()
				mountStatic(router)
				// Exercise obFire and the production response renderer with a deterministic
				// transport fixture. Navigation still follows the actual persisted ref.
				router.Post("/ui/catalog/KeyboardRecord/form-event", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "tableparts": map[string]any{"Lines": []map[string]any{
						{"Text": "new history", "Link": id}, {"Text": "second row", "Link": id},
					}}})
				})
				router.Get("/ui/_ref-open/{entity}/{id}", srv.refOpenRedirect)
				router.Get("/ui/catalog/{entity}/{id}", srv.formEdit)
				router.Get("/", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = w.Write(opened.Body.Bytes())
				})
				server := httptest.NewServer(router)
				defer server.Close()
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				call := func(session, method string, params, out any) {
					t.Helper()
					if err := browser.call(ctx, session, method, params, out); err != nil {
						t.Fatal(err)
					}
				}
				var isolated struct {
					ID string `json:"browserContextId"`
				}
				call("", "Target.createBrowserContext", nil, &isolated)
				defer func() {
					closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer closeCancel()
					if err := browser.call(closeCtx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": isolated.ID}, nil); err != nil {
						t.Error(err)
					}
				}()
				var target struct {
					ID string `json:"targetId"`
				}
				call("", "Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": isolated.ID}, &target)
				var attached struct {
					Session string `json:"sessionId"`
				}
				call("", "Target.attachToTarget", map[string]any{"targetId": target.ID, "flatten": true}, &attached)
				session := attached.Session
				eval := func(expression string) json.RawMessage {
					t.Helper()
					var result struct {
						Result struct {
							Value json.RawMessage `json:"value"`
						} `json:"result"`
						Exception json.RawMessage `json:"exceptionDetails"`
					}
					call(session, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, &result)
					if len(result.Exception) != 0 {
						t.Fatalf("browser evaluation: %s", result.Exception)
					}
					return result.Result.Value
				}
				call(session, "Page.navigate", map[string]any{"url": server.URL}, nil)
				for string(eval(`document.readyState === 'complete' && !!window.obFormKeyboard && !!document.querySelector('[name="First"]')`)) != "true" {
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(20 * time.Millisecond):
					}
				}
				eval(`window.kTable = document.querySelector('.ob-grid, .tp-table');
window.kSettle = () => new Promise(resolve => setTimeout(resolve, 30));
window.kStops = () => Array.from(kTable.querySelectorAll('input, select, textarea, button, a[href], area[href], summary, iframe, object, embed, audio[controls], video[controls], [contenteditable], [tabindex]')).filter(el => (el.tabIndex >= 0 || (!el.hasAttribute('tabindex') && el.isContentEditable)) && !el.disabled && el.getClientRects().length).length;
true`)
				if string(eval(`document.activeElement.name`)) != `"First"` {
					t.Fatal("autofocus did not select First")
				}
				key := func(shift bool) {
					modifiers := 0
					if shift {
						modifiers = 8
					}
					for _, kind := range []string{"keyDown", "keyUp"} {
						call(session, "Input.dispatchKeyEvent", map[string]any{"type": kind, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9, "modifiers": modifiers}, nil)
					}
				}
				for _, phase := range []string{"initial", "event", "recreate"} {
					if phase == "event" {
						eval(`document.querySelector('[name="Last"]').focus(); window.obFire('History', 'Refresh').then(kSettle)`)
						if string(eval(`document.activeElement.name`)) != `"Last"` {
							t.Fatal("event moved manual focus")
						}
						if string(eval(`kTable.matches('.tp-table') ? kTable.tBodies[0].rows.length === 2 : window._obGrids.Lines.dataView.getLength() === 2`)) != "true" {
							t.Fatal("event did not replace rows")
						}
					}
					if phase == "recreate" {
						eval(`(() => {
 if (kTable.matches('.tp-table')) {
   const row = kTable.tBodies[0].rows[1];
   const replacement = row.cloneNode(true);
   replacement.querySelectorAll('[data-ob-tab-orig]').forEach(el => { const orig = JSON.parse(el.getAttribute('data-ob-tab-orig')); if (orig === null) el.removeAttribute('tabindex'); else el.setAttribute('tabindex', orig); el.removeAttribute('data-ob-tab-orig'); });
   row.replaceWith(replacement);
   window.obDOMSetCurrentRow(kTable, replacement, false);
 } else {
   const sink = document.createElement('div'); sink.tabIndex = 0; kTable.appendChild(sink);
 }
 // New implicit focus nodes must also be caught by the observer, without obFire.
 const extras = document.createElement('span');
 extras.innerHTML = '<button type="button">command</button><a href="#">link</a><input type="button" value="input button"><span tabindex="3">explicit</span><button tabindex="">empty tabindex</button><span contenteditable="true">edit</span>';
 const roScope = kTable.querySelector('[data-ob-table-readonly]') || kTable;
 roScope.appendChild(extras);
 window.kExtras = extras;
 return kSettle();
})()`)
					}
					var stops int
					if err := json.Unmarshal(eval(`kStops()`), &stops); err != nil {
						t.Fatal(err)
					}
					if tc.skip && tc.readOnly {
						if stops != 0 {
							t.Errorf("%s: readonly table has %d Tab stops", phase, stops)
						}
						eval(`document.querySelector('[name="First"]').focus()`)
						key(false)
						if string(eval(`document.activeElement.name`)) != `"Last"` {
							t.Errorf("%s: Tab did not skip readonly table", phase)
						}
						key(true)
						if string(eval(`document.activeElement.name`)) != `"First"` {
							t.Errorf("%s: Shift+Tab did not skip readonly table", phase)
						}
					} else {
						if stops == 0 {
							t.Errorf("%s: table lost its Tab stops", phase)
						}
						eval(`document.querySelector('[name="First"]').focus()`)
						key(false)
						if string(eval(`kTable.contains(document.activeElement)`)) != "true" {
							t.Errorf("%s: control table was skipped by Tab", phase)
						}
					}
					if t.Failed() {
						t.FailNow()
					}
					if tc.noGrid && phase == "initial" {
						// Native mouse input, not .click(): readonly ref navigation stays enabled.
						var point struct{ X, Y float64 }
						if err := json.Unmarshal(eval(`(() => { const b = kTable.querySelector('[data-ob-ref-current]'); if (!b || b.disabled) throw new Error('reference button missing/disabled'); const r = b.getBoundingClientRect(); return {X:r.x+r.width/2, Y:r.y+r.height/2}; })()`), &point); err != nil {
							t.Fatal(err)
						}
						for _, kind := range []string{"mousePressed", "mouseReleased"} {
							call(session, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": point.X, "y": point.Y, "button": "left", "clickCount": 1}, nil)
						}
						eval(`kSettle()`)
						var targets struct {
							Infos []struct {
								ID  string `json:"targetId"`
								URL string `json:"url"`
							} `json:"targetInfos"`
						}
						found := false
						for !found {
							call("", "Target.getTargets", nil, &targets)
							for _, popup := range targets.Infos {
								if popup.URL == fmt.Sprintf("%s/ui/catalog/KeyboardRecord/%s", server.URL, id) {
									found = true
									call("", "Target.closeTarget", map[string]any{"targetId": popup.ID}, nil)
								}
							}
							if !found {
								select {
								case <-ctx.Done():
									t.Fatalf("reference navigation timed out: %+v", targets.Infos)
								case <-time.After(20 * time.Millisecond):
								}
							}
						}
					}
				}
				// Removing readonly restores exact tabindex values, including absence/empty.
				if tc.skip && tc.readOnly {
					eval(`document.querySelector('[name="Last"]').focus();
window.kOriginal = Array.from(kTable.querySelectorAll('[data-ob-tab-orig]')).filter(el => !el.readOnly && !el.disabled).map(el => [el, JSON.parse(el.getAttribute('data-ob-tab-orig'))]);
kTable.removeAttribute('data-sg-ro'); kTable.querySelectorAll('[data-ob-table-readonly]').forEach(el => el.removeAttribute('data-ob-table-readonly'));
kSettle()`)
					if string(eval(`kOriginal.every(([el, orig]) => el.getAttribute('tabindex') === orig && !el.hasAttribute('data-ob-tab-orig'))`)) != "true" {
						t.Error("original tabindex was not restored")
					}
					if string(eval(`document.activeElement.name`)) != `"Last"` {
						t.Error("observer moved manual focus")
					}
					// Idempotence and autofocus: a table button never becomes the input pick.
					eval(`window.obFormKeyboard(true); window.obFormKeyboard(true)`)
					if string(eval(`document.activeElement.name`)) != `"Last"` {
						t.Error("reapplying keyboard policy moved focus")
					}
				}

			})
		}
	})
}
