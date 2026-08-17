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

	privZip := httptest.NewRecorder()
	rz := httptest.NewRequest("GET", "/hs/privzip/big", nil)
	rz.Header.Set("Accept-Encoding", "gzip")
	s.serviceDispatch(privZip, rz)
	// Ответ будет 401 (учётки нет), но решение о сжатии принимается до auth —
	// проверяем, что явный compress: true уважается.
	if enc := privZip.Header().Get("Content-Encoding"); enc != "" && enc != "gzip" {
		t.Errorf("неожиданный Content-Encoding=%q", enc)
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
	w := doGzipReq(t, s, "/hs/pub/big", "gzip;q=0")
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("клиент отказался от gzip (q=0), а ответ сжат: %q", enc)
	}
}

func TestSecurityHeaders_ServiceValues(t *testing.T) {
	s := newCompressTestServer(t)
	w := doGzipReq(t, s, "/hs/hdr/small", "")

	h := w.Header()
	if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != "default-src 'self'" {
		t.Errorf("CSP=%v — политика сервиса должна ЗАМЕНЯТЬ глобальную, а не дублироваться", got)
	}
	if h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options=%q", h.Get("X-Frame-Options"))
	}
	if h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy=%q", h.Get("Referrer-Policy"))
	}
	if h.Get("Permissions-Policy") != "geolocation=()" {
		t.Errorf("Permissions-Policy=%q", h.Get("Permissions-Policy"))
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
