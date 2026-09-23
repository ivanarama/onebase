package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/storage"
)

// policyBase готовит базу с администратором и возвращает вызовы двух публичных
// точек входа конфигуратора: сохранение «Параметров базы» и смена пароля.
func policyBase(t *testing.T, baseID string) (userID string, saveSettings func(string) *httptest.ResponseRecorder, passwd func(string) *httptest.ResponseRecorder, settingsHTML func(...string) string, dbPath string) {
	t.Helper()
	ctx := context.Background()
	dbPath = filepath.Join(t.TempDir(), baseID+".db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	user, err := repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Администратор", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := baseOnSQLite(t, baseID, dbPath)
	h := &handler{store: store, runner: NewRunner()}
	saveSettings = func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/settings/save", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = requestWithBaseID(req, baseID)
		rec := httptest.NewRecorder()
		h.cfgAdminSettingsSave(rec, req)
		return rec
	}
	passwd = func(password string) *httptest.ResponseRecorder {
		body := `{"id":"` + user.ID + `","password":"` + password + `"}`
		req := httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/users/passwd", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = requestWithBaseID(req, baseID)
		rec := httptest.NewRecorder()
		h.cfgAdminUserPasswd(rec, req)
		return rec
	}
	settingsHTML = func(langs ...string) string {
		req := httptest.NewRequest(http.MethodGet, "/bases/"+baseID+"/configurator/admin/settings", nil)
		if len(langs) > 0 {
			req.Header.Set("Accept-Language", langs[0])
		}
		req = requestWithBaseID(req, baseID)
		rec := httptest.NewRecorder()
		h.cfgAdminSettings(rec, req)
		return rec.Body.String()
	}
	return user.ID, saveSettings, passwd, settingsHTML, dbPath
}

