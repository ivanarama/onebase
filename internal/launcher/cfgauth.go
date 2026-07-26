package launcher

import (
	"context"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
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

// getAuthDB opens (or returns cached) storage.DB for the given base, routing
// by DBType (postgres/sqlite). Cache key is the base ID, which is stable.
func getAuthDB(ctx context.Context, b *Base) (*storage.DB, error) {
	key := b.ID
	if v, ok := cfgAuthDBs.Load(key); ok {
		return v.(*storage.DB), nil
	}
	db, err := OpenDB(ctx, b)
	if err != nil {
		return nil, err
	}
	if actual, loaded := cfgAuthDBs.LoadOrStore(key, db); loaded {
		db.Close()
		return actual.(*storage.DB), nil
	}
	return db, nil
}

func CloseAuthPools() {
	cfgAuthDBs.Range(func(key, value any) bool {
		value.(*storage.DB).Close()
		cfgAuthDBs.Delete(key)
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
	if b.ConfigSource == "database" {
		if db, dbErr := OpenDB(r.Context(), b); dbErr == nil {
			defer db.Close()
			var content []byte
			if qErr := db.QueryRow(r.Context(), "SELECT content FROM _onebase_config WHERE path = $1", "config/app.yaml").Scan(&content); qErr == nil {
				yaml.Unmarshal(content, &cfg)
			}
		}
	} else if b.Path != "" {
		if raw, rErr := os.ReadFile(filepath.Join(b.Path, "config", "app.yaml")); rErr == nil {
			yaml.Unmarshal(raw, &cfg)
		}
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

func (h *handler) cfgLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	id := chi.URLParam(r, "id")
	b, err := h.store.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cfgLoginTmpl.Execute(w, h.cfgLoginData(r, b))
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
		cfgLoginTmpl.Execute(w, data)
	}

	r.ParseForm()
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
		Name:     "onebase_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *handler) cfgLogout(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cookie, err := r.Cookie("onebase_session")
	if err == nil {
		if b, berr := h.store.Get(id); berr == nil {
			if db, dberr := getAuthDB(r.Context(), b); dberr == nil {
				auth.NewRepo(db).DeleteSession(r.Context(), cookie.Value)
			}
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:    "onebase_session",
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
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie("onebase_session")
		if err != nil {
			http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
			return
		}

		user, err := repo.LookupSession(r.Context(), cookie.Value)
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
		repo.TouchSession(r.Context(), cookie.Value, time.Now())

		ctx := context.WithValue(r.Context(), cfgUserKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// cfgAdminAuthorized повторяет проверку cfgAuthMiddleware, но возвращает ошибку
// отдельно от отказа в доступе. Только успешно подтверждённое отсутствие
// пользователей включает first-run режим; сбой БД должен закрывать доступ.
func (h *handler) cfgAdminAuthorized(r *http.Request, b *Base) (bool, error) {
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
	cookie, err := r.Cookie("onebase_session")
	if err != nil {
		return false, nil
	}
	user, err := repo.LookupSession(r.Context(), cookie.Value)
	if err != nil || user == nil || !user.IsAdmin {
		return false, nil
	}
	return true, nil
}
