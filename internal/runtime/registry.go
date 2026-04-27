package runtime

import (
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
)

type Registry struct {
	mu          sync.RWMutex
	entities    map[string]*metadata.Entity
	entitySlug  map[string]*metadata.Entity // lowercase name → entity
	registers   map[string]*metadata.Register
	inforegs    map[string]*metadata.InfoRegister
	enums       map[string]*metadata.Enum
	constants   map[string]*metadata.Constant
	reports     map[string]*report.Report
	procs       map[string]map[string]*ast.ProcedureDecl
}

func NewRegistry() *Registry {
	return &Registry{
		entities:   make(map[string]*metadata.Entity),
		entitySlug: make(map[string]*metadata.Entity),
		registers:  make(map[string]*metadata.Register),
		inforegs:   make(map[string]*metadata.InfoRegister),
		enums:      make(map[string]*metadata.Enum),
		constants:  make(map[string]*metadata.Constant),
		reports:    make(map[string]*report.Report),
		procs:      make(map[string]map[string]*ast.ProcedureDecl),
	}
}

func (r *Registry) Load(entities []*metadata.Entity, programs map[string]*ast.Program, registers []*metadata.Register, inforegs []*metadata.InfoRegister, enums []*metadata.Enum, constants []*metadata.Constant, reports []*report.Report) {
	newEntities := make(map[string]*metadata.Entity, len(entities))
	newSlugs := make(map[string]*metadata.Entity, len(entities))
	for _, e := range entities {
		newEntities[e.Name] = e
		newSlugs[strings.ToLower(e.Name)] = e
	}
	newRegs := make(map[string]*metadata.Register, len(registers))
	for _, reg := range registers {
		newRegs[reg.Name] = reg
	}
	newInfoRegs := make(map[string]*metadata.InfoRegister, len(inforegs))
	for _, ir := range inforegs {
		newInfoRegs[ir.Name] = ir
	}
	newEnums := make(map[string]*metadata.Enum, len(enums))
	for _, e := range enums {
		newEnums[e.Name] = e
	}
	newConsts := make(map[string]*metadata.Constant, len(constants))
	for _, c := range constants {
		newConsts[c.Name] = c
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
	r.entitySlug = newSlugs
	r.registers = newRegs
	r.inforegs = newInfoRegs
	r.enums = newEnums
	r.constants = newConsts
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
	if e, ok := r.entities[name]; ok {
		return e
	}
	return r.entitySlug[strings.ToLower(name)]
}

// GetEntityBySlug looks up by lowercase slug — O(1), URL-safe.
func (r *Registry) GetEntityBySlug(slug string) *metadata.Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entitySlug[strings.ToLower(slug)]
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

func (r *Registry) GetInfoRegister(name string) *metadata.InfoRegister {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ir, ok := r.inforegs[name]; ok {
		return ir
	}
	nl := strings.ToLower(name)
	for k, v := range r.inforegs {
		if strings.ToLower(k) == nl {
			return v
		}
	}
	return nil
}

func (r *Registry) InfoRegisters() []*metadata.InfoRegister {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.InfoRegister, 0, len(r.inforegs))
	for _, ir := range r.inforegs {
		out = append(out, ir)
	}
	return out
}

func (r *Registry) GetEnum(name string) *metadata.Enum {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enums[name]
}

func (r *Registry) Enums() []*metadata.Enum {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Enum, 0, len(r.enums))
	for _, e := range r.enums {
		out = append(out, e)
	}
	return out
}

func (r *Registry) GetConstantMeta(name string) *metadata.Constant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.constants[name]
}

func (r *Registry) Constants() []*metadata.Constant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Constant, 0, len(r.constants))
	for _, c := range r.constants {
		out = append(out, c)
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
		// case-insensitive fallback: DSL filename may differ in case from entity name
		nl := strings.ToLower(entityName)
		for k, v := range r.procs {
			if strings.ToLower(k) == nl {
				pm = v
				break
			}
		}
		if pm == nil {
			return nil
		}
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
