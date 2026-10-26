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

func managedPageCheckboxEntity(t *testing.T, scenario string) *metadata.Entity {
	t.Helper()
	flag := &metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "FlagControl", DataPath: "Объект.Flag", Required: scenario == "local-required"}
	page := &metadata.FormElement{Kind: metadata.FormElementPage, Name: "Conditional", HiddenWhen: "Hide", Children: []*metadata.FormElement{flag}}
	if scenario == "nested" {
		page.Children = []*metadata.FormElement{{Kind: metadata.FormElementPages, Name: "Inner", Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "InnerPage", Children: []*metadata.FormElement{flag}},
		}}}
	}
	if scenario == "readonly" {
		flag.ReadOnly = true
	}
	if scenario == "readonly-when" {
		flag.ReadOnlyWhen = "Истина"
	}
	if scenario == "not-rendered" {
		page.Children = nil
	}
	form := managedObjectForm(
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "HideControl", DataPath: "Объект.Hide"},
		&metadata.FormElement{Kind: metadata.FormElementPages, Name: "Outer", Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "Home"}, page,
		}},
	)
	if scenario == "visible-copy" {
		form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "VisibleFlag", DataPath: "Объект.Flag", Required: true})
	}
	for _, name := range []string{"HidePage", "Save", "Read"} {
		form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: name,
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: name}})
	}
	assignment := "Объект.Flag = Ложь;"
	if scenario == "checked" {
		assignment = "Объект.Flag = Истина;"
	}
	if checkboxPagePreservesValue(scenario) {
		assignment = ""
	}
	form.ProgramAST = mustParse(t, `
Процедура HidePage()
 Объект.Hide = Истина;
 `+assignment+`
КонецПроцедуры
Процедура Save()
 Объект.Записать();
КонецПроцедуры
Процедура Read()
КонецПроцедуры
`)
	return &metadata.Entity{Name: "PageCheckbox" + strings.ReplaceAll(scenario, "-", ""), Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Flag", Type: metadata.FieldTypeBool, Required: true, Default: "истина"},
			{Name: "Hide", Type: metadata.FieldTypeBool},
		}, Forms: []*metadata.FormModule{form}}
}

func checkboxPagePreservesValue(scenario string) bool {
	return scenario == "not-rendered" || scenario == "readonly" || scenario == "readonly-when" || scenario == "absent-marker"
}

// Exercise actual Server.Mount routes, initial HTML, a successful hide event,
// and final create/edit/Object.Write. In particular, unchecked HTML controls
// send a presence marker but no Flag key; false still satisfies Field.required.
func TestManagedPageCheckboxHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"create", "edit", "managed-save"} {
			for _, scenario := range []string{"unchecked", "checked", "nested", "local-required", "visible-copy", "not-rendered", "readonly", "readonly-when", "absent-marker"} {
				// A regular event on a new object does not initialise unplaced
				// defaults. Preservation controls therefore use persisted records.
				if mode == "create" && checkboxPagePreservesValue(scenario) {
					continue
				}
				t.Run(mode+"/"+scenario, func(t *testing.T) {
					entity := managedPageCheckboxEntity(t, scenario)
					entity.Name += strings.ReplaceAll(mode, "-", "")
					ts := tpw1074Server(t, db, []*metadata.Entity{entity})
					client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					id := uuid.New()
					if mode != "create" {
						if err := db.Upsert(t.Context(), entity.Name, id, map[string]any{"Flag": true, "Hide": false}, entity); err != nil {
							t.Fatal(err)
						}
					}
					endpoint := "/ui/catalog/" + entity.Name + "/new"
					if mode != "create" {
						endpoint = "/ui/catalog/" + entity.Name + "/" + id.String()
					}
					response, err := client.Get(ts.URL + endpoint)
					if err != nil {
						t.Fatal(err)
					}
					rendered, err := io.ReadAll(response.Body)
					if err := response.Body.Close(); err != nil {
						t.Errorf("close response body: %v", err)
					}
					if err != nil || response.StatusCode != http.StatusOK {
						t.Fatalf("GET status=%d err=%v: %s", response.StatusCode, err, rendered)
					}
					initial := url.Values{"Hide": {"false"}}
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
						t.Fatal("unplaced field rendered a presence marker")
					}
					initial.Set("_kind", "object")
					initial.Set("_element", "HidePage")
					initial.Set("_event", string(metadata.FormEventOnClick))
					if mode != "create" {
						initial.Set("_id", id.String())
					}
					eventURL := ts.URL + "/ui/catalog/" + entity.Name + "/form-event"
					response, err = client.PostForm(eventURL, initial)
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
					wantFlag := scenario == "checked" || checkboxPagePreservesValue(scenario)
					if !event.OK || !asBool(event.Values["Hide"]) || asBool(event.Values["Flag"]) != wantFlag || event.ElementStates == nil {
						t.Fatalf("hide event: %s", body)
					}
					hidden := false
					for _, value := range event.ElementStates.Hidden {
						hidden = hidden || value
					}
					if !hidden {
						t.Fatalf("event did not hide the page: %s", body)
					}
					final := url.Values{"Hide": {"true"}, "action": {"save"}, "_version": {"1"}}
					if !checkboxPagePreservesValue(scenario) {
						final.Set("_ob_present_Flag", initial.Get("_ob_present_Flag"))
						if wantFlag {
							final.Set("Flag", "true")
						}
					}
					if mode == "managed-save" {
						// Object.Write bypasses form-local validation. Exercise
						// its typed false invariant separately from HTML omission.
						if !checkboxPagePreservesValue(scenario) {
							final.Set("Flag", fmt.Sprint(wantFlag))
						}
						endpoint = "/ui/catalog/" + entity.Name + "/form-event"
						final.Set("_id", id.String())
						final.Set("_kind", "object")
						final.Set("_element", "Save")
						final.Set("_event", string(metadata.FormEventOnClick))
					}
					response, err = client.PostForm(ts.URL+endpoint, final)
					if err != nil {
						t.Fatal(err)
					}
					body, err = io.ReadAll(response.Body)
					if err := response.Body.Close(); err != nil {
						t.Errorf("close response body: %v", err)
					}
					if err != nil {
						t.Fatal(err)
					}
					if mode == "managed-save" {
						if result := decodeFormEventResponse(t, body); !result.OK {
							t.Fatalf("save event: %s", body)
						}
					} else if response.StatusCode != http.StatusSeeOther {
						t.Fatalf("save status=%d: %s", response.StatusCode, body)
					}
					rows, err := db.List(t.Context(), entity.Name, entity, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) != 1 || rows[0]["Flag"] == nil || asBool(rows[0]["Flag"]) != wantFlag || !asBool(rows[0]["Hide"]) {
						t.Fatalf("saved rows=%#v want Flag=%v", rows, wantFlag)
					}
					if mode != "create" {
						version, err := db.EntityVersion(t.Context(), entity.Name, id)
						if err != nil || version != 2 {
							t.Fatalf("saved version=%v err=%v rows=%s", version, err, fmt.Sprint(rows))
						}
					}
				})
			}
		}
	})
}
