package ui

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/webassets"
)

//go:embed static/ui.js
var uiJS []byte

//go:embed static/managed.js
var managedJS []byte

//go:embed static/query-builder.js
var queryBuilderJS []byte

// ETag'и приложенческого JS считаются один раз при старте по содержимому.
var (
	uiJSETag           = assetETag(uiJS)
	managedJSETag      = assetETag(managedJS)
	queryBuilderJSETag = assetETag(queryBuilderJS)
)

func assetETag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// serveAppJS отдаёт встроенный JS приложения с ревалидацией по ETag. Путь
// фиксирован (/static/ui.js) и не содержит хэша, а сам код меняется с каждым
// билдом, поэтому immutable-кэш на год отдавал бы устаревший скрипт после
// обновления (старый JS против нового HTML-bootstrap → тихо сломанные формы).
// no-cache заставляет клиента ревалидировать: при совпадении ETag ответ — 304
// без повторной передачи тела, поэтому свежесть не стоит лишнего трафика.
func serveAppJS(w http.ResponseWriter, r *http.Request, body []byte, etag string) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

// mountStatic регистрирует отдачу общих встроенных ассетов. Самохостинг вместо
// CDN: графики и редактор работают офлайн — десктопная база не должна зависеть
// от интернета. ECharts и Monaco вендорятся один раз в webassets и раздаются
// тем же путём, что и в конфигураторе лаунчера, чтобы рабочий стол и
// предпросмотр виджетов рисовались идентично.
func mountStatic(r chi.Router) {
	r.Get("/static/ui.js", func(w http.ResponseWriter, req *http.Request) {
		serveAppJS(w, req, uiJS, uiJSETag)
	})
	r.Get("/static/managed.js", func(w http.ResponseWriter, req *http.Request) {
		serveAppJS(w, req, managedJS, managedJSETag)
	})
	r.Get("/static/query-builder.js", func(w http.ResponseWriter, req *http.Request) {
		serveAppJS(w, req, queryBuilderJS, queryBuilderJSETag)
	})
	r.Handle("/vendor/echarts/*", http.StripPrefix("/vendor/echarts/", webassets.EChartsHandler()))
	// Monaco editor — инструменты разработчика (консоль кода/запросов, отладчик)
	// грузят его офлайн вместо CDN.
	r.Handle("/vendor/monaco/*", http.StripPrefix("/vendor/monaco/", webassets.MonacoHandler()))
	// SlickGrid — грид для редактируемых табличных частей managed-форм.
	r.Handle("/vendor/slickgrid/*", http.StripPrefix("/vendor/slickgrid/", webassets.SlickGridHandler()))
	// Quill — WYSIWYG-редактор для richtext-полей (план 65). Раздаётся только в
	// пользовательском режиме: формы сущностей с richtext-реквизитами здесь.
	// Конфигуратор лаунчера правит метаданные, а не данные — Quill ему не нужен.
	r.Handle("/vendor/quill/*", http.StripPrefix("/vendor/quill/", webassets.QuillHandler()))
	// Lucide — спрайт иконок навигации (план 73): <use> ссылается на символ по
	// этому пути, поэтому он нужен на каждой странице с панелью разделов.
	r.Handle("/vendor/lucide/*", http.StripPrefix("/vendor/lucide/", webassets.LucideHandler()))
}
