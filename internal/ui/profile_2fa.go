package ui

// Экран «Профиль → Второй фактор» (план 84): включение TOTP по QR-коду,
// резервные коды и отключение. Всё — про свою учётку; чужой второй фактор
// администратор не настраивает (секрет знает только владелец телефона), он
// может лишь потребовать 2FA политикой или сбросить её в карточке пользователя.

import (
	"net/http"
	"time"

	"github.com/ivantit66/onebase/internal/auth"
)

const tplProfile2FA = `{{define "profile-2fa"}}` + adminHead + `
<main>
<div style="margin-bottom:16px"><a href="/ui" style="color:#64748b;font-size:13px;text-decoration:none">← Главная</a></div>
<h2>Второй фактор</h2>
{{if .Error}}<div class="error" style="max-width:620px">{{.Error}}</div>{{end}}
{{if .Success}}<div style="background:#f0fdf4;border:1px solid #86efac;color:#15803d;padding:12px 16px;border-radius:7px;margin-bottom:16px;font-size:14px;max-width:620px">✓ {{.Success}}</div>{{end}}

{{if .Codes}}
<div class="card" style="max-width:620px;margin-bottom:16px">
  <h3 style="margin-bottom:10px">Резервные коды</h3>
  <p style="font-size:13px;color:#475569;margin-bottom:12px">Сохраните их: каждый работает один раз и заменяет код из приложения, если телефон недоступен. Показываются только сейчас.</p>
  <div style="display:grid;grid-template-columns:repeat(2,1fr);gap:6px;font-family:Consolas,monospace;font-size:15px">
    {{range .Codes}}<span style="background:#f8fafc;border:1px solid #e2e8f0;border-radius:5px;padding:6px;text-align:center">{{.}}</span>{{end}}
  </div>
</div>
{{end}}

{{if .Setup}}
<div class="card" style="max-width:620px">
  <h3 style="margin-bottom:10px">Подключение приложения</h3>
  <p style="font-size:13px;color:#475569;margin-bottom:12px">Отсканируйте QR-код приложением-аутентификатором (Google Authenticator, Aegis, 1Password и совместимые) или введите ключ вручную, затем подтвердите кодом из приложения.</p>
  <div style="text-align:center;margin-bottom:12px"><img src="/ui/profile/2fa/qr" width="220" height="220" alt="QR-код"></div>
  <div style="font-family:Consolas,monospace;font-size:14px;letter-spacing:1px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:5px;padding:8px;text-align:center;margin-bottom:16px;word-break:break-all">{{.Secret}}</div>
  <form method="POST">
    <input type="hidden" name="action" value="confirm">
    <div class="form-group">
      <label>Код из приложения</label>
      <input type="text" name="code" autocomplete="one-time-code" inputmode="numeric" placeholder="123456" autofocus>
    </div>
    <button class="btn btn-primary" type="submit">Включить второй фактор</button>
    <a class="btn btn-sm" href="/ui/profile/2fa" style="background:#e2e8f0;color:#334155;margin-left:8px">Отмена</a>
  </form>
</div>
{{else}}
<div class="card" style="max-width:620px;margin-bottom:16px">
  <h3 style="margin-bottom:10px">Состояние</h3>
  {{if .Info.Enabled}}
    <p style="font-size:14px;color:#15803d;font-weight:600;margin-bottom:8px">✓ Второй фактор включён</p>
    <p style="font-size:13px;color:#475569">Осталось резервных кодов: <b>{{.Info.BackupCodesLeft}}</b></p>
    {{if .Info.SecretPlaintext}}<p style="font-size:13px;color:#b45309;margin-top:8px">⚠ Секрет хранится в базе открытым текстом: мастер-ключ (ONEBASE_MASTER_KEY, план 83) не был задан в момент включения. Задайте ключ и переподключите приложение.</p>{{end}}
  {{else}}
    <p style="font-size:14px;color:#64748b;margin-bottom:8px">Второй фактор выключен.</p>
    {{if .Required}}<p style="font-size:13px;color:#b45309">⚠ Политика базы требует второй фактор для вашей учётной записи — при следующем входе его придётся настроить.</p>{{end}}
  {{end}}
</div>

{{if .Info.Enabled}}
<div class="card" style="max-width:620px;margin-bottom:16px">
  <h3 style="margin-bottom:10px">Новые резервные коды</h3>
  <p style="font-size:13px;color:#475569;margin-bottom:12px">Прежние коды перестанут работать. Подтвердите паролем или кодом из приложения.</p>
  <form method="POST">
    <input type="hidden" name="action" value="codes">
    <div class="form-group">
      <label>Пароль или код подтверждения</label>
      <input type="password" name="confirm" autocomplete="current-password">
    </div>
    <button class="btn btn-primary" type="submit">Выпустить коды</button>
  </form>
</div>
<div class="card" style="max-width:620px;margin-bottom:16px">
  <h3 style="margin-bottom:10px">Привязать другое устройство</h3>
  <p style="font-size:13px;color:#475569;margin-bottom:12px">Прежний аутентификатор и все резервные коды перестанут работать. Подтвердите паролем или текущим кодом.</p>
  <form method="POST">
    <input type="hidden" name="action" value="start">
    <div class="form-group">
      <label>Пароль или код подтверждения</label>
      <input type="password" name="confirm" autocomplete="current-password">
    </div>
    <button class="btn btn-primary" type="submit">Привязать другое устройство</button>
  </form>
</div>
<div class="card" style="max-width:620px">
  <h3 style="margin-bottom:10px">Отключить второй фактор</h3>
  {{if .Required}}
  <p style="font-size:13px;color:#b45309">Политика базы требует второй фактор для вашей учётной записи — отключить его нельзя.</p>
  {{else}}
  <form method="POST" data-ob-confirm="Отключить второй фактор?">
    <input type="hidden" name="action" value="disable">
    <div class="form-group">
      <label>Пароль или код подтверждения</label>
      <input type="password" name="confirm" autocomplete="current-password">
    </div>
    <button class="btn btn-danger" type="submit">Отключить</button>
  </form>
  {{end}}
</div>
{{else}}
<div class="card" style="max-width:620px">
  <form method="POST">
    <input type="hidden" name="action" value="start">
    <button class="btn btn-primary" type="submit">Включить второй фактор</button>
  </form>
</div>
{{end}}
{{end}}
</main></body></html>
{{end}}`

