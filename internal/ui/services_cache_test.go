package ui

// Тесты плана 126: кэш ответов HTTP-сервисов. Всё через serviceDispatch —
// тем же путём, каким приходит реальный запрос.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Число исполнений обработчика считается по записям справочника: каждый вызов
// пишет элемент. Ответ из кэша обработчик не запускает, поэтому счётчик стоит.
const cacheHandlersSrc = `
Функция Страница(Запрос) Экспорт
    Э = Справочники.Вызовы.Создать();
    Э.Наименование = "вызов";
    Э.Записать();
    Возврат ОтветТекст(200, "тело страницы");
КонецФункции

Функция Ошибка404(Запрос) Экспорт
    Э = Справочники.Вызовы.Создать();
    Э.Наименование = "вызов";
    Э.Записать();
    Возврат ОтветТекст(404, "не найдено");
КонецФункции

Функция Куки(Запрос) Экспорт
    Э = Справочники.Вызовы.Создать();
    Э.Наименование = "вызов";
    Э.Записать();
    Отв = Новый HTTPСервисОтвет(200);
    Отв.УстановитьЗаголовок("Set-Cookie", "sid=abc");
    Отв.УстановитьТелоИзСтроки("сессия");
    Возврат Отв;
КонецФункции

Функция Большая(Запрос) Экспорт
    Э = Справочники.Вызовы.Создать();
    Э.Наименование = "вызов";
    Э.Записать();
    т = "";
    Для Сч = 1 По 300 Цикл
        т = т + "0123456789";
    КонецЦикла;
    Возврат ОтветТекст(200, т);
КонецФункции
`

type cacheTestServer struct {
	srv   *Server
	cache *serviceCache
	clock time.Time
	db    *storage.DB
	t     *testing.T
}

// calls — сколько раз реально исполнялся обработчик.
func (c *cacheTestServer) calls() int {
	c.t.Helper()
	row := c.db.QueryRow(c.t.Context(), "SELECT COUNT(*) FROM вызовы")
	var n int
	if err := row.Scan(&n); err != nil {
		c.t.Fatalf("подсчёт вызовов: %v", err)
	}
	return n
}

// reset очищает счётчик вызовов между этапами теста.
func (c *cacheTestServer) reset() {
	c.t.Helper()
	if _, err := c.db.Exec(c.t.Context(), "DELETE FROM вызовы"); err != nil {
		c.t.Fatalf("сброс счётчика: %v", err)
	}
}

func newCacheTestServer(t *testing.T) *cacheTestServer {
	t.Helper()
	ctx := t.Context()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	counter := &metadata.Entity{
		Name:   "Вызовы",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{counter}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	prog, err := parser.New(lexer.New(cacheHandlersSrc, "pub.service.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{counter},
		ServicePrograms: map[string]*ast.Program{
			"Pub": prog, "NoVary": prog, "Priv": prog,
		},
	})

	tmpl := []httpservice.URLTemplate{
		{Template: "/page", Methods: map[string]string{"GET": "Страница", "POST": "Страница"}},
		{Template: "/missing", Methods: map[string]string{"GET": "Ошибка404"}},
		{Template: "/cookie", Methods: map[string]string{"GET": "Куки"}},
		{Template: "/big", Methods: map[string]string{"GET": "Большая"}},
	}
	pub := &httpservice.Service{Name: "Pub", RootURL: "pub", Auth: "none", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60, Vary: []string{"query", "host"}, Public: true, MaxBody: 2048}}
	noVary := &httpservice.Service{Name: "NoVary", RootURL: "novary", Auth: "none", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60, Vary: []string{}}}
	priv := &httpservice.Service{Name: "Priv", RootURL: "priv", Auth: "basic", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60}}
	for _, svc := range []*httpservice.Service{pub, noVary, priv} {
		svc.Normalize()
	}
	registry.LoadHTTPServices([]*httpservice.Service{pub, noVary, priv})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	cache := newServiceCache(0)
	cts := &cacheTestServer{cache: cache, clock: time.Now(), db: db, t: t}
	cache.now = func() time.Time { return cts.clock }

	s := &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		authRepo:         authRepo,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
		loginLimit:       auth.NewLoginLimiter(5, time.Minute),
		svcCache:         cache,
	}
	s.entitySvc = s.newEntityService(nil)
	cts.srv = s
	return cts
}

