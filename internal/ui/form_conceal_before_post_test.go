package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise cancellation and subsequent events/writes through the mounted router.
func TestConcealBeforePostCancellationHTTP(t *testing.T) {
	for _, parameter := range []string{"Отказ", "Cancel"} {
		for _, fresh := range []bool{false, true} {
			for _, prior := range []bool{false, true} {
				for _, cancelled := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/new=%v/prior=%v/cancel=%v", parameter, fresh, prior, cancelled), func(t *testing.T) {
						dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
							c, id := concealFixture(t, db)
							c.entity.Posting = true
							form := c.entity.Forms[0]
							form.Handlers[metadata.FormEventBeforePost] = "ПроверитьПроведение"
							form.ProgramAST = mustParse(t, concealTestModule+fmt.Sprintf(`
Процедура ПроверитьПроведение(%s)
 Объект.Маской = Ложь;
 %s = Объект.Причина = "отмена";
КонецПроцедуры
`, parameter, parameter))
							key, initial := id.String(), "секрет"
							if fresh {
								key = ""
								initial = "значение по умолчанию"
								c.entity.Fields[len(c.entity.Fields)-1].Default = initial
							}
							router := chi.NewRouter()
							c.server.Mount(router)
							request := func(method, path string, body url.Values) *httptest.ResponseRecorder {
								t.Helper()
								if c.token != "" {
									path += "?" + concealQuery + "=" + url.QueryEscape(c.token)
								}
								req := httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
								req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
								req.AddCookie(&http.Cookie{Name: "onebase_session", Value: c.session})
								req = req.WithContext(auth.ContextWithUser(req.Context(), c.user))
								rec := httptest.NewRecorder()
								router.ServeHTTP(rec, req)
								return rec
							}
							base := "/ui/catalog/" + c.entity.Name + "/"
							open := func() {
								t.Helper()
								c.token = ""
								target := key
								if target == "" {
									target = "new"
								}
								rec := request(http.MethodGet, base+target, nil)
								if rec.Code != http.StatusOK {
									t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
								}
								match := regexp.MustCompile(`data-ob-conceal-context="([^"]+)"`).FindStringSubmatch(rec.Body.String())
								if len(match) != 2 {
									t.Fatal("missing form context")
								}
								c.token = match[1]
							}
							event := func(kind metadata.FormEventType, element, reason string) formEventResponse {
								t.Helper()
								rec := request(http.MethodPost, base+"form-event", url.Values{"_id": {key}, "_event": {string(kind)}, "_element": {element}, "Причина": {reason}, "Секрет": {initial}})
								if rec.Code != http.StatusOK {
									t.Fatalf("event: %d %s", rec.Code, rec.Body.String())
								}
								resp := decodeFormEventResponse(t, rec.Body.Bytes())
								if !resp.OK || resp.Error != "" {
									t.Fatalf("event failed: %+v", resp)
								}
								return resp
							}
							assertDisclosure := func(resp formEventResponse, want bool) {
								t.Helper()
								if got := len(resp.Revealed) > 0; got != want {
									t.Fatalf("revealed=%v, want %v", got, want)
								}
								if got := anyString(valueCI(resp.Values, "Секрет")); (want && got != initial) || (!want && got != "") {
									t.Fatalf("unexpected disclosure state")
								}
							}
							submit := func(value string) string {
								t.Helper()
								target := key
								if target == "" {
									target = "new"
								}
								rec := request(http.MethodPost, base+target, url.Values{"Наименование": {"сохранено"}, "Секрет": {value}})
								if rec.Code != http.StatusSeeOther {
									t.Fatalf("submit: %d %s", rec.Code, rec.Body.String())
								}
								if key == "" {
									location := rec.Header().Get("Location")
									var err error
									id, err = uuid.Parse(location[strings.LastIndex(location, "/")+1:])
									if err != nil {
										t.Fatal(err)
									}
									key = id.String()
								}
								row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
								if err != nil {
									t.Fatal(err)
								}
								if anyString(valueCI(row, "Наименование")) != "сохранено" {
									t.Fatal("ordinary field write lost")
								}
								return anyString(valueCI(row, "Секрет"))
							}
							open()
							if prior {
								assertDisclosure(event(metadata.FormEventOnClick, "Показать", "проверена"), true)
							}
							reason := "продолжить"
							if cancelled {
								reason = "отмена"
							}
							resp := event(metadata.FormEventBeforePost, "", reason)
							if resp.Cancel != cancelled || resp.Question != nil {
								t.Fatalf("posting result changed: %+v", resp)
							}
							assertDisclosure(resp, !cancelled)
							assertDisclosure(event(metadata.FormEventOnOpen, "", ""), !cancelled)
							want := initial
							if !cancelled {
								want = "разрешённая правка"
							}
							if got := submit("разрешённая правка"); got != want {
								t.Fatal("write did not honor cancellation result")
							}
							// A subsequent successful handler can issue a fresh grant after cancellation.
							initial = want
							open()
							assertDisclosure(event(metadata.FormEventBeforePost, "", "продолжить"), true)
							if got := submit("новая правка"); got != "новая правка" {
								t.Fatal("new successful grant did not permit edit")
							}
						})
					})
				}
			}
		}
	}
}
