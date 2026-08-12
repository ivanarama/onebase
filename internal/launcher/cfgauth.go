package launcher

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/backup"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/storage"
)

type cfgUserKey struct{}

func cfgUserFromContext(ctx context.Context) *auth.User {
	u, _ := ctx.Value(cfgUserKey{}).(*auth.User)
	return u
}

// cfgAuthDBs caches storage.DB per base key so we don't open a new connection
// on every configurator request. Key: base.ID (or DSN/path for legacy paths).
var cfgAuthDBs sync.Map // map[string]*storage.DB

// cfgAuthDBGates protects the lifetime of cached pools. Normal configurator
// requests hold a read lease for their whole handler; restore/delete/edit take
// the exclusive lease, evict the pool and keep new requests out until the
// database files and registry entry are consistent again.
var cfgAuthDBGates sync.Map // map[string]*sync.RWMutex

// Ports are not a cookie boundary. The launcher and enterprise servers use
// the same numeric loopback host, so the configurator session needs its own
// cookie name. The proxy deliberately translates it to onebase_session only
// on the already authenticated connection to the selected base.
const configuratorSessionCookieName = "onebase_launcher_session"

type cfgDBReadLeaseKey struct{}
type cfgDBExclusiveLeaseKey struct{}
type cfgAuthCredentialKey struct{}

type cfgAuthCredential struct {
	token             string
	initialOpenAccess bool
}

func cfgAuthDBGate(baseID string) *sync.RWMutex {
	gate, _ := cfgAuthDBGates.LoadOrStore(baseID, &sync.RWMutex{})
	return gate.(*sync.RWMutex)
}

func cfgDBReadLeaseHeld(ctx context.Context, baseID string) bool {
	heldID, _ := ctx.Value(cfgDBReadLeaseKey{}).(string)
	return heldID == baseID
}

func (h *handler) cfgDBReadMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		gate := cfgAuthDBGate(id)
		gate.RLock()
		defer gate.RUnlock()
		ctx := context.WithValue(r.Context(), cfgDBReadLeaseKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *handler) cfgDBExclusiveMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		release := acquireCfgDBExclusive(id)
		defer release()
		ctx := context.WithValue(r.Context(), cfgDBExclusiveLeaseKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func cfgDBExclusiveLeaseHeld(ctx context.Context, baseID string) bool {
	heldID, _ := ctx.Value(cfgDBExclusiveLeaseKey{}).(string)
	return heldID == baseID
}

func cfgAuthCredentialFromContext(ctx context.Context) (cfgAuthCredential, bool) {
	credential, ok := ctx.Value(cfgAuthCredentialKey{}).(cfgAuthCredential)
	return credential, ok
}

// acquireCfgDBExclusive waits for in-flight configurator requests, prevents
// new ones, evicts the cached pool and returns a release function.
func acquireCfgDBExclusive(baseID string) func() {
	gate := cfgAuthDBGate(baseID)
	gate.Lock()
	if value, ok := cfgAuthDBs.LoadAndDelete(baseID); ok {
		value.(*storage.DB).Close()
	}
	return gate.Unlock
}

// getAuthDB opens (or returns cached) storage.DB for the given base, routing
// by DBType (postgres/sqlite). Cache key is the base ID, which is stable.
func getAuthDB(ctx context.Context, b *Base) (*storage.DB, error) {
	key := b.ID
	if v, ok := cfgAuthDBs.Load(key); ok {
		db := v.(*storage.DB)
		if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
			return nil, err
		}
		return db, nil
	}
	db, err := OpenDB(ctx, b)
	if err != nil {
		return nil, err
	}
	if actual, loaded := cfgAuthDBs.LoadOrStore(key, db); loaded {
		db.Close()
		db = actual.(*storage.DB)
		if err := backup.CheckNoPendingRestore(ctx, db); err != nil {
			return nil, err
		}
		return db, nil
	}
	return db, nil
}

func CloseAuthPools() {
	cfgAuthDBs.Range(func(key, value any) bool {
		id := key.(string)
		release := acquireCfgDBExclusive(id)
		release()
		return true
	})
}

