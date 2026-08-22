package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

const serviceHandlersSrc = `
Функция Корень(Запрос) Экспорт
    Возврат ОтветТекст(200, "корень");
КонецФункции

Функция Получить(Запрос) Экспорт
    стр = Новый Структура;
    стр.Вставить("id", Запрос.ПараметрыURL.Получить("id"));
    стр.Вставить("q", Запрос.ПараметрЗапроса("q"));
    Возврат ОтветJSON(200, стр);
КонецФункции

Функция Эхо(Запрос) Экспорт
    Возврат Запрос.ТелоJSON();
КонецФункции

Функция Кто(Запрос) Экспорт
    Возврат ОтветТекст(200, ИмяПользователя());
КонецФункции

Функция Хост(Запрос) Экспорт
    Возврат ОтветТекст(200, Запрос.Заголовки.Получить("Host"));
КонецФункции
`

func newServiceTestServer(t *testing.T) (*Server, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}

	l := lexer.New(serviceHandlersSrc, "echo.service.os")
	prog, err := parser.New(l).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	// Один и тот же модуль зарегистрирован под именами всех сервисов —
	// GetServiceProcedure(serviceName, handlerName) находит обработчик по имени сервиса.
	registry.Load(runtime.LoadOptions{ServicePrograms: map[string]*ast.Program{"Echo": prog, "Secure": prog, "RG": prog}})

	echo := &httpservice.Service{Name: "Echo", RootURL: "echo", Auth: "none",
		CORS: &httpservice.CORSConfig{Origins: []string{"*"}, Headers: []string{"Content-Type"}, MaxAge: 600},
		Templates: []httpservice.URLTemplate{
			{Template: "/", Methods: map[string]string{"GET": "Корень", "POST": "Эхо"}},
			{Template: "/{id}", Methods: map[string]string{"GET": "Получить"}},
			{Template: "/meta/host", Methods: map[string]string{"GET": "Хост"}},
		}}
	secure := &httpservice.Service{Name: "Secure", RootURL: "secure", Auth: "basic", Templates: []httpservice.URLTemplate{
		{Template: "/", Methods: map[string]string{"GET": "Кто"}},
	}}
	// Сервис с ролевым ограничением: basic + roles.
	rg := &httpservice.Service{Name: "RG", RootURL: "rg", Auth: "basic", Roles: []string{"Менеджер"}, Templates: []httpservice.URLTemplate{
		{Template: "/", Methods: map[string]string{"GET": "Кто"}},
	}}
	echo.Normalize()
	secure.Normalize()
	rg.Normalize()
	registry.LoadHTTPServices([]*httpservice.Service{echo, secure, rg})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// Предохранитель сети (план 62) включаем — эти тесты проверяют
	// маршрутизацию/auth/CORS, а не блокировку сети.
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		authRepo:         authRepo,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
		loginLimit:       auth.NewLoginLimiter(5, time.Minute),
	}
	s.entitySvc = s.newEntityService(nil)
	return s, ctx
}

func TestService_GetWithPathAndQuery(t *testing.T) {
	s, _ := newServiceTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hs/echo/42?q=abc", nil)
	s.serviceDispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v (body=%s)", err, w.Body.String())
	}
	if got["id"] != "42" || got["q"] != "abc" {
		t.Errorf("got %v, want id=42 q=abc", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type=%q", ct)
	}
}

func TestService_RootText(t *testing.T) {
	s, _ := newServiceTestServer(t)
	for _, target := range []string{"/hs/echo", "/hs/echo/"} {
		w := httptest.NewRecorder()
		s.serviceDispatch(w, httptest.NewRequest("GET", target, nil))
		if w.Code != http.StatusOK || w.Body.String() != "корень" {
			t.Errorf("%s → status=%d body=%q", target, w.Code, w.Body.String())
		}
	}
}

func TestService_PostBodyEchoAutoJSON(t *testing.T) {
	s, _ := newServiceTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/hs/echo", strings.NewReader(`{"x":5}`))
	s.serviceDispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got["x"] != float64(5) {
		t.Errorf("got %v, want x=5", got)
	}
}

func TestService_MethodNotAllowed(t *testing.T) {
	s, _ := newServiceTestServer(t)
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("DELETE", "/hs/echo/42", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow=%q, want GET", allow)
	}
}

