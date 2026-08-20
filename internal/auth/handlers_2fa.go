package auth

// Шаг второго фактора на входе (план 84): страница ввода кода, принудительная
// настройка 2FA, когда её требует политика, и QR для приложения-аутентификатора.

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"
)

// challengeCookie — кука с токеном наполовину выполненного входа. Кука, а не
// параметр URL: токен в URL утекает в логи, Referer и историю (план 53).
const challengeCookie = "onebase_2fa"

// twoFactorCSS — оформление шага 2FA; повторяет форму входа.
const twoFactorCSS = `
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:'Segoe UI',Arial,sans-serif;background:#f0f0f0;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:24px}
.box{background:#fff;padding:32px 40px;border:1px solid #ccc;border-radius:4px;width:400px;box-shadow:0 2px 8px rgba(0,0,0,.15)}
h2{margin:0 0 8px;color:#1a5fa8;font-size:18px;font-weight:600}
p.sub{font-size:13px;color:#555;margin-bottom:20px;line-height:1.5}
label{display:block;font-size:13px;margin-bottom:4px;color:#333;font-weight:500}
input{width:100%;padding:8px 10px;border:1px solid #bbb;border-radius:3px;font-size:14px;margin-bottom:16px;outline:none;background:#fff}
input:focus{border-color:#1a5fa8;box-shadow:0 0 0 2px rgba(26,95,168,.15)}
.btn{width:100%;background:#1a5fa8;color:#fff;border:none;padding:10px;font-size:14px;border-radius:3px;cursor:pointer;font-weight:500}
.btn:hover{background:#1550a0}
.err{color:#c00;font-size:13px;margin-bottom:14px;padding:8px;background:#fff0f0;border-radius:3px;border:1px solid #fcc}
.qr{text-align:center;margin-bottom:14px}
.secret{font-family:Consolas,monospace;font-size:14px;letter-spacing:1px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:3px;padding:8px;text-align:center;margin-bottom:16px;word-break:break-all}
.codes{display:grid;grid-template-columns:1fr 1fr;gap:6px;font-family:Consolas,monospace;font-size:15px;margin:14px 0}
.codes span{background:#f8fafc;border:1px solid #e2e8f0;border-radius:3px;padding:6px;text-align:center}
.back{display:block;margin-top:14px;font-size:12px;color:#1a5fa8;text-decoration:none;text-align:center}
`

var twoFactorTmpl = template.Must(template.New("2fa").Parse(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>Подтверждение входа — onebase</title>
<style>` + twoFactorCSS + `</style></head>
<body>
<div class="box">
  <h2>🔐 Подтверждение входа</h2>
  <p class="sub">Учётная запись <b>{{.Login}}</b>. Введите шестизначный код из приложения-аутентификатора либо один из резервных кодов.</p>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST" action="{{.Action}}">
    <label>Код подтверждения</label>
    <input name="code" autofocus autocomplete="one-time-code" inputmode="text" placeholder="123456">
    <button class="btn" type="submit">Подтвердить</button>
  </form>
  <a class="back" href="{{.CancelURL}}">← Войти под другой учётной записью</a>
</div>
</body></html>`))

var twoFactorEnrollTmpl = template.Must(template.New("2fa-enroll").Parse(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>Настройка второго фактора — onebase</title>
<style>` + twoFactorCSS + `</style></head>
<body>
<div class="box">
  <h2>🔐 Требуется второй фактор</h2>
  <p class="sub">Политика безопасности этой базы требует двухфакторной аутентификации для учётной записи <b>{{.Login}}</b>. Отсканируйте QR-код приложением-аутентификатором (Google Authenticator, Aegis, 1Password и совместимые) и введите код из него.</p>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <div class="qr"><img src="{{.QRURL}}" width="220" height="220" alt="QR-код"></div>
  <div class="secret">{{.Secret}}</div>
  <form method="POST" action="{{.Action}}">
    <label>Код из приложения</label>
    <input name="code" autofocus autocomplete="one-time-code" inputmode="numeric" placeholder="123456">
    <button class="btn" type="submit">Включить и войти</button>
  </form>
  <a class="back" href="{{.CancelURL}}">← Войти под другой учётной записью</a>
</div>
</body></html>`))

