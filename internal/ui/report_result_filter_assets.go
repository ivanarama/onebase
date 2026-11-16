package ui

import (
	_ "embed"
	"github.com/go-chi/chi/v5"
	"net/http"
)

//go:embed static/report-result-filter.js
var reportResultFilterJS []byte
var reportResultFilterETag = assetETag(reportResultFilterJS)

func mountReportResultFilters(r chi.Router) {
	r.Get("/static/report-result-filter.js", func(w http.ResponseWriter, r *http.Request) {
		serveAppJS(w, r, reportResultFilterJS, reportResultFilterETag)
	})
}
