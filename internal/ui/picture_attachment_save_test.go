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
	for _, editor := range []string{"absent", "hidden", "visible", "hidden-two-previews"} {
		t.Run(editor, func(t *testing.T) {
			oldID, newID := uuid.New().String(), uuid.New().String()
			elements := []*metadata.FormElement{
				{Kind: metadata.FormElementPicture, Name: "ПолеФото", DataPath: "Объект.Наименование"},
				{Kind: metadata.FormElementButton, Name: "Обновить", Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnClick: "ОбновитьФото",
				}},
			}
			if editor != "absent" {
				field := &metadata.FormElement{Kind: metadata.FormElementField, Name: "РедакторФото", DataPath: "Объект.Наименование"}
				if strings.HasPrefix(editor, "hidden") {
					field = &metadata.FormElement{Kind: metadata.FormElementGroupBox, Name: "СкрытаяГруппа",
						HiddenWhen: `Наименование <> ""`, Children: []*metadata.FormElement{field}}
				}
				elements = append(elements, field)
			}
			if editor == "hidden-two-previews" {
				elements = append(elements, &metadata.FormElement{Kind: metadata.FormElementPicture,
					Name: "ВтороеФото", DataPath: "Объект.Наименование"})
			}
			srv, ent := setupManagedEventsServer(t, fmt.Sprintf(`
Процедура ОбновитьФото()
 Объект.Наименование = "%s";
КонецПроцедуры
`, newID), nil, elements)
			id := uuid.New()
			ctx := context.Background()
			if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": oldID}, ent); err != nil {
				t.Fatal(err)
			}
			params := map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()}
			path := "/ui/catalog/" + ent.Name + "/" + id.String()
			get := httptest.NewRecorder()
			srv.formEdit(get, reqWithChi(http.MethodGet, path, nil, params))
			if get.Code != http.StatusOK {
				t.Fatalf("GET: %d %s", get.Code, get.Body.String())
			}
			event := pictureFormControls(t, get.Body.String())
			event.Set("_id", id.String())
			event.Set("_kind", "object")
			event.Set("_element", "Обновить")
			event.Set("_event", string(metadata.FormEventOnClick))
			response := executeFormEvent(t, srv, ent, event).Body.Bytes()
			if decoded := decodeFormEventResponse(t, response); !decoded.OK {
				t.Fatalf("form-event: %s", decoded.Error)
			}
			doc, err := html.Parse(strings.NewReader(get.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{"page": domElementNode(doc), "response": json.RawMessage(response)})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), node, "static/managed_picture_save_harness.js") //nolint:gosec // test-only executable resolved by exec.LookPath
			cmd.Stdin = strings.NewReader(string(payload))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("applyValues: %v\n%s", err, output)
			}
			var submitted url.Values
			if err := json.Unmarshal(output, &submitted); err != nil {
				t.Fatalf("decode controls: %v; %s", err, output)
			}
			if got := submitted["Наименование"]; len(got) != 1 || got[0] != newID {
				t.Fatalf("form submits %v, want exactly one updated UUID %s", got, newID)
			}
			post := httptest.NewRecorder()
			srv.submitEdit(post, reqWithChi(http.MethodPost, path, submitted, params))
			if post.Code != http.StatusSeeOther {
				t.Fatalf("save: %d %s", post.Code, post.Body.String())
			}
			row, err := srv.store.GetByID(ctx, ent.Name, id, ent)
			if err != nil || row["Наименование"] != newID {
				t.Fatalf("stored UUID = %v, want %s; error: %v", row["Наименование"], newID, err)
			}
		})
	}
}