// selfTwoFactor — /ui/profile/2fa: состояние второго фактора и действия над ним.
func (s *Server) selfTwoFactor(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, defaultFormMemoryBytes)
	u := auth.UserFromContext(r.Context())
	if u == nil || s.authRepo == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	data := map[string]any{}
	if r.Method == http.MethodPost {
		s.limitMultipartRequest(w, r)
		if err := parseBoundedForm(r, defaultFormMemoryBytes); err != nil {
			http.Error(w, s.errText(r, err), uploadErrorStatus(err))
			return
		}
		switch r.FormValue("action") {
		case "start":
			s.startTwoFactorSetup(w, r, u)
			return
		case "confirm":
			s.confirmTwoFactorSetup(w, r, u, data)
		case "codes":
			s.regenerateBackupCodes(w, r, u, data)
		case "disable":
			s.disableTwoFactor(w, r, u, data)
		}
	}
	s.renderTwoFactorProfile(w, r, u, data)
}

// renderTwoFactorProfile дорисовывает состояние и отдаёт страницу.
func (s *Server) renderTwoFactorProfile(w http.ResponseWriter, r *http.Request, u *auth.User, data map[string]any) {
	info, err := s.authRepo.TwoFactorInfoFor(r.Context(), u.ID)
	if err != nil && data["Error"] == nil {
		data["Error"] = s.errText(r, err)
	}
	data["Info"] = info
	data["Required"] = s.authRepo.RequiresTwoFactor(r.Context(), s.authRepo.AuthPolicy(r.Context()), u)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	renderAdminTemplate(w, "profile-2fa", data)
}

