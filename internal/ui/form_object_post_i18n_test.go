package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestFormObjectPostErrorsLocalizedHTTP(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		for _, blocked := range []bool{false, true} {
			for _, method := range []string{"Провести", "Post"} {
				t.Run(fmt.Sprintf("%s/blocked=%t/%s", lang, blocked, method), func(t *testing.T) {
					doc := &metadata.Entity{
						Name: "LocalizedPost", Kind: metadata.KindDocument, Posting: blocked,
						Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
					}
					form := managedObjectForm(fieldEl("Number", "Объект.Номер"), writeButton("Post", "PostClick"))
					form.EntityName = doc.Name
					form.ProgramAST = mustParse(t, fmt.Sprintf("Процедура PostClick()\nОбъект.%s();\nКонецПроцедуры", method))
					if blocked {
						form.Handlers = map[metadata.FormEventType]string{metadata.FormEventBeforeWrite: "PostClick"}
					}
					doc.Forms = []*metadata.FormModule{form}
					s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
					bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
					if err != nil {
						t.Fatal(err)
					}
					s.cfg.Bundle, s.cfg.Lang = bundle, lang

					var rec *httptest.ResponseRecorder
					if blocked {
						// The close-intent HTTP entry calls the production form-event
						// save lifecycle, including its nonrecursive BeforeWrite wrapper.
						body := closeIntentBody(uuid.NewString(), "ok", "")
						body.Set("_close_mode", "save")
						body.Set("Номер", "0001")
						rec = executeFormCloseIntent(t, s, doc, body)
					} else {
						req := httptest.NewRequest(http.MethodPost, "/ui/document/"+doc.Name+"/form-event", strings.NewReader(url.Values{
							"_element": {"Post"}, "_event": {"Нажатие"}, "Номер": {"0001"},
						}.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						rec = httptest.NewRecorder()
						router := chi.NewRouter()
						s.Mount(router)
						router.ServeHTTP(rec, req)
					}
					var response formEventResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
						t.Fatalf("decode response: %v: %s", err, rec.Body.String())
					}
					want := "Провести(LocalizedPost): объект не проводится"
					if lang == "en" {
						want = "Post(LocalizedPost): object does not support posting"
					}
					if blocked {
						want = "Провести недоступно внутри обработчика записи формы"
						if lang == "en" {
							want = "Post is unavailable inside a form write handler"
						}
					}
					if response.OK || !strings.Contains(response.Error, want) {
						t.Fatalf("status=%d ok=%t error=%q, want %q", rec.Code, response.OK, response.Error, want)
					}
					rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{})
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) != 0 || response.SavedID != "" {
						t.Fatalf("rejected posting persisted state: rows=%v response=%+v", rows, response)
					}
				})
			}
		}
	}
}
