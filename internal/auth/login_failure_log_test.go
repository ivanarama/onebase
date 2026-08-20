package auth_test

// Отказ входа обязан называть причину в журнале (#1053).
//
// Отчёт из заявки: вход отвечает 500, а в журнале сервера только строка
// доступа — `status=500 bytes=15`, и ничего больше. Разбирать нечем ни
// администратору, ни нам: все двенадцать мест пакета, отвечавших «internal
// error», молча проглатывали ошибку.

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// captureLog перехватывает журнал по умолчанию на время теста. Возвращает
// функцию чтения накопленного: slog пишет из обработчика запроса, поэтому
// буфер под мьютексом.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// loginPost идёт публичной дверью: POST /login с формой, как из браузера.
func loginPost(t *testing.T, h *auth.Handlers, login, password string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"login": {login}, "password": {password}}
	req := httptest.NewRequest(http.MethodPost, "/login?return=/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "192.168.0.60:52192"
	rec := httptest.NewRecorder()
	h.LoginSubmit(rec, req)
	return rec
}

// Сессию писать некуда — ровно тот отказ, что видел автор заявки. Ответ 500
// остаётся общим (наружу нельзя отдавать ни SQL, ни имена таблиц), но причина
// обязана быть в журнале.
func TestLoginSubmit_SessionFailureIsLogged(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	if _, err := repo.Create(ctx, "ivan", "secret123", "Иван Петров", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	breakSessions(t, ctx, db)

	logs := captureLog(t)
	h := &auth.Handlers{Repo: repo}
	rec := loginPost(t, h, "ivan", "secret123")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код ответа %d, ожидался 500", rec.Code)
	}
	got := logs()
	if got == "" {
		t.Fatal("журнал пуст: отказ входа не оставил следа — ровно то, на что жалуется #1053")
	}
	for _, want := range []string{"внутренняя ошибка аутентификации", "создание сессии", "_sessions"} {
		if !strings.Contains(got, want) {
			t.Errorf("в журнале нет %q — причину по такой записи не найти:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "/login") {
		t.Errorf("в журнале нет маршрута — непонятно, какая дверь отказала:\n%s", got)
	}
}

// Тело ответа наружу остаётся общим: имя таблицы и текст SQL — подсказка
// атакующему, а на странице входа он неаутентифицирован.
func TestLoginSubmit_ResponseKeepsCauseInside(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	if _, err := repo.Create(ctx, "ivan", "secret123", "Иван Петров", false); err != nil {
		t.Fatalf("Create: %v", err)
	}
	breakSessions(t, ctx, db)

	_ = captureLog(t)
	h := &auth.Handlers{Repo: repo}
	rec := loginPost(t, h, "ivan", "secret123")

	body := rec.Body.String()
	for _, leak := range []string{"_sessions", "INSERT", "SQL", "sqlite"} {
		if strings.Contains(body, leak) {
			t.Errorf("ответ раскрывает внутренности (%q): %s", leak, body)
		}
	}
}

// Успешный вход журнал не засоряет: строка уровня ERROR обязана означать
// настоящий отказ, иначе на неё перестанут смотреть.
func TestLoginSubmit_SuccessLogsNoError(t *testing.T) {
	repo, ctx := newTestRepo(t)
	if _, err := repo.Create(ctx, "ivan", "secret123", "Иван Петров", false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	logs := captureLog(t)
	h := &auth.Handlers{Repo: repo}
	rec := loginPost(t, h, "ivan", "secret123")

	if rec.Code != http.StatusFound {
		t.Fatalf("код ответа %d, ожидался 302: %s", rec.Code, rec.Body.String())
	}
	if got := logs(); strings.Contains(got, "внутренняя ошибка аутентификации") {
		t.Errorf("успешный вход записал внутреннюю ошибку:\n%s", got)
	}
}

// breakSessions убирает таблицу сессий: писать сессию становится некуда,
// а всё, что до неё (политика, пароль, 2FA), продолжает работать.
func breakSessions(t *testing.T, ctx context.Context, db *storage.DB) {
	t.Helper()
	if _, err := db.Exec(ctx, `DROP TABLE _sessions`); err != nil {
		t.Fatalf("DROP TABLE _sessions: %v", err)
	}
}
