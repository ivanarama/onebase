package ui

// Встроенная страница интерактивной документации HTTP-сервисов (план 61).
// RapiDoc вендорится в бинарь (как Monaco/ECharts в internal/webassets) —
// самохостинг вместо CDN, чтобы /hs/docs работала офлайн/в закрытом контуре.
// Страница рендерит /hs/openapi.json и доступна только администратору.

import (
	_ "embed"
	"net/http"
	"net/url"

	"github.com/ivantit66/onebase/internal/auth"
)

//go:embed rapidoc/rapidoc-min.js
var rapidocJS []byte

// serviceDocs — GET /hs/docs. Интерактивная документация (RapiDoc) поверх
// OpenAPI-спеки сервиса. Доступ только администратору.
func (s *Server) serviceDocs(w http.ResponseWriter, r *http.Request) {
	if !s.requireServiceAdmin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(rapidocPage))
}

// serviceDocsAsset — GET /hs/docs/rapidoc-min.js. Встроенный бандл RapiDoc.
func (s *Server) serviceDocsAsset(w http.ResponseWriter, r *http.Request) {
	if !s.requireServiceAdmin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(rapidocJS)
}

// requireServiceAdmin пускает только администратора (по сессии). На свежей базе
// без пользователей аутентификация отключена — пускаем (как session-middleware).
// Сервисы монтируются вне middleware, поэтому проверяем сессию сами.
func (s *Server) requireServiceAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.authRepo == nil {
		return true // auth не сконфигурирован (dev/тесты)
	}
	ctx := r.Context()
	if has, err := s.authRepo.HasUsers(ctx); err == nil && !has {
		return true // пользователей нет → auth отключён
	}
	token := sessionToken(r)
	if token == "" {
		s.denyDocs(w, r)
		return false
	}
	u, err := s.authRepo.LookupSessionKind(ctx, token, auth.SessionKindEnterprise)
	if err != nil {
		s.denyDocs(w, r)
		return false
	}
	if !u.IsAdmin {
		writeServiceError(w, http.StatusForbidden, "документация доступна только администратору")
		return false
	}
	return true
}

func (s *Server) denyDocs(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login?return="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
}

const rapidocPage = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <title>HTTP-сервисы — API</title>
  <meta name="viewport" content="width=device-width, minimum-scale=1, initial-scale=1">
  <script type="module" src="/hs/docs/rapidoc-min.js"></script>
</head>
<body style="margin:0">
  <rapi-doc
    spec-url="/hs/openapi.json"
    render-style="read"
    theme="light"
    show-header="true"
    allow-try="true"
    allow-authentication="true"
    allow-server-selection="false"
    regular-font="-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
    heading-text="onebase — HTTP-сервисы">
  </rapi-doc>
</body>
</html>`