func (c *cacheTestServer) get(t *testing.T, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		// Host в Go живёт отдельным полем запроса, а не в Header: установка
		// заголовка «Host» на маршрутизацию не влияет.
		if strings.EqualFold(headers[i], "Host") {
			r.Host = headers[i+1]
			continue
		}
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	c.srv.serviceDispatch(w, r)
	return w
}

func TestServiceCache_HitSkipsHandler(t *testing.T) {
	c := newCacheTestServer(t)

	first := c.get(t, "/hs/pub/page")
	if first.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	second := c.get(t, "/hs/pub/page")

	if c.calls() != 1 {
		t.Fatalf("обработчик вызван %d раз(а), ожидался 1 — второй ответ должен прийти из кэша", c.calls())
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("тела разошлись: %q и %q", first.Body.String(), second.Body.String())
	}
	if cc := second.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=60") {
		t.Errorf("Cache-Control=%q при public: true", cc)
	}
	if second.Header().Get("ETag") == "" {
		t.Errorf("нет ETag")
	}
}

func TestServiceCache_TTLExpires(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/pub/page")
	c.get(t, "/hs/pub/page")
	if c.calls() != 1 {
		t.Fatalf("до истечения TTL обработчик вызван %d раз(а)", c.calls())
	}
	c.clock = c.clock.Add(61 * time.Second)
	c.get(t, "/hs/pub/page")
	if c.calls() != 2 {
		t.Fatalf("после истечения TTL обработчик вызван %d раз(а), ожидалось 2", c.calls())
	}
}

// Порядок параметров не должен плодить записи с одинаковым содержимым.
func TestServiceCache_VaryQuery(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/pub/page?a=1")
	c.get(t, "/hs/pub/page?a=2")
	if c.calls() != 2 {
		t.Fatalf("разный query должен кэшироваться раздельно, вызовов=%d", c.calls())
	}
	c.get(t, "/hs/pub/page?a=1&b=2")
	c.get(t, "/hs/pub/page?b=2&a=1")
	if c.calls() != 3 {
		t.Fatalf("переставленные параметры дали второй промах, вызовов=%d", c.calls())
	}
}

// Пустой vary — осознанный режим «одна страница для всех».
func TestServiceCache_VaryEmptyIgnoresQuery(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/novary/page?a=1")
	c.get(t, "/hs/novary/page?a=2")
	if c.calls() != 1 {
		t.Fatalf("при vary: [] query должен игнорироваться, вызовов=%d", c.calls())
	}
}

func TestServiceCache_VaryHost(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/pub/page", "Host", "a.example")
	c.get(t, "/hs/pub/page", "Host", "b.example")
	if c.calls() != 2 {
		t.Fatalf("разные Host должны кэшироваться раздельно, вызовов=%d", c.calls())
	}
}

func TestServiceCache_OnlyGET(t *testing.T) {
	c := newCacheTestServer(t)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/hs/pub/page", nil)
		w := httptest.NewRecorder()
		c.srv.serviceDispatch(w, r)
	}
	if c.calls() != 2 {
		t.Fatalf("POST кэшироваться не должен, вызовов=%d", c.calls())
	}
}

// «404 залип на час» — типовой инцидент CMS; Set-Cookie в кэше раздал бы одну
// сессию нескольким клиентам.
func TestServiceCache_NoCacheForErrorsAndCookies(t *testing.T) {
	c := newCacheTestServer(t)

	c.get(t, "/hs/pub/missing")
	c.get(t, "/hs/pub/missing")
	if c.calls() != 2 {
		t.Fatalf("404 попал в кэш, вызовов=%d", c.calls())
	}

	c.reset()
	c.get(t, "/hs/pub/cookie")
	c.get(t, "/hs/pub/cookie")
	if c.calls() != 2 {
		t.Fatalf("ответ с Set-Cookie попал в кэш, вызовов=%d", c.calls())
	}
}

