package ui

// Тесты плана 126: кэш ответов HTTP-сервисов. Всё через serviceDispatch —
// тем же путём, каким приходит реальный запрос.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/httpservice"
	"github.com/ivantit66/onebase/internal/i18n"
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

Функция Персональный(Запрос) Экспорт
    Возврат ОтветТекст(200, Запрос.ПолучитьЗаголовок("Cookie") + Запрос.ПолучитьЗаголовок("Authorization"));
КонецФункции

Функция УправлениеКэшем(Запрос) Экспорт
    Отв = Новый HTTPСервисОтвет(200);
    Отв.УстановитьЗаголовок("Cache-Control", Запрос.ПолучитьЗаголовок("X-Cache-Policy"));
    Отв.УстановитьТелоИзСтроки(Запрос.ПолучитьЗаголовок("X-Client"));
    Возврат Отв;
КонецФункции

Функция ПоАрендатору(Запрос) Экспорт
    Отв = Новый HTTPСервисОтвет(200);
    Отв.УстановитьЗаголовок("Vary", "X-Tenant");
    Отв.УстановитьТелоИзСтроки(Запрос.ПолучитьЗаголовок("X-Tenant"));
    Возврат Отв;
КонецФункции

Функция Бум(Запрос) Экспорт
    Возврат Утилиты.Бах(Запрос);
КонецФункции

// Отказ, проходящий через модульный шов: тест подменяет LookupModuleProc и
// держит обработчик внутри вызова, чтобы увидеть, идут запросы параллельно
// или по одному. Ответ 404 в кэш не попадает никогда.
Функция ОтказСМодулем(Запрос) Экспорт
    Попытка
        Утилиты.Ждать();
    Исключение
    КонецПопытки;
    Возврат ОтветТекст(404, "не найдено");
КонецФункции

Функция Язык(Запрос) Экспорт
    Возврат ОтветТекст(200, НСтр("ru = 'привет'; en = 'hello'"));
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
			"Pub": prog, "NoVary": prog, "Priv": prog, "Lang": prog,
		},
	})

	tmpl := []httpservice.URLTemplate{
		{Template: "/page", Methods: map[string]string{"GET": "Страница", "POST": "Страница"}},
		{Template: "/missing", Methods: map[string]string{"GET": "Ошибка404"}},
		{Template: "/cookie", Methods: map[string]string{"GET": "Куки"}},
		{Template: "/personal", Methods: map[string]string{"GET": "Персональный"}},
		{Template: "/cache-policy", Methods: map[string]string{"GET": "УправлениеКэшем"}},
		{Template: "/tenant", Methods: map[string]string{"GET": "ПоАрендатору"}},
		{Template: "/big", Methods: map[string]string{"GET": "Большая"}},
		{Template: "/boom", Methods: map[string]string{"GET": "Бум"}},
		{Template: "/slow404", Methods: map[string]string{"GET": "ОтказСМодулем"}},
		{Template: "/lang", Methods: map[string]string{"GET": "Язык"}},
	}
	pub := &httpservice.Service{Name: "Pub", RootURL: "pub", Auth: "none", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60, Vary: []string{"query", "host"}, Public: true, MaxBody: 2048}}
	noVary := &httpservice.Service{Name: "NoVary", RootURL: "novary", Auth: "none", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60, Vary: []string{}}}
	priv := &httpservice.Service{Name: "Priv", RootURL: "priv", Auth: "basic", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60}}
	langSvc := &httpservice.Service{Name: "Lang", RootURL: "lang", Auth: "none", Templates: tmpl,
		Cache: &httpservice.CacheConfig{TTL: 60, Vary: []string{"lang"}, Public: true}}
	for _, svc := range []*httpservice.Service{pub, noVary, priv, langSvc} {
		svc.Normalize()
	}
	registry.LoadHTTPServices([]*httpservice.Service{pub, noVary, priv, langSvc})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	cache := newServiceCache(0)
	cts := &cacheTestServer{cache: cache, clock: time.Now(), db: db, t: t}
	cache.now = func() time.Time { return cts.clock }

	// Бандл с en и ru: без него resolveLang всегда отвечает «ru», и дробление
	// ключа кэша по языку (vary: lang) нечем проверить. Базовый язык конфигурации
	// пуст намеренно: в цепочке приоритетов он стоит выше Accept-Language и
	// заглушил бы его.
	bundle, err := i18n.Load(fstest.MapFS{
		"locales/en.json": &fstest.MapFile{Data: []byte(`{}`)},
		"locales/ru.json": &fstest.MapFile{Data: []byte(`{}`)},
	}, "")
	if err != nil {
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
		svcCache:         cache,
		cfg:              Config{Bundle: bundle},
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

// Go-паника обработчика (не DSL-ошибка) раскручивает serviceDispatch через
// defer кэширования: без проверки recover туда уезжал бы нетронутый capture —
// пустой 200 — и жил бы в кэше весь TTL. Паника инжектируется через
// LookupModuleProc — шов, который дёргается изнутри исполнения DSL.
func TestServiceCache_PanicNotCached(t *testing.T) {
	c := newCacheTestServer(t)
	c.srv.interp.LookupModuleProc = func(module, name string) *ast.ProcedureDecl {
		panic("рукотворная Go-паника обработчика")
	}

	r := httptest.NewRequest("GET", "/hs/pub/boom", nil)
	w := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		c.srv.serviceDispatch(w, r)
	}()
	if recovered == nil {
		t.Fatal("паника обработчика не дошла до внешнего Recoverer — её съели по дороге")
	}
	if w.Body.Len() != 0 {
		t.Fatalf("при панике клиенту успело уйти тело: %q", w.Body.String())
	}
	if n := c.cache.Size(); n != 0 {
		t.Fatalf("паника оставила запись в кэше (size=%d)", n)
	}
}

