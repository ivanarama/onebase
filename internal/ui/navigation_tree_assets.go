package ui

import (
	_ "embed"
	"github.com/go-chi/chi/v5"
	"net/http"
)

//go:embed static/navigation-tree.js
var navigationTreeJS []byte
var navigationTreeETag = assetETag(navigationTreeJS)

func mountNavigationTree(r chi.Router) {
	r.Get("/static/navigation-tree.js", func(w http.ResponseWriter, r *http.Request) { serveAppJS(w, r, navigationTreeJS, navigationTreeETag) })
}
