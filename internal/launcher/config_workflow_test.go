package launcher

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

func requestWithBaseID(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// Создание первого администратора происходит в конфигураторе, который до
// появления пользователей работает без сессии. Ответ должен сразу выдать
// configurator-cookie, иначе следующий AJAX-запрос загрузит форму входа внутрь
// панели, а её submit уйдёт POST-запросом на GET-only /configurator (HTTP 405).
func TestCfgAdminUserCreate_FirstAdminStartsConfiguratorSession(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "first-admin.db")
	t.Cleanup(CloseAuthPools)
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "first-admin-base", Name: "First admin", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}

	req := httptest.NewRequest(http.MethodPost, "/bases/first-admin-base/configurator/admin/users/create",
		strings.NewReader(`{"login":"admin","password":"secret","fullName":"Admin","isAdmin":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithBaseID(req, base.ID)
	rec := httptest.NewRecorder()
	h.cfgAdminUserCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "onebase_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("first admin response did not start configurator session: %v", rec.Header())
	}
	user, err := repo.LookupSession(ctx, sessionCookie.Value)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if user.Login != "admin" || !user.IsAdmin {
		t.Fatalf("session user=%+v", user)
	}
}

func TestCfgAdminUserCreate_FirstUserMustBeAdmin(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "first-user.db")
	t.Cleanup(CloseAuthPools)
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "first-user-base", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	req := httptest.NewRequest(http.MethodPost, "/bases/first-user-base/configurator/admin/users/create",
		strings.NewReader(`{"login":"user","password":"secret","isAdmin":false}`))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithBaseID(req, base.ID)
	rec := httptest.NewRecorder()
	h.cfgAdminUserCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if has, err := repo.HasUsers(ctx); err != nil || has {
		t.Fatalf("first non-admin must not be created: has=%v err=%v", has, err)
	}
}

// Пустая/неверная папка не должна очищать _onebase_config. Раньше
// ImportFromDir сначала делал DELETE, а пустой workspace считался успешным
// импортом нулевой конфигурации.
func TestConfigImport_RejectsFolderWithoutAppConfigAndPreservesDatabase(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "config.db")
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config", "app.yaml"), []byte("name: Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repo.ImportFromDir(ctx, source); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "safe-import-base", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	form := url.Values{"path": {t.TempDir()}}
	req := httptest.NewRequest(http.MethodPost, "/bases/safe-import-base/config/import", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = requestWithBaseID(req, base.ID)
	rec := httptest.NewRecorder()
	h.configImport(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "config/app.yaml") {
		t.Fatalf("expected invalid config folder error, status=%d body=%s", rec.Code, rec.Body.String())
	}
	checkDB, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer checkDB.Close()
	content, ok, err := configdb.New(checkDB).ReadFile(ctx, "config/app.yaml")
	if err != nil || !ok || !bytes.Contains(content, []byte("Existing")) {
		t.Fatalf("existing config was damaged: ok=%v content=%q err=%v", ok, content, err)
	}
}

func TestConfigResult_UsesConfiguratorBackURL(t *testing.T) {
	var out bytes.Buffer
	err := tmpl.ExecuteTemplate(&out, "page-config-result", map[string]any{
		"Title":   "Result",
		"BackURL": "/bases/base-id/configurator?tab=files",
		"Lang":    "ru",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `href="/bases/base-id/configurator?tab=files"`) {
		t.Fatalf("configurator back URL missing: %s", out.String())
	}
}
