package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ivantit66/onebase/internal/fsmode"
)

// chromiumWorkplacePrefs — настройки профиля, которым лончер открывает окна
// Предприятия в установленном браузере (Edge/Chrome). Окно — рабочее место
// оператора, а не личный браузер: автозаполнение запоминало адреса и телефоны
// из прошлых заявок и предлагало «Сохранить адрес?» над формой, а менеджер
// паролей — «Сохранить пароль?» при входе. Ключи действуют только на этот
// отдельный профиль (--user-data-dir), личный браузер пользователя не меняется
// — в отличие от политик браузера в реестре.
var chromiumWorkplacePrefs = map[string]map[string]any{
	"autofill": {
		"profile_enabled":     false, // адреса, имена, телефоны
		"credit_card_enabled": false,
	},
	"profile": {
		"password_manager_enabled": false,
	},
}

// chromiumWorkplaceTopPrefs — ключи верхнего уровня Preferences.
var chromiumWorkplaceTopPrefs = map[string]any{
	"credentials_enable_service": false, // «Сохранить пароль?»
}

// seedChromiumPrefs сливает chromiumWorkplacePrefs в <profileDir>/Default/
// Preferences до запуска браузера: остальные настройки профиля сохраняются,
// файл пишется через временный и переименование. Повреждённый JSON не
// трогается — браузер сам его пересоздаст, а затирать чужое состояние нельзя.
// Профиль к этому моменту свободен: pickProfileDir выбирает незапущенный.
func seedChromiumPrefs(profileDir string) error {
	dir := filepath.Join(profileDir, "Default")
	path := filepath.Join(dir, "Preferences")
	prefs := map[string]any{}
	data, err := os.ReadFile(path) //nolint:gosec // G304: путь собран из каталога профиля лончера
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &prefs); err != nil {
			return fmt.Errorf("Preferences профиля %s: %w", profileDir, err)
		}
	case !os.IsNotExist(err):
		return err
	}
	for section, values := range chromiumWorkplacePrefs {
		sub, _ := prefs[section].(map[string]any)
		if sub == nil {
			sub = map[string]any{}
		}
		for k, v := range values {
			sub[k] = v
		}
		prefs[section] = sub
	}
	for k, v := range chromiumWorkplaceTopPrefs {
		prefs[k] = v
	}
	out, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, fsmode.Dir); err != nil {
		return err
	}
	tmp := path + ".onebase-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
