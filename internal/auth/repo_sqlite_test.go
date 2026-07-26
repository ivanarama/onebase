package auth_test

// Тесты критичных путей аутентификации/авторизации на реальной SQLite-базе
// через t.TempDir() — без внешних зависимостей и build-тегов, поэтому
// выполняются в обычном `go test ./...` и в CI. Это покрывает многопользовательские
// сценарии (несколько пользователей, роли, сессии), которые в integration_test.go
// заперты за тегом integration + TEST_DATABASE_URL и в CI не запускаются.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// newTestRepo поднимает чистую SQLite-базу со схемой auth.
func newTestRepo(t *testing.T) (*auth.Repo, context.Context) {
	t.Helper()
	repo, _, ctx := newTestRepoDB(t)
	return repo, ctx
}

// newTestRepoDB — то же, но с доступом к *storage.DB (для тестов, которым
// нужно подправить данные напрямую, например состарить сессию).
func newTestRepoDB(t *testing.T) (*auth.Repo, *storage.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "auth.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return repo, db, ctx
}

func TestCreateAndAuthenticate(t *testing.T) {
	repo, ctx := newTestRepo(t)

	has, err := repo.HasUsers(ctx)
	if err != nil {
		t.Fatalf("HasUsers: %v", err)
	}
	if has {
		t.Fatal("свежая база не должна содержать пользователей")
	}

	user, err := repo.Create(ctx, "ivan", "secret123", "Иван Петров", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.ID == "" {
		t.Fatal("Create должен присвоить ID")
	}

	if has, _ := repo.HasUsers(ctx); !has {
		t.Fatal("после Create HasUsers должен вернуть true")
	}

	// Верный пароль.
	got, err := repo.Authenticate(ctx, "ivan", "secret123")
	if err != nil {
		t.Fatalf("Authenticate (верный пароль): %v", err)
	}
	if got.Login != "ivan" || got.FullName != "Иван Петров" {
		t.Fatalf("неожиданный пользователь: %+v", got)
	}

	// Неверный пароль — отказ.
	if _, err := repo.Authenticate(ctx, "ivan", "wrong"); err == nil {
		t.Fatal("Authenticate с неверным паролем должен вернуть ошибку")
	}

	// Несуществующий пользователь — отказ.
	if _, err := repo.Authenticate(ctx, "nobody", "secret123"); err == nil {
		t.Fatal("Authenticate с неизвестным логином должен вернуть ошибку")
	}
}

func TestUpdatePasswordInvalidatesOldPassword(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "petr", "oldpass", "", false)

	if err := repo.UpdatePassword(ctx, user.ID, "newpass"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	if _, err := repo.Authenticate(ctx, "petr", "oldpass"); err == nil {
		t.Fatal("старый пароль не должен работать после смены")
	}
	if _, err := repo.Authenticate(ctx, "petr", "newpass"); err != nil {
		t.Fatalf("новый пароль должен работать: %v", err)
	}
}

func TestSessionsLifecycle(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "sess", "pass", "", false)

	token, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if token == "" {
		t.Fatal("CreateSession должен вернуть непустой токен")
	}

	looked, err := repo.LookupSession(ctx, token)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if looked.ID != user.ID {
		t.Fatalf("сессия вернула чужого пользователя: %s != %s", looked.ID, user.ID)
	}
	if count, err := repo.ActiveSessionCount(ctx); err != nil || count != 1 {
		t.Fatalf("ActiveSessionCount = %d, %v; want 1, nil", count, err)
	}

	if err := repo.DeleteSession(ctx, token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := repo.LookupSession(ctx, token); err == nil {
		t.Fatal("LookupSession после удаления должен вернуть ошибку")
	}
	if count, err := repo.ActiveSessionCount(ctx); err != nil || count != 0 {
		t.Fatalf("ActiveSessionCount after delete = %d, %v; want 0, nil", count, err)
	}
}

func TestAPITokensLifecycle(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "api", "pass", "API User", false)
	role := []*auth.Role{{
		Name: "API runner",
		Permissions: auth.Permission{
			Reports: map[string][]string{"Остатки": {"run"}},
		},
	}}
	if err := repo.SyncRoles(ctx, role); err != nil {
		t.Fatalf("SyncRoles: %v", err)
	}
	if err := repo.AssignRole(ctx, user.ID, role[0].ID); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}

	expires := time.Now().Add(time.Hour)
	token, raw, err := repo.CreateAPIToken(ctx, "warehouse", user.ID, &expires)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if token.ID == "" || raw == "" || !strings.HasPrefix(raw, "ob_") {
		t.Fatalf("bad token create result: token=%+v raw=%q", token, raw)
	}

	looked, err := repo.LookupAPIToken(ctx, raw)
	if err != nil {
		t.Fatalf("LookupAPIToken: %v", err)
	}
	if looked.ID != user.ID || looked.Login != "api" {
		t.Fatalf("LookupAPIToken returned wrong user: %+v", looked)
	}
	if len(looked.Roles) != 1 || !looked.Has("report", "Остатки", "run") {
		t.Fatalf("LookupAPIToken must load roles, got %+v", looked.Roles)
	}

	tokens, err := repo.ListAPITokens(ctx)
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "warehouse" || tokens[0].UserLogin != "api" {
		t.Fatalf("bad token list: %+v", tokens)
	}
	if tokens[0].LastUsedAt == nil {
		t.Fatalf("last_used_at must be set after lookup: %+v", tokens[0])
	}

	if _, err := repo.LookupAPIToken(ctx, raw+"x"); err == nil {
		t.Fatal("LookupAPIToken with invalid secret must fail")
	}
	if err := repo.RevokeAPIToken(ctx, token.ID); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}
	if _, err := repo.LookupAPIToken(ctx, raw); err == nil {
		t.Fatal("revoked API token must not authenticate")
	}

	expired := time.Now().Add(-time.Hour)
	_, expiredRaw, err := repo.CreateAPIToken(ctx, "expired", user.ID, &expired)
	if err != nil {
		t.Fatalf("CreateAPIToken expired: %v", err)
	}
	if _, err := repo.LookupAPIToken(ctx, expiredRaw); err == nil {
		t.Fatal("expired API token must not authenticate")
	}
}

