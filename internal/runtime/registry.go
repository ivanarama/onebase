package runtime

import (
	"sync"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
)

type Registry struct {
	mu       sync.RWMutex
	entities map[string]*metadata.Entity
	// entity name → procedure name → procedure
	procs map[string]map[string]*ast.ProcedureDecl
}

func NewRegistry() *Registry {
	return &Registry{
		entities: make(map[string]*metadata.Entity),
		procs:    make(map[string]map[string]*ast.ProcedureDecl),
	}
}

func (r *Registry) Load(entities []*metadata.Entity, programs map[string]*ast.Program) {
	newEntities := make(map[string]*metadata.Entity, len(entities))
	for _, e := range entities {
		newEntities[e.Name] = e
	}
	newProcs := make(map[string]map[string]*ast.ProcedureDecl)
	for entityName, prog := range programs {
		pm := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, p := range prog.Procedures {
			pm[p.Name.Literal] = p
		}
		newProcs[entityName] = pm
	}
	r.mu.Lock()
	r.entities = newEntities
	r.procs = newProcs
	r.mu.Unlock()
}

func (r *Registry) GetEntity(name string) *metadata.Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entities[name]
}

func (r *Registry) Entities() []*metadata.Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Entity, 0, len(r.entities))
	for _, e := range r.entities {
		out = append(out, e)
	}
	return out
}

// eventAliases maps canonical English event names to their Russian equivalents.
var eventAliases = map[string]string{
	"OnWrite": "ПриЗаписи",
}

func (r *Registry) GetProcedure(entityName, procName string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pm, ok := r.procs[entityName]
	if !ok {
		return nil
	}
	if p, ok := pm[procName]; ok {
		return p
	}
	// try Russian alias
	if ru, ok := eventAliases[procName]; ok {
		return pm[ru]
	}
	return nil
}
