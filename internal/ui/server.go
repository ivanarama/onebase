package ui

import (
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

type Server struct {
	reg      *runtime.Registry
	store    *storage.DB
	interp   *interpreter.Interpreter
	authRepo *auth.Repo
}

func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo) *Server {
	return &Server{reg: reg, store: store, interp: interp, authRepo: authRepo}
}

func (s *Server) Mount(r chi.Router) {
	r.Get("/ui", s.index)
	r.Get("/ui/", s.index)
	r.Get("/ui/{kind}/{entity}", s.list)
	r.Get("/ui/{kind}/{entity}/new", s.form)
	r.Post("/ui/{kind}/{entity}/new", s.submit)
	r.Get("/ui/{kind}/{entity}/{id}", s.formEdit)
	r.Post("/ui/{kind}/{entity}/{id}", s.submitEdit)
	r.Get("/ui/register/{name}", s.registerMovements)
	r.Get("/ui/register/{name}/balances", s.registerBalances)
	r.Get("/ui/report/{name}", s.reportForm)
	r.Post("/ui/report/{name}", s.reportRun)

	// Admin: user management
	r.Get("/ui/admin/users", s.adminUsers)
	r.Get("/ui/admin/users/new", s.adminUserNew)
	r.Post("/ui/admin/users/new", s.adminUserCreate)
	r.Post("/ui/admin/users/{id}/delete", s.adminUserDelete)
}

type navSection struct {
	Kind     string
	Entities []*metadata.Entity
}

type navItem struct {
	Label string
	URL   string
}

type navGroup struct {
	Kind  string
	Items []navItem
}

func (s *Server) buildNav() []navGroup {
	entities := s.reg.Entities()
	sort.Slice(entities, func(i, j int) bool { return entities[i].Name < entities[j].Name })

	var catalogs, documents []navItem
	for _, e := range entities {
		url := "/ui/" + strings.ToLower(string(e.Kind)) + "/" + strings.ToLower(e.Name)
		item := navItem{Label: e.Name, URL: url}
		if e.Kind == metadata.KindCatalog {
			catalogs = append(catalogs, item)
		} else {
			documents = append(documents, item)
		}
	}

	registers := s.reg.Registers()
	sort.Slice(registers, func(i, j int) bool { return registers[i].Name < registers[j].Name })
	var regItems []navItem
	for _, reg := range registers {
		regItems = append(regItems, navItem{
			Label: reg.Name + " (движения)",
			URL:   "/ui/register/" + strings.ToLower(reg.Name),
		})
		regItems = append(regItems, navItem{
			Label: reg.Name + " (остатки)",
			URL:   "/ui/register/" + strings.ToLower(reg.Name) + "/balances",
		})
	}

	var nav []navGroup
	if len(catalogs) > 0 {
		nav = append(nav, navGroup{Kind: "Справочники", Items: catalogs})
	}
	if len(documents) > 0 {
		nav = append(nav, navGroup{Kind: "Документы", Items: documents})
	}
	if len(regItems) > 0 {
		nav = append(nav, navGroup{Kind: "Регистры", Items: regItems})
	}

	reps := s.reg.Reports()
	sort.Slice(reps, func(i, j int) bool { return reps[i].Name < reps[j].Name })
	var repItems []navItem
	for _, rep := range reps {
		label := rep.Title
		if label == "" {
			label = rep.Name
		}
		repItems = append(repItems, navItem{
			Label: label,
			URL:   "/ui/report/" + strings.ToLower(rep.Name),
		})
	}
	if len(repItems) > 0 {
		nav = append(nav, navGroup{Kind: "Отчёты", Items: repItems})
	}
	return nav
}
