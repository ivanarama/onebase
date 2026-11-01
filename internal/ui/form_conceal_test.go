package ui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

func valueCI(values map[string]any, name string) any {
	for k, v := range values {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

func anyString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func TestConcealBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нужен node")
	}
	cmd := exec.Command(node, "--test", "static/conceal_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node conceal test: %v\n%s", err, out)
	}
}

const concealTestModule = `
Процедура Открыть()
 Объект.Длина = СтрДлина(Объект.Секрет);
КонецПроцедуры
Процедура Показать()
 Если Объект.Причина = "проверена" Тогда
  Объект.Маской = Ложь;
 КонецЕсли;
КонецПроцедуры
Процедура ПроверитьКонтроллер()
 Если НЕ Маской Тогда
  Объект.Маской = Ложь;
 КонецЕсли;
КонецПроцедуры
Процедура Отозвать()
 Объект.Маской = Истина;
КонецПроцедуры
Процедура Ошибка()
 Объект.Маской = Ложь;
 ВызватьИсключение("отказ");
КонецПроцедуры
Процедура Присвоить()
 Объект.Секрет = "сервер";
КонецПроцедуры
Процедура ЗаписатьСервером()
 Объект.Секрет = "сервер";
 Объект.Записать();
КонецПроцедуры
`

type concealTestClient struct {
	server  *Server
	entity  *metadata.Entity
	token   string
	session string
	user    *auth.User
}

func (c concealTestClient) request(t *testing.T, method, id, element string, body url.Values) *httptest.ResponseRecorder {
	t.Helper()
	target := "/ui/catalog/" + c.entity.Name + "/" + id
	if method == "EVENT" {
		target = "/ui/catalog/" + c.entity.Name + "/form-event"
	}
	if method == "CLOSE" {
		target = "/ui/catalog/" + c.entity.Name + "/close-intent"
	}
	if c.token != "" {
		target += "?" + concealQuery + "=" + c.token
	}
	verb := "POST"
	if method == "GET" {
		verb = "GET"
	}
	if body == nil {
		body = url.Values{}
	}
	if method == "EVENT" {
		body.Set("_id", id)
		if body.Get("_event") == "" {
			body.Set("_event", string(metadata.FormEventOnClick))
		}
		body.Set("_element", element)
		if element == "" {
			body.Set("_event", string(metadata.FormEventOnOpen))
		}
	}
	req := httptest.NewRequest(verb, target, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: c.session})
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("kind", "catalog")
	ctx.URLParams.Add("entity", c.entity.Name)
	if id != "" {
		ctx.URLParams.Add("id", id)
	}
	req = req.WithContext(auth.ContextWithUser(context.WithValue(req.Context(), chi.RouteCtxKey, ctx), c.user))
	rec := httptest.NewRecorder()
	switch method {
	case "GET":
		if id == "" {
			c.server.form(rec, req)
		} else {
			c.server.formEdit(rec, req)
		}
	case "EVENT":
		c.server.handleManagedFormEvent(rec, req)
	case "CLOSE":
		setEntityCloseIntentHeaders(req, body)
		c.server.handleManagedFormCloseIntent(rec, req)
	default:
		if id == "" {
			c.server.submit(rec, req)
		} else {
			c.server.submitEdit(rec, req)
		}
	}
	return rec
}

func (c concealTestClient) open(t *testing.T, id string) concealTestClient {
	t.Helper()
	rec := c.request(t, "GET", id, "", nil)
	if rec.Code != 200 {
		t.Fatalf("GET %d: %s", rec.Code, rec.Body.String())
	}
	match := regexp.MustCompile(`data-ob-conceal-context="([^"]+)"`).FindStringSubmatch(rec.Body.String())
	if len(match) != 2 {
		t.Fatal("missing server instance")
	}
	c.token = match[1]
	if strings.Contains(rec.Body.String(), `value="секрет"`) || strings.Contains(rec.Body.String(), `>секрет</textarea>`) {
		t.Fatal("secret in HTML")
	}
	return c
}

func (c concealTestClient) event(t *testing.T, id, element string, body url.Values) formEventResponse {
	t.Helper()
	rec := c.request(t, "EVENT", id, element, body)
	if rec.Code != 200 {
		t.Fatalf("EVENT %d: %s", rec.Code, rec.Body.String())
	}
	return decodeFormEventResponse(t, rec.Body.Bytes())
}