// vary: lang дробит ключ по языку из Accept-Language, поэтому внешние кэши
// обязаны получить Vary: Accept-Language — иначе public-ответ на языке первого
// клиента прокси раздаст клиентам с другим языком.
func TestServiceCache_VaryLang(t *testing.T) {
	c := newCacheTestServer(t)

	en := c.get(t, "/hs/lang/page", "Accept-Language", "en")
	if en.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", en.Code, en.Body.String())
	}
	varyOK := false
	for _, v := range en.Header().Values("Vary") {
		if strings.Contains(v, "Accept-Language") {
			varyOK = true
		}
	}
	if !varyOK {
		t.Errorf("нет Vary: Accept-Language, Vary=%v", en.Header().Values("Vary"))
	}
	if cc := en.Header().Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Errorf("ожидался Cache-Control: public, получен %q", cc)
	}

	c.get(t, "/hs/lang/page", "Accept-Language", "ru")
	if got := c.calls(); got != 2 {
		t.Errorf("другой язык должен дать промах: обработчик вызван %d раз(а), ожидалось 2", got)
	}
	c.get(t, "/hs/lang/page", "Accept-Language", "en")
	if got := c.calls(); got != 2 {
		t.Errorf("повтор языка должен дать хит: обработчик вызван %d раз(а), ожидалось 2", got)
	}
}

