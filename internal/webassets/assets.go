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
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"sync"

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

// ECharts vendored once here so both the base UI (dashboard charts) and the
// launcher's configurator (widget preview) serve the same library from the same
// URL — предпросмотр виджета рисуется тем же ECharts, что и рабочий стол, без
// расхождений. Самохостинг вместо CDN: графики работают офлайн.
//
//go:embed echarts
var echartsFS embed.FS

// SlickGrid (6pac fork, MIT) vendored for editable table parts in managed forms.
// Only the IIFE browser-global builds are embedded — core, grid, dataview, editors,
// formatters, interactions. Самохостинг вместо CDN: грид работает офлайн,
// десктопная база не зависит от интернета. SortableJS не включён (reorder отключён
// в v1).
//
//go:embed slickgrid
var slickgridFS embed.FS

// Quill (snow theme, BSD-3) vendored for the richtext field WYSIWYG editor.
// Only the self-contained UMD bundle (quill.js) and the snow theme CSS are
// embedded — Quill 2.x bundles all its dependencies and inlines toolbar icons
// as data-URI SVG in the CSS, so no separate icon/font files are needed.
// Самохостинг вместо CDN: редактор richtext работает офлайн, десктопная база не
// зависит от интернета.
//
//go:embed quill
var quillFS embed.FS

// Lucide (ISC) — весь набор иконок одним SVG-спрайтом: каждая иконка это
// <symbol id="имя">, а страница ссылается на неё через <use>. Спрайт заменил
// курируемый набор инлайн-SVG в бинаре (план 72, подход A): теперь работает
// любое имя Lucide, а расширение набора не требует правки кода. Файл — источник
// правды и для разметки, и для списка имён (LucideSymbolNames), поэтому апгрейд
// версии = подмена одного файла. Самохостинг вместо CDN: иконки рисуются без
// интернета, как и остальные вендоренные ассеты.
//
// Embed the complete vendor directory, including the ISC license. Release
// archives ship the executable, so the required notice must travel with the
// embedded copy rather than exist only in the source checkout.
//
//go:embed lucide
var lucideFS embed.FS

// MonacoHandler serves the embedded Monaco tree. Mount it under
// /vendor/monaco/ in every server that renders a Monaco editor.
func MonacoHandler() http.Handler {
	sub, err := fs.Sub(monacoFS, "monaco")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// URL вендора НЕ версионируются, но содержимое привязано к версии
		// onebase: между релизами байты те же, поэтому кэшируем надолго. Сброс
		// при апгрейде обеспечивает service worker (имя кэша = ревизия сборки),
		// а не ревалидация по URL.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, req)
	})
}

// EChartsHandler serves the embedded ECharts bundle. Mount it under
// /vendor/echarts/ in every server that renders charts (base UI, configurator).
func EChartsHandler() http.Handler {
	sub, err := fs.Sub(echartsFS, "echarts")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, req)
	})
}

// SlickGridHandler serves the embedded SlickGrid assets. Mount it under
// /vendor/slickgrid/ in every server that renders editable table parts
// (base UI managed forms).
func SlickGridHandler() http.Handler {
	sub, err := fs.Sub(slickgridFS, "slickgrid")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, req)
	})
}

// LucideHandler serves the embedded Lucide sprite. Mount it under
// /vendor/lucide/ in every server that renders icons (base UI navigation,
// launcher configurator preview) — <use href="/vendor/lucide/sprite.svg#name">
// works only if the sprite lives on the same origin as the page.
func LucideHandler() http.Handler {
	sub, err := fs.Sub(lucideFS, "lucide")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// http.StripPrefix may leave this path with or without a leading slash,
		// depending on the mount prefix used by the caller.
		if req.URL.Path == "sprite.svg" || req.URL.Path == "/sprite.svg" {
			meta := lucideAssetMetadataValue()
			w.Header().Set("ETag", meta.etag)
			if req.URL.Query().Get("v") == meta.version {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				// The bare path is intentionally revalidated: future Lucide upgrades
				// must not leave browsers pinned to the previous sprite for a year.
				w.Header().Set("Cache-Control", "no-cache")
			}
		}
		fileSrv.ServeHTTP(w, req)
	})
}

// LucideSpriteVersion returns the content-derived cache key used by pages that
// reference the sprite. A changed vendor file therefore gets a different URL.
func LucideSpriteVersion() string {
	return lucideAssetMetadataValue().version
}

// LucideSymbolNames возвращает отсортированные идентификаторы всех <symbol>
// спрайта — это и есть множество доступных имён иконок. Разбирается сам
// вендоренный файл, а не отдельный сгенерированный список: список не может
// разойтись со спрайтом, потому что его нет. Результат считается один раз.
func LucideSymbolNames() []string {
	lucideNamesOnce.Do(func() {
		data, err := lucideFS.ReadFile("lucide/sprite.svg")
		if err != nil {
			return
		}
		names := make([]string, 0, 2048)
		for _, m := range lucideSymbolIDRe.FindAllSubmatch(data, -1) {
			names = append(names, string(m[1]))
		}
		sort.Strings(names)
		lucideNames = names
	})
	return slices.Clone(lucideNames)
}

var (
	lucideNamesOnce sync.Once
	lucideNames     []string
	// Идентификатор символа Lucide — kebab-case из латиницы и цифр. Узкий класс
	// символов не даёт ничему постороннему из спрайта попасть в имя, которое
	// потом подставляется в href.
	lucideSymbolIDRe   = regexp.MustCompile(`<symbol id="([a-z0-9-]+)"`)
	lucideMetadataOnce sync.Once
	lucideMetadata     lucideAssetMetadata
)

type lucideAssetMetadata struct {
	version string
	etag    string
}

func lucideAssetMetadataValue() lucideAssetMetadata {
	lucideMetadataOnce.Do(func() {
		data, err := lucideFS.ReadFile("lucide/sprite.svg")
		if err != nil {
			// The go:embed directive makes this unreachable in a valid build. Keep
			// a non-immutable fallback instead of making package initialization fatal.
			lucideMetadata = lucideAssetMetadata{version: "unavailable", etag: `"unavailable"`}
			return
		}
		sum := sha256.Sum256(data)
		lucideMetadata = lucideAssetMetadata{
			version: hex.EncodeToString(sum[:12]),
			etag:    `"` + hex.EncodeToString(sum[:]) + `"`,
		}
	})
	return lucideMetadata
}

// QuillHandler serves the embedded Quill bundle. Mount it under /vendor/quill/
// in the base UI server — richtext-fields on entity forms load quill.js and
// quill.snow.css from there.
func QuillHandler() http.Handler {
	sub, err := fs.Sub(quillFS, "quill")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileSrv := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileSrv.ServeHTTP(w, req)
	})
}
