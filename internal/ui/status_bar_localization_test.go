package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
)

func mountedLocalizedStatusBarTestServer(t *testing.T, original *Server, lang, role string) *httptest.Server {
	t.Helper()
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	s := New(original.reg, original.store, original.interp, nil, Config{Bundle: bundle, Lang: lang}, nil)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := statusBarUser(role)
			u.Lang = lang
			next.ServeHTTP(w, r.WithContext(auth.ContextWithUser(r.Context(), u)))
		})
	})
	s.MountStatic(router)
	s.Mount(router)
	server := httptest.NewServer(router)
	t.Cleanup(func() { s.CloseEventStreams(); server.Close(); s.Close() })
	return server
}

func TestStatusBarLocalizedErrors(t *testing.T) {
	for _, tc := range []struct {
		lang, network, hidden, missing, unavailable, procedure string
	}{
		{"en", "No connection to the server", "The button is currently unavailable", "Button not found", "Status bar unavailable", "status bar: procedure «Absent.Source» was not found in shared modules"},
		{"ru", "Нет связи с сервером", "кнопка сейчас недоступна", "кнопка не найдена", "строка состояния недоступна", "строка состояния: процедура «Absent.Source» не найдена в общих модулях"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			s := newStatusBarTestServer(t)
			server := mountedLocalizedStatusBarTestServer(t, s, tc.lang, "Оператор")
			request := func(method, target string, form url.Values) (int, string) {
				t.Helper()
				var data io.Reader
				if form != nil {
					data = strings.NewReader(form.Encode())
				}
				req, err := http.NewRequest(method, target, data)
				if err != nil {
					t.Fatal(err)
				}
				if form != nil {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = response.Body.Close() }()
				body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
				if err != nil {
					t.Fatal(err)
				}
				return response.StatusCode, string(body)
			}
			_, html := request(http.MethodGet, server.URL+"/ui/", nil)
			match := regexp.MustCompile(`(?s)<script type="application/json" id="ob-ui-messages">(.*?)</script>`).FindStringSubmatch(html)
			if len(match) != 2 {
				t.Fatal("public page has no UI messages")
			}
			var messages map[string]string
			if err := json.Unmarshal([]byte(match[1]), &messages); err != nil {
				t.Fatal(err)
			}
			if got := messages["statusBarNetworkError"]; got != tc.network {
				t.Errorf("page network message = %q, want %q", got, tc.network)
			}
			for _, button := range []struct {
				name, want string
				status     int
			}{{"НаЛинии", tc.hidden, http.StatusConflict}, {"Absent", tc.missing, http.StatusNotFound}} {
				status, body := request(http.MethodPost, server.URL+"/ui/_status-bar", url.Values{"button": {button.name}})
				body = strings.TrimSpace(body)
				if status != button.status || body != button.want {
					t.Errorf("POST %s: %d %q, want %d %q", button.name, status, body, button.status, button.want)
				}
			}
			denied := mountedLocalizedStatusBarTestServer(t, newStatusBarTestServer(t), tc.lang, "Кладовщик")
			status, body := request(http.MethodPost, denied.URL+"/ui/_status-bar", url.Values{"button": {"НаЛинии"}})
			body = strings.TrimSpace(body)
			if status != http.StatusNotFound || body != tc.unavailable {
				t.Errorf("denied POST: %d %q, want 404 %q", status, body, tc.unavailable)
			}
			s.reg.HomePage().StatusBar.Source = "Absent.Source"
			_, body = request(http.MethodGet, server.URL+"/ui/_status-bar", nil)
			var state statusBarState
			if err := json.Unmarshal([]byte(body), &state); err != nil {
				t.Fatal(err)
			}
			if state.Error != tc.procedure {
				t.Errorf("missing source = %q, want %q", state.Error, tc.procedure)
			}
		})
	}
}
