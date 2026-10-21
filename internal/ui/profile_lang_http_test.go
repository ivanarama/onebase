package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"golang.org/x/net/html"
)

type profileLangHTTPFixture struct {
	server *Server
	router http.Handler
	alice  *auth.User
	bob    *auth.User
}

func newProfileLangHTTPFixture(t *testing.T, withUsers bool) profileLangHTTPFixture {
	t.Helper()
	ctx := t.Context()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "profile-lang.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	s := New(runtime.NewRegistry(), db, interpreter.New(), repo, Config{Bundle: bundle, Lang: "ru"}, nil)
	t.Cleanup(s.Close)
	f := profileLangHTTPFixture{server: s}
	if withUsers {
		f.alice, err = repo.Create(ctx, "alice", "Profile-Lang-2026!", "Alice", false)
		if err != nil {
			t.Fatal(err)
		}
		f.bob, err = repo.Create(ctx, "bob", "Profile-Lang-2026!", "Bob", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SetDenyPasswdChange(ctx, f.alice.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	r := chi.NewRouter()
	r.Use(repo.Middleware)
	s.Mount(r)
	f.router = r
	return f
}

func (f profileLangHTTPFixture) session(t *testing.T, user *auth.User) string {
	t.Helper()
	token, err := f.server.authRepo.CreateSession(t.Context(), user.ID, auth.SessionMeta{Kind: auth.SessionKindEnterprise})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f profileLangHTTPFixture) request(token, method, path, body, referer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Referer", referer)
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "onebase_session", Value: token})
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

// Read the actual rendered control, including the distinction between the
// personal preference and the effective language inherited from the base.
func selectedProfileLanguage(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("GET UI: status=%d body=%s", response.Code, response.Body.String())
	}
	document, err := html.Parse(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	selected := "<missing>"
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, inSelect bool) {
		attributes := map[string]string{}
		for _, attr := range node.Attr {
			attributes[attr.Key] = attr.Val
		}
		if node.Type == html.ElementNode && node.Data == "select" {
			inSelect = attributes["id"] == "ob-profile-lang"
		}
		if inSelect && node.Type == html.ElementNode && node.Data == "option" {
			if _, ok := attributes["selected"]; ok {
				if selected != "<missing>" {
					t.Fatal("Multiple selected language options")
				}
				selected = attributes["value"]
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, inSelect)
		}
	}
	visit(document, false)
	return selected
}

func TestProfileLanguageHTTP_PersonalSelectionPersists(t *testing.T) {
	f := newProfileLangHTTPFixture(t, true)
	alice, bob := f.session(t, f.alice), f.session(t, f.bob)
	page := f.request(alice, http.MethodGet, "/ui/", "", "")
	if got := selectedProfileLanguage(t, page); got != "" {
		t.Fatalf("initial personal selection=%q, want base default", got)
	}
	for _, text := range []string{"Язык интерфейса", "Русский", "English", "Deutsch"} {
		if !strings.Contains(page.Body.String(), text) {
			t.Errorf("Language selector missing %q", text)
		}
	}
	body := url.Values{"lang": {" EN "}, "user_id": {f.bob.ID}}.Encode()
	saved := f.request(alice, http.MethodPost, "/ui/profile/lang", body, "http://example.com/ui/?subsystem=Sales")
	if saved.Code != http.StatusSeeOther || saved.Header().Get("Location") != "/ui/?subsystem=Sales" {
		t.Fatalf("save: status=%d location=%q", saved.Code, saved.Header().Get("Location"))
	}
	// Existing sessions and new logins both pick up the preference from storage.
	for _, token := range []string{alice, f.session(t, f.alice)} {
		page = f.request(token, http.MethodGet, "/ui/", "", "")
		if got := selectedProfileLanguage(t, page); got != "en" || !strings.Contains(page.Body.String(), "Interface language") {
			t.Fatalf("personal selection=%q; English UI not applied", got)
		}
	}
	if got := selectedProfileLanguage(t, f.request(bob, http.MethodGet, "/ui/", "", "")); got != "" {
		t.Fatalf("Another user's language changed to %q", got)
	}
	saved = f.request(alice, http.MethodPost, "/ui/profile/lang", "lang=", "")
	if saved.Code != http.StatusSeeOther || saved.Header().Get("Location") != "/ui/" {
		t.Fatalf("reset: status=%d location=%q", saved.Code, saved.Header().Get("Location"))
	}
	page = f.request(alice, http.MethodGet, "/ui/", "", "")
	if got := selectedProfileLanguage(t, page); got != "" || !strings.Contains(page.Body.String(), "Язык интерфейса") {
		t.Fatalf("base language not restored: selection=%q", got)
	}
}

func TestProfileLanguageHTTP_RejectsUnavailableLanguage(t *testing.T) {
	f := newProfileLangHTTPFixture(t, true)
	token := f.session(t, f.alice)
	for _, lang := range []string{"xx-unavailable", strings.Repeat("a", 300)} {
		response := f.request(token, http.MethodPost, "/ui/profile/lang", url.Values{"lang": {lang}}.Encode(), "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid language: status=%d", response.Code)
		}
	}
	if got := selectedProfileLanguage(t, f.request(token, http.MethodGet, "/ui/", "", "")); got != "" {
		t.Fatalf("rejected language was saved: %q", got)
	}
}

func TestProfileLanguageHTTP_ReportsSaveFailure(t *testing.T) {
	f := newProfileLangHTTPFixture(t, true)
	token := f.session(t, f.alice)
	if _, err := f.server.store.Exec(t.Context(), `CREATE TRIGGER reject_profile_lang
		BEFORE UPDATE OF lang ON _users BEGIN SELECT RAISE(ABORT, 'test save failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := f.request(token, http.MethodPost, "/ui/profile/lang", "lang=en", "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "Не удалось сохранить язык интерфейса") {
		t.Fatalf("save failure: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := selectedProfileLanguage(t, f.request(token, http.MethodGet, "/ui/", "", "")); got != "" {
		t.Fatalf("failed save changed the selection: %q", got)
	}
}

func TestProfileLanguageHTTP_NoPersonalSettingWithoutAccount(t *testing.T) {
	f := newProfileLangHTTPFixture(t, false)
	page := f.request("", http.MethodGet, "/ui/", "", "")
	if got := selectedProfileLanguage(t, page); got != "<missing>" {
		t.Fatalf("no-auth base shows personal language control: %q", got)
	}
	if response := f.request("", http.MethodPost, "/ui/profile/lang", "lang=en", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous change: status=%d", response.Code)
	}
}
