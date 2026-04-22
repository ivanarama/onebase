package ui

import (
	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type Server struct {
	reg    *runtime.Registry
	store  *storage.DB
	interp *interpreter.Interpreter
}

func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter) *Server {
	return &Server{reg: reg, store: store, interp: interp}
}

func (s *Server) Mount(r chi.Router) {
	r.Get("/ui", s.index)
	r.Get("/ui/", s.index)
	r.Get("/ui/{kind}/{entity}", s.list)
	r.Get("/ui/{kind}/{entity}/new", s.form)
	r.Post("/ui/{kind}/{entity}/new", s.submit)
	r.Get("/ui/{kind}/{entity}/{id}", s.formEdit)
	r.Post("/ui/{kind}/{entity}/{id}", s.submitEdit)
}

type navSection struct {
	Kind     string
	Entities []*metadata.Entity
}

func (s *Server) buildNav() []navSection {
	entities := s.reg.Entities()
	var catalogs, documents []*metadata.Entity
	for _, e := range entities {
		if e.Kind == metadata.KindCatalog {
			catalogs = append(catalogs, e)
		} else {
			documents = append(documents, e)
		}
	}
	var nav []navSection
	if len(catalogs) > 0 {
		nav = append(nav, navSection{Kind: "Справочники", Entities: catalogs})
	}
	if len(documents) > 0 {
		nav = append(nav, navSection{Kind: "Документы", Entities: documents})
	}
	return nav
}

