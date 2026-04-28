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

		cookie, err := req.Cookie("onebase_session")
		if err != nil {
			redirectToLogin(w, req)
			return
		}

		user, err := r.LookupSession(ctx, cookie.Value)
		if err != nil {
			redirectToLogin(w, req)
			return
		}

		// Load roles for this user (best-effort — don't fail if table missing yet)
		if roles, err2 := r.GetRolesForUser(ctx, user.ID); err2 == nil {
			user.Roles = roles
		}

		ctx = context.WithValue(ctx, userKey, user)
		// Inject audit user info for storage layer
		ctx = storage.WithAuditUser(ctx, user.ID, user.Login)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func redirectToLogin(w http.ResponseWriter, req *http.Request) {
	if strings.Contains(req.Header.Get("Accept"), "text/html") {
		http.Redirect(w, req, "/login?return="+req.URL.RequestURI(), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
}

func UserFromContext(ctx context.Context) *User {
	if u, ok := ctx.Value(userKey).(*User); ok {
		return u
	}
	return nil
}
