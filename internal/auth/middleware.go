package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

type contextKey string

const (
	userKey       contextKey = "auth_user"
	openAccessKey contextKey = "auth_open_access"
)

func (r *Repo) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		hasUsers, err := r.hasUsersCached(ctx)
		if err != nil {
			writeAuthUnavailable(w, false)
			return
		}
		if !hasUsers {
			next.ServeHTTP(w, req.WithContext(ContextWithOpenAccess(ctx)))
			return
		}

		r.serveWithSession(next, w, req)
	})
}

func (r *Repo) serveWithSession(next http.Handler, w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()

	// Сессия принимается только из cookie. Токен в query (?_tk=) больше
	// не поддерживается: он утекал в stdout-лог (middleware.Logger пишет
	// полный RequestURI), Referer и историю браузера — план 53, этап 1.
	// Конфигуратор передаёт сессию через /auth/bootstrap?code=<одноразовый>.
	var token string
	if cookie, err := req.Cookie("onebase_session"); err == nil {
		token = cookie.Value
	}

	if token == "" {
		redirectToLogin(w, req)
		return
	}

	user, err := r.LookupSessionKind(ctx, token, SessionKindEnterprise)
	if err != nil {
		redirectToLogin(w, req)
		return
	}

	// last_seen_at для админки сессий (план 78); троттлится внутри. Ошибка не
	// влияет на доступ — пользователь уже аутентифицирован, устареет лишь
	// отметка «последняя активность». Отказывать в запросе из-за неё нельзя,
	// поэтому пишем в лог на уровне Debug: это горячий путь каждого запроса,
	// и Warn залил бы журнал при недоступной БД.
	if err := r.TouchSession(ctx, token, time.Now()); err != nil {
		authLog().Debug("не удалось обновить last_seen_at сессии", "err", err)
	}

	next.ServeHTTP(w, req.WithContext(r.contextWithUser(ctx, user)))
}

// APITokenOrSessionMiddleware accepts REST API Bearer tokens first and falls
// back to the regular session cookie middleware. It is intentionally separate
// from Middleware so UI routes keep their cookie-only authentication behavior.
func (r *Repo) APITokenOrSessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		hasUsers, err := r.hasUsersCached(ctx)
		if err != nil {
			writeAuthUnavailable(w, true)
			return
		}
		if !hasUsers {
			next.ServeHTTP(w, req.WithContext(ContextWithOpenAccess(ctx)))
			return
		}

		token, present := bearerToken(req)
		if !present {
			r.serveWithSession(next, w, req)
			return
		}
		if token == "" {
			writeUnauthorizedJSON(w)
			return
		}
		user, err := r.LookupAPIToken(ctx, token)
		if err != nil {
			writeUnauthorizedJSON(w)
			return
		}
		next.ServeHTTP(w, req.WithContext(r.contextWithUser(ctx, user)))
	})
}

func bearerToken(req *http.Request) (string, bool) {
	h := strings.TrimSpace(req.Header.Get("Authorization"))
	if h == "" {
		return "", false
	}
	parts := strings.Fields(h)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1], true
	}
	if len(parts) > 0 && strings.EqualFold(parts[0], "Bearer") {
		return "", true
	}
	return "", false
}

func (r *Repo) contextWithUser(ctx context.Context, user *User) context.Context {
	// Load roles for this user (best-effort — don't fail if table missing yet).
	// Cached on the hot path; invalidated on role changes (see cache.go).
	if roles, err := r.rolesForUserCached(ctx, user.ID); err == nil {
		user.Roles = roles
	}
	ctx = context.WithValue(ctx, userKey, user)
	return storage.WithAuditUser(ctx, user.ID, user.Login)
}

func redirectToLogin(w http.ResponseWriter, req *http.Request) {
	if strings.Contains(req.Header.Get("Accept"), "text/html") {
		http.Redirect(w, req, "/login?return="+req.URL.RequestURI(), http.StatusFound)
		return
	}
	writeUnauthorizedJSON(w)
}

func writeUnauthorizedJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
}

func writeAuthUnavailable(w http.ResponseWriter, jsonResponse bool) {
	if jsonResponse {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"authentication service unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "authentication service unavailable", http.StatusServiceUnavailable)
}

func UserFromContext(ctx context.Context) *User {
	if u, ok := ctx.Value(userKey).(*User); ok {
		return u
	}
	return nil
}

// ContextWithUser возвращает контекст с привязанным пользователем. Симметрично
// UserFromContext (userKey не экспортируется) — используется тестами и кодом,
// которому нужно подменить пользователя запроса (например роутером HTTP-сервисов
// с Basic-аутом — чтобы ТекущийПользователь()/аудит видели вызывающего).
func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// ContextWithOpenAccess marks the explicit bootstrap mode where the repository
// was reachable and confirmed to contain no users. A missing user alone must
// never imply unrestricted access: it may also mean middleware was bypassed or
// the authentication database failed.
func ContextWithOpenAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, openAccessKey, true)
}

// OpenAccessFromContext reports the bootstrap decision made by auth middleware.
func OpenAccessFromContext(ctx context.Context) bool {
	open, _ := ctx.Value(openAccessKey).(bool)
	return open
}
