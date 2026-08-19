package ui

// Тесты плана 128: сжатие ответов и заголовки безопасности уровня сервиса.
// Проверка идёт через serviceDispatch — тот же путь, по которому приходит
// реальный запрос.

import (
	"compress/gzip"
	"io"
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
	"github.com/ivantit66/onebase/internal/websec"
)

const compressHandlersSrc = `
Функция Большой(Запрос) Экспорт
    т = "";
    Для Сч = 1 По 300 Цикл
        т = т + "0123456789";
    КонецЦикла;
    Отв = Новый HTTPСервисОтвет(200);
    Отв.УстановитьЗаголовок("Content-Type", "text/html; charset=utf-8");
    Отв.УстановитьТелоИзСтроки(т);
    Возврат Отв;
КонецФункции

Функция Маленький(Запрос) Экспорт
    Возврат ОтветТекст(200, "мало");
КонецФункции

Функция Картинка(Запрос) Экспорт
    т = "";
    Для Сч = 1 По 300 Цикл
        т = т + "0123456789";
    КонецЦикла;
    Отв = Новый HTTPСервисОтвет(200);
    Отв.УстановитьЗаголовок("Content-Type", "image/png");
    Отв.УстановитьТелоИзСтроки(т);
    Возврат Отв;
КонецФункции

Функция Готовый(Запрос) Экспорт
    т = "";
    Для Сч = 1 По 300 Цикл
        т = т + "0123456789";
    КонецЦикла;
    Отв = Новый HTTPСервисОтвет(200);
    Отв.УстановитьЗаголовок("Content-Type", "text/html; charset=utf-8");
    Отв.УстановитьЗаголовок("Content-Encoding", "gzip");
    Отв.УстановитьТелоИзСтроки(т);
    Возврат Отв;
КонецФункции
`

// newCompressTestServer поднимает сервер с тремя сервисами: публичный (сжатие
// по умолчанию), защищённый (по умолчанию без сжатия) и защищённый с явным
// compress: true.
func newCompressTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := t.Context()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	prog, err := parser.New(lexer.New(compressHandlersSrc, "pub.service.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{ServicePrograms: map[string]*ast.Program{
		"Pub": prog, "Priv": prog, "PrivZip": prog, "Hdr": prog,
	}})

	tmpl := []httpservice.URLTemplate{
		{Template: "/big", Methods: map[string]string{"GET": "Большой"}},
		{Template: "/small", Methods: map[string]string{"GET": "Маленький"}},
		{Template: "/png", Methods: map[string]string{"GET": "Картинка"}},
		{Template: "/precompressed", Methods: map[string]string{"GET": "Готовый"}},
	}
	yes := true
	pub := &httpservice.Service{Name: "Pub", RootURL: "pub", Auth: "none", Templates: tmpl}
	priv := &httpservice.Service{Name: "Priv", RootURL: "priv", Auth: "basic", Templates: tmpl}
	privZip := &httpservice.Service{Name: "PrivZip", RootURL: "privzip", Auth: "basic", Compress: &yes, Templates: tmpl}
	hdr := &httpservice.Service{Name: "Hdr", RootURL: "hdr", Auth: "none", Templates: tmpl,
		SecurityHeaders: &httpservice.SecurityHeadersConfig{
			CSP:            "default-src 'self'",
			FrameOptions:   "DENY",
			ReferrerPolicy: "no-referrer",
			HSTS:           15552000,
			Extra:          map[string]string{"Permissions-Policy": "geolocation=()"},
		}}
	for _, svc := range []*httpservice.Service{pub, priv, privZip, hdr} {
		svc.Normalize()
	}
	registry.LoadHTTPServices([]*httpservice.Service{pub, priv, privZip, hdr})

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	authRepo := auth.NewRepo(db)
	if err := authRepo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		authRepo:         authRepo,
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
		loginLimit:       auth.NewLoginLimiter(5, time.Minute),
	}
}

func doGzipReq(t *testing.T, s *Server, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	if acceptEncoding != "" {
		r.Header.Set("Accept-Encoding", acceptEncoding)
	}
	s.serviceDispatch(w, r)
	return w
}

