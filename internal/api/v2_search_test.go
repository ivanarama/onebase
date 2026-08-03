package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// REST-эндпоинт глобального поиска (план 82). Права те же, что в списках:
// объектный RBAC и строковые политики.

func searchTestHandler(t *testing.T) (*handler, *metadata.Entity, *metadata.Entity) {
	t.Helper()
	cat := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Менеджер", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "РасходнаяНакладная",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Основание", Type: metadata.FieldTypeString},
		},
	}
	h, _ := newAPITestHandler(t, []*metadata.Entity{cat, doc}, nil)
	return h, cat, doc
}

type searchResponse struct {
	Data []restSearchHit `json:"data"`
	Meta struct {
		Limit      int  `json:"limit"`
		NextOffset int  `json:"next_offset"`
		HasMore    bool `json:"has_more"`
	} `json:"meta"`
}

func doSearch(t *testing.T, h *handler, query string, user *auth.User) (*httptest.ResponseRecorder, searchResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v2/search?"+query, nil)
	if user != nil {
		r = withUser(r, user)
	}
	w := httptest.NewRecorder()
	h.searchV2().ServeHTTP(w, r)
	var resp searchResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json: %v; тело %s", err, w.Body.String())
		}
	}
	return w, resp
}

func seedSearchData(t *testing.T, h *handler, cat, doc *metadata.Entity) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if err := h.store.Upsert(ctx, cat.Name, id, map[string]any{
		"Наименование": "ООО Ромашка", "Менеджер": "ivanov",
	}, cat); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
		"Номер": "РН-000012", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAPI_V2Search_FindsAcrossEntities(t *testing.T) {
	h, cat, doc := searchTestHandler(t)
	catID := seedSearchData(t, h, cat, doc)

	user := apiUser("reader", auth.Permission{
		Catalogs:  map[string][]string{cat.Name: {"read"}},
		Documents: map[string][]string{doc.Name: {"read"}},
	})
	w, resp := doSearch(t, h, "q="+url.QueryEscape("ромашк"), user)
	if w.Code != http.StatusOK {
		t.Fatalf("код %d, тело %s", w.Code, w.Body.String())
	}
	if len(resp.Data) != 2 {
		t.Fatalf("ожидались справочник и документ, получено %+v", resp.Data)
	}
	if resp.Data[0].ID != catID.String() || resp.Data[0].Title != "ООО Ромашка" {
		t.Fatalf("первым ожидалось совпадение в наименовании: %+v", resp.Data)
	}
	if resp.Data[0].Kind != "catalog" || resp.Data[0].Entity != cat.Name {
		t.Fatalf("неверные вид/объект совпадения: %+v", resp.Data[0])
	}
}

func TestAPI_V2Search_RespectsObjectRBAC(t *testing.T) {
	h, cat, doc := searchTestHandler(t)
	seedSearchData(t, h, cat, doc)

	user := apiUser("reader", auth.Permission{Catalogs: map[string][]string{cat.Name: {"read"}}})
	_, resp := doSearch(t, h, "q="+url.QueryEscape("ромашк"), user)
	if len(resp.Data) != 1 || resp.Data[0].Entity != cat.Name {
		t.Fatalf("объект без права read попал в выдачу: %+v", resp.Data)
	}
}

func TestAPI_V2Search_RespectsRowPolicy(t *testing.T) {
	h, cat, doc := searchTestHandler(t)
	seedSearchData(t, h, cat, doc)
	if err := h.store.Upsert(context.Background(), cat.Name, uuid.New(), map[string]any{
		"Наименование": "ЗАО Ромашка-2", "Менеджер": "petrov",
	}, cat); err != nil {
		t.Fatal(err)
	}

	user := apiUser("ivanov", auth.Permission{
		Catalogs: map[string][]string{cat.Name: {"read"}},
		RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
			cat.Name: {"read": auth.RowPolicy{
				Field: "Менеджер", Op: "eq", Value: auth.RowValue{User: "login"},
			}},
		}},
	})
	_, resp := doSearch(t, h, "q="+url.QueryEscape("ромашк"), user)
	if len(resp.Data) != 1 || resp.Data[0].Title != "ООО Ромашка" {
		t.Fatalf("строковая политика не применилась: %+v", resp.Data)
	}
}

func TestAPI_V2Search_RejectsBadParams(t *testing.T) {
	h, cat, doc := searchTestHandler(t)
	seedSearchData(t, h, cat, doc)
	user := apiUser("reader", auth.Permission{Catalogs: map[string][]string{cat.Name: {"read"}}})

	for _, query := range []string{"", "q=", "q=ромашка&limit=0", "q=ромашка&limit=abc", "q=ромашка&offset=-1"} {
		if w, _ := doSearch(t, h, query, user); w.Code != http.StatusBadRequest {
			t.Fatalf("запрос %q: код %d, ожидался 400", query, w.Code)
		}
	}
}

func TestAPI_V2Search_PaginatesByNextOffset(t *testing.T) {
	h, cat, doc := searchTestHandler(t)
	seedSearchData(t, h, cat, doc)

	user := apiUser("reader", auth.Permission{
		Catalogs:  map[string][]string{cat.Name: {"read"}},
		Documents: map[string][]string{doc.Name: {"read"}},
	})
	_, first := doSearch(t, h, "q="+url.QueryEscape("ромашк")+"&limit=1", user)
	if len(first.Data) != 1 || !first.Meta.HasMore {
		t.Fatalf("первая страница: %+v, meta %+v", first.Data, first.Meta)
	}
	_, second := doSearch(t, h,
		"q="+url.QueryEscape("ромашк")+"&limit=1&offset="+strconv.Itoa(first.Meta.NextOffset), user)
	if len(second.Data) != 1 {
		t.Fatalf("вторая страница: %+v", second.Data)
	}
	if second.Data[0].ID == first.Data[0].ID {
		t.Fatalf("вторая страница повторила первую: %+v", second.Data)
	}
}
