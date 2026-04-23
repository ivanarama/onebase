package launcher

import (
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server is the launcher HTTP server (list of registered bases).
type Server struct {
	h  *handler
	ln net.Listener
}

// NewServer creates a launcher server bound to a random available port.
func NewServer(store *Store, runner *Runner) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	h := &handler{store: store, runner: runner}
	return &Server{h: h, ln: ln}, nil
}

// URL returns the base URL of the launcher server.
func (s *Server) URL() string { return "http://" + s.ln.Addr().String() }

func (s *Server) ListenAndServe() error {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Get("/", s.h.index)
	r.Get("/bases/new", s.h.newForm)
	r.Post("/bases", s.h.create)
	r.Get("/bases/{id}/edit", s.h.editForm)
	r.Post("/bases/{id}", s.h.update)
	r.Post("/bases/{id}/delete", s.h.delete)
	r.Post("/bases/{id}/start", s.h.start)
	r.Post("/bases/{id}/stop", s.h.stop)
	r.Post("/bases/{id}/migrate", s.h.migrate)
	r.Post("/bases/{id}/config/export", s.h.configExport)
	r.Post("/bases/{id}/config/import", s.h.configImport)

	return http.Serve(s.ln, r)
}