// Ради этого всё и делается: администратор разрешает пустые пароли в
// «Параметрах базы» — и пустой пароль принимается тут же, без перезапуска
// лаунчера с переменной окружения.
func TestSettingsPasswordPolicyEnablesEmptyPassword(t *testing.T) {
	_, saveSettings, passwd, _, _ := policyBase(t, "pw-policy-empty")

	if rec := passwd(""); rec.Code != http.StatusBadRequest {
		t.Fatalf("до изменения политики пустой пароль обязан отвергаться: код=%d", rec.Code)
	}
	rec := saveSettings(`{"list_page_size":50,"password_min_length":8,"allow_empty_passwords":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("сохранение политики: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	if rec := passwd(""); rec.Code != http.StatusOK {
		t.Fatalf("пустой пароль отвергнут после разрешения: код=%d тело=%q", rec.Code, rec.Body.String())
	}
}

// Минимальная длина из формы применяется к следующей же смене пароля.
func TestSettingsPasswordPolicyAppliesMinLength(t *testing.T) {
	_, saveSettings, passwd, _, _ := policyBase(t, "pw-policy-len")

	if rec := passwd("abcd"); rec.Code != http.StatusBadRequest {
		t.Fatalf("умолчательный минимум 8 не сработал: код=%d", rec.Code)
	}
	if rec := saveSettings(`{"list_page_size":50,"password_min_length":3}`); rec.Code != http.StatusOK {
		t.Fatalf("сохранение политики: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	if rec := passwd("abcd"); rec.Code != http.StatusOK {
		t.Fatalf("пароль в 4 символа отвергнут при минимуме 3: код=%d тело=%q", rec.Code, rec.Body.String())
	}
}

// Длина вне диапазона — ошибка ввода, а не сбой базы: 400 с внятным текстом.
func TestSettingsPasswordPolicyRejectsOutOfRange(t *testing.T) {
	_, saveSettings, _, _, _ := policyBase(t, "pw-policy-range")

	rec := saveSettings(`{"list_page_size":50,"password_min_length":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("нулевая длина принята: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	if msg, _ := jsonBody(t, rec)["error"].(string); !strings.Contains(msg, "1") {
		t.Errorf("отказ не называет допустимый диапазон: %q", msg)
	}
	if rec := saveSettings(`{"list_page_size":50,"password_min_length":500}`); rec.Code != http.StatusBadRequest {
		t.Errorf("длина больше предела bcrypt принята: код=%d", rec.Code)
	}
}

// Невалидное поле политики не должно превращать один запрос в серию частично
// успешных записей. В том числе JSON null — результат пустого number-input в
// браузере — обязан быть отклонён до изменения остальных параметров базы.
func TestSettingsPasswordPolicyValidatesBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "ноль", value: "0"},
		{name: "больше предела", value: "500"},
		{name: "null из пустого поля", value: "null"},
		{name: "дробь строкой", value: `"8.5"`},
		{name: "дробь у верхней границы", value: `"72.9"`},
		{name: "экспонента строкой", value: `"1e2"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, saveSettings, _, _, dbPath := policyBase(t, "pw-policy-atomic")
			baseline := `{"list_page_size":25,"collapsible_nav":false,"network_enabled":false,"exec_enabled":false,"form_open_mode":"pages","password_min_length":9,"allow_empty_passwords":false}`
			if rec := saveSettings(baseline); rec.Code != http.StatusOK {
				t.Fatalf("начальные настройки: код=%d тело=%q", rec.Code, rec.Body.String())
			}

			invalid := `{"list_page_size":99,"collapsible_nav":true,"network_enabled":true,"exec_enabled":true,"form_open_mode":"tabs","password_min_length":` + tc.value + `,"allow_empty_passwords":true}`
			rec := saveSettings(invalid)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("невалидный запрос принят: код=%d тело=%q", rec.Code, rec.Body.String())
			}

			ctx := context.Background()
			db, err := storage.ConnectSQLite(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if got := db.GetListPageSize(ctx); got != 25 {
				t.Errorf("размер страницы частично сохранён: %d", got)
			}
			if db.GetNavCollapsible(ctx) {
				t.Error("режим меню частично сохранён")
			}
			if db.GetNetworkEnabled(ctx) {
				t.Error("сетевой предохранитель частично сохранён")
			}
			if db.GetExecEnabled(ctx) {
				t.Error("разрешение команд ОС частично сохранено")
			}
			if got := db.GetFormOpenMode(ctx); got != storage.FormModePages {
				t.Errorf("режим форм частично сохранён: %q", got)
			}
			policy := auth.NewRepo(db).AuthPolicy(ctx)
			if policy.PasswordMinLength != 9 || policy.AllowEmptyPasswords {
				t.Errorf("политика паролей частично сохранена: %+v", policy)
			}
		})
	}
}

// Политика паролей лежит в той же записи _settings, что и требование второго
// фактора. Сохранение «Параметров базы» не должно её ронять — иначе включение
// пустых паролей на стенде тихо снимало бы 2FA.
func TestSettingsPasswordPolicyKeepsAuthPolicy(t *testing.T) {
	baseID := "pw-policy-keep"
	ctx := context.Background()
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
	if _, err := repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "", true); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repo.SaveAuthPolicy(ctx, auth.Policy{Require2FAAdmins: true, SelfEnroll2FA: true}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := baseOnSQLite(t, baseID, dbPath)
	h := &handler{store: store, runner: NewRunner()}
	req := httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/settings/save",
		strings.NewReader(`{"list_page_size":50,"password_min_length":4,"allow_empty_passwords":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithBaseID(req, baseID)
	rec := httptest.NewRecorder()
	h.cfgAdminSettingsSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("сохранение: код=%d тело=%q", rec.Code, rec.Body.String())
	}

	fresh, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	policy := auth.NewRepo(fresh).AuthPolicy(ctx)
	if !policy.Require2FAAdmins || !policy.SelfEnroll2FA {
		t.Errorf("политика второго фактора затёрта: %+v", policy)
	}
	if policy.PasswordMinLength != 4 || !policy.AllowEmptyPasswords {
		t.Errorf("политика паролей не сохранена: %+v", policy)
	}
}

// Форма отдельно показывает сохранённое и действующее значения: пустое поле
// оставляет наследование, а placeholder и подпись объясняют текущий источник.
func TestSettingsPasswordPolicyShowsEffectiveValues(t *testing.T) {
	t.Setenv("ONEBASE_MIN_PASSWORD_LENGTH", "12")
	t.Setenv("ONEBASE_ALLOW_EMPTY_PASSWORDS", "true")
	_, _, _, settingsHTML, _ := policyBase(t, "pw-policy-env")

	html := settingsHTML()
	if !strings.Contains(html, `id="st-pwlen" min="1" max="72" value="" placeholder="12"`) {
		t.Errorf("форма не различает пустое сохранённое значение и действующий минимум из env")
	}
	if !strings.Contains(html, `id="st-pwsource">переменная ONEBASE_MIN_PASSWORD_LENGTH</span>`) {
		t.Error("форма не показывает источник действующего минимума")
	}
	if !strings.Contains(html, "ONEBASE_ALLOW_EMPTY_PASSWORDS") {
		t.Error("форма не предупреждает, что пустые пароли разрешены переменной окружения")
	}
	if !strings.Contains(html, "72 байт UTF-8") {
		t.Error("форма не объясняет байтовый предел bcrypt")
	}
	if strings.Contains(html, `var pwlen=parseInt(document.getElementById('st-pwlen').value`) {
		t.Error("отрендерованная панель обрезает исходное значение через parseInt")
	}
	if !strings.Contains(html, `var pwlen=document.getElementById('st-pwlen').value;`) {
		t.Error("отрендерованная панель не отправляет исходное значение поля")
	}
}

func TestSettingsPasswordPolicyResetRestoresEnvironmentDefault(t *testing.T) {
	t.Setenv("ONEBASE_MIN_PASSWORD_LENGTH", "12")
	_, saveSettings, passwd, settingsHTML, dbPath := policyBase(t, "pw-policy-reset")

	if rec := saveSettings(`{"list_page_size":50,"password_min_length":10}`); rec.Code != http.StatusOK {
		t.Fatalf("сохранение override: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	rec := saveSettings(`{"list_page_size":50,"password_min_length":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("сброс override: код=%d тело=%q", rec.Code, rec.Body.String())
	}
	body := jsonBody(t, rec)
	if body["password_min_length_stored"] != float64(0) || body["password_min_length_effective"] != float64(12) {
		t.Fatalf("ответ не разделяет сохранённое и действующее значения: %#v", body)
	}
	if body["password_min_length_source"] != string(auth.PasswordMinLengthSourceEnvironment) {
		t.Fatalf("источник после сброса = %#v", body["password_min_length_source"])
	}

	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := auth.NewRepo(db).AuthPolicy(ctx).PasswordMinLength; got != 0 {
		db.Close()
		t.Fatalf("сброс оставил override %d", got)
	}
	db.Close()

	// Переменная остаётся умолчанием процесса: после условного рестарта новый
	// Repo видит её новое значение, потому что форма не материализовала старое.
	t.Setenv("ONEBASE_MIN_PASSWORD_LENGTH", "14")
	if html := settingsHTML(); !strings.Contains(html, `value="" placeholder="14"`) {
		t.Fatalf("после смены env форма не показывает новый унаследованный минимум")
	}
	if rec := passwd("abcdefghijklm"); rec.Code != http.StatusBadRequest {
		t.Fatalf("13 символов приняты при новом минимуме 14: код=%d", rec.Code)
	}
}

func TestSettingsPasswordPolicyLocalizesNewControls(t *testing.T) {
	saved := launcherBundle
	t.Cleanup(func() { launcherBundle = saved })
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	launcherBundle = bundle
	_, _, _, settingsHTML, _ := policyBase(t, "pw-policy-en")

	html := settingsHTML("en")
	for _, want := range []string{"Passwords", "Minimum password length", "Effective value:", "Save password policy"} {
		if !strings.Contains(html, want) {
			t.Errorf("в английской панели нет %q", want)
		}
	}
}