func concealFixture(t *testing.T, db *storage.DB) (concealTestClient, uuid.UUID) {
	t.Helper()
	elements := []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "Секрет", DataPath: "Объект.Секрет", Conceal: true, RevealWhen: "НЕ Маской"},
		{Kind: metadata.FormElementField, Name: "Название", DataPath: "Объект.Наименование"},
	}
	for _, name := range []string{"Показать", "ПроверитьКонтроллер", "Отозвать", "Ошибка", "Присвоить", "ЗаписатьСервером"} {
		elements = append(elements, &metadata.FormElement{Kind: metadata.FormElementButton, Name: name, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: name}})
	}
	srv, ent := setupManagedEventsServer(t, concealTestModule, map[metadata.FormEventType]string{metadata.FormEventOnOpen: "Открыть"}, elements)
	ent.Fields = append(ent.Fields, metadata.Field{Name: "Секрет", Type: metadata.FieldTypeString})
	ent.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "Маской", TypeRef: "bool"}, {Name: "Причина", TypeRef: "string"}, {Name: "Длина", TypeRef: "number"}}
	srv.store = db
	srv.entitySvc = srv.newEntityService(nil)
	if err := db.Migrate(context.Background(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": "обычное", "Секрет": "секрет"}, ent); err != nil {
		t.Fatal(err)
	}
	return concealTestClient{server: srv, entity: ent, session: uuid.NewString(), user: &auth.User{ID: uuid.NewString(), Login: "operator", IsAdmin: true}}, id
}

func TestManagedFormConcealedField(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		c, id := concealFixture(t, db)
		key := id.String()
		c = c.open(t, key)
		hidden := func(resp formEventResponse) {
			t.Helper()
			if anyString(valueCI(resp.Values, "Секрет")) != "" || len(resp.Revealed) > 0 {
				t.Fatalf("unauthorized disclosure: %+v", resp)
			}
		}
		opened := c.event(t, key, "", url.Values{"Маской": {"false"}})
		hidden(opened)
		if anyString(valueCI(opened.Values, "Длина")) == "0" {
			t.Fatal("handler did not see stored secret")
		}
		hidden(c.event(t, key, "Показать", url.Values{"Маской": {"false"}, "Секрет": {"подмена"}}))
		hidden(c.event(t, key, "ПроверитьКонтроллер", url.Values{"Маской": {"false"}}))
		read := func() string {
			t.Helper()
			row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
			if err != nil {
				t.Fatal(err)
			}
			return anyString(row["Секрет"])
		}
		submit := func(value string) {
			t.Helper()
			rec := c.request(t, "POST", key, "", url.Values{"Секрет": {value}, "Наименование": {"обычное"}, "Маской": {"false"}})
			if rec.Code != 303 {
				t.Fatalf("submit %d: %s", rec.Code, rec.Body.String())
			}
		}
		submit("подмена")
		if read() != "секрет" {
			t.Fatal("forged hidden input persisted")
		}
		shown := c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}})
		if !shown.OK || anyString(valueCI(shown.Values, "Секрет")) != "секрет" {
			t.Fatalf("trusted disclosure failed: %+v", shown)
		}
		submit("изменено")
		if read() != "изменено" {
			t.Fatal("revealed edit did not persist")
		}
		hidden(c.event(t, key, "Отозвать", nil))
		submit("подмена")
		if read() != "изменено" {
			t.Fatal("revoked input persisted")
		}
		_ = c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}})
		hidden(c.event(t, key, "Ошибка", nil))
		server := c.event(t, key, "Присвоить", nil)
		hidden(server)
		if !server.OK {
			t.Fatal(server.Error)
		}
		// A server assignment remains legal even when hidden from the response.
		hidden(c.event(t, key, "ЗаписатьСервером", nil))
		if read() != "сервер" {
			t.Fatal("trusted assignment lost")
		}
	})
}