// startTwoFactorSetup выдаёт новый секрет и показывает QR. Секрет в базу не
// пишется до подтверждения кодом: брошенная на полпути настройка не должна
// оставлять учётку с секретом, которого нет ни в одном телефоне.
func (s *Server) startTwoFactorSetup(w http.ResponseWriter, r *http.Request, u *auth.User) {
	// Перепривязка фактора — операция чувствительнее отключения: она переносит
	// второй фактор на другое устройство И отзывает резервные коды владельца.
	// Раньше она была единственной незащищённой: угнанная (или просто открытая)
	// сессия выдавала себе новый секрет, подтверждала его и заодно обходила
	// guard на disable. Теперь тот же confirmIdentity, что у disable и у
	// перевыпуска кодов.
	enabled, err := s.authRepo.TOTPEnabled(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if enabled {
		//nolint:gosec // G120: предел тела ставит selfTwoFactor (MaxBytesReader + parseBoundedForm)
		if !s.confirmIdentity(r, u, r.FormValue("confirm")) {
			s.renderTwoFactorProfile(w, r, u, map[string]any{
				"Rebind": true,
				"Error":  "Второй фактор уже включён. Чтобы привязать другое устройство, подтвердите пароль или текущий код",
			})
			return
		}
		s.logSessionAudit(r, "2fa_rebind_started", u.Login, u.ID)
	}
	token, secret, err := auth.StartEnrollment(u.ID, u.Login)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.EnrollCookie,
		Value:    token,
		Path:     "/ui/profile",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	s.renderTwoFactorProfile(w, r, u, map[string]any{
		"Setup":  true,
		"Secret": auth.FormatTOTPSecret(secret),
	})
}

// currentEnrollment достаёт начатую настройку текущего пользователя.
func currentEnrollment(r *http.Request, u *auth.User) (auth.Challenge, string, bool) {
	cookie, err := r.Cookie(auth.EnrollCookie)
	if err != nil || cookie.Value == "" {
		return auth.Challenge{}, "", false
	}
	ch, ok := auth.Enrollment(cookie.Value)
	if !ok || ch.UserID != u.ID {
		return auth.Challenge{}, "", false
	}
	return ch, cookie.Value, true
}

func clearEnrollmentCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.EnrollCookie,
		Value:    "",
		Path:     "/ui/profile",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// confirmTwoFactorSetup включает второй фактор кодом из приложения.
func (s *Server) confirmTwoFactorSetup(w http.ResponseWriter, r *http.Request, u *auth.User, data map[string]any) {
	ch, token, ok := currentEnrollment(r, u)
	if !ok {
		data["Error"] = "Настройка не найдена или устарела — начните заново"
		return
	}
	step, valid := auth.VerifyTOTP(ch.Secret, r.FormValue("code"), time.Now(), 0) //nolint:gosec // G120: предел тела ставит selfTwoFactor (MaxBytesReader + parseBoundedForm); gosec видит только эту функцию
	if !valid {
		data["Setup"] = true
		data["Secret"] = auth.FormatTOTPSecret(ch.Secret)
		data["Error"] = "Неверный код — проверьте время на телефоне и попробуйте ещё раз"
		return
	}
	if err := s.authRepo.EnableTOTP(r.Context(), u.ID, ch.Secret, step); err != nil {
		data["Error"] = s.errText(r, err)
		return
	}
	auth.FinishEnrollment(token)
	clearEnrollmentCookie(w, r)
	s.logSessionAudit(r, "2fa_enabled", u.Login, u.ID)
	codes, err := s.authRepo.ReplaceBackupCodes(r.Context(), u.ID)
	if err != nil {
		data["Error"] = "Второй фактор включён, но резервные коды не выпущены: " + s.errText(r, err)
		return
	}
	data["Codes"] = codes
	data["Success"] = "Второй фактор включён"
}

// confirmIdentity проверяет пароль ИЛИ код второго фактора. Пароль подходит не
// всем: у учётки, заведённой через SSO, его нет — там подтверждением служит код.
func (s *Server) confirmIdentity(r *http.Request, u *auth.User, value string) bool {
	if value == "" {
		return false
	}
	if _, err := s.authRepo.Authenticate(r.Context(), u.Login, value); err == nil {
		return true
	}
	return s.authRepo.VerifySecondFactor(r.Context(), u.ID, value, time.Now()) == nil
}

func (s *Server) regenerateBackupCodes(w http.ResponseWriter, r *http.Request, u *auth.User, data map[string]any) {
	//nolint:gosec // G120: предел тела ставит selfTwoFactor (MaxBytesReader + parseBoundedForm); gosec видит только эту функцию
	if !s.confirmIdentity(r, u, r.FormValue("confirm")) {
		data["Error"] = "Неверный пароль или код подтверждения"
		return
	}
	codes, err := s.authRepo.ReplaceBackupCodes(r.Context(), u.ID)
	if err != nil {
		data["Error"] = s.errText(r, err)
		return
	}
	s.logSessionAudit(r, "2fa_backup_codes_reissued", u.Login, u.ID)
	data["Codes"] = codes
	data["Success"] = "Выпущены новые резервные коды"
}

func (s *Server) disableTwoFactor(w http.ResponseWriter, r *http.Request, u *auth.User, data map[string]any) {
	if s.authRepo.RequiresTwoFactor(r.Context(), s.authRepo.AuthPolicy(r.Context()), u) {
		data["Error"] = "Политика базы требует второй фактор для вашей учётной записи"
		return
	}
	//nolint:gosec // G120: см. выше — тело запроса ограничено в selfTwoFactor
	if !s.confirmIdentity(r, u, r.FormValue("confirm")) {
		data["Error"] = "Неверный пароль или код подтверждения"
		return
	}
	if err := s.authRepo.DisableTOTP(r.Context(), u.ID); err != nil {
		data["Error"] = s.errText(r, err)
		return
	}
	s.logSessionAudit(r, "2fa_disabled", u.Login, u.ID)
	data["Success"] = "Второй фактор отключён"
}

// selfTwoFactorQR отдаёт QR начатой настройки. Секрет берётся из хранилища по
// токену настройки, а не из запроса.
func (s *Server) selfTwoFactorQR(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		http.NotFound(w, r)
		return
	}
	ch, _, ok := currentEnrollment(r, u)
	if !ok || ch.Secret == "" {
		http.NotFound(w, r)
		return
	}
	issuer := s.cfg.AppName
	if issuer == "" {
		issuer = "OneBase"
	}
	auth.WriteOTPAuthQR(w, r, issuer, u.Login, ch.Secret)
}