// doReqThroughStack прогоняет запрос через тот же стек, что и прод: глобальная
// websec.SecurityHeaders (в api/server.go она r.Use до MountServices), а за ней
// serviceDispatch.
//
// Отдельный хелпер, а не замена doGzipReq: без глобальной политики проверяются
// заголовки САМОГО сервиса (например «nosniff ставится всегда» — с глобальной
// middleware этот ассерт стал бы бессмысленным, nosniff пришёл бы снаружи), а с
// ней — взаимодействие двух слоёв. Ассерт «политика сервиса заменяет
// глобальную» без глобального слоя упасть не мог вовсе: замена h.Set на h.Add
// оставляла тест зелёным (#1004).
func doReqThroughStack(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	websec.SecurityHeaders(http.HandlerFunc(s.serviceDispatch)).ServeHTTP(w, r)
	return w
}

func gunzip(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() {
		if err := zr.Close(); err != nil {
			t.Errorf("закрытие gzip-потока: %v", err)
		}
	}()
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("чтение gzip: %v", err)
	}
	return string(data)
}

func TestCompress_GzipWhenAccepted(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/big", "gzip, deflate")

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if enc := w.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding=%q, ожидался gzip", enc)
	}
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Errorf("Vary=%q — без него кэш отдаст сжатое тело клиенту без gzip", v)
	}
	body := gunzip(t, w)
	if len(body) != 3000 {
		t.Errorf("после распаковки %d байт, ожидалось 3000", len(body))
	}
	if w.Body.Len() >= 3000 {
		t.Errorf("сжатое тело %d байт — не меньше исходных 3000", w.Body.Len())
	}
}

func TestCompress_NoAcceptEncoding(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/big", "")
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding=%q при клиенте без gzip", enc)
	}
	if w.Body.Len() != 3000 {
		t.Errorf("тело %d байт, ожидалось 3000", w.Body.Len())
	}
}

// Короткий ответ не сжимаем: на нём gzip даёт отрицательную экономию.
func TestCompress_BelowThreshold(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/small", "gzip")
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("короткий ответ сжат: Content-Encoding=%q", enc)
	}
	if got := w.Body.String(); got != "мало" {
		t.Errorf("тело=%q, ожидалось «мало»", got)
	}
}

// Уже сжатые форматы не трогаем.
func TestCompress_BinaryTypeSkipped(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/png", "gzip")
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("image/png сжат: Content-Encoding=%q", enc)
	}
	if w.Body.Len() != 3000 {
		t.Errorf("тело %d байт, ожидалось 3000", w.Body.Len())
	}
}

// Умолчание зависит от auth: анонимный сервис сжимается, аутентифицированный —
// нет (BREACH), но владелец может включить явно.
func TestCompress_DefaultByAuth(t *testing.T) {
	s := newCompressTestServer(t)

	pub := doGzipReq(t, s, "/hs/pub/big", "gzip")
	if pub.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("публичный сервис не сжат")
	}

	priv := httptest.NewRecorder()
	rp := httptest.NewRequest("GET", "/hs/priv/big", nil)
	rp.Header.Set("Accept-Encoding", "gzip")
	rp.SetBasicAuth("нет", "нет")
	s.serviceDispatch(priv, rp)
	if priv.Header().Get("Content-Encoding") == "gzip" {
		t.Errorf("сервис с auth: basic сжат по умолчанию — это BREACH-риск")
	}

	// Явный compress: true при auth: basic — под настоящей учёткой, чтобы
	// обработчик реально исполнился: ассерт на 401-ответе не может упасть.
	if _, err := s.authRepo.Create(t.Context(), "admin", "S3cret-pass", "Админ", true); err != nil {
		t.Fatalf("Create: %v", err)
	}
	privZip := httptest.NewRecorder()
	rz := httptest.NewRequest("GET", "/hs/privzip/big", nil)
	rz.Header.Set("Accept-Encoding", "gzip")
	rz.SetBasicAuth("admin", "S3cret-pass")
	s.serviceDispatch(privZip, rz)
	if privZip.Code != http.StatusOK {
		t.Fatalf("privzip под учёткой: status=%d body=%s", privZip.Code, privZip.Body.String())
	}
	if enc := privZip.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("явный compress: true при auth: basic не сжал ответ (Content-Encoding=%q)", enc)
	}
}

// Content-Length от несжатого тела на сжатом ответе — битый ответ.
func TestCompress_ContentLengthRemoved(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/big", "gzip")
	if cl := w.Header().Get("Content-Length"); cl != "" && cl != "0" {
		t.Errorf("Content-Length=%q на сжатом ответе", cl)
	}
}

