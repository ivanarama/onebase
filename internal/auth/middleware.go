package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/storage"
)

type contextKey string

const userKey contextKey = "auth_user"

func (r *Repo) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		hasUsers, err := r.HasUsers(ctx)
		if err != nil || !hasUsers {
			next.ServeHTTP(w, req)
			return
		}

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

		user, err := r.LookupSession(ctx, token)
		if err != nil {
			redirectToLogin(w, req)
			return
		}

		next.ServeHTTP(w, req.WithContext(r.contextWithUser(ctx, user)))
	})
}

// APITokenOrSessionMiddleware accepts REST API Bearer tokens first and falls
// back to the regular session cookie middleware. It is intentionally separate
// from Middleware so UI routes keep their cookie-only authentication behavior.
func (r *Repo) APITokenOrSessionMiddleware(next http.Handler) http.Handler {
	session := r.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		hasUsers, err := r.HasUsers(ctx)
		if err != nil || !hasUsers {
			next.ServeHTTP(w, req)
			return
		}

		token, present := bearerToken(req)
		if !present {
			session.ServeHTTP(w, req)
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
	if roles, err := r.GetRolesForUser(ctx, user.ID); err == nil {
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
