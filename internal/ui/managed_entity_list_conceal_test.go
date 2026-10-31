package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Exercise the mounted GET, event and refresh routes. A signed list state is
// deliberately replayed across revoke/expiry and between users and instances.
func TestManagedEntityListConcealPublicMatrix(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		for _, readonly := range []bool{false, true} {
			t.Run(map[bool]string{false: "existing", true: "new"}[fresh]+map[bool]string{false: "/editable", true: "/readonly"}[readonly], func(t *testing.T) {
				dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
					c, id := concealFixture(t, db)
					form := c.entity.Forms[0]
					c.entity.Posting = true
					form.Handlers[metadata.FormEventBeforePost] = "ОтменитьПроведение"
					form.ProgramAST = mustParse(t, concealTestModule+`
Процедура ОтменитьПроведение(Отказ)
 Объект.Маской = Ложь;
 Отказ = Истина;
КонецПроцедуры
`)

					form.Elements[0].ReadOnly = readonly
					target := &metadata.Entity{Name: "СобытияСекрета", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Ключ", Type: metadata.FieldTypeString}}}
					form.Elements = append(form.Elements, &metadata.FormElement{ID: "history", Name: "История", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: target.Name, Columns: []string{"Наименование"}, Limit: 5, Filter: []metadata.FormEntityListCondition{{Field: "Ключ", Op: "eq", Source: "Объект.Секрет"}}}})
					if fresh {
						c.entity.Fields[len(c.entity.Fields)-1].Default = "секрет"
					}
					c.server.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{c.entity, target}})
					if err := metadata.ValidateFormEntityLists(c.entity, form, []*metadata.Entity{c.entity, target}); err != nil {
						t.Fatal(err)
					}
					if err := db.Migrate(context.Background(), []*metadata.Entity{c.entity, target}); err != nil {
						t.Fatal(err)
					}
					if err := db.Upsert(context.Background(), target.Name, uuid.New(), map[string]any{"Наименование": "matched-history", "Ключ": "секрет"}, target); err != nil {
						t.Fatal(err)
					}
					key := id.String()
					if fresh {
						key = ""
					}
					now := time.Now()
					c.server.concealClock = func() time.Time { return now }
					router := chi.NewRouter()
					c.server.Mount(router)
					request := func(client concealTestClient, method, path string, body url.Values) *httptest.ResponseRecorder {
						t.Helper()
						r := httptest.NewRequest(method, path, strings.NewReader(body.Encode()))
						r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						r.Header.Set(concealHeader, client.token)
						r.AddCookie(&http.Cookie{Name: "onebase_session", Value: client.session})
						r = r.WithContext(auth.ContextWithUser(r.Context(), client.user))
						w := httptest.NewRecorder()
						router.ServeHTTP(w, r)
						if w.Code != http.StatusOK {
							t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
						}
						return w
					}
					prefix := "/ui/catalog/" + url.PathEscape(c.entity.Name) + "/"
					getKey := key
					if fresh {
						getKey = "new"
					}
					page := request(c, "GET", prefix+getKey, nil).Body.String()
					match := regexp.MustCompile(`data-ob-conceal-context="([^"]+)"`).FindStringSubmatch(page)
					if len(match) != 2 {
						t.Fatal("missing disclosure instance")
					}
					c.token = match[1]
					if strings.Contains(page, "matched-history") || strings.Contains(page, `value="секрет"`) {
						t.Fatal("hidden source or rows in initial HTML")
					}
					stateBody := regexp.MustCompile(`(?s)id="ob-entity-list-config">(.*?)</script>`).FindStringSubmatch(page)
					if len(stateBody) != 2 {
						t.Fatal("missing list configuration")
					}
					var cfg struct{ State string }
					if err := json.Unmarshal([]byte(stateBody[1]), &cfg); err != nil {
						t.Fatal(err)
					}
					assertHiddenState := func(token string) {
						t.Helper()
						body, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
						if err != nil || strings.Contains(string(body), "секрет") {
							t.Fatalf("hidden value in signed state: %v", err)
						}
					}
					assertHiddenState(cfg.State)
					event := func(element string, body url.Values) formEventResponse {
						t.Helper()
						if body == nil {
							body = url.Values{}
						}
						body.Set("_id", key)
						body.Set("_element", element)
						if body.Get("_event") == "" {
							body.Set("_event", string(metadata.FormEventOnClick))
						}
						body.Set("_entity_list_state", cfg.State)
						return decodeFormEventResponse(t, request(c, "POST", prefix+"form-event", body).Body.Bytes())
					}
					refresh := func(client concealTestClient, token string) entityListData {
						t.Helper()
						body := url.Values{"form": {form.Name}, "element": {"history"}, "id": {key}, "state": {token}, "sources": {`{"Объект.Секрет":"секрет"}`}, "Маской": {"false"}}
						var result struct {
							Lists map[string]entityListData `json:"entityLists"`
						}
						if err := json.Unmarshal(request(client, "POST", prefix+"form-list-refresh", body).Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						return result.Lists["history"]
					}
					denied := func(list entityListData) {
						t.Helper()
						if list.Status != "denied" || len(list.Rows) != 0 {
							t.Fatalf("expected neutral denial: %+v", list)
						}
					}
					denied(refresh(c, cfg.State))
					cancelled := event("", url.Values{"_event": {string(metadata.FormEventBeforePost)}})
					if !cancelled.Cancel {
						t.Fatal("BeforePosting was not cancelled")
					}
					denied(cancelled.EntityLists["history"])
					opened := event("", url.Values{"_event": {string(metadata.FormEventOnOpen)}})
					if anyString(valueCI(opened.Values, "Длина")) == "0" {
						t.Fatal("trusted DSL lost the domain source")
					}

					forged := event("Показать", url.Values{"Маской": {"false"}, "Секрет": {"подмена"}})
					if !forged.OK {
						t.Fatalf("hidden source blocked ordinary event: %s", forged.Error)
					}
					denied(forged.EntityLists["history"])
					assertHiddenState(forged.EntityListState)
					shown := event("Показать", url.Values{"Причина": {"проверена"}})
					if !shown.OK || len(shown.EntityLists["history"].Rows) != 1 || len(shown.EntityLists["history"].DisclosureExpires) != 1 {
						t.Fatalf("reveal positive control: %+v", shown)
					}
					if list := refresh(c, shown.EntityListState); len(list.Rows) != 1 {
						t.Fatalf("disclosed refresh: %+v", list)
					}

					cancelled = event("", url.Values{"_event": {string(metadata.FormEventBeforePost)}})
					denied(cancelled.EntityLists["history"])
					denied(refresh(c, shown.EntityListState))
					shown = event("Показать", url.Values{"Причина": {"проверена"}})
					other := c
					other.user = &auth.User{ID: uuid.NewString(), Login: "other", IsAdmin: true}
					denied(refresh(other, shown.EntityListState))
					revoked := event("Отозвать", nil)
					denied(revoked.EntityLists["history"])
					assertHiddenState(revoked.EntityListState)
					denied(refresh(c, shown.EntityListState))
					shown = event("Показать", url.Values{"Причина": {"проверена"}})
					now = now.Add(concealTTL)
					denied(refresh(c, shown.EntityListState))
					shown = event("Показать", url.Values{"Причина": {"проверена"}})
					if !shown.OK {
						t.Fatal(shown.Error)
					}
					failed := event("Ошибка", nil)
					denied(failed.EntityLists["history"])
					denied(refresh(c, shown.EntityListState))

					// If an unauthorized source reached the actual target query this absent
					// table would produce error, rather than the required neutral denial.
					if _, err := db.Exec(context.Background(), "DROP TABLE "+metadata.TableName(target.Name)); err != nil {
						t.Fatal(err)
					}
					denied(refresh(c, shown.EntityListState))
					queried := event("Показать", url.Values{"Причина": {"проверена"}})
					if queried.EntityLists["history"].Status != "error" {
						t.Fatal("missing-table query positive control did not fail")
					}
					row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
					if err != nil || row["Секрет"] != "секрет" {
						t.Fatalf("read-only flow changed stored source: %v", err)
					}
				})
			})
		}
	}
}
