// Package webassets embeds heavy third-party browser assets shared by more than
// one HTTP server (the launcher's configurator and the base UI dev tools).
//
// Monaco is vendored here once and served by both servers under
// /vendor/monaco/, so the ~4 MB editor lives a single time in the repository
// and in the binary instead of being duplicated per package. Самохостинг
// вместо CDN: редактор и отладчик работают офлайн — десктопная база не должна
// зависеть от интернета.
package webassets

import (
	"io/fs"
	"net/http"

	"embed"
)

// Only the minimal Monaco subset is vendored: the AMD loader, the core editor
// bundle, the editor web worker, the codicon font and the YAML grammar. The
// heavy language services (TypeScript/CSS/HTML/JSON) and other grammars are
// intentionally omitted — OneBase uses only yaml, plaintext and its own
// Monarch-registered languages (onebase-dsl, onebase-query).
//
//go:embed monaco
var monacoFS embed.FS

// MonacoHandler serves the embedded Monaco tree. Mount it under
// /vendor/monaco/ in every server that renders a Monaco editor.
func MonacoHandler() http.Handler {
	sub, err := fs.Sub(monacoFS, "monaco")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Версионируется в URL (vendor/monaco) — кэшируем надолго.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, req)
	})
}