var twoFactorBindTmpl = template.Must(template.New("2fa-bind").Parse(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>Код привязки — onebase</title>
<style>` + twoFactorCSS + `</style></head>
<body>
<div class="box">
  <h2>🔐 Требуется второй фактор</h2>
  <p class="sub">Политика безопасности этой базы требует двухфакторной аутентификации для учётной записи <b>{{.Login}}</b>. Чтобы привязать его, получите у администратора одноразовый <b>код привязки</b> и введите его ниже — после этого откроется настройка приложения-аутентификатора.</p>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <form method="POST" action="{{.Action}}">
    <label>Код привязки</label>
    <input name="code" autofocus autocomplete="off" inputmode="text" placeholder="abcd-efgh-ijkl-mnop">
    <button class="btn" type="submit">Продолжить</button>
  </form>
  <a class="back" href="{{.CancelURL}}">← Войти под другой учётной записью</a>
</div>
</body></html>`))

var backupCodesTmpl = template.Must(template.New("2fa-codes").Parse(`<!DOCTYPE html>
<html lang="ru">
<head><meta charset="utf-8"><title>Резервные коды — onebase</title>
<style>` + twoFactorCSS + `</style></head>
<body>
<div class="box">
  <h2>✅ Второй фактор включён</h2>
  <p class="sub">Сохраните резервные коды. Каждый работает один раз и заменяет код из приложения, если телефон недоступен. Показываются они только сейчас.</p>
  <div class="codes">{{range .Codes}}<span>{{.}}</span>{{end}}</div>
  <a class="btn" href="{{.ContinueURL}}" style="display:block;text-align:center;text-decoration:none;line-height:20px">Продолжить</a>
</div>
</body></html>`))

// challenges возвращает хранилище challenge'ей обработчика.
func (h *Handlers) challenges() *Challenges {
	if h.Challenges != nil {
		return h.Challenges
	}
	return DefaultChallenges()
}

// issuerName — что видно в приложении-аутентификаторе рядом с кодом.
func (h *Handlers) issuerName() string {
	if name := strings.TrimSpace(h.AppName); name != "" {
		return name
	}
	return "OneBase"
}

// setChallengeCookie кладёт токен незавершённого входа в куку. Session-cookie
// (без MaxAge): закрыл вкладку — начал вход заново.
func (h *Handlers) setChallengeCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     challengeCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.SecureCookies || r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handlers) clearChallengeCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     challengeCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.SecureCookies || r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// beginSecondFactor выдаёт challenge и показывает нужный шаг: ввод кода либо
// первичную настройку. Вызывается из формы входа после проверки пароля.
//
// enrollAuthorized разрешает показать QR и секрет сразу (самопривязка). При
// enroll и enrollAuthorized=false секрет не генерируется вовсе — пользователю
// сперва предлагается ввести одноразовый код привязки от администратора
// (issue #577), иначе один предъявленный пароль закреплял бы за собой второй
// фактор чужой учётки.
func (h *Handlers) beginSecondFactor(w http.ResponseWriter, r *http.Request, user *User, enroll, enrollAuthorized bool, returnURL string) {
	ch := Challenge{UserID: user.ID, Login: user.Login, Enroll: enroll, ReturnURL: returnURL}
	if enroll && enrollAuthorized {
		secret, err := GenerateTOTPSecret()
		if err != nil {
			h.internalError(w, r, "генерация секрета 2FA", err)
			return
		}
		ch.Secret = secret
		ch.EnrollAuthorized = true
	}
	token, err := h.challenges().Issue(ch)
	if err != nil {
		h.internalError(w, r, "выдача challenge второго фактора", err)
		return
	}
	h.setChallengeCookie(w, r, token)
	h.renderTwoFactor(w, ch, "")
}

// renderTwoFactor рисует страницу шага 2FA.
func (h *Handlers) renderTwoFactor(w http.ResponseWriter, ch Challenge, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	}
	data := map[string]any{
		"Login":     ch.Login,
		"Error":     errMsg,
		"Action":    "/login/2fa",
		"CancelURL": "/login",
	}
	if !ch.Enroll {
		renderTemplate(w, twoFactorTmpl, data)
		return
	}
	if !ch.EnrollAuthorized {
		// Привязка ещё не разрешена: сперва одноразовый код от администратора,
		// QR и секрет не показываем (issue #577).
		renderTemplate(w, twoFactorBindTmpl, data)
		return
	}
	data["QRURL"] = "/login/2fa/qr"
	data["Secret"] = FormatTOTPSecret(ch.Secret)
	renderTemplate(w, twoFactorEnrollTmpl, data)
}

// TwoFactorPage — GET /login/2fa: возврат на шаг подтверждения (например, по
// кнопке «назад»). Без действующего challenge отправляет вводить пароль.
func (h *Handlers) TwoFactorPage(w http.ResponseWriter, r *http.Request) {
	ch, _, ok := h.currentChallenge(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	h.renderTwoFactor(w, ch, "")
}

// currentChallenge достаёт challenge текущего браузера.
func (h *Handlers) currentChallenge(r *http.Request) (Challenge, string, bool) {
	cookie, err := r.Cookie(challengeCookie)
	if err != nil || cookie.Value == "" {
		return Challenge{}, "", false
	}
	ch, ok := h.challenges().Get(cookie.Value)
	return ch, cookie.Value, ok
}

// TwoFactorQR отдаёт QR-код otpauth:// для текущей настройки. Секрет берётся
// из challenge, а не из параметра запроса: иначе картинка стала бы способом
// подсунуть чужой секрет.
func (h *Handlers) TwoFactorQR(w http.ResponseWriter, r *http.Request) {
	ch, _, ok := h.currentChallenge(r)
	// Гейт — EnrollAuthorized, тот же, по которому страница решает показывать
	// ли QR. Раньше проверялось только «секрет есть», а это следствие, а не
	// разрешение: держалось оно на том, что секрет в challenge появляется лишь
	// после кода привязки. Инвариант верный, но незаписанный — картинка отдавала
	// секрет по условию, которое к правам отношения не имеет, и любая правка,
	// заводящая секрет раньше (скажем, доделанная настройка 2FA в JSON-потоке),
	// молча превращала бы это в выдачу второго фактора по одному паролю (#615).
	if !ok || !ch.EnrollAuthorized || ch.Secret == "" {
		http.NotFound(w, r)
		return
	}
	writeOTPAuthQR(w, r, h.issuerName(), ch.Login, ch.Secret)
}

// EnrollCookie — кука с токеном начатой настройки 2FA в профиле. Секрет живёт
// в памяти процесса, наружу уходит только токен: попади он в лог, из него
// нельзя получить ни секрет, ни доступ — QR по нему отдаётся лишь вместе с
// действующей сессией владельца.
const EnrollCookie = "onebase_2fa_setup"

// StartEnrollment начинает настройку второго фактора для уже вошедшего
// пользователя (экран профиля). Возвращает токен настройки и новый секрет.
func StartEnrollment(userID, login string) (token, secret string, err error) {
	secret, err = GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	token, err = DefaultChallenges().Issue(Challenge{
		UserID: userID, Login: login, Enroll: true, Secret: secret,
	})
	if err != nil {
		return "", "", err
	}
	return token, secret, nil
}

// Enrollment возвращает начатую настройку по токену.
func Enrollment(token string) (Challenge, bool) {
	ch, ok := DefaultChallenges().Get(token)
	if !ok || !ch.Enroll {
		return Challenge{}, false
	}
	return ch, true
}

// FinishEnrollment гасит начатую настройку (успешную или брошенную).
func FinishEnrollment(token string) { DefaultChallenges().Delete(token) }

// WriteOTPAuthQR отдаёт PNG с QR-кодом otpauth:// — для экрана профиля.
func WriteOTPAuthQR(w http.ResponseWriter, r *http.Request, issuer, account, secret string) {
	writeOTPAuthQR(w, r, issuer, account, secret)
}

// writeOTPAuthQR рисует QR со ссылкой otpauth://.
func writeOTPAuthQR(w http.ResponseWriter, r *http.Request, issuer, account, secret string) {
	code, err := qr.Encode(OTPAuthURI(issuer, account, secret), qr.M)
	if err != nil {
		internalErrorMsg(w, r, "построение QR-кода 2FA", "не удалось построить QR-код", err)
		return
	}
	png := code.PNG()
	w.Header().Set("Content-Type", "image/png")
	// Картинка содержит секрет — ни прокси, ни браузер её кэшировать не должны.
	w.Header().Set("Cache-Control", "no-store, private")
	if _, err := w.Write(png); err != nil {
		authLog().Debug("не удалось отправить QR-код", "err", err)
	}
}

// TwoFactorSubmit — POST /login/2fa: проверка кода (или подтверждение первичной
// настройки) и создание сессии.
func (h *Handlers) TwoFactorSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "некорректные данные формы", http.StatusBadRequest)
		return
	}
	ch, token, ok := h.currentChallenge(r)
	if !ok {
		h.clearChallengeCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	// Лимитер тот же, что у формы входа: перебор кода — это перебор входа,
	// размазывать его по двум счётчикам нельзя.
	if h.limitExceeded(w, r, ch.Login) {
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))

	if ch.Enroll {
		if !ch.EnrollAuthorized {
			// Шаг «введите код привязки»: код из формы — это одноразовый код от
			// администратора, а не код TOTP (issue #577).
			h.completeBindTicket(w, r, ch, token, code)
			return
		}
		h.completeEnrollment(w, r, ch, token, code)
		return
	}
	if err := h.Repo.VerifySecondFactor(r.Context(), ch.UserID, code, time.Now()); err != nil {
		h.failSecondFactor(w, r, ch, token, err)
		return
	}
	h.finishSecondFactor(w, r, ch, token, "login_2fa")
}

// completeBindTicket проверяет одноразовый код привязки от администратора и,
// если он подошёл, переводит вход к настройке 2FA: генерирует секрет и
// показывает QR. Секрет появляется только здесь — привязать второй фактор по
// одному паролю нельзя (issue #577).
func (h *Handlers) completeBindTicket(w http.ResponseWriter, r *http.Request, ch Challenge, token, code string) {
	// Проверяем, но не гасим: билет сгорит, когда фактор действительно привяжут
	// (completeEnrollment). Прежнее гашение здесь оставляло сорвавшуюся привязку
	// и без второго фактора, и без кода — за новым надо было к администратору
	// (#615).
	if err := h.Repo.VerifyBindTicket(r.Context(), ch.UserID, code, time.Now()); err != nil {
		h.failSecondFactor(w, r, ch, token, err)
		return
	}
	secret, err := GenerateTOTPSecret()
	if err != nil {
		h.internalError(w, r, "генерация секрета 2FA", err)
		return
	}
	// Правим challenge на месте — тот же токен и кука. Счётчик попыток сбрасываем:
	// неудачные вводы кода привязки не должны съедать попытки ввода кода из
	// приложения на следующем шаге.
	if !h.challenges().Update(token, func(c *Challenge) {
		c.EnrollAuthorized = true
		c.Secret = secret
		c.bindCode = code
		c.attempts = 0
	}) {
		h.clearChallengeCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	// Отсчёт TTL шёл от ввода пароля, а впереди самый долгий шаг: поставить
	// приложение, отсканировать QR, дождаться окна кода. Продлеваем на полный
	// TTL — иначе привязка срывалась по времени тем чаще, чем дольше искали
	// телефон (#615).
	h.challenges().Renew(token)
	ch.EnrollAuthorized = true
	ch.Secret = secret
	h.renderTwoFactor(w, ch, "")
}

// completeEnrollment включает второй фактор кодом из приложения и показывает
// резервные коды. Сессия создаётся здесь же: пользователь уже подтвердил и
// пароль, и владение секретом.
func (h *Handlers) completeEnrollment(w http.ResponseWriter, r *http.Request, ch Challenge, token, code string) {
	if !ch.EnrollAuthorized {
		// Защита в глубину: сюда нельзя попасть, минуя код привязки, но если
		// попали — второй фактор не включаем (issue #577).
		h.failSecondFactor(w, r, ch, token, ErrInvalidSecondFactor)
		return
	}
	step, ok := VerifyTOTP(ch.Secret, code, time.Now(), 0)
	if !ok {
		h.failSecondFactor(w, r, ch, token, ErrInvalidSecondFactor)
		return
	}
	if err := h.Repo.EnableTOTP(r.Context(), ch.UserID, ch.Secret, step); err != nil {
		h.internalError(w, r, "включение 2FA", err)
		return
	}
	// Билет гасим здесь: привязка состоялась. Ошибку только логируем — второй
	// фактор уже включён, и разворачивать это ради непогашенного билета хуже,
	// чем оставить его дожить до истечения (он одноразовый и с TTL).
	if ch.bindCode != "" {
		if err := h.Repo.ConsumeBindTicket(r.Context(), ch.UserID, ch.bindCode, time.Now()); err != nil {
			authLog().Warn("не удалось погасить код привязки после настройки 2FA", "логин", ch.Login, "err", err)
		}
	}
	codes, err := h.Repo.ReplaceBackupCodes(r.Context(), ch.UserID)
	if err != nil {
		// Второй фактор уже включён — вход разрешаем, но об отсутствии резервных
		// кодов надо знать: их выпустят из профиля.
		authLog().Error("не удалось выпустить резервные коды при настройке 2FA", "err", err)
	}
	outcome, err := h.completeChallengeSession(w, r, ch, token, "2fa_enabled")
	if err != nil {
		return
	}
	if len(codes) == 0 {
		http.Redirect(w, r, outcome.returnURL, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderTemplate(w, backupCodesTmpl, map[string]any{"Codes": codes, "ContinueURL": outcome.returnURL})
}

// challengeOutcome — куда вести пользователя после успешного второго фактора.
type challengeOutcome struct{ returnURL string }

// completeChallengeSession гасит challenge, создаёт сессию и ставит куку.
func (h *Handlers) completeChallengeSession(w http.ResponseWriter, r *http.Request, ch Challenge, token, auditAction string) (challengeOutcome, error) {
	user, err := h.Repo.GetByID(r.Context(), ch.UserID)
	if err != nil {
		h.internalError(w, r, "чтение пользователя после второго фактора", err)
		return challengeOutcome{}, err
	}
	sessionToken, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		h.internalError(w, r, "создание сессии после второго фактора", err)
		return challengeOutcome{}, err
	}
	h.challenges().Delete(token)
	h.clearChallengeCookie(w, r)
	if h.LoginLimit != nil {
		h.LoginLimit.Reset(LoginKey(r, ch.Login))
	}
	if h.Auditor != nil {
		h.Auditor.LogAction(r.Context(), auditAction, "", "", "", user.ID, user.Login, r.RemoteAddr)
	}
	h.setSessionCookie(w, r, sessionToken)
	returnURL := ch.ReturnURL
	if returnURL == "" || !isLocalURL(returnURL) {
		returnURL = "/ui"
	}
	return challengeOutcome{returnURL: returnURL}, nil
}

// finishSecondFactor — успешное подтверждение обычным кодом.
func (h *Handlers) finishSecondFactor(w http.ResponseWriter, r *http.Request, ch Challenge, token, auditAction string) {
	outcome, err := h.completeChallengeSession(w, r, ch, token, auditAction)
	if err != nil {
		return
	}
	http.Redirect(w, r, outcome.returnURL, http.StatusFound)
}

// failSecondFactor показывает ошибку и, если попытки исчерпаны, отправляет
// вводить пароль заново.
func (h *Handlers) failSecondFactor(w http.ResponseWriter, r *http.Request, ch Challenge, token string, cause error) {
	if h.LoginLimit != nil {
		h.LoginLimit.Fail(LoginKey(r, ch.Login))
	}
	if !h.challenges().Fail(token) {
		h.clearChallengeCookie(w, r)
		http.Redirect(w, r, "/login?err=2fa", http.StatusFound)
		return
	}
	msg := "Неверный код подтверждения"
	if ch.Enroll && !ch.EnrollAuthorized {
		msg = "Неверный или просроченный код привязки"
	}
	if cause != nil && !strings.Contains(cause.Error(), "неверный код") {
		authLog().Warn("сбой проверки второго фактора", "логин", ch.Login, "err", cause)
	}
	h.renderTwoFactor(w, ch, msg)
}

// TwoFactorJSON — POST /auth/2fa: тот же шаг для клиентов JSON API. Challenge
// принимается телом запроса или кукой — REST-клиенту кука не нужна.
func (h *Handlers) TwoFactorJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginFormBytes)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(req.Challenge)
	if token == "" {
		if cookie, err := r.Cookie(challengeCookie); err == nil {
			token = cookie.Value
		}
	}
	ch, ok := h.challenges().Get(token)
	if !ok {
		http.Error(w, `{"error":"challenge expired"}`, http.StatusUnauthorized)
		return
	}
	if h.limitExceeded(w, r, ch.Login) {
		return
	}
	if ch.Enroll {
		// Первичная настройка требует показать QR и резервные коды — это работа
		// интерфейса, а не JSON-клиента.
		http.Error(w, `{"error":"2fa_setup_required"}`, http.StatusForbidden)
		return
	}
	if err := h.Repo.VerifySecondFactor(r.Context(), ch.UserID, strings.TrimSpace(req.Code), time.Now()); err != nil {
		if h.LoginLimit != nil {
			h.LoginLimit.Fail(LoginKey(r, ch.Login))
		}
		h.challenges().Fail(token)
		http.Error(w, `{"error":"invalid code"}`, http.StatusUnauthorized)
		return
	}
	user, err := h.Repo.GetByID(r.Context(), ch.UserID)
	if err != nil {
		h.internalErrorJSON(w, r, "чтение пользователя после второго фактора", err)
		return
	}
	sessionToken, err := h.Repo.CreateSession(r.Context(), user.ID, sessionMetaFromRequest(r))
	if err != nil {
		h.internalErrorJSON(w, r, "создание сессии после второго фактора (JSON)", err)
		return
	}
	h.challenges().Delete(token)
	h.clearChallengeCookie(w, r)
	if h.LoginLimit != nil {
		h.LoginLimit.Reset(LoginKey(r, ch.Login))
	}
	if h.Auditor != nil {
		h.Auditor.LogAction(r.Context(), "login_2fa", "", "", "", user.ID, user.Login, r.RemoteAddr)
	}
	h.setSessionCookie(w, r, sessionToken)
	respondJSONTo(w, map[string]any{
		"ok":   true,
		"user": map[string]any{"id": user.ID, "login": user.Login, "is_admin": user.IsAdmin},
	})
}
