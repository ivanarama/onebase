package ui

// Заголовки безопасности уровня HTTP-сервиса (план 128).
//
// Глобальный websec.SecurityHeaders подобран под админку: CSP там ограничивает
// только frame-ancestors, потому что интерфейс грузит свои скрипты и инлайн-
// обработчики. Публичному сервису, отдающему HTML постороннему посетителю,
// этого мало — ему нужен настоящий default-src, запрет фреймов и HSTS за
// TLS-терминатором.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/ivantit66/onebase/internal/httpservice"
)

// forbiddenExtraHeaders — заголовки, которые нельзя задать через extra:
// nosniff отключать незачем, а CORS живёт своим механизмом сервиса.
func forbiddenExtraHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "x-content-type-options" || strings.HasPrefix(n, "access-control-")
}

// applyServiceSecurityHeaders ставит заголовки ДО исполнения обработчика —
// чтобы они были и на ответах об ошибке (404 ресурса, 403, 500). Страница
// ошибки без политики — дыра в этой самой политике.
func applyServiceSecurityHeaders(w http.ResponseWriter, r *http.Request, svc *httpservice.Service) {
	h := w.Header()
	// nosniff ставим всегда и не даём переопределить: отключаемый nosniff нужен
	// только чтобы выстрелить себе в ногу.
	h.Set("X-Content-Type-Options", "nosniff")

	cfg := svc.SecurityHeaders
	if cfg == nil {
		return
	}
	if cfg.CSP != "" {
		// Именно Set: два заголовка CSP браузер применяет как пересечение —
		// политика получилась бы строже задуманной.
		h.Set("Content-Security-Policy", cfg.CSP)
	}
	if cfg.FrameOptions != "" {
		h.Set("X-Frame-Options", cfg.FrameOptions)
	}
	if cfg.ReferrerPolicy != "" {
		h.Set("Referrer-Policy", cfg.ReferrerPolicy)
	}
	if cfg.HSTS > 0 && requestIsHTTPS(r) {
		// На http-ответе браузер HSTS игнорирует, но в localhost-разработке
		// заголовок способен закрыть доступ по http к тому же хосту — дорогая
		// ошибка, поэтому ставим только за TLS.
		h.Set("Strict-Transport-Security", "max-age="+strconv.Itoa(cfg.HSTS))
	}
	for name, value := range cfg.Extra {
		if forbiddenExtraHeader(name) {
			continue // отсеивается ещё в onebase check; здесь — страховка
		}
		h.Set(name, value)
	}
}

// requestIsHTTPS определяет, пришёл ли запрос по TLS. X-Forwarded-Proto
// принимается: платформа штатно живёт за reverse proxy, а подделка этого
// заголовка даёт атакующему лишь HSTS на собственном соединении.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}
