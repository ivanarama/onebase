package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// passwdFixture готовит базу с админом и обычным пользователем и возвращает
// функцию, которая шлёт смену пароля ровно тем же путём, что и панель
// администрирования: через cfgAuthMiddleware с cookie сессии конфигуратора.
func passwdFixture(t *testing.T, baseID string) (admin, other *auth.User, token string, post func(string, string) *httptest.ResponseRecorder, reopen func() *auth.Repo) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), baseID+".db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	admin, err = repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Администратор", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	other, err = repo.Create(ctx, "user2", "Str0ng-Passw0rd!", "Второй", false)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	token, err = repo.CreateSession(ctx, admin.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := baseOnSQLite(t, baseID, dbPath)
	h := &handler{store: store, runner: NewRunner()}
	post = func(userID, password string) *httptest.ResponseRecorder {
		body := strings.NewReader(`{"id":"` + userID + `","password":"` + password + `"}`)
		req := httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/users/passwd", body)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
		req = requestWithBaseID(req, baseID)
		rec := httptest.NewRecorder()
		h.cfgAuthMiddleware(http.HandlerFunc(h.cfgAdminUserPasswd)).ServeHTTP(rec, req)
		return rec
	}
	reopen = func() *auth.Repo {
		t.Helper()
		fresh, err := storage.ConnectSQLite(context.Background(), dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fresh.Close() })
		return auth.NewRepo(fresh)
	}
	return admin, other, token, post, reopen
}

// Административная смена пароля отзывает и текущую сессию конфигуратора: она
// тоже может быть украденной. Первый POST успевает завершиться, а следующий
// запрос тем же публичным путём обязан уйти на повторный вход.
func TestUserPasswdRevokesOwnConfiguratorSession(t *testing.T) {
	admin, other, _, post, _ := passwdFixture(t, "self-passwd")

	if rec := post(admin.ID, "An0ther-Str0ng!"); rec.Code != http.StatusOK {
		t.Fatalf("смена своего пароля: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	rec := post(other.ID, "An0ther-Str0ng!")
	if rec.Code != http.StatusFound {
		t.Fatalf("отозванная сессия не отправлена на повторный вход: код=%d location=%q",
			rec.Code, rec.Header().Get("Location"))
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "/configurator/login") {
		t.Errorf("redirect=%q, ожидался вход конфигуратора", location)
	}
}

// При смене своего пароля завершаются все сессии администратора: текущий
// configurator token, другая машина и Предприятие. Исключение для текущего
// токена оставляло украденной сессии способ пережить собственный reset.
func TestUserPasswdRevokesAllSessionsOfSelf(t *testing.T) {
	admin, _, token, post, reopen := passwdFixture(t, "self-passwd-others")

	repo := reopen()
	ctx := context.Background()
	elsewhere, err := repo.CreateSession(ctx, admin.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		t.Fatal(err)
	}
	enterprise, err := repo.CreateSession(ctx, admin.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}

	if rec := post(admin.ID, "An0ther-Str0ng!"); rec.Code != http.StatusOK {
		t.Fatalf("смена своего пароля: код=%d тело=%q", rec.Code, rec.Body.String())
	}

	if _, err := repo.LookupSessionKind(ctx, elsewhere, auth.SessionKindConfigurator); err == nil {
		t.Error("чужая сессия конфигуратора того же админа пережила смену пароля")
	}
	if _, err := repo.LookupSessionKind(ctx, enterprise, auth.SessionKindEnterprise); err == nil {
		t.Error("сессия Предприятия того же админа пережила смену пароля")
	}
	if _, err := repo.LookupSessionKind(ctx, token, auth.SessionKindConfigurator); err == nil {
		t.Error("текущая сессия конфигуратора пережила смену пароля")
	}
}

// Смена пароля другому пользователю по-прежнему завершает все его сессии.
func TestUserPasswdRevokesTargetSessions(t *testing.T) {
	_, other, _, post, reopen := passwdFixture(t, "other-passwd")

	repo := reopen()
	ctx := context.Background()
	victim, err := repo.CreateSession(ctx, other.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(other.ID, "An0ther-Str0ng!"); rec.Code != http.StatusOK {
		t.Fatalf("смена чужого пароля: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	if _, err := repo.LookupSessionKind(ctx, victim, auth.SessionKindEnterprise); err == nil {
		t.Error("сессия пользователя пережила смену его пароля администратором")
	}
}

// Отказ политики паролей обязан называть способ её смягчить: иначе на тестовом
// стенде пустой пароль выглядит невозможным в принципе — переменная окружения
// упомянута только в исходниках.
func TestUserPasswdEmptyPasswordNamesTheOptIn(t *testing.T) {
	_, other, _, post, _ := passwdFixture(t, "empty-passwd")

	rec := post(other.ID, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("пустой пароль: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	msg, _ := jsonBody(t, rec)["error"].(string)
	if !strings.Contains(msg, "ONEBASE_ALLOW_EMPTY_PASSWORDS") {
		t.Errorf("отказ не называет переменную окружения: %q", msg)
	}
}
