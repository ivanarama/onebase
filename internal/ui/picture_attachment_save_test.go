package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// GET → form-event → production applyValues → submitEdit → database. In
// particular, a preview must submit the event's UUID when a hidden group has
// omitted its editor from the DOM. Visible editors must remain the sole sender.
func TestManagedPictureEventValueSurvivesSave(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the picture save round trip")
	}
	for _, editor := range []string{
		"absent", "hidden", "visible", "hidden-two-previews",
		"disabled", "disabled-two-previews", "visible-to-disabled", "visible-to-disabled-two-previews",
	} {
		maxSteps := 1
		if strings.Contains(editor, "disabled") {
			maxSteps = 3
		}
		for steps := 1; steps <= maxSteps; steps++ {
			t.Run(fmt.Sprintf("%s/%d-events", editor, steps), func(t *testing.T) {
				oldID, newID, visibleID := uuid.New().String(), uuid.New().String(), uuid.New().String()
				transitions := strings.Contains(editor, "disabled")
				elements := []*metadata.FormElement{
					{Kind: metadata.FormElementPicture, Name: "ПолеФото", DataPath: "Объект.Наименование"},
					{Kind: metadata.FormElementButton, Name: "Обновить", Handlers: map[metadata.FormEventType]string{
						metadata.FormEventOnClick: "ОбновитьФото",
					}},
				}
				if editor != "absent" {
					field := &metadata.FormElement{Kind: metadata.FormElementField, Name: "РедакторФото", DataPath: "Объект.Наименование"}
					if transitions {
						field.HiddenWhen = fmt.Sprintf(`Наименование <> "%s"`, visibleID)
						if strings.HasPrefix(editor, "visible-to") {
							field.HiddenWhen = fmt.Sprintf(`Наименование = "%s"`, newID)
						}
					} else if strings.HasPrefix(editor, "hidden") {
						field = &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "СкрытаяГруппа",
							HiddenWhen: `Наименование <> ""`, Children: []*metadata.FormElement{field}}
					}
					elements = append(elements, field)
				}
				if strings.HasSuffix(editor, "two-previews") {
					elements = append(elements, &metadata.FormElement{Kind: metadata.FormElementPicture,
						Name: "ВтороеФото", DataPath: "Объект.Наименование"})
				}
				srv, ent := setupManagedEventsServer(t, fmt.Sprintf(`
Процедура ОбновитьФото()
 Если Объект.Наименование = "%s" Тогда
  Объект.Наименование = "%s";
 Иначе
  Объект.Наименование = "%s";
 КонецЕсли;
КонецПроцедуры
`, newID, visibleID, newID), nil, elements)
				id := uuid.New()
				ctx := context.Background()
				if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": oldID}, ent); err != nil {
					t.Fatal(err)
				}
				router := chi.NewRouter()
				srv.Mount(router)
				request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
					t.Helper()
					req := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					return rec
				}
				path := "/ui/catalog/" + ent.Name + "/" + id.String()
				get := request(http.MethodGet, path, nil)
				if get.Code != http.StatusOK {
					t.Fatalf("GET: %d %s", get.Code, get.Body.String())
				}
				doc, err := html.Parse(strings.NewReader(get.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				responses := []json.RawMessage{}
				submitted := pictureFormControls(t, get.Body.String())
				want := newID
				for step := range steps {
					event := url.Values{}
					for key, values := range submitted {
						event[key] = append([]string(nil), values...)
					}
					event.Set("_id", id.String())
					event.Set("_kind", "object")
					event.Set("_element", "Обновить")
					event.Set("_event", string(metadata.FormEventOnClick))
					rec := request(http.MethodPost, "/ui/catalog/"+ent.Name+"/form-event", event)
					if rec.Code != http.StatusOK {
						t.Fatalf("form-event: %d %s", rec.Code, rec.Body.String())
					}
					response := rec.Body.Bytes()
					if decoded := decodeFormEventResponse(t, response); !decoded.OK {
						t.Fatalf("form-event: %s", decoded.Error)
					}
					responses = append(responses, json.RawMessage(response))
					payload, err := json.Marshal(map[string]any{
						"page": domElementNode(doc), "responses": responses, "checkEditor": transitions,
						"initiallyDisabled": strings.HasPrefix(editor, "disabled"),
					})
					if err != nil {
						t.Fatal(err)
					}
					cmd := exec.CommandContext(t.Context(), node, "static/managed_picture_save_harness.js") //nolint:gosec // test-only executable resolved by exec.LookPath
					cmd.Stdin = strings.NewReader(string(payload))
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("step %d applyValues: %v\n%s", step, err, output)
					}
					if err := json.Unmarshal(output, &submitted); err != nil {
						t.Fatalf("decode controls: %v; %s", err, output)
					}
					want = newID
					if step == 1 {
						want = visibleID
					}
					if got := submitted["Наименование"]; len(got) != 1 || got[0] != want {
						t.Fatalf("step %d form submits %v, want exactly one updated UUID %s", step, got, want)
					}
				}
				post := request(http.MethodPost, path, submitted)
				if post.Code != http.StatusSeeOther {
					t.Fatalf("save: %d %s", post.Code, post.Body.String())
				}
				row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
				if err != nil || row["Наименование"] != want {
					t.Fatalf("stored UUID = %v, want %s; error: %v", row["Наименование"], want, err)
				}
			})
		}
	}
}
