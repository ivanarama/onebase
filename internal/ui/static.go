package ui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/webassets"
)

// Встроенные сторонние ассеты пользовательского режима. Самохостинг вместо CDN:
// графики дашбордов (ECharts) рисуются мгновенно и работают офлайн — десктопная
// база не должна зависеть от интернета.
//
//go:embed static/vendor/*
var vendorFS embed.FS

// mountStatic регистрирует отдачу встроенных ассетов под /static/vendor/.
// Путь намеренно отделён от /static/forms/ (пользовательские картинки форм),
// чтобы не перехватывать чужие маршруты.
func mountStatic(r chi.Router) {
	sub, err := fs.Sub(vendorFS, "static/vendor")
	if err != nil {
		return
	}
	fileSrv := http.StripPrefix("/static/vendor/", http.FileServer(http.FS(sub)))
	r.Get("/static/vendor/*", func(w http.ResponseWriter, req *http.Request) {
		// Ассеты версионируются в имени файла/пути, поэтому кэшируем надолго.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, req)
	})
	// Monaco editor (общий встроенный пакет) — инструменты разработчика
	// (консоль кода/запросов, отладчик) грузят его офлайн вместо CDN.
	r.Handle("/vendor/monaco/*", http.StripPrefix("/vendor/monaco/", webassets.MonacoHandler()))
}
