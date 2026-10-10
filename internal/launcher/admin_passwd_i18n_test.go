package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/storage"
)

// passwdI18nBase готовит базу с администратором и сохранённым минимумом
// пароля; passwdLang дергает публичную смену пароля с заданным
// Accept-Language (#1571).
func passwdI18nBase(t *testing.T, baseID, minLen string) (userID string, passwdLang func(lang, password string) *httptest.ResponseRecorder) {
	t.Helper()
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
	user, err := repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Администратор", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := baseOnSQLite(t, baseID, dbPath)
	h := &handler{store: store, runner: NewRunner()}
	save := `{"list_page_size":50,"allow_empty_passwords":false`
	if minLen != "" {
		save += `,"password_min_length":` + minLen
	}
	save += `}`
	req := httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/settings/save", strings.NewReader(save))
	req.Header.Set("Content-Type", "application/json")
	req = requestWithBaseID(req, baseID)
	rec := httptest.NewRecorder()
	h.cfgAdminSettingsSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save settings: status=%d body=%s", rec.Code, rec.Body.String())
	}

	passwdLang = func(lang, password string) *httptest.ResponseRecorder {
		body := `{"id":"` + user.ID + `","password":"` + password + `"}`
		req := httptest.NewRequest(http.MethodPost, "/bases/"+baseID+"/configurator/admin/users/passwd", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		req = requestWithBaseID(req, baseID)
		rec := httptest.NewRecorder()
		h.cfgAdminUserPasswd(rec, req)
		return rec
	}
	return user.ID, passwdLang
}

func i18nBundleForTest(t *testing.T) {
	t.Helper()
	bundle, err := i18n.Load(i18n.EmbeddedLocales, "")
	if err != nil {
		t.Fatal(err)
	}
	saved := launcherBundle
	launcherBundle = bundle
	t.Cleanup(func() { launcherBundle = saved })
}

func errBody(rec *httptest.ResponseRecorder) string {
	return rec.Body.String()
}

// Три отказа политики локализуются на границе HTTP с фактическими пределами:
// пустой, короче сохранённого минимума базы, длиннее байтового предела bcrypt.
func TestPasswordPolicyMessagesLocalized(t *testing.T) {
	i18nBundleForTest(t)
	_, passwdLang := passwdI18nBase(t, "pw-i18n-min", "12")

	en := passwdLang("en", "")
	if en.Code != http.StatusBadRequest {
		t.Fatalf("empty: status=%d body=%s", en.Code, errBody(en))
	}
	if !strings.Contains(errBody(en), "The password cannot be empty") {
		t.Errorf("empty en: %s", errBody(en))
	}

	en = passwdLang("en", "короткий")
	if en.Code != http.StatusBadRequest {
		t.Fatalf("short: status=%d body=%s", en.Code, errBody(en))
	}
	// Минимум — сохранённый в базе (12), а не глобальное умолчание (8).
	if !strings.Contains(errBody(en), "minimum 12 characters") {
		t.Errorf("short en: %s", errBody(en))
	}

	en = passwdLang("en", strings.Repeat("ё", 40)) // 80 байт UTF-8
	if en.Code != http.StatusBadRequest {
		t.Fatalf("long: status=%d body=%s", en.Code, errBody(en))
	}
	if !strings.Contains(errBody(en), "maximum 72 bytes") {
		t.Errorf("long en: %s", errBody(en))
	}

	// Русский интерфейс: ключ и есть ответ, параметры на месте.
	ru := passwdLang("ru", "короткий")
	if !strings.Contains(errBody(ru), "пароль слишком короткий: минимум 12 символов") {
		t.Errorf("short ru: %s", errBody(ru))
	}
	ru = passwdLang("ru", strings.Repeat("ё", 40))
	if !strings.Contains(errBody(ru), "пароль слишком длинный: максимум 72 байта") {
		t.Errorf("long ru: %s", errBody(ru))
	}
}

