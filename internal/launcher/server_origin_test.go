package launcher

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/websec"
)

func TestLauncherBrowserOriginIsCookieIsolatedFromBasePorts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &Server{ln: ln}
	launcherURL, err := url.Parse(srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	if launcherURL.Hostname() != "localhost" {
		t.Fatalf("launcher cookie host = %q, want localhost", launcherURL.Hostname())
	}
	entryURL, err := url.Parse(srv.EntryURL())
	if err != nil {
		t.Fatal(err)
	}
	if entryURL.Hostname() != "127.0.0.1" || entryURL.Path != launcherCookieMigrationPath {
		t.Fatalf("launcher entry URL = %q, want legacy host plus migration path", entryURL)
	}
	baseURL, err := url.Parse(NewRunner().BaseURL(&Base{Port: 8080}))
	if err != nil {
		t.Fatal(err)
	}
	if baseURL.Hostname() != "127.0.0.1" || baseURL.Hostname() == launcherURL.Hostname() {
		t.Fatalf("base host %q is not isolated from launcher host %q",
			baseURL.Hostname(), launcherURL.Hostname())
	}
}

func TestLauncherRejectsDNSRebindingHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := &Server{ln: ln}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := srv.requireLauncherHost(websec.CSRFProtect(next))

	for _, host := range []string{"localhost:" + port, "LOCALHOST:" + port, "127.0.0.1:" + port} {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/close-stop", nil)
		req.Host = host
		req.Header.Set("Origin", "http://"+host)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("allowed Host %q status = %d, want %d", host, rec.Code, http.StatusNoContent)
		}
	}

	// Origin == Host would pass the ordinary same-origin CSRF check. The
	// launcher authority allowlist must still reject a domain rebound to this
	// listener, a wrong port, and a host without the listener port.
	for _, host := range []string{"attacker.example:" + port, "localhost:65535", "localhost"} {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/close-stop", nil)
		req.Host = host
		req.Header.Set("Origin", "http://"+host)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("rebound Host %q status = %d, want %d", host, rec.Code, http.StatusMisdirectedRequest)
		}
	}
}

func TestLauncherEntryClearsLegacyCookiesBeforeRedirectingToCanonicalOrigin(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{ln: ln}

	var (
		mu                    sync.Mutex
		migrationRequestNames []string
		canonicalRequestNames []string
	)
	mux := http.NewServeMux()
	mux.HandleFunc(launcherCookieMigrationPath, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		for _, cookie := range r.Cookies() {
			migrationRequestNames = append(migrationRequestNames, cookie.Name)
		}
		mu.Unlock()
		srv.migrateLegacyLauncherCookies(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		for _, cookie := range r.Cookies() {
			canonicalRequestNames = append(canonicalRequestNames, cookie.Name)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	entryURL, err := url.Parse(srv.EntryURL())
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(entryURL, []*http.Cookie{
		{Name: legacySharedSessionCookieName, Value: "legacy-configurator", Path: "/"},
		{Name: configuratorSessionCookieName, Value: "pre-release-configurator", Path: "/"},
		{Name: "unrelated", Value: "keep", Path: "/"},
	})

	dialer := &net.Dialer{}
	client := &http.Client{
		Jar: jar,
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			// Keep the test independent of the machine's localhost IPv4/IPv6
			// resolver order while preserving URL hosts for cookie-jar scoping.
			return dialer.DialContext(ctx, network, ln.Addr().String())
		}},
	}
	t.Cleanup(client.CloseIdleConnections)

	resp, err := client.Get(entryURL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Request.URL.Hostname() != "localhost" {
		t.Fatalf("final response = %d at %q, want 204 at localhost", resp.StatusCode, resp.Request.URL)
	}

	mu.Lock()
	gotMigrationNames := append([]string(nil), migrationRequestNames...)
	gotCanonicalNames := append([]string(nil), canonicalRequestNames...)
	mu.Unlock()
	if !containsString(gotMigrationNames, legacySharedSessionCookieName) ||
		!containsString(gotMigrationNames, configuratorSessionCookieName) {
		t.Fatalf("legacy-origin request cookies = %v, want both session cookies", gotMigrationNames)
	}
	if containsString(gotCanonicalNames, legacySharedSessionCookieName) ||
		containsString(gotCanonicalNames, configuratorSessionCookieName) {
		t.Fatalf("canonical-origin request leaked a legacy cookie: %v", gotCanonicalNames)
	}

	baseURL, err := url.Parse("http://127.0.0.1:6553/")
	if err != nil {
		t.Fatal(err)
	}
	baseCookies := jar.Cookies(baseURL)
	if cookieValue(baseCookies, legacySharedSessionCookieName) != "" ||
		cookieValue(baseCookies, configuratorSessionCookieName) != "" {
		t.Fatalf("legacy cookies survived migration for base origin: %v", baseCookies)
	}
	if cookieValue(baseCookies, "unrelated") != "keep" {
		t.Fatalf("migration removed an unrelated cookie: %v", baseCookies)
	}

	// The path-scoped marker prevents every later launcher start from deleting
	// a fresh Enterprise session created after the one-time migration.
	jar.SetCookies(baseURL, []*http.Cookie{{
		Name: legacySharedSessionCookieName, Value: "fresh-enterprise", Path: "/",
	}})
	resp, err = client.Get(entryURL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := cookieValue(jar.Cookies(baseURL), legacySharedSessionCookieName); got != "fresh-enterprise" {
		t.Fatalf("repeat launcher entry removed fresh Enterprise session: got %q", got)
	}
}

func TestQuitRespondsBeforeSignalingDone(t *testing.T) {
	var scheduled func()
	srv := &Server{
		quit: make(chan struct{}),
		scheduleQuit: func(delay time.Duration, fn func()) {
			if delay != launcherQuitDelay {
				t.Fatalf("quit delay = %s, want %s", delay, launcherQuitDelay)
			}
			scheduled = fn
		},
	}
	rec := httptest.NewRecorder()

	srv.handleQuit(rec, httptest.NewRequest(http.MethodPost, "/quit", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("quit status = %d, want 200", rec.Code)
	}
	var response map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil || !response["ok"] {
		t.Fatalf("quit response = %q, err=%v", rec.Body.String(), err)
	}
	select {
	case <-srv.Done():
		t.Fatal("quit signaled Done before the response could complete")
	default:
	}
	if scheduled == nil {
		t.Fatal("quit signal was not scheduled")
	}
	scheduled()
	select {
	case <-srv.Done():
	default:
		t.Fatal("scheduled quit did not signal Done")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}
