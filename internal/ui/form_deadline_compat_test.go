package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Open, edit, execute a form event, save and reopen through Server.Mount.
// These assertions bind the deadline to the same controls as main's masks,
// hints, required/readonly state and event attributes, on both databases.
func TestManagedDeadlineCompatibilityHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, warn := range []int{0, 30} {
			for _, policy := range []string{"full", "mask_all"} {
				t.Run(fmt.Sprintf("warn=%d/%s", warn, policy), func(t *testing.T) {
					cat := uiClientEntity()
					cat.Fields = append(cat.Fields, metadata.Field{Name: "Срок", Type: metadata.FieldTypeDate})
					text := fieldEl("Телефон", "Объект.Телефон")
					text.DeadlineWarn, text.InputMask, text.Mask = warn, "00:00", "[0-9]{2}:[0-9]{2}"
					text.Required, text.Hint, text.AccessKey = true, "Срок звонка", "t"
					text.Handlers = map[metadata.FormEventType]string{metadata.FormEventOnChange: "Проверить"}
					local := fieldEl("Локальное", "Локальное")
					local.DeadlineWarn, local.InputMask, local.Mask = warn, "00:00", text.Mask
					local.Required, local.Hint, local.AccessKey = true, text.Hint, "l"
					local.Handlers = text.Handlers
					date := fieldEl("ДатаСрока", "Объект.Срок")
					date.DeadlineWarn, date.Required, date.Hint = warn, true, text.Hint
					readonly := fieldEl("Название", "Объект.Наименование")
					readonly.DeadlineWarn, readonly.ReadOnly, readonly.Required = warn, true, true
					form := managedObjectForm(text, date, local, readonly, maskEventButton("Проверить", "Проверить"))
					form.EntityName = cat.Name
					form.Attributes = []*metadata.FormAttribute{{Name: "Локальное", TypeRef: "string"}}
					form.ProgramAST = mustParse(t, "Процедура Проверить()\nКонецПроцедуры")
					cat.Forms = []*metadata.FormModule{form}
					ctx := context.Background()
					srv := newSubmitTestServerOn(t, db, []*metadata.Entity{cat})
					if err := db.EnsureAuditSchema(ctx); err != nil {
						t.Fatal(err)
					}
					id := uuid.New()
					if err := db.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "Клиент", "Телефон": "17:20", "Срок": time.Date(2026, 10, 21, 12, 15, 0, 0, time.Local)}, cat); err != nil {
						t.Fatal(err)
					}
					user := uiMaskUser([]string{"read", "write", "disclose"}, auth.FieldPolicies{"Телефон": {Read: policy}})
					router := chi.NewRouter()
					srv.Mount(router)
					base := "/ui/catalog/" + url.PathEscape(cat.Name)
					request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
						t.Helper()
						r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
						r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						r = r.WithContext(auth.ContextWithUser(r.Context(), user))
						w := httptest.NewRecorder()
						router.ServeHTTP(w, r)
						return w
					}
					page := request("GET", base+"/"+id.String(), nil)
					if page.Code != http.StatusOK {
						t.Fatalf("GET: %d %s", page.Code, page.Body.String())
					}
					for _, name := range []string{"Телефон", "Срок", "Локальное", "Наименование"} {
						a := concealMaskInput(t, page.Body.String(), name)
						got, has := a["data-ob-deadline"]
						if has != (warn > 0) || (has && got != "30") {
							t.Fatalf("%s deadline attributes: %v", name, a)
						}
						if name == "Телефон" || name == "Локальное" {
							for key, want := range map[string]string{"autocomplete": "one-time-code", "placeholder": "__:__", "pattern": text.Mask, "data-ob-form-mask": "", "data-ob-input-mask": "00:00", "inputmode": "numeric", "title": text.Hint, "data-ob-fire-change": name} {
								if got, ok := a[key]; !ok || got != want {
									t.Errorf("%s %s=%q present=%v want=%q", name, key, got, ok, want)
								}
							}
							if _, has := a["required"]; !has {
								t.Errorf("%s lost required", name)
							}
						}
						if name == "Срок" && a["type"] != "datetime-local" {
							t.Fatalf("date control: %v", a)
						}
						if name == "Наименование" {
							if _, has := a["readonly"]; !has {
								t.Fatal("lost readonly")
							}
							if _, has := a["required"]; has {
								t.Fatal("readonly acquired required")
							}
						}
					}
					phone := concealMaskInput(t, page.Body.String(), "Телефон")["value"]
					if (policy == "full" && phone != "17:20") || (policy == "mask_all" && (phone == "17:20" || strings.Contains(page.Body.String(), "17:20"))) {
						t.Fatalf("read policy %s: %q", policy, phone)
					}
					event := request("POST", base+"/form-event", url.Values{"_id": {id.String()}, "_event": {string(metadata.FormEventOnChange)}, "_element": {"Телефон"}, "Телефон": {"18:30"}, "Локальное": {"19:45"}})
					if event.Code != http.StatusOK {
						t.Fatalf("event: %d %s", event.Code, event.Body.String())
					}
					response := decodeFormEventResponse(t, event.Body.Bytes())
					if !response.OK || response.Values["Локальное"] != "19:45" {
						t.Fatalf("event result: %+v", response)
					}
					save := request("POST", base+"/"+id.String(), url.Values{"Наименование": {"Клиент"}, "Телефон": {"18:30"}, "Срок": {"2026-10-21T18:30"}, "Локальное": {"19:45"}})
					if save.Code != http.StatusSeeOther {
						t.Fatalf("save: %d %s", save.Code, save.Body.String())
					}
					row, err := db.GetByID(context.Background(), cat.Name, id, cat)
					if err != nil {
						t.Fatal(err)
					}
					want := "18:30"
					if policy == "mask_all" {
						want = "17:20"
					}
					if row["Телефон"] != want {
						t.Fatalf("write policy %s: got=%v want=%s", policy, row["Телефон"], want)
					}
					again := request("GET", base+"/"+id.String(), nil)
					if again.Code != http.StatusOK {
						t.Fatalf("reopen: %d", again.Code)
					}
					if policy == "mask_all" {
						disclose := request("POST", base+"/"+id.String()+"/disclose", url.Values{"field": {"Телефон"}, "reason": {"Звонок клиента"}})
						var result struct {
							Value     string `json:"value"`
							Disclosed bool   `json:"disclosed"`
						}
						if err := json.Unmarshal(disclose.Body.Bytes(), &result); err != nil || disclose.Code != http.StatusOK || !result.Disclosed || result.Value != want {
							t.Fatalf("disclose: %d %s err=%v", disclose.Code, disclose.Body.String(), err)
						}
					}
				})
			}
		}
	})
}
