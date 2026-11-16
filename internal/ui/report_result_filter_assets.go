package ui

import (
	_ "embed"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed static/report-result-filter.js
var reportResultFilterJS []byte
var reportResultFilterETag = assetETag(reportResultFilterJS)

func mountReportResultFilters(r chi.Router) {
	r.Get("/static/report-result-filter.js", func(w http.ResponseWriter, r *http.Request) {
		serveAppJS(w, r, reportResultFilterJS, reportResultFilterETag)
	})
}
