package ui

// Тесты auth token/hmac и rate-limit HTTP-сервисов (поглощено из плана 58
// при объединении с планом 61).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

// newSecuredServiceServer поднимает Server с одним сервисом заданной конфигурации.
func newSecuredServiceServer(t *testing.T, svc *httpservice.Service) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	prog, err := parser.New(lexer.New(serviceHandlersSrc, "x.service.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{ServicePrograms: map[string]*ast.Program{svc.Name: prog}})
	svc.Normalize()
	registry.LoadHTTPServices([]*httpservice.Service{svc})

	// Сеть включаем — иначе предохранитель (план 62) даст 503 до auth/rate-limit,
	// которые проверяют эти тесты. Тест блокировки выключает её отдельно.
	if err := db.SaveNetworkEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	srv := &Server{
		store:            db,
		reg:              registry,
		interp:           interp,
		authRepo:         auth.NewRepo(db),
		lockMgr:          runtime.NewLockManager(),
		messages:         NewMessageStore(),
		maxFileSizeBytes: 1 << 20,
		loginLimit:       auth.NewLoginLimiter(5, time.Minute),
	}
	srv.entitySvc = srv.newEntityService(nil)
	return srv
}

func TestService_TokenAuth(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "T", RootURL: "t", Auth: "token", Secret: "s3cret",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})

	// без токена → 401
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/t/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("без токена ожидался 401, получен %d", w.Code)
	}

	// неверный токен → 401
	r := httptest.NewRequest("GET", "/hs/t/", nil)
	r.Header.Set("X-Webhook-Token", "wrong")
	w = httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("с неверным токеном ожидался 401, получен %d", w.Code)
	}

	// верный токен → обработчик
	r = httptest.NewRequest("GET", "/hs/t/", nil)
	r.Header.Set("X-Webhook-Token", "s3cret")
	w = httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("с верным токеном ожидался 200, получен %d (%s)", w.Code, w.Body.String())
	}
}

func TestService_TokenAuthEmptyRuntimeSecretFailsClosed(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "T", RootURL: "t", Auth: "token", Secret: "",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})

	r := httptest.NewRequest("GET", "/hs/t/", nil)
	r.Header.Set("X-Webhook-Token", "")
	w := httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("пустой runtime secret должен fail-closed, получен %d (%s)", w.Code, w.Body.String())
	}
}

func TestService_HMACAuth(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "H", RootURL: "h", Auth: "hmac", Secret: "s3cret",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"POST": "Эхо"}}},
	})
	body := `{"x":1}`

	// без подписи → 401
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("POST", "/hs/h/", strings.NewReader(body)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("без подписи ожидался 401, получен %d", w.Code)
	}

	// верная подпись (с префиксом sha256=) → обработчик
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	r := httptest.NewRequest("POST", "/hs/h/", strings.NewReader(body))
	r.Header.Set("X-Webhook-Signature", sig)
	w = httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("с верной подписью ожидался 200, получен %d (%s)", w.Code, w.Body.String())
	}
}

func TestService_HMACAuthEmptyRuntimeSecretFailsClosed(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "H", RootURL: "h", Auth: "hmac", Secret: "",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"POST": "Эхо"}}},
	})
	body := `{"x":1}`
	mac := hmac.New(sha256.New, nil)
	mac.Write([]byte(body))

	r := httptest.NewRequest("POST", "/hs/h/", strings.NewReader(body))
	r.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	s.serviceDispatch(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("пустой runtime secret должен fail-closed, получен %d (%s)", w.Code, w.Body.String())
	}
}

func TestService_BlockedWhenNetworkLocked(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "N", RootURL: "n", Auth: "none",
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})
	// Выключаем предохранитель (фикстура включает его по умолчанию).
	if err := s.store.SaveNetworkEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/n/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("при заблокированной сети ожидался 503, получен %d", w.Code)
	}

	// После включения предохранителя сервис отвечает.
	if err := s.store.SaveNetworkEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/n/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("после включения сети ожидался 200, получен %d (%s)", w.Code, w.Body.String())
	}
}

func TestService_RateLimit(t *testing.T) {
	s := newSecuredServiceServer(t, &httpservice.Service{
		Name: "R", RootURL: "r", Auth: "none", RateLimit: 2,
		Templates: []httpservice.URLTemplate{{Template: "/", Methods: map[string]string{"GET": "Корень"}}},
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/r/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("запрос %d: ожидался 200, получен %d", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.serviceDispatch(w, httptest.NewRequest("GET", "/hs/r/", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3-й запрос: ожидался 429, получен %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 без Retry-After")
	}
}