func TestConcealIdentityAndExpiry(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		c, id := concealFixture(t, db)
		key := id.String()
		now := time.Now()
		c.server.concealClock = func() time.Time { return now }
		c = c.open(t, key)
		reveal := func(client concealTestClient) {
			t.Helper()
			r := client.event(t, key, "Показать", url.Values{"Причина": {"проверена"}})
			if len(r.Revealed) != 1 {
				t.Fatalf("reveal failed: %+v", r)
			}
		}
		hidden := func(client concealTestClient, record string) {
			t.Helper()
			r := client.event(t, record, "", url.Values{"Маской": {"false"}})
			if anyString(valueCI(r.Values, "Секрет")) != "" {
				t.Fatal("identity/TTL bypass")
			}
		}
		reveal(c)
		other := c
		other.session = uuid.NewString()
		hidden(other, key)
		other = c
		other.user = &auth.User{ID: uuid.NewString(), Login: "other", IsAdmin: true}
		hidden(other, key)
		other = c
		other.token = "unknown"
		hidden(other, key)
		tab := c
		tab.token = ""
		tab = tab.open(t, key)
		hidden(tab, key)
		another := insertContragent(t, c.server, c.entity, "another")
		hidden(c, another.String())
		// A mismatched object invalidates rather than transferring its authority.
		c.token = ""
		c = c.open(t, key)
		reveal(c)
		now = now.Add(9 * time.Minute)
		hidden(tab, key)
		_ = c.event(t, key, "", nil) // ordinary read does not renew
		now = now.Add(2 * time.Minute)
		hidden(c, key)
		reveal(c) // a new trusted handler may reauthorize after grant expiry
		c.token = ""
		c = c.open(t, key)
		reveal(c)
		old := c
		c = c.open(t, key)
		hidden(old, key) // reload
		reveal(c)
		c.server.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{c.entity}})
		hidden(c, key)
		c.token = ""
		c = c.open(t, key)
		reveal(c)
		c.server.concealInstances = nil
		hidden(c, key) // process/state loss
		c.token = ""
		c = c.open(t, key)
		reveal(c)
		otherDB, err := storage.ConnectSQLite(context.Background(), filepath.Join(t.TempDir(), "other-base.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(otherDB.Close)
		if err := otherDB.Migrate(context.Background(), []*metadata.Entity{c.entity}); err != nil {
			t.Fatal(err)
		}
		if err := otherDB.Upsert(context.Background(), c.entity.Name, id, map[string]any{"Наименование": "other", "Секрет": "other-secret"}, c.entity); err != nil {
			t.Fatal(err)
		}
		originalDB := c.server.store
		originalSvc := c.server.entitySvc
		c.server.store = otherDB
		c.server.entitySvc = c.server.newEntityService(nil)
		hidden(c, key)
		c.server.store = originalDB
		c.server.entitySvc = originalSvc
		hidden(c, key) // a database switch cannot resurrect an old grant

	})
}

func TestConcealNewObjectAndControls(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, multiline := range []bool{false, true} {
			for _, readonly := range []bool{false, true} {
				t.Run(fmt.Sprintf("multiline=%v/readonly=%v", multiline, readonly), func(t *testing.T) {
					c, _ := concealFixture(t, db)
					el := c.entity.Forms[0].Elements[0]
					el.Multiline = multiline
					el.ReadOnly = readonly
					c = c.open(t, "")
					opened := c.event(t, "", "Показать", url.Values{"Причина": {"проверена"}})
					if !opened.OK || len(opened.Revealed) != 1 {
						t.Fatalf("unsaved grant failed: %+v", opened)
					}
					// Reload starts a distinct unsaved identity with no grants.
					old := c
					c = c.open(t, "")
					stale := old.event(t, "", "", nil)
					if len(stale.Revealed) != 0 {
						t.Fatal("reload retained unsaved grant")
					}
					rec := c.request(t, "POST", "", "", url.Values{"Наименование": {uuid.NewString()}, "Секрет": {"подмена"}, "Маской": {"false"}})
					if rec.Code != 303 {
						t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
					}
					location := rec.Header().Get("Location")
					id, err := uuid.Parse(location[strings.LastIndex(location, "/")+1:])
					if err != nil {
						t.Fatal(err)
					}
					row, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
					if err != nil {
						t.Fatal(err)
					}
					if anyString(row["Секрет"]) != "" {
						t.Fatal("hidden new input without default persisted")
					}
					// A later trusted handler assignment survives default overlay on its write.
					c.token = ""
					c = c.open(t, "")
					saved := c.event(t, "", "ЗаписатьСервером", url.Values{"Наименование": {uuid.NewString()}, "Секрет": {"подмена"}})
					if !saved.OK || saved.SavedID == "" {
						t.Fatalf("trusted new write: %+v", saved)
					}
					savedID, err := uuid.Parse(saved.SavedID)
					if err != nil {
						t.Fatal(err)
					}
					row, err = db.GetByID(context.Background(), c.entity.Name, savedID, c.entity)
					if err != nil {
						t.Fatal(err)
					}
					if row["Секрет"] != "сервер" {
						t.Fatal("new server assignment lost")
					}
					r := c.event(t, saved.SavedID, "", nil)
					if len(r.Revealed) != 0 {
						t.Fatal("unsaved grant transferred to saved object")
					}
				})
			}
		}
	})
}

