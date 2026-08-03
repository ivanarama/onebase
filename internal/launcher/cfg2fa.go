package launcher

// Второй фактор при входе в конфигуратор (план 84). Конфигуратор — самая
// привилегированная точка входа (правка метаданных, выгрузка базы), поэтому
// требование 2FA обязано действовать и здесь, иначе политика обходится сменой
// адреса в браузере.
//
// Первичная настройка 2FA здесь НЕ делается: она требует показать QR и
// резервные коды, и её место — режим Предприятия. Администратор без второго
// фактора, которому политика его предписывает, сначала включает 2FA в
// Предприятии и только затем входит в конфигуратор.

import (
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
)

// cfgChallengeCookie — кука с токеном незавершённого входа в конфигуратор.
const cfgChallengeCookie = "onebase_cfg_2fa"

var cfg2FATmpl = template.Must(template.New("cfg-2fa").Funcs(template.FuncMap{"t": tr}).Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head><meta charset="utf-8"><title>{{t $.Lang "Конфигуратор — Вход"}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;background:#ECE9D8;display:flex;align-items:center;justify-content:center;height:100vh}
.box{background:#fff;padding:32px 40px;border:1px solid #ACA899;border-radius:2px;width:360px;box-shadow:0 2px 8px rgba(0,0,0,.12)}
h2{margin:0 0 6px;color:#1a5fa8;font-size:17px;font-weight:600}
.sub{font-size:12px;color:#666;margin-bottom:20px}
label{display:block;font-size:12px;margin-bottom:3px;color:#444;font-weight:600}
input{width:100%;padding:7px 9px;border:1px solid #ACA899;border-radius:2px;font-size:13px;margin-bottom:14px;outline:none;background:#fff}
input:focus{border-color:#3070D8;box-shadow:0 0 0 2px rgba(48,112,216,.15)}
.btn{width:100%;background:#1a5fa8;color:#fff;border:1px solid #1a5fa8;padding:8px;font-size:13px;border-radius:2px;cursor:pointer;font-weight:500}
.btn:hover{background:#1550a0}
.err{color:#c00;font-size:12px;margin-bottom:12px;padding:7px;background:#fff0f0;border-radius:2px;border:1px solid #fcc}
.back{display:block;margin-top:14px;font-size:12px;color:#1a5fa8;text-decoration:none}
</style></head>
<body>
<div class="box">
  <h2>🔐 {{t $.Lang "Подтверждение входа"}}</h2>
  <div class="sub">{{t $.Lang "Код из приложения-аутентификатора или резервный код"}} — {{.Login}}</div>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST">
    <label>{{t $.Lang "Код подтверждения"}}</label>
    <input name="code" autofocus autocomplete="one-time-code" placeholder="123456">
    <button class="btn" type="submit">{{t $.Lang "Подтвердить"}}</button>
  </form>
  <a class="back" href="/bases/{{.BaseID}}/configurator/login">← {{t $.Lang "Войти под другой учётной записью"}}</a>
</div>
</body></html>`))

// setCfgChallengeCookie кладёт токен незавершённого входа. Секретов кука не
// содержит: challenge живёт в памяти процесса и гаснет через минуты.
func setCfgChallengeCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfgChallengeCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCfgChallengeCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfgChallengeCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// beginCfgSecondFactor выдаёт challenge и показывает страницу ввода кода.
func (h *handler) beginCfgSecondFactor(w http.ResponseWriter, r *http.Request, baseID string, user *auth.User) {
	token, err := auth.DefaultChallenges().Issue(auth.Challenge{
		UserID:       user.ID,
		Login:        user.Login,
		Configurator: true,
		BaseID:       baseID,
	})
	if err != nil {
		http.Error(w, tr(resolveLang(r), "Внутренняя ошибка"), http.StatusInternalServerError)
		return
	}
	setCfgChallengeCookie(w, token)
	renderCfg2FA(w, r, baseID, user.Login, "", http.StatusOK)
}

func renderCfg2FA(w http.ResponseWriter, r *http.Request, baseID, login, errMsg string, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	renderTemplate(w, cfg2FATmpl, map[string]any{
		"Lang": resolveLang(r), "BaseID": baseID, "Login": login, "Error": errMsg,
	})
}

// cfg2FAPage — GET .../configurator/2fa: возврат на шаг подтверждения.
func (h *handler) cfg2FAPage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ch, _, ok := currentCfgChallenge(r, id)
	if !ok {
		http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
		return
	}
	renderCfg2FA(w, r, id, ch.Login, "", http.StatusOK)
}

// currentCfgChallenge достаёт незавершённый вход текущего браузера и проверяет,
// что он относится к этой базе: одна кука на лаунчер, баз в нём много.
func currentCfgChallenge(r *http.Request, baseID string) (auth.Challenge, string, bool) {
	cookie, err := r.Cookie(cfgChallengeCookie)
	if err != nil || cookie.Value == "" {
		return auth.Challenge{}, "", false
	}
	ch, ok := auth.DefaultChallenges().Get(cookie.Value)
	if !ok || !ch.Configurator || ch.BaseID != baseID {
		return auth.Challenge{}, "", false
	}
	return ch, cookie.Value, true
}

// cfg2FASubmit — POST .../configurator/2fa: проверка кода и выдача сессии
// конфигуратора.
func (h *handler) cfg2FASubmit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	lang := resolveLang(r)
	b, err := h.store.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ch, token, ok := currentCfgChallenge(r, id)
	if !ok {
		clearCfgChallengeCookie(w)
		http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
		return
	}
	if failForm(w, r) {
		return
	}
	limiter := h.configuratorLoginLimiter()
	loginKey := auth.LoginKey(r, ch.Login)
	if allowed, retry := limiter.Allow(loginKey); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		renderCfg2FA(w, r, id, ch.Login, tr(lang, "Слишком много попыток входа — повторите позже"), http.StatusTooManyRequests)
		return
	}

	db, err := getAuthDB(r.Context(), b)
	if err != nil {
		renderCfg2FA(w, r, id, ch.Login, tr(lang, "Ошибка подключения к БД")+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	repo := auth.NewRepo(db)
	//nolint:gosec // G120: тело формы ограничивает failForm выше (parseFormLimited); gosec видит только это присваивание
	if err := repo.VerifySecondFactor(r.Context(), ch.UserID, r.FormValue("code"), time.Now()); err != nil {
		limiter.Fail(loginKey)
		if !auth.DefaultChallenges().Fail(token) {
			clearCfgChallengeCookie(w)
			http.Redirect(w, r, "/bases/"+id+"/configurator/login", http.StatusFound)
			return
		}
		renderCfg2FA(w, r, id, ch.Login, tr(lang, "Неверный код подтверждения"), http.StatusUnauthorized)
		return
	}
	limiter.Reset(loginKey)
	auth.DefaultChallenges().Delete(token)
	clearCfgChallengeCookie(w)

	sessionToken, err := repo.CreateSession(r.Context(), ch.UserID, auth.SessionMeta{
		Kind: auth.SessionKindConfigurator, IP: r.RemoteAddr, UserAgent: r.UserAgent(),
	})
	if err != nil {
		http.Error(w, tr(lang, "Внутренняя ошибка"), http.StatusInternalServerError)
		return
	}
	setConfiguratorSessionCookie(w, sessionToken)
	http.Redirect(w, r, "/bases/"+id+"/configurator", http.StatusFound)
}
