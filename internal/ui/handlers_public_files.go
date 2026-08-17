package ui

// Отдача опубликованных вложений (план 127): GET /pub/{token} без
// аутентификации. Токен непредсказуем и отзывается — см. storage/public_files.go.

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// inlineSafeType сообщает, можно ли показывать такой тип прямо в браузере.
//
// text/html и svg на своём домене = XSS с доступом к cookie админки: страница
// откроется в origin платформы. Такие файлы отдаются вложением с нейтральным
// типом, а не inline.
func inlineSafeType(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	switch {
	case m == "image/svg+xml": // векторная картинка со скриптом внутри
		return false
	case strings.HasPrefix(m, "image/"), strings.HasPrefix(m, "video/"), strings.HasPrefix(m, "audio/"):
		return true
	case m == "application/pdf", m == "text/plain":
		return true
	}
	return false
}

// publicFileServe отдаёт файл по токену публикации. Любая неудача — 404:
// существование вложения тоже информация.
func (s *Server) publicFileServe(w http.ResponseWriter, r *http.Request) {
	// Публичная отдача — та же поверхность конфигурации наружу, что и /hs/*,
	// поэтому подчиняется предохранителю сети (план 62).
	if !s.netEnabled(r.Context()) {
		http.Error(w, ErrNetworkLocked.Error(), http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		http.NotFound(w, r)
		return
	}
	pf, err := s.store.PublicFileByToken(r.Context(), token)
	if err != nil || pf == nil || pf.Expired(time.Now()) {
		http.NotFound(w, r)
		return
	}
	f, att, err := s.store.OpenAttachment(r.Context(), pf.AttachmentID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer closeRead("публичный файл", f)

	name := pf.Filename
	if name == "" {
		name = att.Filename
	}

	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	// Файл пользователя не должен исполняться как часть интерфейса, даже если
	// тип оказался «безопасным»: sandbox отключает скрипты и формы.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(pf.CacheSeconds)+", immutable")

	if inlineSafeType(att.MimeType) {
		h.Set("Content-Type", att.MimeType)
		h.Set("Content-Disposition", "inline; filename="+strconv.Quote(name))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition(name))
	}
	// ServeContent сам обрабатывает Range, If-Modified-Since и If-None-Match.
	http.ServeContent(w, r, name, att.UploadedAt, f)
}