func TestConcealCloseAndPermissionLoss(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		c, id := concealFixture(t, db)
		key := id.String()
		c = c.open(t, key)
		grant := func() {
			t.Helper()
			r := c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}})
			if len(r.Revealed) != 1 {
				t.Fatalf("grant failed %+v", r)
			}
		}
		grant()
		body := closeIntentBody(uuid.NewString(), "close", "")
		body.Set("_id", key)
		ensureEntityCloseIntentSchema(c.entity, body)
		rec := c.request(t, "CLOSE", key, "", body)
		resp := decodeFormEventResponse(t, rec.Body.Bytes())
		if !resp.OK || resp.Close == nil || !resp.Close.Allowed {
			t.Fatalf("close failed: %d %+v", rec.Code, resp)
		}
		hidden := c.event(t, key, "", nil)
		if len(hidden.Revealed) != 0 {
			t.Fatal("closed context survived")
		}
		c.token = ""
		c = c.open(t, key)
		grant()
		original := c.user
		c.user = &auth.User{ID: original.ID, Login: original.Login} // no permissions
		_ = c.request(t, "EVENT", key, "", nil)
		c.user = original
		hidden = c.event(t, key, "", nil)
		if len(hidden.Revealed) != 0 {
			t.Fatal("permission loss resurrected grant")
		}
		c.token = ""
		c = c.open(t, key)
		grant()
		c.session = "" // logout
		hidden = c.event(t, key, "", nil)
		if len(hidden.Revealed) != 0 {
			t.Fatal("logout retained grant")
		}
	})
}

func TestConcealConcurrentRevokeAndSubmit(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		c, id := concealFixture(t, db)
		key := id.String()
		c = c.open(t, key)
		for i := 0; i < 12; i++ {
			if r := c.event(t, key, "Показать", url.Values{"Причина": {"проверена"}}); len(r.Revealed) != 1 {
				t.Fatal("grant failed")
			}
			start := make(chan struct{})
			results := make(chan *httptest.ResponseRecorder, 3)
			go func() { <-start; results <- c.request(t, "EVENT", key, "Отозвать", nil) }()
			go func() {
				<-start
				results <- c.request(t, "EVENT", key, "Показать", url.Values{"Маской": {"false"}})
			}()
			go func() {
				<-start
				results <- c.request(t, "POST", key, "", url.Values{"Наименование": {"обычное"}, "Секрет": {"разрешённый-в-гонке"}})
			}()
			close(start)
			for j := 0; j < 3; j++ {
				r := <-results
				if r.Code != 200 && r.Code != 303 {
					t.Fatalf("concurrent request: %d %s", r.Code, r.Body.String())
				}
			}
			// Regardless of order, a no-reason show cannot reauthorize after revoke.
			if r := c.event(t, key, "", nil); len(r.Revealed) != 0 {
				t.Fatal("revoke lost")
			}
			before, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
			if err != nil {
				t.Fatal(err)
			}
			_ = c.request(t, "POST", key, "", url.Values{"Наименование": {"обычное"}, "Секрет": {"после-отзыва"}, "Маской": {"false"}})
			after, err := db.GetByID(context.Background(), c.entity.Name, id, c.entity)
			if err != nil {
				t.Fatal(err)
			}
			if after["Секрет"] != before["Секрет"] {
				t.Fatal("post-revoke input persisted")
			}
		}
	})
}

func TestConcealBoundedAdmission(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		c, id := concealFixture(t, db)
		c = c.open(t, id.String())
		s := c.server
		e := s.concealInstances[c.token]
		// Fill limits without allocating 2048 complete HTML renderings. Assertions
		// use GET admission, not a private admission helper.
		for i := 1; i < concealSessionLimit; i++ {
			clone := *e
			s.concealInstances[uuid.NewString()] = &clone
		}
		noToken := c
		noToken.token = ""
		r := noToken.request(t, "GET", id.String(), "", nil)
		if strings.Contains(r.Body.String(), "data-ob-conceal-context=") {
			t.Fatal("session cap bypassed")
		}
		for len(s.concealInstances) < concealServerLimit {
			clone := *e
			clone.owner = sha256.Sum256([]byte(uuid.NewString()))
			s.concealInstances[uuid.NewString()] = &clone
		}
		noToken.session = uuid.NewString()
		r = noToken.request(t, "GET", id.String(), "", nil)
		if strings.Contains(r.Body.String(), "data-ob-conceal-context=") {
			t.Fatal("global cap bypassed")
		}
		s.concealClock = func() time.Time { return e.expires.Add(time.Second) }
		noToken = noToken.open(t, id.String())
		if len(s.concealInstances) != 1 {
			t.Fatalf("expired entries not cleaned: %d", len(s.concealInstances))
		}
	})
}
