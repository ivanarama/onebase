package ui

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func entityListFixture(t *testing.T) choiceHTTPFixture {
	f := newChoiceHTTPFixture(t)
	f.owner.Forms[0].Elements = append(f.owner.Forms[0].Elements, &metadata.FormElement{ID: "history", Name: "History", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: f.target.Name, Columns: []string{"Наименование"}, OrderBy: []string{"Наименование asc"}, Limit: 2, OnRowClick: "открыть", Filter: []metadata.FormEntityListCondition{{Field: "Направление", Op: "eq", Source: "Объект.Направление"}}}})
	return f
}
func listHTTP(t *testing.T, f choiceHTTPFixture, id, source string, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{"form": {"ФормаОбъекта"}, "element": {"history"}, "id": {id}, "sources": {`{"Объект.Направление":"` + source + `"}`}}
	for k, v := range extra {
		values[k] = v
	}
	router := chi.NewRouter()
	f.server.Mount(router)
	req := httptest.NewRequest(http.MethodPost, "/ui/document/"+url.PathEscape(f.owner.Name)+"/form-list-refresh", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}
func decodeList(t *testing.T, r *httptest.ResponseRecorder) entityListData {
	t.Helper()
	if r.Code != 200 {
		t.Fatalf("status=%d %s", r.Code, r.Body.String())
	}
	var body struct {
		OK    bool                      `json:"ok"`
		Lists map[string]entityListData `json:"entityLists"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("body=%s err=%v", r.Body.String(), err)
	}
	return body.Lists["history"]
}
func TestManagedEntityListPublicReadRefresh(t *testing.T) {
	f := entityListFixture(t)
	got := decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
	if got.Status != "ready" || len(got.Rows) != 2 {
		t.Fatalf("list=%+v", got)
	}
	for _, row := range got.Rows {
		if strings.Contains(strings.Join(row.Cells, " "), "hidden") || row.URL == "" {
			t.Fatalf("RLS/projection=%+v", got)
		}
	}
	before, err := f.server.store.GetByID(context.Background(), f.owner.Name, f.ownerID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	got = decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootB.String(), nil))
	if len(got.Rows) != 2 || !strings.Contains(got.Rows[0].Cells[0], " B") {
		t.Fatalf("changed source=%+v", got)
	}
	after, err := f.server.store.GetByID(context.Background(), f.owner.Name, f.ownerID, f.owner)
	if err != nil || before["Направление"] != after["Направление"] || before["_version"] != after["_version"] {
		t.Fatalf("refresh wrote owner: %v %v", before, after)
	}
	got = decodeList(t, listHTTP(t, f, "", "", nil))
	if got.Status != "empty" || len(got.Rows) != 0 {
		t.Fatalf("empty=%+v", got)
	}
	if r := listHTTP(t, f, f.ownerID.String(), f.rootA.String(), url.Values{"sources": {`{"unexpected":"value"}`}}); r.Code == 200 {
		t.Fatal("unexpected sources accepted")
	}
	if r := listHTTP(t, f, f.ownerID.String(), f.rootA.String(), url.Values{"element": {"fault-picker"}}); r.Code == 200 {
		t.Fatal("foreign element accepted")
	}
	// Readonly sources cannot be replaced by browser values, even with a valid owner.
	f.owner.Forms[0].Elements[0].ReadOnly = true
	got = decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootB.String(), nil))
	if len(got.Rows) != 2 || strings.Contains(got.Rows[0].Cells[0], " B") {
		t.Fatalf("forged readonly=%+v", got)
	}
	if r := listHTTP(t, f, "", f.rootA.String(), nil); r.Code == 200 {
		t.Fatal("unsigned readonly new owner accepted")
	}
}
func TestManagedEntityListPublicOwnerAndTargetAccess(t *testing.T) {
	f := entityListFixture(t)
	f.user.Login = "bob"
	got := decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
	if len(got.Rows) != 1 || got.Rows[0].Cells[0] != "needle hidden" {
		t.Fatalf("second user RLS=%+v", got)
	}
	delete(f.user.Roles[0].Permissions.Catalogs, f.target.Name)
	got = decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
	if got.Status != "denied" || len(got.Rows) != 0 {
		t.Fatalf("target denied=%+v", got)
	}
	delete(f.user.Roles[0].Permissions.Documents, f.owner.Name)
	if r := listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil); r.Code != 403 {
		t.Fatalf("owner denied %d %s", r.Code, r.Body.String())
	}
}
func TestManagedEntityListPublicInitialHTML(t *testing.T) {
	f := entityListFixture(t)
	router := chi.NewRouter()
	f.server.Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/"+f.ownerID.String(), nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `data-ob-list-id="history"`) || !strings.Contains(body, "needle") || strings.Contains(body, "needle hidden") {
		t.Fatalf("initial HTML status=%d %s", rec.Code, body)
	}
}

func TestManagedEntityListPublicMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		f := entityListFixture(t)
		ctx := context.Background()
		entities := []*metadata.Entity{f.direction, f.target, f.owner}
		if err := db.Migrate(ctx, entities); err != nil {
			t.Fatal(err)
		}
		for _, entity := range entities {
			rows, err := f.server.store.List(ctx, entity.Name, entity, storage.ListParams{Sort: "id", RowFilterEvaluated: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				_, id, ok := uuidFromValue(row["id"])
				if !ok {
					t.Fatal("invalid fixture id")
				}
				if err := db.Upsert(ctx, entity.Name, id, row, entity); err != nil {
					t.Fatal(err)
				}
			}
		}
		f.server.store = db
		f.server.entitySvc = f.server.newEntityService(nil)
		list := decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
		if len(list.Rows) != 2 {
			t.Fatalf("matrix result=%+v", list)
		}
		f.target.Fields[0].PII = true
		// Filtering/sorting by a protected target field cannot become an oracle.
		list = decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
		if list.Status != "denied" || len(list.Rows) != 0 {
			t.Fatalf("masked order leaked=%+v", list)
		}
		f.owner.Forms[0].Elements[2].EntityList.OrderBy = []string{"Аудитория"}
		list = decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
		if list.Status != "ready" {
			t.Fatalf("masked display=%+v", list)
		}
		for _, r := range list.Rows {
			if strings.Contains(strings.Join(r.Cells, " "), "element") || strings.Contains(strings.Join(r.Cells, " "), "needle") {
				t.Fatalf("raw masked cell=%+v", list)
			}
		}
		f.owner.Fields[0].PII = true
		list = decodeList(t, listHTTP(t, f, f.ownerID.String(), f.rootA.String(), nil))
		if list.Status != "denied" || len(list.Rows) != 0 {
			t.Fatalf("protected source=%+v", list)
		}
	})
}

func TestManagedEntityListWriteSurvivesListReadError(t *testing.T) {
	srv, owner := setupManagedEventsServer(t, `Процедура СохранитьИсторию()
 Объект.Наименование = "written";
 Объект.Записать();
КонецПроцедуры`, nil, []*metadata.FormElement{
		{ID: "name", Name: "Name", Kind: metadata.FormElementField, DataPath: "Объект.Наименование"},
		{ID: "write", Name: "Write", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "СохранитьИсторию"}},
	})
	target := &metadata.Entity{Name: "HistoryUnavailable", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Text", Type: metadata.FieldTypeString}}}
	srv.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{owner, target}})
	if err := srv.store.Migrate(context.Background(), []*metadata.Entity{target}); err != nil {
		t.Fatal(err)
	}
	form := owner.Forms[0]
	form.Elements = append(form.Elements, &metadata.FormElement{ID: "history", Name: "History", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: target.Name, Columns: []string{"Text"}, Filter: []metadata.FormEntityListCondition{{Field: "Text", Op: "eq", Source: "Объект.Наименование"}}}})
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), owner.Name, id, map[string]any{"Наименование": "before"}, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Exec(context.Background(), "DROP TABLE "+metadata.TableName(target.Name)); err != nil {
		t.Fatal(err)
	}
	rec := executeFormEvent(t, srv, owner, url.Values{"_element": {"Write"}, "_event": {"Нажатие"}, "_kind": {"object"}, "_id": {id.String()}, "Наименование": {"before"}})
	response := decodeFormEventResponse(t, rec.Body.Bytes())
	if !response.OK || response.Error != "" || response.EntityLists["history"].Status != "error" {
		t.Fatalf("write/list state=%s", rec.Body.String())
	}
	row, err := srv.store.GetByID(context.Background(), owner.Name, id, owner)
	if err != nil || row["Наименование"] != "written" {
		t.Fatalf("write did not commit: %v %v", row, err)
	}
}
func TestManagedEntityListReadonlyRestorationDoesNotEchoSecret(t *testing.T) {
	srv, owner := setupManagedEventsServer(t, `Процедура ПроверитьИсторию()
 Сообщить("ok");
КонецПроцедуры`, nil, []*metadata.FormElement{
		{ID: "name", Name: "Name", Kind: metadata.FormElementField, ReadOnly: true, DataPath: "Объект.Наименование"},
		{ID: "check", Name: "Check", Kind: metadata.FormElementButton, Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ПроверитьИсторию"}},
	})
	owner.Fields[0].PII = true
	owner.Forms[0].Elements = append(owner.Forms[0].Elements, &metadata.FormElement{ID: "history", Name: "History", Kind: metadata.FormElementEntityList, EntityList: &metadata.FormEntityListSpec{Entity: owner.Name, Columns: []string{"Наименование"}, Filter: []metadata.FormEntityListCondition{{Field: "Наименование", Op: "eq", Source: "Объект.Наименование"}}}})
	id := uuid.New()
	if err := srv.store.Upsert(context.Background(), owner.Name, id, map[string]any{"Наименование": "DB secret"}, owner); err != nil {
		t.Fatal(err)
	}
	user := &auth.User{Login: "reader", Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{owner.Name: {"read", "write"}}}}}}
	body := url.Values{"_element": {"Check"}, "_event": {"Нажатие"}, "_kind": {"object"}, "_id": {id.String()}, "Наименование": {"client input"}}
	router := chi.NewRouter()
	srv.Mount(router)
	req := httptest.NewRequest(http.MethodPost, "/ui/catalog/"+url.PathEscape(owner.Name)+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(auth.ContextWithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "DB secret") {
		t.Fatalf("restored secret leaked: %d %s", rec.Code, rec.Body.String())
	}
	response := decodeFormEventResponse(t, rec.Body.Bytes())
	if !response.OK || response.EntityLists["history"].Status != "denied" {
		t.Fatalf("response=%s", rec.Body.String())
	}
}

func TestManagedEntityListReadonlyNewStateBoundToUser(t *testing.T) {
	f := entityListFixture(t)
	f.owner.Forms[0].Elements[0].ReadOnly = true
	router := chi.NewRouter()
	f.server.Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.owner.Name)+"/new", nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), f.user))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	html := w.Body.String()
	start := strings.Index(html, `id="ob-entity-list-config">`)
	if start < 0 {
		t.Fatalf("config missing: %s", html)
	}
	start += len(`id="ob-entity-list-config">`)
	end := strings.Index(html[start:], "</script>")
	if end < 0 {
		t.Fatal("unterminated config")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(html[start:start+end]), &config); err != nil {
		t.Fatal(err)
	}
	token, _ := config["state"].(string)
	if token == "" {
		t.Fatal("readonly new state missing")
	}
	got := decodeList(t, listHTTP(t, f, "", f.rootB.String(), url.Values{"state": {token}}))
	if got.Status != "empty" || len(got.Rows) != 0 {
		t.Fatalf("forged readonly new source=%+v", got)
	}
	f.user.Login = "other"
	if r := listHTTP(t, f, "", f.rootA.String(), url.Values{"state": {token}}); r.Code == 200 {
		t.Fatal("another user reused signed state")
	}
	f.user.Login = "anna"
	if r := listHTTP(t, f, "", f.rootA.String(), url.Values{"state": {token + "x"}}); r.Code == 200 {
		t.Fatal("tampered state accepted")
	}
}
