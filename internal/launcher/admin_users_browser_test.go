package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/storage"
)

// renderCfgAdminUsersBrowserHTML проходит через production middleware и
// обработчик панели: Node ниже исполняет ровно тот cfgPost, который получает
// браузер, а не копию функции из теста.
func renderCfgAdminUsersBrowserHTML(t *testing.T, acceptLanguage string) (string, string) {
	t.Helper()
	ctx := context.Background()
	baseID := "cfg-users-browser"
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
	admin, err := repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Administrator", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	token, err := repo.CreateSession(ctx, admin.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	savedBundle := launcherBundle
	launcherBundle = bundle
	defer func() { launcherBundle = savedBundle }()

	store := baseOnSQLite(t, baseID, dbPath)
	h := &handler{store: store, runner: NewRunner()}
	req := httptest.NewRequest(http.MethodGet, "/bases/"+baseID+"/configurator/admin/users", nil)
	req.Header.Set("Accept-Language", acceptLanguage)
	req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
	req = requestWithBaseID(req, baseID)
	rec := httptest.NewRecorder()
	h.cfgAuthMiddleware(http.HandlerFunc(h.cfgAdminUsers)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render users panel: status=%d body=%q", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()

	body := strings.NewReader(`{"id":"` + admin.ID + `","password":"An0ther-Str0ng!"}`)
	req = httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/users/passwd", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
	req = requestWithBaseID(req, baseID)
	rec = httptest.NewRecorder()
	h.cfgAuthMiddleware(http.HandlerFunc(h.cfgAdminUserPasswd)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("change own password: status=%d body=%q", rec.Code, rec.Body.String())
	}
	return html, rec.Body.String()
}

func TestCfgAdminUsersPostBehaviorInNode(t *testing.T) {
	html, selfPasswordResponse := renderCfgAdminUsersBrowserHTML(t, "en")
	for _, want := range []string{
		"The Configurator session has ended — sign in again",
		"Unexpected server response",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered English panel does not contain %q", want)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	htmlPath := filepath.Join(t.TempDir(), "admin-users.html")
	if err := os.WriteFile(htmlPath, []byte(html), 0o600); err != nil {
		t.Fatalf("write rendered users panel: %v", err)
	}
	cmd := exec.Command(node, "--test", "admin_users_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	cmd.Env = append(os.Environ(),
		"ONEBASE_ADMIN_USERS_HTML="+htmlPath,
		"ONEBASE_SELF_PASSWORD_RESPONSE="+selfPasswordResponse,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node admin users behavior test: %v\n%s", err, output)
	}
}
