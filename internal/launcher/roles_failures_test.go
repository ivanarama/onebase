package launcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/storage"
)

// baseOnSQLite регистрирует базу, указывающую на файл SQLite по пути dbPath.
func baseOnSQLite(t *testing.T, id, dbPath string) *Store {
	t.Helper()
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: id, Name: id, ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseAuthPools)
	return store
}

func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("ответ не JSON: %v (%q)", err, rec.Body.String())
	}
	return out
}

// Сохранение ролей считает диф от текущего состояния. Пока ошибка чтения
// глоталась, недоступные таблицы давали пустой current и пустой allRoles: цикл
// не выполнял ни одного назначения и ни одного снятия, а ответ был {"ok":true}.
// То есть админ снимал роль, получал подтверждение — и роль оставалась.
//
// Обработчик не вызывает EnsureSchema, поэтому база без схемы воспроизводит
// сбой чтения точно и без хрупких трюков с правами файлов.
func TestUserRolesSaveReportsReadFailureInsteadOfOK(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "no-schema.db")
	store := baseOnSQLite(t, "roles-save", dbPath)

	body := strings.NewReader(`{"userId":"u1","roleIds":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/bases/roles-save/configurator/admin/user-roles", body)
	req = requestWithBaseID(req, "roles-save")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).cfgAdminUserRolesSave(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("сбой чтения ролей выдан за успех: код 200, тело %q", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код = %d, ожидался 500; тело %q", rec.Code, rec.Body.String())
	}
	if out := jsonBody(t, rec); out["ok"] != nil {
		t.Errorf("в ответе не должно быть ok: %v", out)
	}
}

// Смена пароля из конфигуратора обязана завершить сессии пользователя — ради
// этого она и завершает их (план 78). Если отзыв не удался, пароль уже сменён,
// а украденная сессия продолжает работать. Раньше обработчик отвечал
// {"ok":true} и писал в аудит «сессии отозваны» — то есть журнал утверждал
// ровно то, чего не произошло.
//
// Сбой воспроизводится удалением таблицы _sessions: смена пароля (таблица
// _users на месте) проходит, отзыв — нет.
func TestUserPasswdReportsSessionsNotRevoked(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "no-sessions.db")

	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	user, err := repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Администратор", true)
	if err != nil {
		db.Close()
		t.Fatalf("Create: %v", err)
	}
	if _, err := db.Exec(ctx, `DROP TABLE _sessions`); err != nil {
		db.Close()
		t.Fatalf("DROP TABLE _sessions: %v", err)
	}
	db.Close()

	store := baseOnSQLite(t, "passwd-kick", dbPath)

	body := strings.NewReader(`{"id":"` + user.ID + `","password":"An0ther-Str0ng!"}`)
	req := httptest.NewRequest(http.MethodPost, "/bases/passwd-kick/configurator/admin/passwd", body)
	req = requestWithBaseID(req, "passwd-kick")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).cfgAdminUserPasswd(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("незавершённые сессии выданы за успех: код 200, тело %q", rec.Body.String())
	}
	out := jsonBody(t, rec)
	if out["ok"] != nil {
		t.Errorf("в ответе не должно быть ok: %v", out)
	}
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "сесси") && !strings.Contains(strings.ToLower(msg), "session") {
		t.Errorf("ошибка должна называть незавершённые сессии, получено %q", msg)
	}
}
