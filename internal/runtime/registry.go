package runtime

import (
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
)

type Registry struct {
	mu        sync.RWMutex
	entities  map[string]*metadata.Entity
	registers map[string]*metadata.Register
	reports   map[string]*report.Report
	procs     map[string]map[string]*ast.ProcedureDecl
}

func NewRegistry() *Registry {
	return &Registry{
		entities:  make(map[string]*metadata.Entity),
		registers: make(map[string]*metadata.Register),
		reports:   make(map[string]*report.Report),
		procs:     make(map[string]map[string]*ast.ProcedureDecl),
	}
}

func (r *Registry) Load(entities []*metadata.Entity, programs map[string]*ast.Program, registers []*metadata.Register, reports []*report.Report) {
	newEntities := make(map[string]*metadata.Entity, len(entities))
	for _, e := range entities {
		newEntities[e.Name] = e
	}
	newRegs := make(map[string]*metadata.Register, len(registers))
	for _, reg := range registers {
		newRegs[reg.Name] = reg
	}
	newReps := make(map[string]*report.Report, len(reports))
	for _, rep := range reports {
		newReps[rep.Name] = rep
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
	r.registers = newRegs
	r.reports = newReps
	r.procs = newProcs
	r.mu.Unlock()
}

func (r *Registry) GetReport(name string) *report.Report {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if rep, ok := r.reports[name]; ok {
		return rep
	}
	// case-insensitive fallback
	nl := strings.ToLower(name)
	for k, v := range r.reports {
		if strings.ToLower(k) == nl {
			return v
		}
	}
	return nil
}

func (r *Registry) Reports() []*report.Report {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*report.Report, 0, len(r.reports))
	for _, rep := range r.reports {
		out = append(out, rep)
	}
	return out
}

func (r *Registry) GetEntity(name string) *metadata.Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entities[name]
}

func (r *Registry) GetRegister(name string) *metadata.Register {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if reg, ok := r.registers[name]; ok {
		return reg
	}
	// case-insensitive fallback (URL routes use lowercase names)
	nl := strings.ToLower(name)
	for k, v := range r.registers {
		if strings.ToLower(k) == nl {
			return v
		}
	}
	return nil
}

func (r *Registry) Registers() []*metadata.Register {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Register, 0, len(r.registers))
	for _, reg := range r.registers {
		out = append(out, reg)
	}
	return out
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
