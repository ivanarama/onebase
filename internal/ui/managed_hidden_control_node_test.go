package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Пути и начальные состояния берём из GET формы, карты обновлений — из POST
// события. Node применяет их кодом managed.js для обоих порядков копий в DOM.
func TestManagedHiddenControlBehaviorInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the managed hidden control behavior regression test")
	}
	for _, shownFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("shownFirst=%t", shownFirst), func(t *testing.T) {
			ent := заявкаСКопиямиУлицы(t, metadata.FormElementField, shownFirst)
			ent.Forms[0].ProgramAST = mustParse(t, `
Процедура Принять()
	Если Объект.СтадияОформления = "Принята" Тогда
		Объект.СтадияОформления = "Черновик";
		Объект.Улица = "Обновлённая";
	Иначе
		Объект.СтадияОформления = "Принята";
		Объект.Улица = "Новая";
	КонецЕсли;
КонецПроцедуры
`)
			srv, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			id := uuid.New()
			if err := srv.store.Upsert(ctx, ent.Name, id, map[string]any{
				"СтадияОформления": "Черновик", "Улица": "Старая",
			}, ent); err != nil {
				t.Fatal(err)
			}
			req := reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/"+id.String(), nil,
				map[string]string{"kind": "catalog", "entity": ent.Name, "id": id.String()})
			rec := httptest.NewRecorder()
			srv.formEdit(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("public form GET: status=%d body=%s", rec.Code, rec.Body.String())
			}
			fixture := struct {
				Page      managedPage       `json:"page"`
				Responses []json.RawMessage `json:"responses"`
			}{Page: parseManagedPage(t, rec.Body.String())}
			event := url.Values{
				"_id": {id.String()}, "_element": {"КнопкаПринять"},
				"_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"},
				"СтадияОформления": {"Черновик"}, "Улица": {"Старая"},
			}
			for range 2 {
				response := executeFormEvent(t, srv, ent, event).Body.Bytes()
				decoded := decodeFormEventResponse(t, response)
				if !decoded.OK {
					t.Fatalf("public form event: %s", response)
				}
				fixture.Responses = append(fixture.Responses, json.RawMessage(response))
				for _, name := range []string{"СтадияОформления", "Улица"} {
					event.Set(name, fmt.Sprint(decoded.Values[name]))
				}
			}
			payload, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "hidden-control.json")
			if err := os.WriteFile(path, payload, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), node, "--test", "static/managed_hidden_control_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
			cmd.Env = append(os.Environ(), "OB_HIDDEN_CONTROL_FIXTURE="+path)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("node managed hidden control behavior test: %v\n%s", err, output)
			}
		})
	}
}
