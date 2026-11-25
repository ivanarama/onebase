package launcher

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

type domainConfigurator struct {
	repo          *auth.Repo
	operator      *auth.User
	adminToken    string
	ordinaryToken string
	client        *http.Client
	origin        string
	adminPath     string
}

func newDomainConfigurator(t *testing.T, baseID string) *domainConfigurator {
	t.Helper()
	h, base, repo, adminTokens := newSessionsFixture(t, baseID)
	operator, err := repo.Create(t.Context(), "operator", "onebase-test-password", "", false)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryToken, err := repo.CreateSession(t.Context(), operator.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
	if err != nil {
		t.Fatal(err)
	}
	savedBundle := launcherBundle
	t.Cleanup(func() { launcherBundle = savedBundle })
	srv, err := NewServer(h.store, NewRunner())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		srv.Close()
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	return &domainConfigurator{
		repo: repo, operator: operator, adminToken: adminTokens[1], ordinaryToken: ordinaryToken,
		client: client, origin: srv.URL(), adminPath: "/bases/" + base.ID + "/configurator/admin/",
	}
}

// A nil payload reads a panel. A map submits the same JSON as the Configurator.
func (f *domainConfigurator) request(t *testing.T, path, token string, payload map[string]any, wantStatus int) string {
	t.Helper()
	method := http.MethodGet
	var body bytes.Buffer
	if payload != nil {
		method = http.MethodPost
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.origin+f.adminPath+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", f.origin)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, resp.StatusCode, wantStatus, data)
	}
	if method == http.MethodPost && wantStatus == http.StatusOK {
		var result struct{ OK bool }
		if err := json.Unmarshal(data, &result); err != nil || !result.OK {
			t.Fatalf("%s: expected JSON success, got %s", path, data)
		}
	}
	return string(data)
}

func TestCfgAdminDomainSettings(t *testing.T) {
	t.Setenv("ONEBASE_PUBLIC_URL", "https://previous.example.test")
	t.Setenv("ONEBASE_WINDOWS_SPN", "HTTP/previous.example.test")
	t.Setenv("ONEBASE_KRB5_REALM", "")
	t.Setenv("ONEBASE_KRB5_KEYTAB", "")
	f := newDomainConfigurator(t, "domain-settings")
	config := map[string]any{
		"public_url": "https://erp.example.test",
		"spn":        "HTTP/erp.example.test",
		"realm":      "EXAMPLE.TEST",
		"keytab":     "/etc/onebase/erp.keytab",
	}
	clear := map[string]any{"public_url": "", "spn": "", "realm": "", "keytab": ""}
	checkForm := func(want map[string]any) {
		t.Helper()
		page := f.request(t, "settings", f.adminToken, nil, http.StatusOK)
		fields := browserSubmit(t, page, "/configurator/admin/settings/domain")
		for name, expected := range want {
			if !fields.Has(name) || fields.Get(name) != expected {
				t.Errorf("Configurator field %q = %q, want %q", name, fields[name], expected)
			}
		}
	}

	const save = "settings/domain"
	checkForm(clear)
	f.request(t, save, f.adminToken, config, http.StatusOK)
	checkForm(config)
	if runtime.GOOS == "linux" {
		page := f.request(t, "users", f.adminToken, nil, http.StatusOK)
		if !strings.Contains(page, `data-user-id="`+f.operator.ID+`"`) {
			t.Fatal("saving Linux domain settings did not enable the account editor for this base")
		}
	}
	f.request(t, save, f.ordinaryToken, clear, http.StatusFound)
	f.request(t, save, f.adminToken, map[string]any{"realm": ""}, http.StatusBadRequest)
	invalid := map[string]any{"public_url": "://invalid", "spn": "", "realm": "", "keytab": ""}
	f.request(t, save, f.adminToken, invalid, http.StatusBadRequest)
	checkForm(config)
	f.request(t, save, f.adminToken, clear, http.StatusOK)
	checkForm(clear)
	want := auth.DomainSettings{PublicURL: "https://previous.example.test", SPN: "HTTP/previous.example.test"}
	if current, err := f.repo.EffectiveDomainSettings(t.Context()); err != nil || current != want {
		t.Fatalf("cleared domain settings should inherit the environment: got %+v, err=%v", current, err)
	}
}