// Мультисессии (план 78): новая сессия НЕ выбивает прежние — оба окна живут.
func TestCreateSessionKeepsOtherSessions(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "multi", "pass", "", false)

	first, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	second, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})

	if _, err := repo.LookupSession(ctx, first); err != nil {
		t.Fatalf("первая сессия должна остаться валидной: %v", err)
	}
	if _, err := repo.LookupSession(ctx, second); err != nil {
		t.Fatalf("вторая сессия должна быть валидной: %v", err)
	}
}

// CreateSession подчищает истёкшие сессии (единственное, что она удаляет).
func TestCreateSessionCleansExpired(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	user, _ := repo.Create(ctx, "expired", "pass", "", false)

	old, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	// Состариваем сессию напрямую (сеттера TTL у репозитория нет намеренно).
	if _, err := db.Exec(ctx, `UPDATE _sessions SET expires_at = '2000-01-01 00:00:00' WHERE token = ?`, old); err != nil {
		t.Fatalf("состарить сессию: %v", err)
	}

	fresh, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	if _, err := repo.LookupSession(ctx, fresh); err != nil {
		t.Fatalf("свежая сессия должна работать: %v", err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM _sessions WHERE token = ?`, old).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatal("истёкшая сессия должна быть удалена при следующем логине")
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	user, _ := repo.Create(ctx, "sweeper", "pass", "", false)
	token, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	if _, err := db.Exec(ctx, `UPDATE _sessions SET expires_at = '2000-01-01 00:00:00' WHERE token = ?`, token); err != nil {
		t.Fatalf("состарить сессию: %v", err)
	}
	if err := repo.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if _, err := repo.LookupSession(ctx, token); err == nil {
		t.Fatal("истёкшая сессия должна быть удалена")
	}
}

func TestKickUserDropsSession(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "kickme", "pass", "", false)
	token, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})

	if err := repo.KickUser(ctx, "kickme"); err != nil {
		t.Fatalf("KickUser: %v", err)
	}
	if _, err := repo.LookupSession(ctx, token); err == nil {
		t.Fatal("после KickUser сессия должна быть недействительна")
	}
}

// ActiveSessions возвращает по строке на сессию с метаданными (план 78).
func TestActiveSessionsMultiRowsAndMeta(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "meta", "pass", "Мета Тестов", false)

	entToken, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{
		Kind: auth.SessionKindEnterprise, IP: "127.0.0.1:5555", UserAgent: "Mozilla/5.0 Chrome/126",
	})
	cfgToken, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{
		Kind: auth.SessionKindConfigurator, IP: "127.0.0.1:6666", UserAgent: "Mozilla/5.0 Edg/126",
	})
	if entToken == cfgToken {
		t.Fatal("токены сессий должны отличаться")
	}

	sessions, err := repo.ActiveSessions(ctx)
	if err != nil {
		t.Fatalf("ActiveSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ожидались 2 строки одного логина, получено %d", len(sessions))
	}
	kinds := map[string]*auth.SessionInfo{}
	for _, s := range sessions {
		if s.Login != "meta" {
			t.Fatalf("чужой логин в списке: %q", s.Login)
		}
		if s.PublicID == "" {
			t.Fatal("PublicID должен быть заполнен")
		}
		if s.CreatedAt.IsZero() || s.LastSeenAt.IsZero() {
			t.Fatal("created_at/last_seen_at должны быть заполнены")
		}
		kinds[s.Kind] = s
	}
	ent, cfg := kinds[auth.SessionKindEnterprise], kinds[auth.SessionKindConfigurator]
	if ent == nil || cfg == nil {
		t.Fatalf("ожидались kind enterprise и configurator, получено %v", kinds)
	}
	if ent.IP != "127.0.0.1:5555" || cfg.IP != "127.0.0.1:6666" {
		t.Fatalf("IP не сохранился: %q / %q", ent.IP, cfg.IP)
	}
	if ent.PublicID == cfg.PublicID {
		t.Fatal("public_id сессий должны отличаться")
	}
}

// KickSession по public_id завершает только одну сессию (план 78).
func TestKickSessionByPublicID(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "kickone", "pass", "", false)

	entToken, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	cfgToken, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})

	sessions, _ := repo.ActiveSessions(ctx)
	var entPublicID string
	for _, s := range sessions {
		if s.Kind == auth.SessionKindEnterprise {
			entPublicID = s.PublicID
		}
	}
	if entPublicID == "" {
		t.Fatal("не нашли enterprise-сессию")
	}

	if err := repo.KickSession(ctx, entPublicID); err != nil {
		t.Fatalf("KickSession: %v", err)
	}
	if _, err := repo.LookupSession(ctx, entToken); err == nil {
		t.Fatal("завершённая сессия должна быть недействительна")
	}
	if _, err := repo.LookupSession(ctx, cfgToken); err != nil {
		t.Fatalf("вторая сессия не должна пострадать: %v", err)
	}

	// Пустой public_id — ошибка, а не удаление всех строк с NULL.
	if err := repo.KickSession(ctx, ""); err == nil {
		t.Fatal("KickSession с пустым public_id должен вернуть ошибку")
	}
}

// KickOtherSessions — «выйти со всех устройств, кроме текущего».
func TestKickOtherSessions(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "others", "pass", "", false)
	stranger, _ := repo.Create(ctx, "stranger", "pass", "", false)

	current, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	other1, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	other2, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	foreign, _ := repo.CreateSession(ctx, stranger.ID, auth.SessionMeta{})

	if err := repo.KickOtherSessions(ctx, user.ID, current); err != nil {
		t.Fatalf("KickOtherSessions: %v", err)
	}
	if _, err := repo.LookupSession(ctx, current); err != nil {
		t.Fatalf("текущая сессия должна остаться: %v", err)
	}
	for _, tok := range []string{other1, other2} {
		if _, err := repo.LookupSession(ctx, tok); err == nil {
			t.Fatal("остальные сессии пользователя должны быть завершены")
		}
	}
	if _, err := repo.LookupSession(ctx, foreign); err != nil {
		t.Fatalf("сессии других пользователей не должны пострадать: %v", err)
	}
}

// KickUserSessions удаляет все сессии по ID пользователя (ревокация при
// смене пароля администратором).
func TestKickUserSessionsByID(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "byid", "pass", "", false)
	t1, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	t2, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})

	if err := repo.KickUserSessions(ctx, user.ID); err != nil {
		t.Fatalf("KickUserSessions: %v", err)
	}
	for _, tok := range []string{t1, t2} {
		if _, err := repo.LookupSession(ctx, tok); err == nil {
			t.Fatal("все сессии пользователя должны быть завершены")
		}
	}
}

// Политика лимита сессий (план 78, п. 1.6): при превышении вытесняется
// старейшая по активности enterprise-сессия; конфигуратор не задет.
func TestSessionLimitDisplacesOldestEnterprise(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	if got := db.GetMaxSessionsPerUser(ctx); got != 0 {
		t.Fatalf("по умолчанию лимит должен быть 0 (безлимит), получено %d", got)
	}
	if err := db.SaveMaxSessionsPerUser(ctx, 2); err != nil {
		t.Fatalf("SaveMaxSessionsPerUser: %v", err)
	}
	if got := db.GetMaxSessionsPerUser(ctx); got != 2 {
		t.Fatalf("лимит должен сохраниться: %d", got)
	}

	user, _ := repo.Create(ctx, "limited", "pass", "", false)
	e1, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	e2, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	cfg, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})

	// Делаем e1 заведомо самой давней по активности.
	if _, err := db.Exec(ctx, `UPDATE _sessions SET last_seen_at = '2000-01-01 00:00:00' WHERE token = ?`, e1); err != nil {
		t.Fatalf("состарить last_seen: %v", err)
	}

	e3, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatalf("CreateSession при лимите должен вытеснять, а не отказывать: %v", err)
	}

	if _, err := repo.LookupSession(ctx, e1); err == nil {
		t.Fatal("самая давняя enterprise-сессия должна быть вытеснена")
	}
	for name, tok := range map[string]string{"вторая enterprise": e2, "новая enterprise": e3, "конфигуратор": cfg} {
		if _, err := repo.LookupSession(ctx, tok); err != nil {
			t.Fatalf("сессия «%s» не должна пострадать: %v", name, err)
		}
	}
}

// Лимит 1 воспроизводит поведение прежних версий, но только для Предприятия.
func TestSessionLimitOneKeepsConfigurator(t *testing.T) {
	repo, db, ctx := newTestRepoDB(t)
	if err := db.SaveMaxSessionsPerUser(ctx, 1); err != nil {
		t.Fatalf("SaveMaxSessionsPerUser: %v", err)
	}
	user, _ := repo.Create(ctx, "single2", "pass", "", false)

	cfg, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	e1, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	e2, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})

	if _, err := repo.LookupSession(ctx, e1); err == nil {
		t.Fatal("при лимите 1 прежняя enterprise-сессия должна быть вытеснена")
	}
	if _, err := repo.LookupSession(ctx, e2); err != nil {
		t.Fatalf("новая enterprise-сессия должна работать: %v", err)
	}
	if _, err := repo.LookupSession(ctx, cfg); err != nil {
		t.Fatalf("сессия конфигуратора не должна вытесняться лимитом: %v", err)
	}
}

// TouchSession обновляет last_seen_at не чаще раза в 5 минут (троттлинг).
func TestTouchSessionThrottled(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "toucher", "pass", "", false)
	token, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})

	lastSeen := func() time.Time {
		t.Helper()
		sessions, err := repo.ActiveSessions(ctx)
		if err != nil || len(sessions) != 1 {
			t.Fatalf("ActiveSessions: %v (%d строк)", err, len(sessions))
		}
		return sessions[0].LastSeenAt
	}

	// UTC: SQLite-драйвер хранит время без зоны, и parseSessionTime читает
	// его как UTC — локальное время не пережило бы round-trip.
	base := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	if err := repo.TouchSession(ctx, token, base); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	afterFirst := lastSeen()
	if !afterFirst.Equal(base) {
		t.Fatalf("last_seen_at должен обновиться: %v != %v", afterFirst, base)
	}

	// Через минуту — троттлинг, значение не меняется.
	if err := repo.TouchSession(ctx, token, base.Add(time.Minute)); err != nil {
		t.Fatalf("TouchSession (троттлинг): %v", err)
	}
	if got := lastSeen(); !got.Equal(afterFirst) {
		t.Fatalf("троттлинг: last_seen_at не должен меняться чаще 5 минут (%v)", got)
	}

	// Через 6 минут — обновление проходит.
	next := base.Add(6 * time.Minute)
	if err := repo.TouchSession(ctx, token, next); err != nil {
		t.Fatalf("TouchSession (после интервала): %v", err)
	}
	if got := lastSeen(); !got.Equal(next) {
		t.Fatalf("после интервала last_seen_at должен обновиться: %v != %v", got, next)
	}
}

// Миграция _sessions идемпотентна и поднимает базы со старой схемой.
func TestEnsureSchemaUpgradesLegacySessions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)

	// Схема до плана 78: _sessions без public_id/kind/created_at/....
	if _, err := db.Exec(ctx, `CREATE TABLE _users (
		id TEXT PRIMARY KEY, login TEXT UNIQUE NOT NULL, password_hash BLOB NOT NULL,
		full_name TEXT NOT NULL DEFAULT '', is_admin INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatalf("legacy _users: %v", err)
	}
	if _, err := db.Exec(ctx, `CREATE TABLE _sessions (
		token TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES _users(id) ON DELETE CASCADE,
		expires_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("legacy _sessions: %v", err)
	}

	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema на старой схеме: %v", err)
	}
	// Повторный вызов — идемпотентен.
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("повторный EnsureSchema: %v", err)
	}

	user, err := repo.Create(ctx, "legacy", "pass", "", false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatalf("CreateSession после миграции: %v", err)
	}
	if _, err := repo.LookupSession(ctx, token); err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	sessions, err := repo.ActiveSessions(ctx)
	if err != nil {
		t.Fatalf("ActiveSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Kind != auth.SessionKindEnterprise {
		t.Fatalf("ожидалась 1 enterprise-сессия, получено %+v", sessions)
	}
}

func TestRolesAssignAndPermissions(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "manager", "pass", "Менеджер", false)

	roles := []*auth.Role{{
		Name:        "Продажник",
		Description: "Работа с продажами",
		Permissions: auth.Permission{
			AIDataAccess: true,
			Catalogs:     map[string][]string{"Контрагенты": {"read", "write"}},
			Documents:    map[string][]string{"Реализация": {"read", "post"}},
		},
	}}
	if err := repo.SyncRoles(ctx, roles); err != nil {
		t.Fatalf("SyncRoles: %v", err)
	}
	if roles[0].ID == "" {
		t.Fatal("SyncRoles должен заполнить ID роли")
	}

	if err := repo.AssignRole(ctx, user.ID, roles[0].ID); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	// Повторное назначение идемпотентно.
	if err := repo.AssignRole(ctx, user.ID, roles[0].ID); err != nil {
		t.Fatalf("повторный AssignRole должен быть идемпотентным: %v", err)
	}

	loaded, err := repo.GetRolesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetRolesForUser: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "Продажник" {
		t.Fatalf("ожидалась 1 роль 'Продажник', получено %+v", loaded)
	}

	// Проверка прав через загруженные из БД роли (round-trip permissions JSON).
	user.Roles = loaded
	if !user.Has("catalog", "Контрагенты", "write") {
		t.Error("право catalog/Контрагенты/write должно сохраниться через БД")
	}
	if user.Has("document", "Реализация", "write") {
		t.Error("право document/Реализация/write не выдавалось")
	}
	if !user.AllowsAIDataAccess() {
		t.Error("ai_data_access роли должен сохраниться через БД")
	}

	ids, err := repo.GetUserRoleIDs(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserRoleIDs: %v", err)
	}
	if !ids[roles[0].ID] {
		t.Fatal("GetUserRoleIDs должен содержать назначенную роль")
	}

	// Снятие роли.
	if err := repo.UnassignRole(ctx, user.ID, roles[0].ID); err != nil {
		t.Fatalf("UnassignRole: %v", err)
	}
	if after, _ := repo.GetRolesForUser(ctx, user.ID); len(after) != 0 {
		t.Fatalf("после UnassignRole ролей быть не должно, получено %+v", after)
	}
}

// SyncRoles вызывается повторно (upsert по имени) — роль не дублируется.
func TestSyncRolesUpsert(t *testing.T) {
	repo, ctx := newTestRepo(t)

	r1 := []*auth.Role{{Name: "Кладовщик", Description: "v1"}}
	if err := repo.SyncRoles(ctx, r1); err != nil {
		t.Fatalf("SyncRoles v1: %v", err)
	}
	r2 := []*auth.Role{{Name: "Кладовщик", Description: "v2"}}
	if err := repo.SyncRoles(ctx, r2); err != nil {
		t.Fatalf("SyncRoles v2: %v", err)
	}

	all, err := repo.ListRoles(ctx)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ожидалась 1 роль после upsert, получено %d", len(all))
	}
	if all[0].Description != "v2" {
		t.Fatalf("описание должно обновиться до v2, получено %q", all[0].Description)
	}

	if err := repo.DeleteRoleByName(ctx, "Кладовщик"); err != nil {
		t.Fatalf("DeleteRoleByName: %v", err)
	}
	if all, _ := repo.ListRoles(ctx); len(all) != 0 {
		t.Fatalf("после удаления ролей быть не должно, получено %d", len(all))
	}
}

func TestListForSelectionFiltersByShowInList(t *testing.T) {
	repo, ctx := newTestRepo(t)
	visible, _ := repo.Create(ctx, "visible", "p", "", false)
	repo.Create(ctx, "hidden", "p", "", false)

	if err := repo.SetShowInList(ctx, visible.ID, true); err != nil {
		t.Fatalf("SetShowInList: %v", err)
	}

	sel, err := repo.ListForSelection(ctx)
	if err != nil {
		t.Fatalf("ListForSelection: %v", err)
	}
	if len(sel) != 1 || sel[0].Login != "visible" {
		t.Fatalf("в выборку должен попасть только 'visible', получено %+v", sel)
	}

	all, _ := repo.List(ctx)
	if len(all) != 2 {
		t.Fatalf("List должен вернуть всех (2), получено %d", len(all))
	}
}

func TestMiddlewareRequiresSessionWhenUsersExist(t *testing.T) {
	repo, ctx := newTestRepo(t)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFromContext(r.Context()) == nil && !auth.OpenAccessFromContext(r.Context()) {
			t.Fatal("anonymous pass-through must be explicitly marked as open access")
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := repo.Middleware(next)

	// Пока пользователей нет — middleware пропускает (первый запуск).
	rr0 := httptest.NewRecorder()
	handler.ServeHTTP(rr0, httptest.NewRequest(http.MethodGet, "/ui", nil))
	if rr0.Code != http.StatusOK {
		t.Fatalf("без пользователей должен быть проход, получено %d", rr0.Code)
	}

	// Появился пользователь — теперь нужна сессия.
	user, _ := repo.Create(ctx, "guard", "pass", "", false)

	reqNoCookie := httptest.NewRequest(http.MethodGet, "/ui", nil)
	reqNoCookie.Header.Set("Accept", "text/html")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, reqNoCookie)
	if rr1.Code != http.StatusFound {
		t.Fatalf("без cookie ожидался редирект 302, получено %d", rr1.Code)
	}

	// Валидная сессия — проход.
	token, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{})
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: token})
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusOK {
		t.Fatalf("с валидной сессией ожидался 200, получено %d", rr2.Code)
	}
}

func TestMiddlewareFailsClosedWhenAuthDatabaseErrors(t *testing.T) {
	repo, db, _ := newTestRepoDB(t)
	db.Close()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	for name, handler := range map[string]http.Handler{
		"session": repo.Middleware(next),
		"api":     repo.APITokenOrSessionMiddleware(next),
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/protected", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if called {
				t.Fatal("protected handler was called after auth database error")
			}
		})
	}
}

func TestManagedUserInvariants(t *testing.T) {
	repo, ctx := newTestRepo(t)

	if _, err := repo.CreateManaged(ctx, "user", "pass", "", false); !errors.Is(err, auth.ErrFirstUserMustBeAdmin) {
		t.Fatalf("first non-admin error=%v, want ErrFirstUserMustBeAdmin", err)
	}
	admin1, err := repo.CreateManaged(ctx, "admin1", "pass", "", true)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.CreateManaged(ctx, "user", "pass", "", false)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.Update(ctx, admin1.ID, "", false, false, false, false); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("demote last admin error=%v, want ErrLastAdmin", err)
	}
	if err := repo.Delete(ctx, admin1.ID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("delete last admin error=%v, want ErrLastAdmin", err)
	}
	if err := repo.Delete(ctx, user.ID); err != nil {
		t.Fatalf("delete ordinary user: %v", err)
	}
	if err := repo.Delete(ctx, admin1.ID); !errors.Is(err, auth.ErrLastUser) {
		t.Fatalf("delete final user error=%v, want ErrLastUser", err)
	}

	admin2, err := repo.CreateManaged(ctx, "admin2", "pass", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, admin1.ID, "", false, false, false, false); err != nil {
		t.Fatalf("demote admin while another remains: %v", err)
	}
	if err := repo.Delete(ctx, admin2.ID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("delete sole remaining admin error=%v, want ErrLastAdmin", err)
	}
}

func TestAPITokenOrSessionMiddleware(t *testing.T) {
	repo, ctx := newTestRepo(t)
	user, _ := repo.Create(ctx, "guard", "pass", "", false)
	rawToken := ""
	_, rawToken, err := repo.CreateAPIToken(ctx, "integration", user.ID, nil)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil || u.Login != "guard" {
			t.Fatalf("expected authenticated guard user, got %+v", u)
		}
		w.WriteHeader(http.StatusOK)
	})
	handler := repo.APITokenOrSessionMiddleware(next)

	rrNoAuth := httptest.NewRecorder()
	handler.ServeHTTP(rrNoAuth, httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil))
	if rrNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("without token expected 401, got %d", rrNoAuth.Code)
	}

	reqBad := httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil)
	reqBad.Header.Set("Authorization", "Bearer wrong")
	rrBad := httptest.NewRecorder()
	handler.ServeHTTP(rrBad, reqBad)
	if rrBad.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer expected 401, got %d", rrBad.Code)
	}

	reqBearer := httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+rawToken)
	rrBearer := httptest.NewRecorder()
	handler.ServeHTTP(rrBearer, reqBearer)
	if rrBearer.Code != http.StatusOK {
		t.Fatalf("valid bearer expected 200, got %d", rrBearer.Code)
	}

	sessionToken, _ := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	reqCookie := httptest.NewRequest(http.MethodGet, "/api/v2/openapi.json", nil)
	reqCookie.AddCookie(&http.Cookie{Name: "onebase_session", Value: sessionToken})
	rrCookie := httptest.NewRecorder()
	handler.ServeHTTP(rrCookie, reqCookie)
	if rrCookie.Code != http.StatusOK {
		t.Fatalf("valid cookie session expected 200, got %d", rrCookie.Code)
	}
}

func TestLoadRolesYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: Бухгалтер
description: Ведение учёта
permissions:
  ai_data_access: true
  catalogs:
    Контрагенты: [read, write]
  documents:
    Платёж: [read, post, unpost]
`
	if err := os.WriteFile(filepath.Join(dir, "buh.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Файл не-yaml должен игнорироваться.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("WriteFile txt: %v", err)
	}

	roles, err := auth.LoadRolesYAML(dir)
	if err != nil {
		t.Fatalf("LoadRolesYAML: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("ожидалась 1 роль, получено %d", len(roles))
	}
	r := roles[0]
	if r.Name != "Бухгалтер" {
		t.Fatalf("имя роли: %q", r.Name)
	}
	if got := r.Permissions.Documents["Платёж"]; len(got) != 3 {
		t.Fatalf("ожидалось 3 операции для Платёж, получено %v", got)
	}
	if !r.Permissions.AIDataAccess {
		t.Fatal("ai_data_access должен разбираться из YAML роли")
	}

	// Несуществующая папка — не ошибка, пустой результат.
	none, err := auth.LoadRolesYAML(filepath.Join(dir, "nope"))
	if err != nil {
		t.Fatalf("LoadRolesYAML несуществующей папки не должен падать: %v", err)
	}
	if none != nil {
		t.Fatalf("ожидался nil для несуществующей папки, получено %+v", none)
	}
}
