package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Глобальный поиск в UI (план 82). Ключевое требование — выдача ограничена
// правами: объектный RBAC, строковые политики (план 79) и маскирование
// реквизитов (план 88).

func searchTestEntities() (*metadata.Entity, *metadata.Entity) {
	cat := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Менеджер", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
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
	return cat, doc
}

func newSearchTestServer(t *testing.T) (*Server, *metadata.Entity, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cat, doc := searchTestEntities()
	entities := []*metadata.Entity{cat, doc}
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: entities})
	return &Server{reg: reg, store: db, cfg: Config{AppName: "TestApp"}}, cat, doc
}

// escapedPath повторяет кодирование, которое html/template применяет к пути
// ссылки: percent-кодирование в нижнем регистре.
func escapedPath(name string) string {
	return strings.ToLower(url.PathEscape(strings.ToLower(name)))
}

func serveSearch(t *testing.T, s *Server, query string, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/search?q="+url.QueryEscape(query), nil)
	ctx := req.Context()
	if user != nil {
		ctx = auth.ContextWithUser(ctx, user)
	}
	rec := httptest.NewRecorder()
	s.globalSearch(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func TestGlobalSearch_FindsAcrossCatalogsAndDocuments(t *testing.T) {
	ctx := context.Background()
	s, cat, doc := newSearchTestServer(t)

	id := uuid.New()
	if err := s.store.Upsert(ctx, cat.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	docID := uuid.New()
	if err := s.store.Upsert(ctx, doc.Name, docID, map[string]any{
		"Номер": "РН-000012", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	body := serveSearch(t, s, "ромашк", nil).Body.String()
	if !strings.Contains(body, "ООО Ромашка") {
		t.Fatalf("справочник не попал в выдачу:\n%s", body)
	}
	if !strings.Contains(body, "РН-000012") {
		t.Fatalf("документ не попал в выдачу:\n%s", body)
	}
	// Ссылка ведёт прямо в карточку объекта — ради этого сценарий и затевался.
	// Кириллица в пути percent-кодируется шаблонизатором, как и в списках.
	catURL := "/ui/catalog/" + escapedPath(cat.Name) + "/" + id.String()
	docURL := "/ui/document/" + escapedPath(doc.Name) + "/" + docID.String()
	if !strings.Contains(body, catURL) {
		t.Fatalf("нет ссылки %s на карточку справочника:\n%s", catURL, body)
	}
	if !strings.Contains(body, docURL) {
		t.Fatalf("нет ссылки %s на карточку документа:\n%s", docURL, body)
	}
}

func TestGlobalSearch_RespectsObjectRBAC(t *testing.T) {
	ctx := context.Background()
	s, cat, doc := newSearchTestServer(t)

	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{"Наименование": "ООО Ромашка"}, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, doc.Name, uuid.New(), map[string]any{
		"Номер": "РН-000012", "Основание": "договор с Ромашкой",
	}, doc); err != nil {
		t.Fatal(err)
	}

	user := &auth.User{Roles: []*auth.Role{{
		Permissions: auth.Permission{Catalogs: map[string][]string{cat.Name: {"read"}}},
	}}}
	body := serveSearch(t, s, "ромашк", user).Body.String()
	if !strings.Contains(body, "ООО Ромашка") {
		t.Fatalf("разрешённый объект пропал из выдачи:\n%s", body)
	}
	if strings.Contains(body, "РН-000012") {
		t.Fatalf("объект без права read попал в выдачу:\n%s", body)
	}
}

func TestGlobalSearch_RespectsRowPolicy(t *testing.T) {
	ctx := context.Background()
	s, cat, _ := newSearchTestServer(t)

	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
		"Наименование": "ООО Ромашка", "Менеджер": "ivanov",
	}, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
		"Наименование": "ЗАО Ромашка-2", "Менеджер": "petrov",
	}, cat); err != nil {
		t.Fatal(err)
	}

	// Политика «вижу только своих контрагентов» (план 79).
	user := &auth.User{
		Login: "ivanov",
		Roles: []*auth.Role{{
			Permissions: auth.Permission{
				Catalogs: map[string][]string{cat.Name: {"read"}},
				RowAccess: auth.RowAccess{Catalogs: map[string]auth.RowPolicies{
					cat.Name: {"read": auth.RowPolicy{
						Field: "Менеджер",
						Op:    "eq",
						Value: auth.RowValue{User: "login"},
					}},
				}},
			},
		}},
	}
	body := serveSearch(t, s, "ромашк", user).Body.String()
	if !strings.Contains(body, "ООО Ромашка") {
		t.Fatalf("своя строка пропала из выдачи:\n%s", body)
	}
	if strings.Contains(body, "Ромашка-2") {
		t.Fatalf("чужая строка попала в выдачу вопреки политике:\n%s", body)
	}
}

// Совпадение по замаскированному реквизиту не должно подтверждаться: иначе
// поиск превращается в оракул для подбора скрытых значений.
func TestGlobalSearch_DoesNotConfirmMaskedFieldMatch(t *testing.T) {
	ctx := context.Background()
	s, cat, _ := newSearchTestServer(t)

	if err := s.store.Upsert(ctx, cat.Name, uuid.New(), map[string]any{
		"Наименование": "ООО Ромашка", "Телефон": "79990001122",
	}, cat); err != nil {
		t.Fatal(err)
	}

	user := &auth.User{
		Login: "ivanov",
		Roles: []*auth.Role{{
			Permissions: auth.Permission{
				Catalogs: map[string][]string{cat.Name: {"read"}},
				FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{
					cat.Name: {"Телефон": auth.FieldPolicy{Read: "hide"}},
				}},
			},
		}},
	}
	if body := serveSearch(t, s, "79990001122", user).Body.String(); strings.Contains(body, "ООО Ромашка") {
		t.Fatalf("поиск подтвердил совпадение по скрытому реквизиту:\n%s", body)
	}
	// По видимому тексту объект по-прежнему находится.
	if body := serveSearch(t, s, "ромашка", user).Body.String(); !strings.Contains(body, "ООО Ромашка") {
		t.Fatalf("маскирование не должно убирать объект из поиска целиком:\n%s", body)
	}
}

func TestGlobalSearch_EmptyQueryRendersForm(t *testing.T) {
	s, _, _ := newSearchTestServer(t)
	body := serveSearch(t, s, "", nil).Body.String()
	if !strings.Contains(body, `action="/ui/search"`) {
		t.Fatalf("страница поиска должна показывать форму:\n%s", body)
	}
	if strings.Contains(body, "Ничего не найдено") {
		t.Fatalf("пустой запрос не должен считаться неудачным поиском:\n%s", body)
	}
}

// Строка поиска доступна с любой страницы — иначе сценарий «ищу везде» надо
// сначала найти в меню.
func TestNav_HasGlobalSearchBox(t *testing.T) {
	html := renderPage(t, "page-index")
	if !strings.Contains(html, `class="topbar-search"`) {
		t.Fatalf("в шапке нет строки глобального поиска")
	}
}
