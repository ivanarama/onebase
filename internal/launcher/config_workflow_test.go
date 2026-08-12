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
		strings.NewReader(`{"login":"admin","password":"secret123","fullName":"Admin","isAdmin":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithBaseID(req, base.ID)
	rec := httptest.NewRecorder()
	h.cfgAdminUserCreate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == configuratorSessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("first admin response did not start configurator session: %v", rec.Header())
	}
	user, err := repo.LookupSessionKind(ctx, sessionCookie.Value, auth.SessionKindConfigurator)
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
		strings.NewReader(`{"login":"user","password":"secret123","isAdmin":false}`))
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

func TestConfiguratorLoginIsRateLimited(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rate-limit.db")
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
	if _, err := repo.CreateManaged(ctx, "admin", "secret123", "", true); err != nil {
		t.Fatal(err)
	}

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "limited-login", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	login := func() *httptest.ResponseRecorder {
		form := url.Values{"login": {"admin"}, "password": {"wrong-password"}}
		req := httptest.NewRequest(http.MethodPost, "/bases/limited-login/configurator/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "10.0.0.9:4567"
		req = requestWithBaseID(req, base.ID)
		rec := httptest.NewRecorder()
		h.cfgLoginSubmit(rec, req)
		return rec
	}

	for i := 0; i < 5; i++ {
		if rec := login(); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}
	rec := login()
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked login = %d Retry-After=%q, want 429 with retry", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestCfgAuthMiddlewareFailsClosedWhenDatabaseCannotOpen(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "broken-auth", Name: "Broken auth", DBType: "unsupported"}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	called := false
	protected := h.cfgAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := requestWithBaseID(httptest.NewRequest(http.MethodGet, "/bases/broken-auth/configurator", nil), base.ID)
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("protected configurator handler was called after auth database error")
	}
}

func TestCfgAuthMiddlewareRejectsEnterpriseSession(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "configurator-kind.db")
	t.Cleanup(CloseAuthPools)
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	user, err := repo.Create(ctx, "kind-admin", "secret123", "Kind Admin", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	enterprise, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	configurator, err := repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
	base := &Base{ID: "configurator-kind", Name: "Kind", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
	if err := store.save([]*Base{base}); err != nil {
		t.Fatal(err)
	}
	h := &handler{store: store, runner: NewRunner()}
	reached := false
	protected := h.cfgAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	enterpriseReq := requestWithBaseID(httptest.NewRequest(http.MethodGet,
		"/bases/"+base.ID+"/configurator", nil), base.ID)
	enterpriseReq.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: enterprise})
	enterpriseRec := httptest.NewRecorder()
	protected.ServeHTTP(enterpriseRec, enterpriseReq)
	if reached || enterpriseRec.Code != http.StatusFound {
		t.Fatalf("Enterprise token reached configurator: reached=%v status=%d", reached, enterpriseRec.Code)
	}

	configuratorReq := requestWithBaseID(httptest.NewRequest(http.MethodGet,
		"/bases/"+base.ID+"/configurator", nil), base.ID)
	configuratorReq.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: configurator})
	configuratorRec := httptest.NewRecorder()
	protected.ServeHTTP(configuratorRec, configuratorReq)
	if !reached || configuratorRec.Code != http.StatusNoContent {
		t.Fatalf("configurator token was rejected: reached=%v status=%d", reached, configuratorRec.Code)
	}
}

func TestCfgExclusiveAuthRecheckClosesAuthorizationRace(t *testing.T) {
	type testCase struct {
		name          string
		startWithUser bool
		mutateInGap   func(context.Context, *storage.DB, *auth.Repo, string) error
		wantReached   bool
		wantStatus    int
	}
	cases := []testCase{
		{
			name: "first user created after open-access check",
			mutateInGap: func(ctx context.Context, _ *storage.DB, repo *auth.Repo, _ string) error {
				_, err := repo.Create(ctx, "first-admin", "secret123", "First Admin", true)
				return err
			},
			wantStatus: http.StatusFound,
		},
		{
			name:          "configurator session revoked in gap",
			startWithUser: true,
			mutateInGap: func(ctx context.Context, _ *storage.DB, repo *auth.Repo, token string) error {
				return repo.DeleteSession(ctx, token)
			},
			wantStatus: http.StatusFound,
		},
		{
			name:          "configurator session changed to wrong kind in gap",
			startWithUser: true,
			mutateInGap: func(ctx context.Context, db *storage.DB, _ *auth.Repo, _ string) error {
				_, err := db.Exec(ctx, `UPDATE _sessions SET kind = ?`, auth.SessionKindEnterprise)
				return err
			},
			wantStatus: http.StatusFound,
		},
		{
			name:          "current configurator admin remains authorized",
			startWithUser: true,
			wantReached:   true,
			wantStatus:    http.StatusNoContent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "exclusive-auth.db")
			db, err := storage.ConnectSQLite(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			repo := auth.NewRepo(db)
			if err := repo.EnsureSchema(ctx); err != nil {
				db.Close()
				t.Fatal(err)
			}
			var token string
			if tc.startWithUser {
				user, createErr := repo.Create(ctx, "exclusive-admin", "secret123", "Exclusive Admin", true)
				if createErr != nil {
					db.Close()
					t.Fatal(createErr)
				}
				token, err = repo.CreateSession(ctx, user.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
				if err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()

			store := &Store{path: filepath.Join(t.TempDir(), "ibases.yaml")}
			base := &Base{ID: "exclusive-auth-" + strings.ReplaceAll(tc.name, " ", "-"),
				Name: "Exclusive auth", ConfigSource: "database", DBType: "sqlite", DBPath: dbPath}
			if err := store.save([]*Base{base}); err != nil {
				t.Fatal(err)
			}
			h := &handler{store: store, runner: NewRunner()}
			t.Cleanup(CloseAuthPools)

			reached := false
			final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusNoContent)
			})
			chain := h.cfgAuthExclusiveRecheckMiddleware(final)
			chain = h.cfgDBExclusiveMiddleware(chain)
			if tc.mutateInGap != nil {
				next := chain
				chain = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gapDB, gapErr := storage.ConnectSQLite(r.Context(), dbPath)
					if gapErr != nil {
						t.Fatalf("open gap database: %v", gapErr)
					}
					gapRepo := auth.NewRepo(gapDB)
					gapErr = tc.mutateInGap(r.Context(), gapDB, gapRepo, token)
					gapDB.Close()
					if gapErr != nil {
						t.Fatalf("mutate authorization in gap: %v", gapErr)
					}
					next.ServeHTTP(w, r)
				})
			}
			chain = h.cfgAuthMiddleware(chain)

			req := requestWithBaseID(httptest.NewRequest(http.MethodPost,
				"/bases/"+base.ID+"/configurator/backup/full-import", nil), base.ID)
			if token != "" {
				req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
			}
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)

			if reached != tc.wantReached || rec.Code != tc.wantStatus {
				t.Fatalf("exclusive auth result: reached=%v status=%d, want reached=%v status=%d body=%s",
					reached, rec.Code, tc.wantReached, tc.wantStatus, rec.Body.String())
			}
		})
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
