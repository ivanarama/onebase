package ui

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// The dependency deliberately crosses tree order: Text's page can precede
// the checkbox whose normalised value controls its server-side visibility.
func managedPageCheckboxConditionEntity(t *testing.T, scenario string) *metadata.Entity {
	t.Helper()
	flag := &metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "FlagControl", DataPath: "Объект.Flag"}
	text := &metadata.FormElement{Kind: metadata.FormElementField, Name: "TextControl", DataPath: "Объект.Text", Required: true}
	page := &metadata.FormElement{Kind: metadata.FormElementPage, Name: "Conditional", HiddenWhen: "Flag = Ложь", Children: []*metadata.FormElement{text}}
	tabs := &metadata.FormElement{Kind: metadata.FormElementPages, Name: "Outer", Children: []*metadata.FormElement{{Kind: metadata.FormElementPage, Name: "Home"}, page}}
	if scenario == "nested" {
		page.Children = []*metadata.FormElement{{Kind: metadata.FormElementPages, Name: "Inner", Children: []*metadata.FormElement{{Kind: metadata.FormElementPage, Name: "InnerPage", Children: []*metadata.FormElement{text}}}}}
	}
	// A conditional container makes marker absence mean preservation, even
	// though its current false condition leaves the checkbox initially visible.
	control := &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "FlagGroup", HiddenWhen: "Hide", Children: []*metadata.FormElement{flag}}
	if scenario == "readonly" {
		flag.ReadOnly = true
	}
	if scenario == "readonly-when" {
		flag.ReadOnlyWhen = "Истина"
	}
	if scenario == "not-rendered" {
		control.Children = nil
	}
	form := managedObjectForm(control, tabs)
	if scenario == "after" {
		form.Elements = []*metadata.FormElement{tabs, control}
	}
	if scenario == "visible-copy" {
		form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementField, Name: "VisibleText", DataPath: "Объект.Text", Required: true})
	}
	for _, name := range []string{"HidePage", "ShowPage", "Save", "Read"} {
		form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: name, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: name}})
	}
	form.ProgramAST = mustParse(t, `
Процедура HidePage()
 Объект.Flag = Ложь;
 Объект.Text = "";
КонецПроцедуры
Процедура ShowPage()
 Объект.Flag = Истина;
 Объект.Text = "123";
КонецПроцедуры
Процедура Save()
 Объект.Записать();
КонецПроцедуры
Процедура Read()
КонецПроцедуры
`)
	return &metadata.Entity{Name: "PageCondition" + strings.ReplaceAll(scenario, "-", ""), Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Flag", Type: metadata.FieldTypeBool, Required: true, Default: "истина"},
		{Name: "Text", Type: metadata.FieldTypeString},
		{Name: "Hide", Type: metadata.FieldTypeBool},
	}, Forms: []*metadata.FormModule{form}}
}

func TestManagedPageCheckboxConditionHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"create", "edit", "managed-save"} {
			for _, scenario := range []string{"before", "after", "nested", "explicit-false", "checked", "absent-marker", "readonly", "readonly-when", "not-rendered", "visible-copy"} {
				t.Run(mode+"/"+scenario, func(t *testing.T) {
					entity := managedPageCheckboxConditionEntity(t, scenario)
					entity.Name += strings.ReplaceAll(mode, "-", "")
					ts := tpw1074Server(t, db, []*metadata.Entity{entity})
					client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					id := uuid.New()
					if mode != "create" {
						if err := db.Upsert(t.Context(), entity.Name, id, map[string]any{"Flag": true, "Text": "123", "Hide": false}, entity); err != nil {
							t.Fatal(err)
						}
					}
					endpoint := "/ui/catalog/" + entity.Name + "/new"
					if mode != "create" {
						endpoint = "/ui/catalog/" + entity.Name + "/" + id.String()
					}
					resp, err := client.Get(ts.URL + endpoint)
					if err != nil {
						t.Fatal(err)
					}
					rendered, err := io.ReadAll(resp.Body)
					if err := resp.Body.Close(); err != nil {
						t.Errorf("close response body: %v", err)
					}
					if err != nil || resp.StatusCode != http.StatusOK {
						t.Fatalf("GET %d: %v %s", resp.StatusCode, err, rendered)
					}
					initial := url.Values{"Hide": {"false"}, "Text": {"123"}}
					if scenario != "not-rendered" {
						controls := managedCheckboxControls(t, string(rendered), "FlagControl")
						marker := managedControlByName(t, controls, "_ob_present_Flag")
						if !marker.CheckboxPresence || marker.Disabled != (scenario == "readonly" || scenario == "readonly-when") {
							t.Fatalf("marker=%#v", marker)
						}
						for key, values := range successfulManagedControls(controls) {
							initial[key] = values
						}
					} else if strings.Contains(string(rendered), `name="_ob_present_Flag"`) {
						t.Fatal("unplaced checkbox rendered")
					}
					eventURL := ts.URL + "/ui/catalog/" + entity.Name + "/form-event"
					fire := func(name string) {
						t.Helper()
						values := make(url.Values)
						for key, entries := range initial {
							values[key] = append([]string(nil), entries...)
						}
						values.Set("_kind", "object")
						values.Set("_element", name)
						values.Set("_event", string(metadata.FormEventOnClick))
						if mode != "create" {
							values.Set("_id", id.String())
						}
						response, err := client.PostForm(eventURL, values)
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(response.Body)
						if err := response.Body.Close(); err != nil {
							t.Errorf("close response body: %v", err)
						}
						if err != nil {
							t.Fatal(err)
						}
						event := decodeFormEventResponse(t, body)
						hidden := false
						if event.ElementStates != nil {
							for _, value := range event.ElementStates.Hidden {
								hidden = hidden || value
							}
						}
						if !event.OK || asBool(event.Values["Flag"]) != (name == "ShowPage") || hidden != (name == "HidePage") {
							t.Fatalf("%s: %s", name, body)
						}
					}
					// Successful server responses must remain reversible, including a
					// checkbox placed after the page and a hidden ancestor of required Text.
					fire("HidePage")
					fire("ShowPage")
					fire("HidePage")
					preserve := scenario == "absent-marker" || scenario == "readonly" || scenario == "readonly-when" || scenario == "not-rendered"
					wantFlag := preserve || scenario == "checked"
					final := url.Values{"Hide": {"false"}, "Text": {""}, "action": {"save"}, "_version": {"1"}}
					if !preserve {
						final.Set("_ob_present_Flag", "1")
					}
					if scenario == "checked" {
						final.Set("Flag", "true")
					}
					if scenario == "explicit-false" {
						final.Set("Flag", "false")
					}
					if mode == "managed-save" {
						// Object.Write does not enforce form-local required. It receives typed
						// event values; HTML omission is exercised by create/edit, not this path.
						if !preserve {
							final.Set("Flag", fmt.Sprint(wantFlag))
						}
						endpoint = "/ui/catalog/" + entity.Name + "/form-event"
						final.Set("_kind", "object")
						final.Set("_element", "Save")
						final.Set("_event", string(metadata.FormEventOnClick))
						if mode != "create" {
							final.Set("_id", id.String())
						}
					}
					response, err := client.PostForm(ts.URL+endpoint, final)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					if err := response.Body.Close(); err != nil {
						t.Errorf("close response body: %v", err)
					}
					if err != nil {
						t.Fatal(err)
					}
					blocked := mode != "managed-save" && (wantFlag || scenario == "visible-copy")
					if mode == "managed-save" {
						if result := decodeFormEventResponse(t, body); !result.OK {
							t.Fatalf("save: %s", body)
						}
					} else if blocked {
						if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "Text") {
							t.Fatalf("expected required Text rejection, %d: %s", response.StatusCode, body)
						}
					} else if response.StatusCode != http.StatusSeeOther {
						t.Fatalf("save %d: %s", response.StatusCode, body)
					}
					rows, err := db.List(t.Context(), entity.Name, entity, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					if blocked && mode == "create" {
						if len(rows) != 0 {
							t.Fatalf("rejected create wrote %#v", rows)
						}
						return
					}
					if len(rows) != 1 {
						t.Fatalf("rows=%#v", rows)
					}
					wantStoredFlag := wantFlag || blocked
					if rows[0]["Flag"] == nil || asBool(rows[0]["Flag"]) != wantStoredFlag {
						t.Fatalf("Flag: %#v want %v", rows, wantFlag)
					}
					if blocked && rows[0]["Text"] != "123" {
						t.Fatalf("rejected save changed Text: %#v", rows)
					}
					if !blocked && rows[0]["Text"] != nil && rows[0]["Text"] != "" {
						t.Fatalf("save did not clear Text: %#v", rows)
					}
					if mode != "create" {
						version, err := db.EntityVersion(t.Context(), entity.Name, id)
						wantVersion := int64(2)
						if blocked {
							wantVersion = 1
						}
						if err != nil || version != wantVersion {
							t.Fatalf("version=%v want %v err=%v", version, wantVersion, err)
						}
					}
				})
			}
		}
	})
}
