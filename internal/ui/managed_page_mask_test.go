package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Shared by the HTTP matrix and the real-browser fixture. All mutations go
// through mounted public routes; the browser gets actual formEdit HTML and
// successful form-event responses, rather than a hand-written hidden map.
func managedPageMaskEntity(t *testing.T, required, nested, copyField bool) *metadata.Entity {
	t.Helper()
	field := fieldEl("Masked", "Объект.Текст")
	field.Required, field.Mask = required, "[0-9]+"
	number := fieldEl("Number", "Объект.Число")
	page := &metadata.FormElement{Kind: metadata.FormElementPage, Name: "Conditional", HiddenWhen: "Скрыть", Children: []*metadata.FormElement{field, number}}
	if nested {
		page.Children = []*metadata.FormElement{{Kind: metadata.FormElementPages, Name: "Inner", Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "InnerConditional", HiddenWhen: "СкрытьВнутри", Children: []*metadata.FormElement{field, number}},
		}}}
	}
	form := managedObjectForm(
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "HideValue", DataPath: "Объект.Скрыть"},
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "HideInnerValue", DataPath: "Объект.СкрытьВнутри"},
		&metadata.FormElement{Kind: metadata.FormElementPages, Name: "Outer", Children: []*metadata.FormElement{
			{Kind: metadata.FormElementPage, Name: "Home"}, page,
		}},
	)
	if copyField {
		copy := fieldEl("VisibleCopy", "Объект.Текст")
		copy.Required, copy.Mask = true, "[a-z]+"
		form.Elements = append(form.Elements, copy)
	}
	for _, name := range []string{"Hide", "Show", "HideInner", "ShowInner", "Save"} {
		form.Elements = append(form.Elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: name,
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: name}})
	}
	form.ProgramAST = mustParse(t, `
Процедура Hide()
 Объект.Скрыть = Истина;
 Объект.Текст = "invalid";
КонецПроцедуры
Процедура Show()
 Объект.Скрыть = Ложь;
КонецПроцедуры
Процедура HideInner()
 Объект.СкрытьВнутри = Истина;
 Объект.Текст = "invalid";
КонецПроцедуры
Процедура ShowInner()
 Объект.СкрытьВнутри = Ложь;
КонецПроцедуры
Процедура Save()
 Объект.Записать();
КонецПроцедуры
`)
	return &metadata.Entity{
		Name: fmt.Sprintf("PageMask%v%v%v", required, nested, copyField), Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Текст", Type: metadata.FieldTypeString},
			{Name: "Число", Type: metadata.FieldTypeNumber},
			{Name: "Скрыть", Type: metadata.FieldTypeBool},
			{Name: "СкрытьВнутри", Type: metadata.FieldTypeBool},
		},
		Forms: []*metadata.FormModule{form},
	}
}

func TestManagedPageMaskHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"create", "edit", "managed-save"} {
			for _, scenario := range []string{"mask-only", "required-mask", "global-refusal", "global-hook", "forged-hidden"} {
				if mode == "managed-save" && scenario == "forged-hidden" {
					continue
				} // Object.Write owns entity invariants, not form-local required.
				t.Run(mode+"/"+scenario, func(t *testing.T) {
					ctx := context.Background()
					entity := managedPageMaskEntity(t, scenario != "mask-only", true, false)
					entity.Name = "Mask" + strings.ReplaceAll(mode+scenario, "-", "")
					global := strings.HasPrefix(scenario, "global-")
					entity.Fields[0].Required = global
					if scenario == "global-hook" {
						entity.Forms[0].Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeWrite: "Fill"}
						entity.Forms[0].ProgramAST = mustParse(t, `
Процедура Fill()
 Объект.Текст = "filled by hook";
КонецПроцедуры
Процедура Save()
 Fill();
 Объект.Записать();
КонецПроцедуры
`)
					}
					ts := tpw1074Server(t, db, []*metadata.Entity{entity})
					client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					id := uuid.New()
					before := map[string]any{"Текст": "before", "Скрыть": false, "СкрытьВнутри": false}
					if mode != "create" {
						if err := db.Upsert(ctx, entity.Name, id, before, entity); err != nil {
							t.Fatal(err)
						}
					}
					values := url.Values{"Текст": {"invalid"}, "Скрыть": {"true"}, "СкрытьВнутри": {"false"}, "action": {"save"}, "_version": {"1"}}
					if global {
						values.Set("Текст", "")
					}
					if scenario == "forged-hidden" {
						values.Set("Текст", "")
						values.Set("Скрыть", "false")
						values.Set("hidden", "true") // Client visibility cannot suspend a server rule.
					}
					endpoint := "/ui/catalog/" + entity.Name + "/new"
					if mode == "edit" {
						endpoint = "/ui/catalog/" + entity.Name + "/" + id.String()
					}
					if mode == "managed-save" {
						endpoint = "/ui/catalog/" + entity.Name + "/form-event"
						values.Set("_id", id.String())
						values.Set("_kind", "object")
						values.Set("_element", "Save")
						values.Set("_event", string(metadata.FormEventOnClick))
					}
					response, err := client.PostForm(ts.URL+endpoint, values)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					if closeErr := response.Body.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err != nil {
						t.Fatal(err)
					}
					allow := scenario != "global-refusal" && scenario != "forged-hidden"
					if mode == "managed-save" {
						event := decodeFormEventResponse(t, body)
						if event.OK != allow {
							t.Fatalf("event ok=%v allow=%v: %s", event.OK, allow, body)
						}
					} else if (response.StatusCode == http.StatusSeeOther) != allow {
						t.Fatalf("status=%d allow=%v: %s", response.StatusCode, allow, body)
					}
					rows, err := db.List(ctx, entity.Name, entity, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					wantRows := 1
					if mode == "create" && !allow {
						wantRows = 0
					}
					if len(rows) != wantRows {
						t.Fatalf("rows=%#v want count=%d", rows, wantRows)
					}
					if len(rows) > 0 {
						want := "invalid"
						if !allow {
							want = "before"
						} else if scenario == "global-hook" {
							want = "filled by hook"
						}
						if !allow && mode != "create" {
							version, err := db.EntityVersion(ctx, entity.Name, id)
							if err != nil || version != 1 || asBool(rows[0]["Скрыть"]) {
								t.Fatalf("refusal changed state: rows=%#v version=%v err=%v", rows, version, err)
							}
						}
						if fmt.Sprint(rows[0]["Текст"]) != want {
							t.Fatalf("persisted text=%#v want=%q", rows, want)
						}
					}
				})
			}
		}
	})
}

// Playwright starts this isolated, public HTTP fixture as a compiled Go test.
// Closing its stdin stops it; normal Go runs skip it and never wait for input.
// The record URLs go to stdout as one marked line: a file path taken from the
// environment would be a tainted write target (gosec G703).
func TestManagedPageMaskBrowserFixture(t *testing.T) {
	if os.Getenv("ONEBASE_PAGE_MASK_FIXTURE") != "1" {
		t.Skip("started by managed-page-mask.spec.js")
	}
	db, err := storage.ConnectSQLite(t.Context(), filepath.Join(t.TempDir(), "browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureServiceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var entities []*metadata.Entity
	for _, required := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			entities = append(entities, managedPageMaskEntity(t, required, nested, false))
		}
	}
	entities = append(entities, managedPageMaskEntity(t, true, false, true))
	ts := tpw1074Server(t, db, entities)
	urls := make(map[string]string)
	for _, entity := range entities {
		id := uuid.New()
		if err := db.Upsert(t.Context(), entity.Name, id, map[string]any{"Текст": "123", "Скрыть": false, "СкрытьВнутри": false, "Число": "1"}, entity); err != nil {
			t.Fatal(err)
		}
		urls[entity.Name] = ts.URL + "/ui/catalog/" + entity.Name + "/" + id.String()
	}
	data, err := json.Marshal(urls)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(os.Stdout, "ONEBASE_PAGE_MASK_URLS %s\n", data); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}
