package launcher

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readChromiumPrefs(t *testing.T, profileDir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(profileDir, "Default", "Preferences"))
	if err != nil {
		t.Fatalf("Preferences профиля: %v", err)
	}
	prefs := map[string]any{}
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatalf("Preferences не JSON: %v", err)
	}
	return prefs
}

func assertWorkplacePrefs(t *testing.T, prefs map[string]any) {
	t.Helper()
	autofill, _ := prefs["autofill"].(map[string]any)
	profile, _ := prefs["profile"].(map[string]any)
	if autofill["profile_enabled"] != false || autofill["credit_card_enabled"] != false {
		t.Errorf("автозаполнение не выключено: %v", autofill)
	}
	if prefs["credentials_enable_service"] != false || profile["password_manager_enabled"] != false {
		t.Errorf("сохранение паролей не выключено: %v / %v", prefs["credentials_enable_service"], profile)
	}
}

// Окно Предприятия в браузере открывается профилем без автозаполнения и
// сохранения паролей — через обработчик запуска изолированного окна.
func TestStartIsolated_ProfileWithoutAutofill(t *testing.T) {
	fb := &fakeBrowser{}
	h, b := newIsolatedFixture(t, fb)
	if rec := startIsolatedModeReq(t, h, b.ID, "browser"); rec.Code != 200 {
		t.Fatalf("код %d: %s", rec.Code, rec.Body.String())
	}
	assertWorkplacePrefs(t, readChromiumPrefs(t, fb.dir))
}

// Уже открывавшийся профиль: прочие настройки сохраняются, включённое
// пользователем автозаполнение выключается.
func TestStartIsolated_ProfilePrefsMerged(t *testing.T) {
	fb := &fakeBrowser{}
	h, b := newIsolatedFixture(t, fb)
	root, err := profilesRoot(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	def := filepath.Join(root, "profile-1", "Default")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"browser":{"window_placement":{"left":10}},"autofill":{"profile_enabled":true,"last_version_deduped":5},"intl":{"accept_languages":"ru"}}`
	if err := os.WriteFile(filepath.Join(def, "Preferences"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := startIsolatedModeReq(t, h, b.ID, "browser"); rec.Code != 200 {
		t.Fatalf("код %d", rec.Code)
	}
	prefs := readChromiumPrefs(t, fb.dir)
	assertWorkplacePrefs(t, prefs)
	autofill := prefs["autofill"].(map[string]any)
	if autofill["last_version_deduped"] != float64(5) {
		t.Errorf("прочий ключ autofill потерян: %v", autofill)
	}
	if intl, _ := prefs["intl"].(map[string]any); intl["accept_languages"] != "ru" {
		t.Errorf("прочая настройка профиля потеряна: %v", prefs["intl"])
	}
	if _, ok := prefs["browser"].(map[string]any); !ok {
		t.Errorf("раздел browser потерян: %v", prefs)
	}
}

// Повреждённый Preferences не перезаписывается, а окно всё равно открывается.
func TestStartIsolated_BrokenPrefsUntouched(t *testing.T) {
	fb := &fakeBrowser{}
	h, b := newIsolatedFixture(t, fb)
	root, _ := profilesRoot(b.ID)
	def := filepath.Join(root, "profile-1", "Default")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(def, "Preferences"), []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := startIsolatedModeReq(t, h, b.ID, "browser"); rec.Code != 200 || fb.calls != 1 {
		t.Fatalf("окно должно открыться и при битом профиле: код %d, вызовов %d", rec.Code, fb.calls)
	}
	if data, _ := os.ReadFile(filepath.Join(def, "Preferences")); string(data) != "{не json" {
		t.Errorf("битый Preferences перезаписан: %q", data)
	}
}

// Некорректный корень Preferences не должен ни мешать открытию окна,
// ни заменяться объектом настроек. Проверяется настоящий HTTP-маршрут лаунчера.
func TestStartIsolated_NonObjectPrefsHTTP(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"null", "null"},
		{"null_whitespace", " \nnull\t "},
		{"array", "[]"},
		{"string", `"profile"`},
		{"boolean", "false"},
		{"number", "42"},
		{"broken", "{не json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeBrowser{}
			h, b := newIsolatedFixture(t, fb)
			root, err := profilesRoot(b.ID)
			if err != nil {
				t.Fatal(err)
			}
			profileDir := filepath.Join(root, "profile-1")
			def := filepath.Join(profileDir, "Default")
			if err := os.MkdirAll(def, 0o755); err != nil {
				t.Fatal(err)
			}
			prefsPath := filepath.Join(def, "Preferences")
			original := []byte(tc.data)
			if err := os.WriteFile(prefsPath, original, 0o600); err != nil {
				t.Fatal(err)
			}

			savedBundle := launcherBundle
			t.Cleanup(func() { launcherBundle = savedBundle })
			srv, err := NewServer(h.store, h.runner)
			if err != nil {
				t.Fatal(err)
			}
			srv.h.isoBrowser = fb
			done := make(chan error, 1)
			go func() { done <- srv.ListenAndServe() }()
			client := &http.Client{Timeout: 10 * time.Second}
			t.Cleanup(func() {
				client.CloseIdleConnections()
				srv.Close()
				if err := <-done; err != nil && err != http.ErrServerClosed {
					t.Error(err)
				}
			})
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				srv.URL()+"/bases/"+b.ID+"/start-isolated?mode=browser", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", srv.URL())
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, res.Body)
			closeErr := res.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read=%v close=%v", readErr, closeErr)
			}
			if res.StatusCode != http.StatusOK || fb.calls != 1 {
				t.Fatalf("окно должно открыться: HTTP %d, Browser.Open=%d", res.StatusCode, fb.calls)
			}
			if fb.dir != profileDir || fb.mode != "browser" || fb.url != h.runner.BaseURL(b)+"/ui" {
				t.Fatalf("открыто другое окно: dir=%q mode=%q url=%q", fb.dir, fb.mode, fb.url)
			}
			data, err := os.ReadFile(prefsPath)
			if err != nil || !bytes.Equal(data, original) {
				t.Fatalf("Preferences изменён: %q, ошибка %v", data, err)
			}
			if _, err := os.Stat(prefsPath + ".onebase-tmp"); !os.IsNotExist(err) {
				t.Fatalf("после отклонённого Preferences остался временный файл: %v", err)
			}
		})
	}
}
