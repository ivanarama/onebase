package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
)

type Server struct {
	srv     *http.Server
	handler http.Handler
}

func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter) *Server {
	h := &handler{reg: reg, store: store, interp: interp}
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// REST API
	r.Post("/catalogs/{entity}", h.createObject(metadata.KindCatalog))
	r.Get("/catalogs/{entity}/{id}", h.getObject(metadata.KindCatalog))
	r.Post("/documents/{entity}", h.createObject(metadata.KindDocument))
	r.Get("/documents/{entity}/{id}", h.getObject(metadata.KindDocument))

	// Web UI
	uiSrv := ui.New(reg, store, interp)
	uiSrv.Mount(r)

	// Redirect root to UI
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusFound)
	})

	return &Server{handler: r, srv: &http.Server{Addr: ":8080", Handler: r}}
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
