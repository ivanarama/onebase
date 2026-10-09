package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/webhook"
)

// #1716: receive real webhook requests after the form command and Service.Save.
// Version remains available to the form, but neither path exposes it to hooks.
func TestFormCommandWebhookExcludesVersion(t *testing.T) {
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		t.Run(string(kind), func(t *testing.T) {
			ent := &metadata.Entity{
				Name: "Записи", Kind: kind,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
				Forms: []*metadata.FormModule{{
					Name: "ФормаОбъекта", Kind: "object", EntityName: "Записи",
					LayoutKind: metadata.FormLayoutManaged,
					ProgramAST: mustParse(t, `
Процедура ЗаписатьНажатие()
	Объект.Записать();
КонецПроцедуры
`),
					Elements: []*metadata.FormElement{{
						Kind: metadata.FormElementButton, Name: "Записать",
						Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ЗаписатьНажатие"},
					}},
				}},
			}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
			payloads := make(chan map[string]string, 3)
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode webhook: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				payloads <- payload
			}))
			defer sink.Close()
			event := string(kind) + ".save"
			hooks := webhook.New([]webhook.Config{{
				Name: "save", On: event, URL: sink.URL,
				Body: `{"name":"{{Наименование}}","version":"{{_version}}","reference":"{{reference}}","self":"{{ссылка}}"}`,
			}}, nil)
			t.Cleanup(func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := hooks.Close(closeCtx); err != nil {
					t.Errorf("close hooks: %v", err)
				}
			})
			s.cfg.Webhooks = hooks
			s.entitySvc = s.newEntityService(hooks)
			router := chi.NewRouter()
			s.Mount(router)
			body := url.Values{
				"_element": {"Записать"}, "_event": {string(metadata.FormEventOnClick)},
				"_kind": {"object"}, "Наименование": {"Тест"},
			}
			var formID uuid.UUID
			for _, version := range []int64{1, 2} {
				req := httptest.NewRequest(http.MethodPost, "/ui/"+string(kind)+"/Записи/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("form event: status=%d body=%s", rec.Code, rec.Body.String())
				}
				response := decodeFormEventResponse(t, rec.Body.Bytes())
				if !response.OK || response.Version != version {
					t.Fatalf("form event: %+v, want version=%d", response, version)
				}
				if version == 1 {
					formID = uuid.MustParse(response.SavedID)
					body.Set("_id", formID.String())
					body.Set("_version", "1")
				}
			}
			result, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
				Entity: ent, ID: uuid.New(), IsNew: true,
				Fields: map[string]any{"Наименование": "Тест"},
			})
			if err != nil || result.DSLError != "" {
				t.Fatalf("Service.Save: %+v, %v", result, err)
			}
			hooks.Wait()
			want := map[string]string{"name": "Тест", "version": "", "reference": "", "self": ""}
			if len(payloads) != 3 {
				t.Fatalf("webhooks=%d, want 3", len(payloads))
			}
			for range 3 {
				if got := <-payloads; !reflect.DeepEqual(got, want) {
					t.Errorf("webhook=%v, want %v", got, want)
				}
			}
			version, exists, err := s.store.EntityVersionExists(ctx, ent.Name, formID)
			if err != nil || !exists || version != 2 {
				t.Fatalf("stored form version=%d exists=%v err=%v", version, exists, err)
			}
		})
	}
}
