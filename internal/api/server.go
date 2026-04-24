package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ivantit66/onebase/internal/auth"
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

func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo, port int) *Server {
	h := &handler{reg: reg, store: store, interp: interp}
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Public auth routes (no authentication required)
	authH := &auth.Handlers{Repo: authRepo}
	r.Get("/login", authH.LoginPage)
	r.Post("/login", authH.LoginSubmit)
	r.Post("/logout", authH.Logout)
	r.Get("/auth/status", authH.Status)
	r.Post("/auth/login", authH.LoginJSON)
	r.Get("/auth/bootstrap", authH.Bootstrap)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authRepo.Middleware)

		// REST API — catalogs
		r.Get("/catalogs/{entity}", h.listObjects(metadata.KindCatalog))
		r.Post("/catalogs/{entity}", h.createObject(metadata.KindCatalog))
		r.Get("/catalogs/{entity}/{id}", h.getObject(metadata.KindCatalog))
		r.Put("/catalogs/{entity}/{id}", h.updateObject(metadata.KindCatalog))
		r.Delete("/catalogs/{entity}/{id}", h.deleteObject(metadata.KindCatalog))
		// REST API — documents
		r.Get("/documents/{entity}", h.listObjects(metadata.KindDocument))
		r.Post("/documents/{entity}", h.createObject(metadata.KindDocument))
		r.Get("/documents/{entity}/{id}", h.getObject(metadata.KindDocument))
		r.Put("/documents/{entity}/{id}", h.updateObject(metadata.KindDocument))
		r.Delete("/documents/{entity}/{id}", h.deleteObject(metadata.KindDocument))

		// Web UI
		uiSrv := ui.New(reg, store, interp, authRepo)
		uiSrv.Mount(r)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui", http.StatusFound)
		})
	})

	addr := fmt.Sprintf(":%d", port)
	return &Server{handler: r, srv: &http.Server{Addr: addr, Handler: r}}
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
