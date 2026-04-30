package runtime

import (
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/processor"
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
	printForms  map[string][]*printform.PrintForm // lowercase entity name → forms
	procs       map[string]map[string]*ast.ProcedureDecl
	moduleProcs map[string]*ast.ProcedureDecl // flat: proc name → decl
	processors  map[string]*processor.Processor
}

func NewRegistry() *Registry {
	return &Registry{
		entities:    make(map[string]*metadata.Entity),
		entitySlug:  make(map[string]*metadata.Entity),
		registers:   make(map[string]*metadata.Register),
		inforegs:    make(map[string]*metadata.InfoRegister),
		enums:       make(map[string]*metadata.Enum),
		constants:   make(map[string]*metadata.Constant),
		reports:     make(map[string]*report.Report),
		printForms:  make(map[string][]*printform.PrintForm),
		procs:       make(map[string]map[string]*ast.ProcedureDecl),
		moduleProcs: make(map[string]*ast.ProcedureDecl),
		processors:  make(map[string]*processor.Processor),
	}
}

func (r *Registry) Load(entities []*metadata.Entity, programs map[string]*ast.Program, registers []*metadata.Register, inforegs []*metadata.InfoRegister, enums []*metadata.Enum, constants []*metadata.Constant, reports []*report.Report, forms ...[]*printform.PrintForm) {
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
	newPrintForms := make(map[string][]*printform.PrintForm)
	if len(forms) > 0 {
		for _, pf := range forms[0] {
			key := strings.ToLower(pf.Document)
			newPrintForms[key] = append(newPrintForms[key], pf)
		}
	}

	r.mu.Lock()
	r.entities = newEntities
	r.entitySlug = newSlugs
	r.registers = newRegs
	r.inforegs = newInfoRegs
	r.enums = newEnums
	r.constants = newConsts
	r.reports = newReps
	r.printForms = newPrintForms
	r.procs = newProcs
	r.mu.Unlock()
}

// GetPrintForms returns all print forms registered for an entity name (case-insensitive).
func (r *Registry) GetPrintForms(entityName string) []*printform.PrintForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.printForms[strings.ToLower(entityName)]
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
	"OnPost":  "ОбработкаПроведения",
}

func (r *Registry) LoadModules(modules map[string]*ast.Program) {
	flat := make(map[string]*ast.ProcedureDecl)
	for _, prog := range modules {
		for _, p := range prog.Procedures {
			flat[p.Name.Literal] = p
		}
	}
	r.mu.Lock()
	r.moduleProcs = flat
	r.mu.Unlock()
}

func (r *Registry) LoadProcessors(procs []*processor.Processor) {
	m := make(map[string]*processor.Processor, len(procs))
	for _, p := range procs {
		m[p.Name] = p
	}
	r.mu.Lock()
	r.processors = m
	r.mu.Unlock()
}

func (r *Registry) GetModuleProc(name string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.moduleProcs[name]
}

func (r *Registry) Processors() []*processor.Processor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*processor.Processor, 0, len(r.processors))
	for _, p := range r.processors {
		out = append(out, p)
	}
	return out
}

func (r *Registry) GetProcessor(name string) *processor.Processor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.processors[name]; ok {
		return p
	}
	nl := strings.ToLower(name)
	for k, v := range r.processors {
		if strings.ToLower(k) == nl {
			return v
		}
	}
	return nil
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
