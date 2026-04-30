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

// Config holds static info shown in «О программе».
type Config struct {
	AppName     string
	AppVersion  string
	DSN         string
	PlatVersion string
}

type Server struct {
	reg      *runtime.Registry
	store    *storage.DB
	interp   *interpreter.Interpreter
	authRepo *auth.Repo
	cfg      Config
}

func New(reg *runtime.Registry, store *storage.DB, interp *interpreter.Interpreter, authRepo *auth.Repo, cfg ...Config) *Server {
	s := &Server{reg: reg, store: store, interp: interp, authRepo: authRepo}
	if len(cfg) > 0 {
		s.cfg = cfg[0]
	}
	return s
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
	r.Get("/ui/inforeg/{name}", s.infoRegList)
	r.Get("/ui/inforeg/{name}/new", s.infoRegForm)
	r.Post("/ui/inforeg/{name}/new", s.infoRegSubmit)
	r.Post("/ui/inforeg/{name}/delete", s.infoRegDelete)
	r.Get("/ui/report/{name}", s.reportForm)
	r.Post("/ui/report/{name}", s.reportRun)
	r.Get("/ui/processor/{name}", s.processorForm)
	r.Post("/ui/processor/{name}", s.processorRun)

	// Document posting
	r.Post("/ui/{kind}/{entity}/{id}/post", s.postDocument)
	r.Post("/ui/{kind}/{entity}/{id}/unpost", s.unpostDocument)

	// Delete record / mark for deletion
	r.Post("/ui/{kind}/{entity}/{id}/delete", s.deleteRecord)
	r.Post("/ui/{kind}/{entity}/delete-marked", s.deleteMarked)

	// Global delete-marked page
	r.Get("/ui/delete-marked", s.deleteMarkedAll)
	r.Post("/ui/delete-marked", s.deleteMarkedAll)

	// Admin: user management
	r.Get("/ui/admin/users", s.adminUsers)
	r.Get("/ui/admin/users/new", s.adminUserNew)
	r.Post("/ui/admin/users/new", s.adminUserCreate)
	r.Post("/ui/admin/users/{id}/delete", s.adminUserDelete)

	// Admin: active sessions
	r.Get("/ui/admin/sessions", s.adminSessions)
	r.Post("/ui/admin/sessions/{login}/kick", s.adminKickUser)

	// Admin: roles
	r.Get("/ui/admin/roles", s.adminRoles)
	r.Get("/ui/admin/users/{id}/roles", s.adminUserRoles)
	r.Post("/ui/admin/users/{id}/roles", s.adminUserRolesUpdate)

	// Admin: audit log
	r.Get("/ui/admin/audit", s.adminAudit)
	r.Get("/ui/{kind}/{entity}/{id}/history", s.recordHistory)

	// Admin: orphan movements cleanup
	r.Get("/ui/admin/cleanup", s.adminCleanup)
	r.Post("/ui/admin/cleanup", s.adminCleanup)

	// Constants
	r.Get("/ui/constants", s.constantsList)
	r.Post("/ui/constants", s.constantsSave)

	// Print forms
	r.Get("/ui/{kind}/{entity}/{id}/print/{form}", s.printDocument)

	// About
	r.Get("/ui/about", s.about)
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
		url := "/ui/" + strings.ToLower(string(e.Kind)) + "/" + e.Name
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

	inforegs := s.reg.InfoRegisters()
	sort.Slice(inforegs, func(i, j int) bool { return inforegs[i].Name < inforegs[j].Name })
	var inforegItems []navItem
	for _, ir := range inforegs {
		label := ir.Name
		if ir.Periodic {
			label += " (периодический)"
		}
		inforegItems = append(inforegItems, navItem{
			Label: label,
			URL:   "/ui/inforeg/" + strings.ToLower(ir.Name),
		})
	}
	if len(inforegItems) > 0 {
		nav = append(nav, navGroup{Kind: "Регистры сведений", Items: inforegItems})
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

	procs := s.reg.Processors()
	sort.Slice(procs, func(i, j int) bool { return procs[i].Name < procs[j].Name })
	var procItems []navItem
	for _, proc := range procs {
		label := proc.Title
		if label == "" {
			label = proc.Name
		}
		procItems = append(procItems, navItem{
			Label: label,
			URL:   "/ui/processor/" + strings.ToLower(proc.Name),
		})
	}
	if len(procItems) > 0 {
		nav = append(nav, navGroup{Kind: "Обработки", Items: procItems})
	}

	if len(s.reg.Constants()) > 0 {
		nav = append(nav, navGroup{Kind: "Настройки", Items: []navItem{
			{Label: "Константы", URL: "/ui/constants"},
		}})
	}
	return nav
}