func TestService_UnknownServiceAndResource(t *testing.T) {
	s, _ := newServiceTestServer(t)
	for _, target := range []string{"/hs/nope", "/hs/echo/1/2/3"} {
		w := httptest.NewRecorder()
		s.serviceDispatch(w, httptest.NewRequest("GET", target, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s → status=%d, want 404", target, w.Code)
		}
	}
}

func TestService_Index(t *testing.T) {
	s, _ := newServiceTestServer(t)
	w := httptest.NewRecorder()
	s.serviceIndex(w, httptest.NewRequest("GET", "/hs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var got struct {
		Services []struct {
			RootURL string `json:"root_url"`
		} `json:"services"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 3 {
		t.Errorf("want 3 services, got %d (%s)", len(got.Services), w.Body.String())
	}
}

func TestService_BasicAuth(t *testing.T) {
	s, ctx := newServiceTestServer(t)
	if _, err := s.authRepo.Create(ctx, "ivan", "password", "Иван", false); err != nil {
		t.Fatal(err)
	}

	// Без учётных данных — 401 + WWW-Authenticate.
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/secure", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no creds: status=%d, want 401", w.Code)
	}
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("missing WWW-Authenticate: %q", w.Header().Get("WWW-Authenticate"))
	}

	// С верными учётными данными — 200, тело = логин (ТекущийПользователь виден).
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hs/secure", nil)
	r.SetBasicAuth("ivan", "password")
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("good creds: status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "ivan" {
		t.Errorf("body=%q, want ivan", w.Body.String())
	}

	// Неверный пароль — 401.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/hs/secure", nil)
	r.SetBasicAuth("ivan", "wrong")
	s.serviceDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong pass: status=%d, want 401", w.Code)
	}
}

func TestService_CORS_Preflight(t *testing.T) {
	s, _ := newServiceTestServer(t)

	// Preflight OPTIONS → 204 + заголовки CORS.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("OPTIONS", "/hs/echo/42", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	s.serviceDispatch(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin=%q, want *", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("Allow-Methods=%q, want GET", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("Max-Age=%q, want 600", got)
	}

	// Реальный GET тоже несёт Allow-Origin.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/hs/echo/42", nil)
	r.Header.Set("Origin", "https://app.example.com")
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("actual status=%d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("actual Allow-Origin=%q, want *", got)
	}
}

func TestService_RolesGate(t *testing.T) {
	s, ctx := newServiceTestServer(t)
	if _, err := s.authRepo.Create(ctx, "clerk", "password", "Клерк", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.authRepo.Create(ctx, "boss", "password", "Босс", true); err != nil { // админ
		t.Fatal(err)
	}

	// Пользователь без нужной роли — 403.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hs/rg", nil)
	r.SetBasicAuth("clerk", "password")
	s.serviceDispatch(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("clerk: status=%d, want 403", w.Code)
	}

	// Администратор проходит ролевой гейт.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/hs/rg", nil)
	r.SetBasicAuth("boss", "password")
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("boss: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
}

func TestService_OpenAPI(t *testing.T) {
	s, _ := newServiceTestServer(t)
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/openapi.json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi=%v, want 3.0.3", doc["openapi"])
	}
	paths, _ := doc["paths"].(map[string]any)
	if _, ok := paths["/echo/{id}"]; !ok {
		t.Errorf("paths missing /echo/{id}: %v keys", keysOf(paths))
	}
	// basic-сервисы должны добавить security scheme basicAuth.
	comps, _ := doc["components"].(map[string]any)
	schemes, _ := comps["securitySchemes"].(map[string]any)
	if _, ok := schemes["basicAuth"]; !ok {
		t.Errorf("securitySchemes missing basicAuth: %v", schemes)
	}
}

func TestService_Docs(t *testing.T) {
	s, ctx := newServiceTestServer(t)

	// Пользователей нет → аутентификация отключена → docs открыты.
	w := httptest.NewRecorder()
	s.serviceDocs(w, httptest.NewRequest("GET", "/hs/docs", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("docs status=%d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "rapi-doc") || !strings.Contains(body, "/hs/openapi.json") {
		t.Errorf("docs page missing rapi-doc/spec-url")
	}

	// Встроенный ассет RapiDoc отдаётся.
	w = httptest.NewRecorder()
	s.serviceDocsAsset(w, httptest.NewRequest("GET", "/hs/docs/rapidoc-min.js", nil))
	if w.Code != http.StatusOK || w.Body.Len() < 1000 {
		t.Fatalf("asset status=%d len=%d", w.Code, w.Body.Len())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("asset Content-Type=%q", ct)
	}

	// Появился пользователь → docs требуют админ-сессии → без сессии редирект на /login.
	if _, err := s.authRepo.Create(ctx, "u1", "password", "U", false); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.serviceDocs(w, httptest.NewRequest("GET", "/hs/docs", nil))
	if w.Code != http.StatusSeeOther {
		t.Errorf("docs without session: status=%d, want 303 redirect", w.Code)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Разбор формы проверяется на уровне объекта запроса: обработчик в тестовом
// сервисе возвращает тело как есть, а нам нужны именно разобранные поля.
func TestServiceRequest_FormDataParsing(t *testing.T) {
	body := []byte("имя=Иван+Петров&контакт=ivan%40example.com&пусто=&website=")
	req := interpreter.NewServiceRequest("POST", "echo", "/hs/echo/", nil, nil, nil, body)
	res := req.CallMethod("формаданные", nil)
	m, ok := res.(*interpreter.Map)
	if !ok {
		t.Fatalf("ожидалось Соответствие, получено %T", res)
	}
	if got := m.Get("имя"); got != "Иван Петров" {
		t.Errorf("имя=%v — плюс должен раскодироваться в пробел", got)
	}
	if got := m.Get("контакт"); got != "ivan@example.com" {
		t.Errorf("контакт=%v", got)
	}
	// Пустое поле присутствует, но пустое: honeypot-проверка в конфигурации
	// опирается именно на это — «поле есть и заполнено» против «поля нет».
	if got := m.Get("website"); got != "" {
		t.Errorf("website=%v, ожидалась пустая строка", got)
	}
}

// Host виден обработчику как обычный заголовок.
//
// В Go он живёт отдельным полем запроса — net/http вынимает его из карты
// заголовков при разборе, — поэтому «Запрос.Заголовки.Получить("Host")»
// возвращал пустую строку. Конфигурация про это не знает и получает молча
// неверный ответ: в examples/cms так не работал выбор сайта по домену, а
// заметить было нечем — кэш при этом варьируется по host правильно, потому
// что движок берёт r.Host напрямую.
func TestServiceRequest_HostHeaderVisible(t *testing.T) {
	s, _ := newServiceTestServer(t)

	r := httptest.NewRequest("GET", "/hs/echo/meta/host", nil)
	r.Host = "site-b.example:8080"
	w := httptest.NewRecorder()
	s.serviceDispatch(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "site-b.example:8080" {
		t.Fatalf("Заголовки.Получить(\"Host\") = %q, ожидался домен запроса", got)
	}
}
