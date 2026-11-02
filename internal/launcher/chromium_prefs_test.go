package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