// Ключ (при vary: query — весь query-string) и заголовки обязаны входить в
// учёт размера: иначе мусорные уникальные параметры раздувают память мимо
// лимита, а метрика размера кэша врёт в разы.
func TestServiceCache_SizeCountsKeyAndHeaders(t *testing.T) {
	cache := newServiceCache(0)
	key := strings.Repeat("k", 10_000)
	resp := &cachedResponse{
		Status: http.StatusOK,
		Header: http.Header{"X-Long": []string{strings.Repeat("v", 5_000)}},
		Body:   []byte("тело"),
	}
	cache.Put(key, "svc", resp, time.Minute)
	if got := cache.Size(); got < 15_000 {
		t.Fatalf("размер кэша не учитывает ключ и заголовки: %d", got)
	}
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

// Значения одного query-параметра не взаимозаменяемы: DSL берёт первое.
// Имена параметров можно канонизировать, но сортировка значений склеила бы
// семантически разные запросы в одну запись.
func TestServiceCache_VaryQueryPreservesRepeatedValueOrder(t *testing.T) {
	c := newCacheTestServer(t)
	c.get(t, "/hs/pub/page?role=user&role=admin")
	c.get(t, "/hs/pub/page?role=admin&role=user")
	if got := c.calls(); got != 2 {
		t.Fatalf("разный порядок повторяющихся значений склеен в один cache key, вызовов=%d", got)
	}
	c.get(t, "/hs/pub/page?role=user&role=admin")
	if got := c.calls(); got != 2 {
		t.Fatalf("точный повтор первого запроса не дал hit, вызовов=%d", got)
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

// Даже сервис auth:none видит Cookie и Authorization через объект Запрос.
// Такие запросы не должны наполнять общий кэш: иначе ответ первого клиента
// станет ответом второго, даже если обработчик не выставил Vary сам.
func TestServiceCache_SensitiveRequestHeadersKeepClientsIsolated(t *testing.T) {
	for _, tc := range []struct {
		name, header, first, second, path string
	}{
		{"cookie", "Cookie", "client=alice", "client=bob", "/hs/pub/personal?kind=cookie"},
		{"authorization", "Authorization", "Bearer alice", "Bearer bob", "/hs/pub/personal?kind=auth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCacheTestServer(t)
			first := c.get(t, tc.path, tc.header, tc.first)
			second := c.get(t, tc.path, tc.header, tc.second)
			if got := first.Body.String(); got != tc.first {
				t.Fatalf("первый клиент получил %q, ожидалось %q", got, tc.first)
			}
			if got := second.Body.String(); got != tc.second {
				t.Fatalf("второй клиент получил %q, ожидалось %q", got, tc.second)
			}
			if size := c.cache.Size(); size != 0 {
				t.Fatalf("персонализированный ответ попал в общий кэш: size=%d", size)
			}
		})
	}
}

func TestServiceCache_ResponseCacheControlKeepsClientsIsolated(t *testing.T) {
	for _, policy := range []string{
		"no-store", "private, max-age=60", "no-cache", "max-age=0", `s-maxage="0"`,
	} {
		t.Run(policy, func(t *testing.T) {
			c := newCacheTestServer(t)
			first := c.get(t, "/hs/pub/cache-policy", "X-Cache-Policy", policy, "X-Client", "alice")
			second := c.get(t, "/hs/pub/cache-policy", "X-Cache-Policy", policy, "X-Client", "bob")
			if got := first.Body.String(); got != "alice" {
				t.Fatalf("первый клиент получил %q", got)
			}
			if got := second.Body.String(); got != "bob" {
				t.Fatalf("директива %q не изолировала второго клиента: тело=%q", policy, got)
			}
			if got := second.Header().Get("Cache-Control"); got != policy {
				t.Fatalf("Cache-Control потерян: %q", got)
			}
			if size := c.cache.Size(); size != 0 {
				t.Fatalf("ответ с Cache-Control %q попал в кэш: size=%d", policy, size)
			}
		})
	}
}

func TestServiceCache_RequestZeroMaxAgeBypassesReadAndWrite(t *testing.T) {
	for _, directive := range []string{"max-age=0", `s-maxage="0"`} {
		t.Run(directive, func(t *testing.T) {
			c := newCacheTestServer(t)
			path := "/hs/pub/page?request-cache-control=" + url.QueryEscape(directive)

			// Холодный запрос с обязательной ревалидацией не должен наполнять
			// кэш: следующий обычный запрос обязан снова дойти до origin.
			c.get(t, path, "Cache-Control", directive)
			c.get(t, path)
			if got := c.calls(); got != 2 {
				t.Fatalf("первый запрос с %q попал в кэш, вызовов origin=%d", directive, got)
			}

			// Обычный запрос выше уже прогрел запись; точный повтор даёт hit.
			c.get(t, path)
			if got := c.calls(); got != 2 {
				t.Fatalf("обычная запись не дала hit, вызовов origin=%d", got)
			}

			// Но клиент с max-age=0/s-maxage=0 не может получить эту запись
			// без origin revalidation, которой внутренний кэш не реализует.
			c.get(t, path, "Cache-Control", directive)
			if got := c.calls(); got != 3 {
				t.Fatalf("запрос с %q прочитал готовую запись без origin, вызовов=%d", directive, got)
			}
		})
	}
}

func TestServiceCache_UnsupportedHandlerVaryKeepsClientsIsolated(t *testing.T) {
	c := newCacheTestServer(t)
	first := c.get(t, "/hs/pub/tenant", "X-Tenant", "alpha")
	second := c.get(t, "/hs/pub/tenant", "X-Tenant", "beta")
	if got := first.Body.String(); got != "alpha" {
		t.Fatalf("первый tenant получил %q", got)
	}
	if got := second.Body.String(); got != "beta" {
		t.Fatalf("неподдерживаемый Vary склеил клиентов: второй tenant получил %q", got)
	}
	if got := strings.Join(second.Header().Values("Vary"), ","); !strings.Contains(got, "X-Tenant") {
		t.Fatalf("Vary обработчика потерян: %q", got)
	}
	if size := c.cache.Size(); size != 0 {
		t.Fatalf("ответ с неподдерживаемым Vary попал в кэш: size=%d", size)
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

func TestCacheCapture_MaxBodySwitchesToPassthroughWithoutLoss(t *testing.T) {
	out := httptest.NewRecorder()
	capture := newCacheCapture(out, 5)
	capture.Header().Set("X-Capture", "kept")
	capture.WriteHeader(http.StatusCreated)

	if n, err := capture.Write([]byte("1234")); err != nil || n != 4 {
		t.Fatalf("первая запись: n=%d err=%v", n, err)
	}
	if got := out.Body.String(); got != "" {
		t.Fatalf("до достижения max_body данные ушли клиенту: %q", got)
	}
	if got := cap(capture.body); got > 5 {
		t.Fatalf("capture выделил %d байт при max_body=5", got)
	}

	if n, err := capture.Write([]byte("567")); err != nil || n != 3 {
		t.Fatalf("переход в passthrough: n=%d err=%v", n, err)
	}
	if n, err := capture.Write([]byte("89")); err != nil || n != 2 {
		t.Fatalf("запись после перехода: n=%d err=%v", n, err)
	}
	if !capture.passthrough {
		t.Fatal("превышение max_body не включило passthrough")
	}
	if len(capture.body) != 0 || cap(capture.body) != 0 {
		t.Fatalf("capture удерживает тело после перехода: len=%d cap=%d", len(capture.body), cap(capture.body))
	}
	if out.Code != http.StatusCreated {
		t.Fatalf("status=%d, ожидался %d", out.Code, http.StatusCreated)
	}
	if got := out.Header().Get("X-Capture"); got != "kept" {
		t.Fatalf("заголовок потерян: %q", got)
	}
	if got := out.Body.String(); got != "123456789" {
		t.Fatalf("тело потеряно/дублировано при переходе: %q", got)
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
// бы другому. Обработчик обязан реально исполниться под учёткой — тест с 401
// до обработчика не может упасть: не-200 и так не кэшируется.
func TestServiceCache_AuthNotNoneNotCached(t *testing.T) {
	c := newCacheTestServer(t)
	if _, err := c.srv.authRepo.Create(t.Context(), "admin", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/hs/priv/page", nil)
		r.SetBasicAuth("admin", "S3cret-pass")
		w := httptest.NewRecorder()
		c.srv.serviceDispatch(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("запрос %d: status=%d body=%s", i+1, w.Code, w.Body.String())
		}
	}
	if got := c.calls(); got != 2 {
		t.Fatalf("кэш при auth: basic сработал: обработчик вызван %d раз(а), ожидалось 2", got)
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

// Сброс и размер кэша из DSL — через боевую регистрацию builtins. Сброс по
// ИМЕНИ сервиса проверяет и резолв имя → root_url: ключи кэша строятся по URL.
func TestServiceCache_DSLResetAndSize(t *testing.T) {
	c := newCacheTestServer(t)
	if got := c.get(t, "/hs/pub/page"); got.Code != http.StatusOK {
		t.Fatalf("прогрев: status=%d", got.Code)
	}

	vars := map[string]any{}
	c.srv.registerServiceCacheBuiltins(vars)
	call := func(name string, args ...any) any {
		fn, ok := vars[name].(interpreter.BuiltinFunc)
		if !ok {
			t.Fatalf("функция %s не зарегистрирована в DSL", name)
		}
		res, err := fn(args, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}

	if size := call("РазмерКэшаСервисов").(float64); size <= 0 {
		t.Fatalf("РазмерКэшаСервисов=%v после прогрева, ожидалось > 0", size)
	}
	if cleared := call("СброситьКэшСервисов", "Pub").(float64); cleared != 1 {
		t.Fatalf("СброситьКэшСервисов(\"Pub\")=%v, ожидалась 1 запись", cleared)
	}
	if size := call("РазмерКэшаСервисов").(float64); size != 0 {
		t.Fatalf("после сброса размер=%v", size)
	}

	// После сброса запрос снова доходит до обработчика.
	c.reset()
	c.get(t, "/hs/pub/page")
	if got := c.calls(); got != 1 {
		t.Fatalf("после сброса обработчик вызван %d раз(а), ожидался 1", got)
	}
}

// #1000: замок ключа задумывался разовой защитой холодного старта, но для
// страницы, ответ по которой некэшируем всегда (404, Set-Cookie, тело больше
// max_body), кэш по ключу не наполняется никогда — и запросы выстраивались в
// очередь по одному навсегда, то есть параллелизм выходил хуже, чем с
// выключенным кэшем. После первого некэшируемого ответа ключ помечается, и
// замок под него больше не берётся.
func TestServiceCache_UncacheableKeyDoesNotSerialize(t *testing.T) {
	c := newCacheTestServer(t)

	// Шов модуля отдаёт настоящую (пустую) процедуру: вернуть nil значило бы
	// уронить обработчик в 500 и проверять не то.
	modProg, err := parser.New(lexer.New("Процедура Ждать() Экспорт\nКонецПроцедуры", "утилиты.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	waitProc := modProg.Procedures[0]

	// Первый запрос проходит насквозь и помечает ключ: шов пока не держит
	// обработчик.
	c.srv.interp.LookupModuleProc = func(module, name string) *ast.ProcedureDecl { return waitProc }
	if w := c.get(t, "/hs/pub/slow404"); w.Code != http.StatusNotFound {
		t.Fatalf("подготовка: код ответа %d, ожидался 404 (тело: %s)", w.Code, w.Body.String())
	}

	const parallel = 3
	entered := make(chan struct{}, parallel)
	release := make(chan struct{})
	c.srv.interp.LookupModuleProc = func(module, name string) *ast.ProcedureDecl {
		entered <- struct{}{}
		<-release
		return waitProc
	}

	var wg sync.WaitGroup
	codes := make([]int, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/hs/pub/slow404", nil)
			w := httptest.NewRecorder()
			c.srv.serviceDispatch(w, r)
			codes[i] = w.Code
		}(i)
	}

	deadline := time.After(5 * time.Second)
	for i := 0; i < parallel; i++ {
		select {
		case <-entered:
		case <-deadline:
			close(release)
			wg.Wait()
			t.Fatalf("до обработчика дошло %d запрос(а) из %d: остальные ждут замок ключа, "+
				"хотя ответ по нему некэшируем в принципе", i, parallel)
		}
	}
	close(release)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusNotFound {
			t.Fatalf("запрос %d вернул %d, ожидался 404", i, code)
		}
	}
}

// Отметка снимается, когда ответ по ключу снова кэшируется, и протухает сама:
// страница может перестать быть некэшируемой (404 исчез, тело ужалось), и
// отрицательный список не должен этого скрывать.
func TestServiceCache_UncacheableMarkLifecycle(t *testing.T) {
	c := newCacheTestServer(t)
	const key = "pub|GET|/hs/pub/missing"

	c.cache.markUncacheable(key, "pub")
	if !c.cache.uncacheableRecently(key) {
		t.Fatal("отметка не поставилась")
	}

	c.cache.forgetUncacheable(key)
	if c.cache.uncacheableRecently(key) {
		t.Fatal("отметка пережила forgetUncacheable")
	}

	c.cache.markUncacheable(key, "pub")
	c.clock = c.clock.Add(uncacheableTTL + time.Second)
	if c.cache.uncacheableRecently(key) {
		t.Fatal("отметка не протухла по TTL")
	}

	c.cache.markUncacheable(key, "pub")
	c.cache.markUncacheable("other|GET|/hs/novary/missing", "novary")
	c.cache.Clear("pub")
	if c.cache.uncacheableRecently(key) {
		t.Fatal("сброс сервиса не снял его отрицательные отметки")
	}
	if !c.cache.uncacheableRecently("other|GET|/hs/novary/missing") {
		t.Fatal("сброс одного сервиса снял отметки чужого")
	}
}

// Некэшируемый ответ помечает ключ прямо на боевом пути — без этого отметка
// была бы мёртвым кодом, который дёргает только тест.
func TestServiceCache_DispatchMarksUncacheable(t *testing.T) {
	c := newCacheTestServer(t)
	for _, path := range []string{"/hs/pub/missing", "/hs/pub/cookie", "/hs/pub/big"} {
		t.Run(path, func(t *testing.T) {
			c.get(t, path)
			key := "pub|GET|" + path + "||" + strings.ToLower("example.com")
			if !c.cache.uncacheableRecently(key) {
				t.Fatalf("ключ %q не помечен некэшируемым после ответа", key)
			}
		})
	}
}

// #1000: ключ дробился по языку, а обработчику язык не доставался — НСтр всегда
// брал язык базы, и vary: lang заводил по записи на язык с ОДИНАКОВЫМ телом.
func TestServiceCache_VaryLangGivesHandlerTheLanguage(t *testing.T) {
	c := newCacheTestServer(t)

	en := c.get(t, "/hs/lang/lang", "Accept-Language", "en")
	if got := strings.TrimSpace(en.Body.String()); got != "hello" {
		t.Fatalf("Accept-Language: en → %q, ожидалось «hello»", got)
	}
	ru := c.get(t, "/hs/lang/lang", "Accept-Language", "ru")
	if got := strings.TrimSpace(ru.Body.String()); got != "привет" {
		t.Fatalf("Accept-Language: ru → %q, ожидалось «привет»", got)
	}
	if got := c.calls(); got != 0 {
		t.Fatalf("обработчик /lang не пишет в справочник, а счётчик=%d", got)
	}
}

// Обратная сторона правила: если кэш ключ по языку НЕ дробит, язык обработчику
// не отдаётся — иначе ответ, собранный на языке первого клиента, лёг бы в общую
// запись и достался всем остальным.
func TestServiceCache_NoVaryLangKeepsBaseLanguage(t *testing.T) {
	c := newCacheTestServer(t)
	w := c.get(t, "/hs/novary/lang", "Accept-Language", "en")
	if got := strings.TrimSpace(w.Body.String()); got != "привет" {
		t.Fatalf("без vary: lang ответ %q, ожидался язык базы («привет»)", got)
	}
}
