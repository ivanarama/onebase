package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

func TestTablePartSearchListAndPickerHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := &metadata.Entity{Name: "SearchClients", Kind: metadata.KindCatalog, SearchSet: true, Search: []string{"Наименование", "Контакты.Телефон"}, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}, {Name: "Аудитория", Type: metadata.FieldTypeString}}, TableParts: []metadata.TablePart{{Name: "Контакты", Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString}}}}}
		form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", EntityName: "SearchOrder", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{Name: "ПолеКлиент", Kind: metadata.FormElementField, DataPath: "Объект.Клиент", ChoiceContext: map[string]string{"Контекст": "Объект.Аудитория"}}}}
		owner := &metadata.Entity{Name: "SearchOrder", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Аудитория", Type: metadata.FieldTypeString}, {Name: "Клиент", Type: metadata.FieldType("reference:SearchClients"), RefEntity: ent.Name}}, Forms: []*metadata.FormModule{form}}
		if err := db.Migrate(ctx, []*metadata.Entity{ent, owner}); err != nil {
			t.Fatal(err)
		}
		ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		for i, id := range ids {
			if err := db.Upsert(ctx, ent.Name, id, map[string]any{"Наименование": fmt.Sprintf("Client %d", i), "Аудитория": []string{"anna", "anna", "bob"}[i]}, ent); err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertTablePartRows(ctx, ent.Name, "Контакты", id, []map[string]any{{"Телефон": "555-01"}, {"Телефон": "555-02"}}, ent.TableParts[0]); err != nil {
				t.Fatal(err)
			}
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent, owner}})
		s := &Server{reg: reg, store: db, messages: NewMessageStore()}
		router := chi.NewRouter()
		s.Mount(router)
		user := &auth.User{Login: "anna", Roles: []*auth.Role{
			{Permissions: auth.Permission{
				Catalogs: map[string][]string{ent.Name: {"read"}, owner.Name: {"read", "write"}},
				RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
					ent.Name: {"read": {Field: "Аудитория", Op: "eq", Value: auth.RowValue{User: "login"}}},
				}},
			}},
		}}
		serve := func(method, target string, body []byte, u *auth.User) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, target, bytes.NewReader(body))
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			if u != nil {
				req = req.WithContext(auth.ContextWithUser(req.Context(), u))
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec
		}
		getList := func(query string, u *auth.User) *html.Node {
			t.Helper()
			rec := serve(http.MethodGet, "/ui/catalog/searchclients?lang=ru&lm=pages&sort=Наименование&"+query, nil, u)
			if rec.Code != http.StatusOK {
				t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
			}
			doc, err := html.Parse(strings.NewReader(rec.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			return doc
		}
		for _, q := range []string{"555", "Client"} {
			doc := getList("q="+url.QueryEscape(q)+"&limit=10", user)
			text := liveListText(doc)
			if !strings.Contains(text, "Всего: 2") {
				t.Fatalf("summary: %s", text)
			}
			for _, id := range ids[:2] {
				if liveListNode(doc, "data-ob-entity-id", id.String()) == nil {
					t.Fatalf("missing owner %s", id)
				}
			}
			if liveListNode(doc, "data-ob-entity-id", ids[2].String()) != nil {
				t.Fatal("RLS-excluded owner leaked in list")
			}
		}
		checkPicker := func(rec *httptest.ResponseRecorder, want int) {
			t.Helper()
			if rec.Code != http.StatusOK {
				t.Fatalf("picker: %d %s", rec.Code, rec.Body.String())
			}
			var got struct {
				Items []map[string]any `json:"items"`
				Total int              `json:"total"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Total != want || len(got.Items) != 1 {
				t.Fatalf("picker pagination/count: %+v", got)
			}
			for _, row := range got.Items {
				if fmt.Sprint(row["id"]) == ids[2].String() {
					t.Fatal("RLS-excluded owner leaked in picker")
				}
				if !strings.HasPrefix(fmt.Sprint(row["_label"]), "Client ") {
					t.Fatalf("label replaced by table-part value: %v", row)
				}
			}
		}
		for _, offset := range []int{0, 1} {
			checkPicker(serve(http.MethodGet, fmt.Sprintf("/ui/_ref-options/SearchClients?q=555&limit=1&offset=%d", offset), nil, user), 2)
			body, _ := json.Marshal(map[string]any{"q": "555", "limit": 1, "offset": offset, "source": map[string]string{"entity": owner.Name, "form": form.Name, "element": "ПолеКлиент"}})
			checkPicker(serve(http.MethodPost, "/ui/_ref-options/SearchClients/page", body, user), 2)
		}
		denied := &auth.User{Roles: []*auth.Role{{Permissions: auth.Permission{Catalogs: map[string][]string{"Other": {"read"}}}}}}
		for _, path := range []string{"/ui/catalog/searchclients?q=555", "/ui/_ref-options/SearchClients?q=555"} {
			if rec := serve(http.MethodGet, path, nil, denied); rec.Code != http.StatusForbidden {
				t.Fatalf("missing object-read gate: %d %s", rec.Code, rec.Body.String())
			}
		}
		ent.SearchSet = false
		if doc := getList("q=555", user); liveListNode(doc, "data-ob-entity-id", ids[0].String()) != nil {
			t.Fatal("table parts included by default")
		}
		ent.SearchSet = true
		ent.Search = nil
		if doc := getList("q=Client", user); liveListNode(doc, "data-ob-entity-id", ids[0].String()) != nil {
			t.Fatal("explicit empty search is not disabled")
		}
	})
}