func TestCompress_ClientRefusesGzip(t *testing.T) {
	s := newCompressTestServer(t)
	// q=0 в любой записи — отказ: q=0.0 по RFC ровно то же, что q=0.
	for _, accept := range []string{"gzip;q=0", "gzip;q=0.0", "gzip; q=0.000"} {
		w := doGzipReq(t, s, "/hs/pub/big", accept)
		if enc := w.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("клиент отказался от gzip (%q), а ответ сжат: %q", accept, enc)
		}
	}
}

// Vary обязан стоять и на НЕсжатом варианте: общий кэш без него отдаст ответ
// клиента без gzip клиенту с gzip (и наоборот).
func TestCompress_VaryOnUncompressed(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/big", "")
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Fatalf("Vary=%q на несжатом ответе сервиса со сжатием", v)
	}
}

// Тело с уже выставленным Content-Encoding не сжимаем второй раз.
func TestCompress_AlreadyEncodedNotRecompressed(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/precompressed", "gzip")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if w.Body.Len() != 3000 {
		t.Fatalf("тело изменилось (%d байт вместо 3000) — похоже на двойное сжатие", w.Body.Len())
	}
}

// Политика сервиса ЗАМЕНЯЕТ глобальную, а не добавляется к ней: два заголовка
// CSP браузер применяет как пересечение, и политика вышла бы строже задуманной.
// Запрос идёт через прод-стек с глобальной middleware — без неё ассерт про
// замену не мог упасть в принципе (#1004).
func TestSecurityHeaders_ServiceValues(t *testing.T) {
	s := newCompressTestServer(t)
	w := doReqThroughStack(t, s, "/hs/hdr/small")

	h := w.Header()
	if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "default-src 'self'" {
		t.Errorf("CSP=%v — политика сервиса должна ЗАМЕНЯТЬ глобальную, а не дублироваться", got)
	}
	if h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options=%q", h.Get("X-Frame-Options"))
	}
	// Глобальная middleware ставит Referrer-Policy: same-origin — значение
	// сервиса обязано остаться единственным.
	if got := h.Values("Referrer-Policy"); len(got) != 1 || got[0] != "no-referrer" {
		t.Errorf("Referrer-Policy=%v — значение сервиса должно заменять глобальное", got)
	}
	if h.Get("Permissions-Policy") != "geolocation=()" {
		t.Errorf("Permissions-Policy=%q", h.Get("Permissions-Policy"))
	}
}

// Страховка от повторения #1004: тест выше имеет смысл, только пока в стеке
// действительно есть глобальная политика. Если её однажды уберут из хелпера,
// ассерт «сервис заменяет глобальную» снова станет невозможным для падения —
// поймает этот тест.
func TestSecurityHeaders_TestStackCarriesGlobalPolicy(t *testing.T) {
	s := newCompressTestServer(t)
	// Сервис pub объявлен без блока security_headers: всё, что видно в ответе
	// из политики, пришло от глобальной middleware.
	w := doReqThroughStack(t, s, "/hs/pub/small")
	got := w.Header().Values("Content-Security-Policy")
	if len(got) != 1 || !strings.Contains(got[0], "frame-ancestors") {
		t.Fatalf("глобальная CSP в тестовом стеке отсутствует: %v", got)
	}
}

// nosniff ставится всегда, даже без блока security_headers.
func TestSecurityHeaders_NosniffAlways(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/pub/small", "")
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("nosniff отсутствует у сервиса без security_headers")
	}
}

// HSTS на http-ответе браузер игнорирует, а в разработке способен закрыть
// доступ по http к тому же хосту — ставим только за TLS.
func TestSecurityHeaders_HSTSOnlyOverTLS(t *testing.T) {
	s := newCompressTestServer(t)

	plain := doGzipReq(t, s, "/hs/hdr/small", "")
	if plain.Header().Get("Strict-Transport-Security") != "" {
		t.Errorf("HSTS выставлен на http-запросе")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/hs/hdr/small", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	s.serviceDispatch(w, r)
	if got := w.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=15552000") {
		t.Errorf("HSTS=%q за TLS-прокси", got)
	}
}

// Страница ошибки без политики — дыра в этой политике.
func TestSecurityHeaders_OnErrorResponses(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/hdr/нет-такого", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, ожидался 404", w.Code)
	}
	if w.Header().Get("Content-Security-Policy") != "default-src 'self'" {
		t.Errorf("на 404 нет CSP сервиса")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("на 404 нет nosniff")
	}
}