func TestServiceCache_MaxBody(t *testing.T) {
	c := newCacheTestServer(t)
	first := c.get(t, "/hs/pub/big")
	if first.Code != http.StatusOK {
		t.Fatalf("status=%d", first.Code)
	}
	if first.Body.Len() < 3000 {
		t.Fatalf("тело %d байт — тест рассчитан на ответ больше max_body=2048", first.Body.Len())
	}
	c.get(t, "/hs/pub/big")
	if c.calls() != 2 {
		t.Fatalf("ответ больше max_body попал в кэш, вызовов=%d", c.calls())
	}
}

func TestServiceCache_ETag304(t *testing.T) {
	c := newCacheTestServer(t)
	first := c.get(t, "/hs/pub/page")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("нет ETag")
	}
	cond := c.get(t, "/hs/pub/page", "If-None-Match", etag)
	if cond.Code != http.StatusNotModified {
		t.Fatalf("status=%d, ожидался 304", cond.Code)
	}
	if cond.Body.Len() != 0 {
		t.Errorf("304 с телом длиной %d", cond.Body.Len())
	}
	wrong := c.get(t, "/hs/pub/page", "If-None-Match", `W/"deadbeef"`)
	if wrong.Code != http.StatusOK {
		t.Errorf("при несовпавшем ETag status=%d, ожидался 200", wrong.Code)
	}
}

// Кэш при auth ≠ none игнорируется: иначе ответ одного пользователя достался
// бы другому.
func TestServiceCache_AuthNotNoneNotCached(t *testing.T) {
	c := newCacheTestServer(t)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/hs/priv/page", nil)
		r.SetBasicAuth("нет", "нет")
		w := httptest.NewRecorder()
		c.srv.serviceDispatch(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("ожидался 401 (учётки нет), получено %d", w.Code)
		}
	}
	if c.cache.Size() != 0 {
		t.Fatalf("кэш непуст (%d байт) для сервиса с auth: basic", c.cache.Size())
	}
}

// Параллельные запросы на холодный ключ не должны запускать несколько
// исполнений DSL — это момент, когда сайт и падает.
func TestServiceCache_ConcurrentMissRunsHandlerOnce(t *testing.T) {
	c := newCacheTestServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/hs/pub/page", nil)
			w := httptest.NewRecorder()
			c.srv.serviceDispatch(w, r)
		}()
	}
	wg.Wait()
	if got := c.calls(); got != 1 {
		t.Fatalf("обработчик вызван %d раз(а) на 8 параллельных запросов, ожидался 1", got)
	}
}

func TestServiceCache_ClearByService(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/pub/page")
	c.get(t, "/hs/novary/page")
	if c.calls() != 2 {
		t.Fatalf("подготовка: вызовов=%d", c.calls())
	}
	if n := c.cache.Clear("pub"); n != 1 {
		t.Fatalf("Clear(\"pub\") выбросил %d записей, ожидалась 1", n)
	}
	c.get(t, "/hs/pub/page")
	c.get(t, "/hs/novary/page")
	if c.calls() != 3 {
		t.Fatalf("сброс задел чужой сервис или не сработал, вызовов=%d", c.calls())
	}
}

// Правка модуля не должна быть невидимой до истечения TTL.
func TestServiceCache_InvalidateOnReload(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/pub/page")
	c.srv.InvalidateServiceCache()
	c.get(t, "/hs/pub/page")
	if c.calls() != 2 {
		t.Fatalf("после InvalidateServiceCache ответ пришёл из кэша, вызовов=%d", c.calls())
	}
}

func TestServiceCache_LRUEviction(t *testing.T) {
	c := newCacheTestServer(t)
	small := newServiceCache(1200) // хватает примерно на одну запись
	small.now = func() time.Time { return c.clock }
	c.srv.svcCache = small

	c.get(t, "/hs/pub/page?n=1")
	c.get(t, "/hs/pub/page?n=2")
	c.get(t, "/hs/pub/page?n=3")
	if small.Size() > 1200 {
		t.Fatalf("размер кэша %d превысил лимит 1200", small.Size())
	}
	if small.evictions.Load() == 0 {
		t.Errorf("вытеснения не происходило — лимит памяти не работает")
	}
}