var cfgLoginTmpl = template.Must(template.New("cfg-login").Funcs(template.FuncMap{"t": tr}).Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head><meta charset="utf-8"><title>{{t $.Lang "Конфигуратор — Вход"}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;background:#ECE9D8;display:flex;align-items:center;justify-content:center;height:100vh}
.box{background:#fff;padding:32px 40px;border:1px solid #ACA899;border-radius:2px;width:360px;box-shadow:0 2px 8px rgba(0,0,0,.12)}
h2{margin:0 0 6px;color:#1a5fa8;font-size:17px;font-weight:600}
.sub{font-size:12px;color:#666;margin-bottom:20px}
label{display:block;font-size:12px;margin-bottom:3px;color:#444;font-weight:600}
input,select{width:100%;padding:7px 9px;border:1px solid #ACA899;border-radius:2px;font-size:13px;margin-bottom:14px;outline:none;background:#fff}
input:focus,select:focus{border-color:#3070D8;box-shadow:0 0 0 2px rgba(48,112,216,.15)}
.btn{width:100%;background:#1a5fa8;color:#fff;border:1px solid #1a5fa8;padding:8px;font-size:13px;border-radius:2px;cursor:pointer;font-weight:500}
.btn:hover{background:#1550a0}
.err{color:#c00;font-size:12px;margin-bottom:12px;padding:7px;background:#fff0f0;border-radius:2px;border:1px solid #fcc}
.back{display:block;margin-top:14px;font-size:12px;color:#1a5fa8;text-decoration:none}
</style></head>
<body>
<div class="box">
  {{if .LogoURL}}<div style="text-align:center;margin-bottom:16px"><img src="{{.LogoURL}}" alt="" style="max-height:120px;max-width:260px"></div>{{end}}
  <h2>{{t $.Lang "Конфигуратор — Вход"}}</h2>
  <div class="sub">{{t $.Lang "Только для администраторов"}}</div>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST">
    <label>{{t $.Lang "Имя пользователя"}}</label>
    <input name="login" id="loginInput" autofocus autocomplete="off" {{if .Users}}list="cfg-users"{{end}}>
    {{if .Users}}<datalist id="cfg-users">{{range .Users}}<option value="{{.Login}}">{{if .FullName}}{{.FullName}}{{end}}</option>{{end}}</datalist>{{end}}
    <label>{{t $.Lang "Пароль"}}</label>
    <input name="password" type="password" autocomplete="current-password">
    <button class="btn" type="submit">{{t $.Lang "Войти"}}</button>
  </form>
  <a class="back" href="/">← {{t $.Lang "Назад к списку баз"}}</a>
</div>
</body></html>`))

// cfgLoginData builds the template data map for the configurator login page.
func (h *handler) cfgLoginData(r *http.Request, b *Base) map[string]any {
	data := map[string]any{"Error": "", "LogoURL": "", "Lang": resolveLang(r)}
	type appCfg struct {
		Logo string `yaml:"logo"`
	}
	var cfg appCfg
	// Логотип — украшение страницы входа: нечитаемый app.yaml не повод не
	// пустить администратора в конфигуратор.
	if err := readAppYAML(r.Context(), b, &cfg); err != nil {
		cfg.Logo = ""
	}
	if cfg.Logo != "" {
		data["LogoURL"] = "/bases/" + b.ID + "/configurator/logo"
	}
	if db, dbErr := getAuthDB(r.Context(), b); dbErr == nil {
		repo := auth.NewRepo(db)
		if users, uErr := repo.ListForSelection(r.Context()); uErr == nil {
			var admins []*auth.User
			for _, u := range users {
				if u.IsAdmin {
					admins = append(admins, u)
				}
			}
			data["Users"] = admins
		}
	}
	return data
}

// cfgAuthLog — журнал операций входа/выхода конфигуратора.
func cfgAuthLog() *slog.Logger { return oblog.Component("launcher.cfgauth") }

func (h *handler) cfgLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	id := chi.URLParam(r, "id")
	b, err := h.store.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	renderTemplate(w, cfgLoginTmpl, h.cfgLoginData(r, b))
}

func (h *handler) cfgLoginSubmit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	b, err := h.store.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	lang := resolveLang(r)
	renderErr := func(code int, msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		data := h.cfgLoginData(r, b)
		data["Error"] = msg
		renderTemplate(w, cfgLoginTmpl, data)
	}

	if failForm(w, r) {
		return
	}
	login := r.FormValue("login")
	password := r.FormValue("password")
	limiter := h.configuratorLoginLimiter()
	loginKey := auth.LoginKey(r, login)
	if ok, retry := limiter.Allow(loginKey); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		renderErr(http.StatusTooManyRequests, tr(lang, "Слишком много попыток входа — повторите позже"))
		return
	}

	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		renderErr(500, tr(lang, "Ошибка подключения к БД")+": "+err.Error())
		return
	}

	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		renderErr(500, tr(lang, "Ошибка инициализации")+": "+err.Error())
		return
	}

	// Политики плана 84 действуют и здесь: конфигуратор — самый
	// привилегированный вход, и обойти требование второго фактора через него
	// нельзя. Аварийный выход из sso_only — ONEBASE_ALLOW_PASSWORD_LOGIN.
	policy := repo.AuthPolicy(r.Context())
	if !policy.PasswordLoginAllowed() {
		renderErr(403, tr(lang, "Вход по паролю запрещён политикой базы"))
		return
	}

	user, err := repo.Authenticate(r.Context(), login, password)
	if err != nil {
		limiter.Fail(loginKey)
		renderErr(401, tr(lang, "Неверное имя пользователя или пароль"))
		return
	}

	if !user.IsAdmin {
		renderErr(403, tr(lang, "Доступ запрещён. Только для администраторов."))
		return
	}

	switch enabled, terr := repo.TOTPEnabled(r.Context(), user.ID); {
	case terr != nil:
		cfgAuthLog().Error("не удалось проверить состояние 2FA", "логин", user.Login, "err", terr)
		renderErr(503, tr(lang, "Служба аутентификации временно недоступна"))
		return
	case enabled:
		// Пароль принят, но сессия конфигуратора выдаётся только после кода.
		h.beginCfgSecondFactor(w, r, id, user)
		return
	case repo.RequiresTwoFactor(r.Context(), policy, user):
		// Настройка 2FA живёт в Предприятии (QR + резервные коды), поэтому
		// здесь — только отказ с указанием, что сделать.
		renderErr(403, tr(lang, "Политика базы требует двухфакторной аутентификации. Включите её в режиме Предприятия (Профиль → Второй фактор) и повторите вход."))
		return
	}
	limiter.Reset(loginKey)

	token, err := repo.CreateSession(r.Context(), user.ID, auth.SessionMeta{
		Kind: auth.SessionKindConfigurator, IP: r.RemoteAddr, UserAgent: r.UserAgent(),
	})
	if err != nil {
		http.Error(w, tr(lang, "Внутренняя ошибка"), 500)
		return
	}

	setConfiguratorSessionCookie(w, token)

	http.Redirect(w, r, "/bases/"+id+"/configurator", http.StatusFound)
}

func (h *handler) configuratorLoginLimiter() *auth.LoginLimiter {
	h.cfgLoginOnce.Do(func() {
		if h.cfgLoginLimit == nil {
			h.cfgLoginLimit = auth.NewLoginLimiter(5, time.Minute)
		}
	})
	return h.cfgLoginLimit
}

// setConfiguratorSessionCookie starts the dedicated configurator session in
// the browser. It is shared by the normal login and by first-admin bootstrap:
// before the first user exists the configurator is intentionally open, so the
// create-user AJAX response must establish a session before the next request.
func setConfiguratorSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     configuratorSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *handler) cfgLogout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cookie, err := r.Cookie(configuratorSessionCookieName)
	if err == nil {
		if b, berr := h.store.Get(id); berr == nil {
			if db, dberr := getAuthDB(r.Context(), b); dberr == nil {
				// Выход обязан сработать на стороне клиента при любом исходе:
				// куку снимаем ниже безусловно, отказывать пользователю в
				// выходе из-за недоступной БД нельзя. Но сбой удаления строки
				// сессии — не мелочь: токен остаётся действительным до
				// истечения, и скопированная кука продолжит работать. Поэтому
				// Warn, а не Debug: это расхождение между тем, что видит
				// пользователь, и тем, что на сервере.
				if derr := auth.NewRepo(db).DeleteSession(r.Context(), cookie.Value); derr != nil {
					cfgAuthLog().Warn("сессия конфигуратора не удалена на сервере при выходе — токен действителен до истечения",
						"base", id, "err", derr)
				}
			}
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:    configuratorSessionCookieName,
		Value:   "",
		Path:    "/",
		MaxAge:  -1,
		Expires: time.Unix(0, 0),
	})
	http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
}

func (h *handler) cfgAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		// Restore routes intentionally do not have the outer read middleware:
		// authenticate under a short read lease, release it, then let the handler
		// upgrade to an exclusive lease without deadlocking itself.
		var releaseRead func()
		if !cfgDBReadLeaseHeld(r.Context(), id) {
			gate := cfgAuthDBGate(id)
			gate.RLock()
			released := false
			releaseRead = func() {
				if !released {
					released = true
					gate.RUnlock()
				}
			}
			defer releaseRead()
		}
		b, err := h.store.Get(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		db, err := getAuthDB(r.Context(), b)
		if err != nil {
			http.Error(w, "authentication service unavailable", http.StatusServiceUnavailable)
			return
		}

		repo := auth.NewRepo(db)
		if err := repo.EnsureSchema(r.Context()); err != nil {
			http.Error(w, "authentication service unavailable", http.StatusServiceUnavailable)
			return
		}

		hasUsers, err := repo.HasUsers(r.Context())
		if err != nil {
			http.Error(w, "authentication service unavailable", http.StatusServiceUnavailable)
			return
		}
		if !hasUsers {
			if releaseRead != nil {
				releaseRead()
			}
			ctx := context.WithValue(r.Context(), cfgAuthCredentialKey{}, cfgAuthCredential{
				initialOpenAccess: true,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		cookie, err := r.Cookie(configuratorSessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
			return
		}

		user, err := repo.LookupSessionKind(r.Context(), cookie.Value, auth.SessionKindConfigurator)
		if err != nil {
			http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
			return
		}

		if !user.IsAdmin {
			http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
			return
		}

		// last_seen_at и для сессий конфигуратора — иначе они выглядят
		// «мёртвыми» в админке активных сессий (план 78). Троттлится внутри.
		//
		// Единственное здесь по-настоящему best-effort действие: доступ уже
		// разрешён проверкой выше, и отказывать в запросе из-за неудачной
		// отметки времени было бы хуже самой неудачи. Debug, а не Warn: на
		// каждом запросе, шум в журнале не окупает пользы.
		if err := repo.TouchSession(r.Context(), cookie.Value, time.Now()); err != nil {
			cfgAuthLog().Debug("не удалось обновить last_seen_at сессии конфигуратора", "err", err)
		}

		ctx := context.WithValue(r.Context(), cfgUserKey{}, user)
		ctx = context.WithValue(ctx, cfgAuthCredentialKey{}, cfgAuthCredential{token: cookie.Value})
		if releaseRead != nil {
			releaseRead()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// cfgAuthExclusiveRecheckMiddleware closes the authorization TOCTOU window on
// destructive and consistent-snapshot routes. cfgAuthMiddleware must release
// its short read lease before the exclusive lease can be acquired; users and
// sessions can change in that gap. Re-open the current database under the
// already-held exclusive lease and repeat the decision before any stop, read or
// replacement operation begins.
func (h *handler) cfgAuthExclusiveRecheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !cfgDBExclusiveLeaseHeld(r.Context(), id) {
			http.Error(w, "configurator authorization requires an exclusive database lease", http.StatusInternalServerError)
			return
		}
		credential, ok := cfgAuthCredentialFromContext(r.Context())
		if !ok {
			http.Error(w, "configurator authorization context unavailable", http.StatusInternalServerError)
			return
		}
		b, err := h.store.Get(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		user, openAccess, err := recheckCfgAdminExclusive(r.Context(), b, credential)
		if err != nil {
			http.Error(w, "authentication service unavailable", http.StatusServiceUnavailable)
			return
		}
		if !openAccess && user == nil {
			http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
			return
		}
		ctx := r.Context()
		if user != nil {
			ctx = context.WithValue(ctx, cfgUserKey{}, user)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recheckCfgAdminExclusive uses a fresh, uncached connection and closes it
// before returning. The destructive handler that follows must be the next code
// allowed to open/replace this database while the exclusive gate remains held.
func recheckCfgAdminExclusive(ctx context.Context, b *Base, credential cfgAuthCredential) (*auth.User, bool, error) {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return nil, false, err
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		db.Close()
		return nil, false, err
	}
	hasUsers, err := repo.HasUsers(ctx)
	if err != nil {
		db.Close()
		return nil, false, err
	}
	if !hasUsers {
		db.Close()
		return nil, true, nil
	}
	// A request admitted under first-run open access must not inherit access if
	// another request created the first user before this exclusive section.
	if credential.initialOpenAccess || credential.token == "" {
		db.Close()
		return nil, false, nil
	}
	user, lookupErr := repo.LookupSessionKind(ctx, credential.token, auth.SessionKindConfigurator)
	db.Close()
	if lookupErr != nil || user == nil || !user.IsAdmin {
		return nil, false, nil
	}
	return user, false, nil
}

// cfgAdminAuthorized повторяет проверку cfgAuthMiddleware, но возвращает ошибку
// отдельно от отказа в доступе. Только успешно подтверждённое отсутствие
// пользователей включает first-run режим; сбой БД должен закрывать доступ.
func (h *handler) cfgAdminAuthorized(r *http.Request, b *Base) (bool, error) {
	if !cfgDBReadLeaseHeld(r.Context(), b.ID) {
		gate := cfgAuthDBGate(b.ID)
		gate.RLock()
		defer gate.RUnlock()
	}
	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		return false, err
	}
	repo := auth.NewRepo(db)
	if err := repo.EnsureSchema(r.Context()); err != nil {
		return false, err
	}
	hasUsers, err := repo.HasUsers(r.Context())
	if err != nil {
		return false, err
	}
	if !hasUsers {
		return true, nil
	}
	cookie, err := r.Cookie(configuratorSessionCookieName)
	if err != nil {
		return false, nil
	}
	user, err := repo.LookupSessionKind(r.Context(), cookie.Value, auth.SessionKindConfigurator)
	if err != nil || user == nil || !user.IsAdmin {
		return false, nil
	}
	return true, nil
}