// Публичный путь создания пользователя локализован тем же форматтером.
func TestPasswordPolicyMessagesLocalizedOnCreate(t *testing.T) {
	i18nBundleForTest(t)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "pw-i18n-create.db")
	db, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		t.Fatal(err)
	}
	_, err = repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Администратор", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	store := baseOnSQLite(t, "pw-i18n-create", dbPath)
	h := &handler{store: store, runner: NewRunner()}
	req := httptest.NewRequest(http.MethodPost, "/bases/pw-i18n-create/configurator/admin/users/create",
		strings.NewReader(`{"login":"учитель","password":"","fullName":"Учитель","isAdmin":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	req = requestWithBaseID(req, "pw-i18n-create")
	rec := httptest.NewRecorder()
	h.cfgAdminUserCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create empty: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "The password cannot be empty") {
		t.Errorf("create en: %s", rec.Body.String())
	}
	// Проверь отсутствие пользователя через рабочее соединение после обработчика.
	checkDB, err := storage.ConnectSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer checkDB.Close()
	user, err := auth.NewRepo(checkDB).GetByLogin(ctx, "учитель")
	if err != nil {
		t.Fatalf("проверка созданного пользователя: %v", err)
	}
	if user != nil {
		t.Fatal("пользователь с отвергнутым паролем создан")
	}
}

// Проверка проходит через штатные маршруты и middleware лаунчера: сообщение
// должно называть действующий минимум и объяснять приоритет настройки базы.
func TestPasswordMinimumHintHTTP(t *testing.T) {
	for _, policy := range []struct {
		name   string
		stored int
		min    int
	}{
		{"environment", 0, 12},
		{"database_lower", 4, 4},
		{"database_higher", 14, 14},
	} {
		t.Run(policy.name, func(t *testing.T) {
			t.Setenv("ONEBASE_MIN_PASSWORD_LENGTH", "12")
			t.Setenv("ONEBASE_ALLOW_EMPTY_PASSWORDS", "false")
			ctx := context.Background()
			baseID := "password-hint"
			dbPath := filepath.Join(t.TempDir(), "base.db")
			db, err := storage.ConnectSQLite(ctx, dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			repo := auth.NewRepo(db)
			if err := repo.EnsureSchema(ctx); err != nil {
				t.Fatal(err)
			}
			admin, err := repo.Create(ctx, "admin", "Str0ng-Passw0rd!", "Admin", true)
			if err != nil {
				t.Fatal(err)
			}
			other, err := repo.Create(ctx, "other", "Str0ng-Passw0rd!", "Other", false)
			if err != nil {
				t.Fatal(err)
			}
			token, err := repo.CreateSession(ctx, admin.ID, auth.SessionMeta{Kind: auth.SessionKindConfigurator})
			if err != nil {
				t.Fatal(err)
			}
			savedBundle := launcherBundle
			t.Cleanup(func() { launcherBundle = savedBundle })
			srv, err := NewServer(baseOnSQLite(t, baseID, dbPath), NewRunner())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- srv.ListenAndServe() }()
			t.Cleanup(func() { srv.Close(); <-done })
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			post := func(path, lang, body string) (int, string) {
				t.Helper()
				req, err := http.NewRequest(http.MethodPost, srv.URL()+"/bases/"+baseID+"/configurator/admin/"+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept-Language", lang)
				req.AddCookie(&http.Cookie{Name: configuratorSessionCookieName, Value: token})
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				raw, err := io.ReadAll(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				return res.StatusCode, string(raw)
			}
			if policy.stored != 0 {
				status, body := post("settings/save", "en", fmt.Sprintf(`{"list_page_size":50,"password_min_length":%d}`, policy.stored))
				if status != http.StatusOK {
					t.Fatalf("save policy: status=%d body=%s", status, body)
				}
			}
			for _, lang := range []string{"en", "ru"} {
				for _, action := range []string{"create", "passwd"} {
					t.Run(lang+"/"+action, func(t *testing.T) {
						body := func(length int) string {
							return fmt.Sprintf(`{"id":%q,"login":%q,"password":%q}`, other.ID, "candidate-"+lang, strings.Repeat("x", length))
						}
						status, raw := post("users/"+action, lang, body(policy.min-1))
						if status != http.StatusBadRequest {
							t.Fatalf("short password: status=%d body=%s", status, raw)
						}
						var reply struct {
							Error string `json:"error"`
						}
						if err := json.Unmarshal([]byte(raw), &reply); err != nil {
							t.Fatal(err)
						}
						wantMin := fmt.Sprintf("minimum %d characters", policy.min)
						wantHint := "ONEBASE_MIN_PASSWORD_LENGTH sets the default minimum length before the launcher starts. A saved database password policy overrides this value. To change the minimum, open Database parameters → Passwords."
						if lang == "ru" {
							wantMin = fmt.Sprintf("минимум %d символов", policy.min)
							wantHint = "ONEBASE_MIN_PASSWORD_LENGTH задаёт минимальную длину по умолчанию перед запуском лаунчера. Сохранённая политика паролей базы перекрывает это значение. Чтобы изменить минимум, откройте «Параметры базы» → «Пароли»."
						}
						if !strings.Contains(reply.Error, wantMin) || !strings.Contains(reply.Error, wantHint) {
							t.Errorf("password policy message = %q; want %q and %q", reply.Error, wantMin, wantHint)
						}
						if status, raw := post("users/"+action, lang, body(policy.min)); status != http.StatusOK {
							t.Fatalf("password at effective minimum: status=%d body=%s", status, raw)
						}
					})
				}
			}
		})
	}
}
