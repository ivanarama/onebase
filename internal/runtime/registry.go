package runtime

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/httpservice"
	oblog "github.com/ivantit66/onebase/internal/logging"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/page"
	"github.com/ivantit66/onebase/internal/printform"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/report"
)

func registryLog() *slog.Logger {
	return oblog.Component("runtime.registry")
}

type Registry struct {
	mu              sync.RWMutex
	entities        map[string]*metadata.Entity
	entitySlug      map[string]*metadata.Entity // lowercase name → entity
	registers       map[string]*metadata.Register
	inforegs        map[string]*metadata.InfoRegister
	enums           map[string]*metadata.Enum
	constants       map[string]*metadata.Constant
	reports         map[string]*report.Report
	extReports      map[string]*report.Report            // внешние отчёты (из БД), ключ — Name
	printForms      map[string][]*printform.PrintForm    // lowercase entity name → forms
	extPrintForms   map[string][]*printform.PrintForm    // lowercase entity name → внешние формы (из БД)
	dslPrintForms   map[string][]*printform.DSLPrintForm // lowercase entity name → DSL forms
	layoutForms     map[string][]*printform.LayoutForm   // lowercase entity name → декларативные формы (.layout.yaml)
	extLayoutForms  map[string][]*printform.LayoutForm   // lowercase entity name → внешние v2-формы (из БД)
	procs           map[string]map[string]*ast.ProcedureDecl
	extProcs        map[string]map[string]*ast.ProcedureDecl // код внешних обработок (из БД), ключ — имя обработки
	serviceProcs    map[string]map[string]*ast.ProcedureDecl // план 61: имя сервиса → обработчики .service.os
	pageProcs       map[string]map[string]*ast.ProcedureDecl // план 66: имя страницы → обработчики .page.os
	managerProcs    map[string]map[string]*ast.ProcedureDecl // lowercase entity → procs модуля менеджера
	moduleProcs     map[string]*ast.ProcedureDecl            // flat: proc name → decl
	moduleByName    map[string]map[string]*ast.ProcedureDecl // lowercase module → procs in it
	moduleByFile    map[string]map[string]*ast.ProcedureDecl // файл модуля → procs in it (для GetSiblingProc)
	processors      map[string]*processor.Processor
	httpServices    map[string]*httpservice.Service   // lowercase name → HTTP-сервис
	pages           map[string]*page.Page             // lowercase name → страница (план 66)
	exchangePlans   map[string]*metadata.ExchangePlan // lowercase name → план обмена (план 86)
	intakes         map[string]*metadata.Intake       // lowercase name → входной шлюз (план 90)
	extProcessors   map[string]*processor.Processor   // внешние обработки (из БД), ключ — Name
	subsystems      []*metadata.Subsystem             // sorted by Order
	journals        map[string]*metadata.Journal
	accountRegs     map[string]*metadata.AccountRegister
	chartsOfAccount map[string]*metadata.ChartOfAccounts
	widgets         map[string]*metadata.Widget // lowercase name → widget
	widgetOrder     []string                    // preserves load order
	homePage        *metadata.HomePage
	// basedOnIndex — обратный индекс «источник → приёмники» для ввода на
	// основании. Ключ — lowercase имя источника (документа/справочника),
	// значение — список *Entity-приёмников, у которых в YAML указан этот
	// источник в `based_on`. Заполняется в Load(opts).
	basedOnIndex map[string][]*metadata.Entity
}

func NewRegistry() *Registry {
	return &Registry{
		entities:        make(map[string]*metadata.Entity),
		entitySlug:      make(map[string]*metadata.Entity),
		registers:       make(map[string]*metadata.Register),
		inforegs:        make(map[string]*metadata.InfoRegister),
		enums:           make(map[string]*metadata.Enum),
		constants:       make(map[string]*metadata.Constant),
		reports:         make(map[string]*report.Report),
		extReports:      make(map[string]*report.Report),
		printForms:      make(map[string][]*printform.PrintForm),
		extPrintForms:   make(map[string][]*printform.PrintForm),
		dslPrintForms:   make(map[string][]*printform.DSLPrintForm),
		layoutForms:     make(map[string][]*printform.LayoutForm),
		extLayoutForms:  make(map[string][]*printform.LayoutForm),
		procs:           make(map[string]map[string]*ast.ProcedureDecl),
		managerProcs:    make(map[string]map[string]*ast.ProcedureDecl),
		moduleProcs:     make(map[string]*ast.ProcedureDecl),
		moduleByName:    make(map[string]map[string]*ast.ProcedureDecl),
		moduleByFile:    make(map[string]map[string]*ast.ProcedureDecl),
		processors:      make(map[string]*processor.Processor),
		httpServices:    make(map[string]*httpservice.Service),
		pages:           make(map[string]*page.Page),
		exchangePlans:   make(map[string]*metadata.ExchangePlan),
		intakes:         make(map[string]*metadata.Intake),
		extProcessors:   make(map[string]*processor.Processor),
		extProcs:        make(map[string]map[string]*ast.ProcedureDecl),
		serviceProcs:    make(map[string]map[string]*ast.ProcedureDecl),
		journals:        make(map[string]*metadata.Journal),
		accountRegs:     make(map[string]*metadata.AccountRegister),
		chartsOfAccount: make(map[string]*metadata.ChartOfAccounts),
		widgets:         make(map[string]*metadata.Widget),
	}
}

// ReplaceProjectFrom atomically publishes all project-owned registry data from
// src. External reports/forms/processors loaded from the database remain intact.
// Readers therefore observe either the complete old project or the complete new
// project, never an intermediate mixture assembled by multiple Load* calls.
func (r *Registry) ReplaceProjectFrom(src *Registry) {
	if src == nil || src == r {
		return
	}
	src.mu.RLock()
	defer src.mu.RUnlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entities = src.entities
	r.entitySlug = src.entitySlug
	r.registers = src.registers
	r.inforegs = src.inforegs
	r.enums = src.enums
	r.constants = src.constants
	r.reports = src.reports
	r.printForms = src.printForms
	r.dslPrintForms = src.dslPrintForms
	r.layoutForms = src.layoutForms
	r.procs = src.procs
	r.serviceProcs = src.serviceProcs
	r.pageProcs = src.pageProcs
	r.managerProcs = src.managerProcs
	r.moduleProcs = src.moduleProcs
	r.moduleByName = src.moduleByName
	r.moduleByFile = src.moduleByFile
	r.processors = src.processors
	r.httpServices = src.httpServices
	r.pages = src.pages
	r.exchangePlans = src.exchangePlans
	r.intakes = src.intakes
	r.subsystems = src.subsystems
	r.journals = src.journals
	r.accountRegs = src.accountRegs
	r.chartsOfAccount = src.chartsOfAccount
	r.widgets = src.widgets
	r.widgetOrder = src.widgetOrder
	r.homePage = src.homePage
	r.basedOnIndex = src.basedOnIndex
}

// LoadWidgets registers dashboard widgets by name (case-insensitive).
func (r *Registry) LoadWidgets(widgets []*metadata.Widget) {
	m := make(map[string]*metadata.Widget, len(widgets))
	order := make([]string, 0, len(widgets))
	for _, w := range widgets {
		key := strings.ToLower(w.Name)
		m[key] = w
		order = append(order, w.Name)
	}
	r.mu.Lock()
	r.widgets = m
	r.widgetOrder = order
	r.mu.Unlock()
}

// GetWidget returns a widget by name (case-insensitive). nil if not found.
func (r *Registry) GetWidget(name string) *metadata.Widget {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.widgets[strings.ToLower(name)]
}

// Widgets returns all registered widgets in load order.
func (r *Registry) Widgets() []*metadata.Widget {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Widget, 0, len(r.widgetOrder))
	for _, name := range r.widgetOrder {
		if w, ok := r.widgets[strings.ToLower(name)]; ok {
			out = append(out, w)
		}
	}
	return out
}

// LoadHomePage stores the dashboard layout. Pass nil to reset.
func (r *Registry) LoadHomePage(hp *metadata.HomePage) {
	r.mu.Lock()
	r.homePage = hp
	r.mu.Unlock()
}

// HomePage returns the registered dashboard layout. nil = none configured.
func (r *Registry) HomePage() *metadata.HomePage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.homePage
}

// LoadOptions — параметры основного Load. Поля опциональны: nil/пустой slice
// сбрасывает соответствующую категорию (поведение аналогично прежней
// позиционной сигнатуре). Заменил собой Load(entities, programs, registers,
// inforegs, enums, constants, reports, forms...) — позиционные nil'ы стали
// именованными полями.
type LoadOptions struct {
	Entities        []*metadata.Entity
	Programs        map[string]*ast.Program
	ManagerPrograms map[string]*ast.Program // entity name → процедуры модуля менеджера
	ServicePrograms map[string]*ast.Program // план 61: service name → обработчики .service.os
	PagePrograms    map[string]*ast.Program // план 66: page name → обработчики .page.os
	Registers       []*metadata.Register
	InfoRegs        []*metadata.InfoRegister
	Enums           []*metadata.Enum
	Constants       []*metadata.Constant
	Reports         []*report.Report
	PrintForms      []*printform.PrintForm
}

func (r *Registry) Load(opts LoadOptions) {
	newEntities := make(map[string]*metadata.Entity, len(opts.Entities))
	newSlugs := make(map[string]*metadata.Entity, len(opts.Entities))
	for _, e := range opts.Entities {
		newEntities[e.Name] = e
		newSlugs[strings.ToLower(e.Name)] = e
	}
	newBasedOn := make(map[string][]*metadata.Entity)
	for _, e := range opts.Entities {
		for _, src := range e.BasedOn {
			key := strings.ToLower(src)
			newBasedOn[key] = append(newBasedOn[key], e)
		}
	}
	newRegs := make(map[string]*metadata.Register, len(opts.Registers))
	for _, reg := range opts.Registers {
		newRegs[reg.Name] = reg
	}
	newInfoRegs := make(map[string]*metadata.InfoRegister, len(opts.InfoRegs))
	for _, ir := range opts.InfoRegs {
		newInfoRegs[ir.Name] = ir
	}
	newEnums := make(map[string]*metadata.Enum, len(opts.Enums))
	for _, e := range opts.Enums {
		newEnums[e.Name] = e
	}
	newConsts := make(map[string]*metadata.Constant, len(opts.Constants))
	for _, c := range opts.Constants {
		newConsts[c.Name] = c
	}
	newReps := make(map[string]*report.Report, len(opts.Reports))
	for _, rep := range opts.Reports {
		newReps[rep.Name] = rep
	}
	newProcs := make(map[string]map[string]*ast.ProcedureDecl)
	for entityName, prog := range opts.Programs {
		pm := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, p := range prog.Procedures {
			pm[strings.ToLower(p.Name.Literal)] = p
		}
		newProcs[entityName] = pm
	}
	newManagerProcs := make(map[string]map[string]*ast.ProcedureDecl)
	for entityName, prog := range opts.ManagerPrograms {
		pm := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, p := range prog.Procedures {
			pm[strings.ToLower(p.Name.Literal)] = p
		}
		newManagerProcs[strings.ToLower(entityName)] = pm
	}
	newServiceProcs := make(map[string]map[string]*ast.ProcedureDecl)
	for svcName, prog := range opts.ServicePrograms {
		pm := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, p := range prog.Procedures {
			pm[strings.ToLower(p.Name.Literal)] = p
		}
		newServiceProcs[svcName] = pm
	}
	newPageProcs := make(map[string]map[string]*ast.ProcedureDecl)
	for pageName, prog := range opts.PagePrograms {
		pm := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, p := range prog.Procedures {
			pm[strings.ToLower(p.Name.Literal)] = p
		}
		newPageProcs[pageName] = pm
	}
	newPrintForms := make(map[string][]*printform.PrintForm)
	for _, pf := range opts.PrintForms {
		key := strings.ToLower(pf.Document)
		newPrintForms[key] = append(newPrintForms[key], pf)
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
	r.managerProcs = newManagerProcs
	r.serviceProcs = newServiceProcs
	r.pageProcs = newPageProcs
	r.basedOnIndex = newBasedOn
	r.mu.Unlock()
}

// GetServiceProcedure resolves a handler procedure declared in an HTTP service
// module (X.service.os, план 61). Хранится отдельно от procs, поэтому сервис и
// одноимённый документ не конфликтуют. Регистронезависимо по обоим именам.
func (r *Registry) GetServiceProcedure(serviceName, handlerName string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return lookupProc(r.serviceProcs, serviceName, handlerName)
}

// GetManagerProc resolves a procedure declared in the manager module
// (X.manager.os) of the given entity. Case-insensitive on both names.
// nil — нет такого файла или процедуры. Используется CallMethod
// прокси Документы/Справочники для fallback на пользовательский метод
// после встроенных (Создать, НайтиПо…, Удалить).
func (r *Registry) GetManagerProc(entityName, procName string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pm, ok := r.managerProcs[strings.ToLower(entityName)]
	if !ok {
		return nil
	}
	return pm[strings.ToLower(procName)]
}

// ReceiversOf возвращает список сущностей, у которых в `based_on` указан
// данный источник. Используется UI для построения меню «Ввести на основании ▾»
// в форме и списке источника. Поиск регистронезависимый.
func (r *Registry) ReceiversOf(sourceName string) []*metadata.Entity {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.basedOnIndex[strings.ToLower(sourceName)]
	if len(src) == 0 {
		return nil
	}
	out := make([]*metadata.Entity, len(src))
	copy(out, src)
	return out
}

// GetPrintForms returns all print forms registered for an entity name
// (case-insensitive): сначала формы конфигурации, затем включённые внешние
// формы (из таблицы _ext_printforms). Конфигурация всегда имеет приоритет —
// внешние формы не перекрывают одноимённые, а лишь дополняют список (у них
// выставлен флаг External, по которому UI рисует пометку «(внешняя)»).
func (r *Registry) GetPrintForms(entityName string) []*printform.PrintForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := strings.ToLower(entityName)
	cfg := r.printForms[key]
	ext := r.extPrintForms[key]
	if len(ext) == 0 {
		return cfg
	}
	out := make([]*printform.PrintForm, 0, len(cfg)+len(ext))
	out = append(out, cfg...)
	out = append(out, ext...)
	return out
}

// SetExternalPrintForms атомарно заменяет набор внешних печатных форм (из
// внешнего контура расширяемости). Каждой форме выставляется External=true.
// При коллизии имени с формой конфигурации пишется предупреждение в лог —
// конфигурация остаётся основной (см. GetPrintForms). Хранится отдельно от
// printForms, поэтому reload конфигурации (Load) внешние формы не затирает.
func (r *Registry) SetExternalPrintForms(forms []*printform.PrintForm) {
	m := make(map[string][]*printform.PrintForm)
	for _, f := range forms {
		f.External = true
		key := strings.ToLower(f.Document)
		m[key] = append(m[key], f)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, list := range m {
		cfg := r.printForms[key]
		for _, ef := range list {
			for _, cf := range cfg {
				if strings.EqualFold(cf.Name, ef.Name) {
					registryLog().Warn("external print form name collision",
						"form", ef.Name,
						"document", ef.Document,
					)
				}
			}
		}
	}
	r.extPrintForms = m
}

// SetExternalLayoutForms атомарно заменяет набор внешних декларативных форм
// (макет v2 из БД — см. extform.LoadEnabledPrintForms сниффинг). Хранится
// отдельно от layoutForms, поэтому reload конфигурации внешние формы не затирает.
// В GetAllPrintForms они отдаются как Kind=Declarative, External=true и имеют
// приоритет ниже одноимённых форм конфигурации.
func (r *Registry) SetExternalLayoutForms(forms []*printform.LayoutForm) {
	m := make(map[string][]*printform.LayoutForm)
	for _, f := range forms {
		key := strings.ToLower(f.Document)
		m[key] = append(m[key], f)
	}
	r.mu.Lock()
	r.extLayoutForms = m
	r.mu.Unlock()
}

// LoadDSLPrintForms registers DSL (.os) print forms indexed by entity name.
// при коллизии «один и тот же name для одного и того же
// document» с YAML-формой .os перебивает YAML, а YAML удаляется из реестра.
// В лог печатается warning, чтобы автор конфигурации понимал, что
// дубликат игнорируется. Должен вызываться ПОСЛЕ Load (где регистрируются
// YAML-формы); проверка идёт под одной блокировкой.
func (r *Registry) LoadDSLPrintForms(forms []*printform.DSLPrintForm) {
	m := make(map[string][]*printform.DSLPrintForm)
	for _, f := range forms {
		key := strings.ToLower(f.Document)
		m[key] = append(m[key], f)
	}
	r.mu.Lock()
	r.dslPrintForms = m
	var warnings []string
	// Дедуп YAML/.os коллизий: удаляем YAML, если есть .os с тем же именем.
	for entityKey, dslList := range m {
		yamlList := r.printForms[entityKey]
		if len(yamlList) == 0 {
			continue
		}
		var kept []*printform.PrintForm
		for _, yf := range yamlList {
			collides := false
			for _, df := range dslList {
				if strings.EqualFold(yf.Name, df.Name) {
					collides = true
					warnings = append(warnings, fmt.Sprintf(
						"print form %q for %s — YAML и .os коллизия, используется .os (LayoutPath=%s); YAML-вариант игнорируется",
						yf.Name, yf.Document, df.LayoutPath))
					break
				}
			}
			if !collides {
				kept = append(kept, yf)
			}
		}
		r.printForms[entityKey] = kept
	}
	r.mu.Unlock()
	for _, w := range warnings {
		registryLog().Warn("print form priority warning", "warning", w)
	}
}

// GetDSLPrintForms returns all DSL print forms for an entity name (case-insensitive).
func (r *Registry) GetDSLPrintForms(entityName string) []*printform.DSLPrintForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dslPrintForms[strings.ToLower(entityName)]
}

// GetDSLPrintForm returns a specific DSL print form by entity and form name.
func (r *Registry) GetDSLPrintForm(entityName, pfName string) *printform.DSLPrintForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, f := range r.dslPrintForms[strings.ToLower(entityName)] {
		if strings.EqualFold(f.Name, pfName) {
			return f
		}
	}
	// Fallback: search all entities
	for _, forms := range r.dslPrintForms {
		for _, f := range forms {
			if strings.EqualFold(f.Name, pfName) {
				return f
			}
		}
	}
	return nil
}

// LoadLayoutForms registers declarative (standalone .layout.yaml) print forms
// indexed by entity name. Декларативные формы перебивают одноимённые DSL- и
// YAML-формы (приоритет коллизий Declarative > DSL > Legacy) — об этом пишется
// warning. Должен вызываться ПОСЛЕ Load и LoadDSLPrintForms.
func (r *Registry) LoadLayoutForms(forms []*printform.LayoutForm) {
	m := make(map[string][]*printform.LayoutForm)
	for _, f := range forms {
		key := strings.ToLower(f.Document)
		m[key] = append(m[key], f)
	}
	r.mu.Lock()
	r.layoutForms = m
	var warnings []string
	for entityKey, list := range m {
		for _, lf := range list {
			for _, df := range r.dslPrintForms[entityKey] {
				if strings.EqualFold(df.Name, lf.Name) {
					warnings = append(warnings, fmt.Sprintf(
						"print form %q for %s — .layout.yaml перебивает .os (используется декларативная форма)",
						lf.Name, lf.Document))
				}
			}
			for _, yf := range r.printForms[entityKey] {
				if strings.EqualFold(yf.Name, lf.Name) {
					warnings = append(warnings, fmt.Sprintf(
						"print form %q for %s — .layout.yaml перебивает YAML-форму (используется декларативная форма)",
						lf.Name, lf.Document))
				}
			}
		}
	}
	r.mu.Unlock()
	for _, w := range warnings {
		registryLog().Warn("print form priority warning", "warning", w)
	}
}

// GetLayoutForms returns all declarative print forms for an entity (case-insensitive).
func (r *Registry) GetLayoutForms(entityName string) []*printform.LayoutForm {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.layoutForms[strings.ToLower(entityName)]
}

// PrintFormKind — вид печатной формы в едином реестре (план 64, этап 3).
type PrintFormKind int

const (
	// PrintFormDeclarative — standalone .layout.yaml, рендер через BuildSheet.
	PrintFormDeclarative PrintFormKind = iota
	// PrintFormDSL — .os модуль (+опц. макет), рендер через RunWithResult.
	PrintFormDSL
	// PrintFormLegacy — YAML-форма (printform.PrintForm), legacy-рендер.
	PrintFormLegacy
)

// PrintFormRef — единая ссылка на печатную форму любого вида. Заполнено только
// одно из Decl/DSL/Legacy в соответствии с Kind.
type PrintFormRef struct {
	Name     string
	Document string
	Kind     PrintFormKind
	External bool // форма из внешнего контура (только для Legacy)
	HasXLSX  bool // рядом с макетом есть исходный <имя>.template.xlsx
	Decl     *printform.LayoutForm
	DSL      *printform.DSLPrintForm
	Legacy   *printform.PrintForm
}

// GetAllPrintForms возвращает все печатные формы сущности (case-insensitive) в
// едином виде PrintFormRef. Приоритет коллизий по имени: Declarative > DSL >
// Legacy — форма с более высоким приоритетом скрывает одноимённые ниже.
// Порядок: сначала декларативные, затем DSL, затем legacy (конфигурационные,
// потом внешние). Сам берёт RLock на время чтения карт.
func (r *Registry) GetAllPrintForms(entityName string) []PrintFormRef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := strings.ToLower(entityName)

	var refs []PrintFormRef
	seen := make(map[string]bool) // lower(name) → занято формой более высокого приоритета

	for _, lf := range r.layoutForms[key] {
		ln := strings.ToLower(lf.Name)
		if seen[ln] {
			continue
		}
		seen[ln] = true
		refs = append(refs, PrintFormRef{Name: lf.Name, Document: lf.Document, Kind: PrintFormDeclarative, HasXLSX: len(lf.XLSXTemplate) > 0, Decl: lf})
	}
	for _, df := range r.dslPrintForms[key] {
		ln := strings.ToLower(df.Name)
		if seen[ln] {
			continue
		}
		seen[ln] = true
		refs = append(refs, PrintFormRef{Name: df.Name, Document: df.Document, Kind: PrintFormDSL, DSL: df})
	}
	for _, yf := range r.printForms[key] {
		ln := strings.ToLower(yf.Name)
		if seen[ln] {
			continue
		}
		seen[ln] = true
		refs = append(refs, legacyRef(yf, false))
	}
	for _, ef := range r.extPrintForms[key] {
		ln := strings.ToLower(ef.Name)
		if seen[ln] {
			continue
		}
		seen[ln] = true
		refs = append(refs, legacyRef(ef, true))
	}
	// Внешние v2-формы (декларативный макет из БД) — приоритет ниже форм
	// конфигурации, отдаются как Declarative с пометкой External.
	for _, lf := range r.extLayoutForms[key] {
		ln := strings.ToLower(lf.Name)
		if seen[ln] {
			continue
		}
		seen[ln] = true
		refs = append(refs, PrintFormRef{Name: lf.Name, Document: lf.Document, Kind: PrintFormDeclarative, External: true, HasXLSX: len(lf.XLSXTemplate) > 0, Decl: lf})
	}
	return refs
}

// legacyRef строит PrintFormRef для legacy YAML-формы, СРАЗУ конвертируя её в
// макет v2 (план 64, этап 4): Decl несёт результат ConvertLegacy, по которому
// рендерит декларативный движок (BuildSheet). Поле Legacy сохраняется для
// справки (имя/документ/External) и валидации. При сбое конверсии Decl остаётся
// nil — обработчик отдаст 500 с понятной ошибкой, а не упадёт.
func legacyRef(pf *printform.PrintForm, external bool) PrintFormRef {
	ref := PrintFormRef{
		Name:     pf.Name,
		Document: pf.Document,
		Kind:     PrintFormLegacy,
		External: external,
		Legacy:   pf,
	}
	if lt, err := printform.ConvertLegacy(pf); err == nil {
		ref.Decl = &printform.LayoutForm{
			Name:     pf.Name,
			Document: pf.Document,
			Layout:   lt,
		}
	} else {
		registryLog().Warn("legacy print form conversion failed",
			"form", pf.Name,
			"document", pf.Document,
			"err", err,
		)
	}
	return ref
}

// GetPrintFormRef ищет печатную форму сущности по имени (case-insensitive),
// учитывая приоритет коллизий. Возвращает ref и ok.
func (r *Registry) GetPrintFormRef(entityName, formName string) (PrintFormRef, bool) {
	for _, ref := range r.GetAllPrintForms(entityName) {
		if strings.EqualFold(ref.Name, formName) {
			return ref, true
		}
	}
	return PrintFormRef{}, false
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
	// внешние отчёты — только если в конфигурации такого нет (конфиг приоритетнее)
	if rep, ok := r.extReports[name]; ok {
		return rep
	}
	for k, v := range r.extReports {
		if strings.ToLower(k) == nl {
			return v
		}
	}
	return nil
}

// existsCI сообщает, есть ли в карте ключ, совпадающий с name без учёта регистра.
// Имена отчётов/обработок резолвятся регистронезависимо (GetReport/GetProcessor),
// поэтому проверка занятости имени конфигурацией должна быть такой же — иначе
// внешний объект с именем в другом регистре (например «продажи» при «Продажи»)
// дублируется в меню и открывает чужой объект.
func existsCI[V any](m map[string]V, name string) bool {
	if _, ok := m[name]; ok {
		return true
	}
	nl := strings.ToLower(name)
	for k := range m {
		if strings.ToLower(k) == nl {
			return true
		}
	}
	return false
}

func (r *Registry) Reports() []*report.Report {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*report.Report, 0, len(r.reports)+len(r.extReports))
	for _, rep := range r.reports {
		out = append(out, rep)
	}
	// внешние отчёты, чьё имя не занято конфигурацией
	for name, rep := range r.extReports {
		if existsCI(r.reports, name) {
			continue
		}
		out = append(out, rep)
	}
	return out
}

// SetExternalReports атомарно заменяет набор внешних отчётов (из внешнего
// контура расширяемости). Каждому выставляется External=true. При коллизии
// имени с отчётом конфигурации пишется предупреждение — приоритет у
// конфигурации (см. GetReport/Reports). Хранится отдельно от reports, поэтому
// reload конфигурации (Load) внешние отчёты не затирает.
func (r *Registry) SetExternalReports(reports []*report.Report) {
	m := make(map[string]*report.Report, len(reports))
	for _, rep := range reports {
		rep.External = true
		m[rep.Name] = rep
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range m {
		if existsCI(r.reports, name) {
			registryLog().Warn("external report name collision", "report", name)
		}
	}
	r.extReports = m
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

// eventAliases maps lowercase English event names to their Russian equivalents.
var eventAliases = map[string]string{
	"onwrite":  "призаписи",
	"onpost":   "обработкапроведения",
	"onunpost": "обработкаудаленияпроведения",
	"onfill":   "обработказаполнения",
	// Создание нового объекта (план 153). Хук живёт в модуле объекта, а не
	// формы: создают и из формы, и из DSL, и по REST — обработчик формы
	// защищал бы один путь из трёх.
	"oncreate": "присозданиинового",
	// Удаление (issue: события были объявлены в метаданных форм, но не
	// вызывались ниоткуда). Хук живёт в модуле объекта, а не формы: удаляют и
	// из списка, и пачкой, и из DSL, и по REST — форменный обработчик защищал
	// бы только один путь из пяти, то есть не защищал бы вовсе.
	"beforedelete": "передудалением",
	"afterdelete":  "послеудаления",
}

// LoadModules раскладывает процедуры общих модулей по трём картам: плоской (для
// неквалифицированного вызова из чужого файла), по имени модуля (для
// Модуль.Процедура) и по файлу (для «своё раньше чужого», см. GetSiblingProc).
//
// Плоская карта заполняется в порядке сортировки имён модулей, и первый занявший
// имя его удерживает. Раньше это был обход map — то есть при совпадении имён в
// двух модулях победитель выбирался случайно и МЕНЯЛСЯ ОТ ЗАПУСКА К ЗАПУСКУ:
// конфигурация вела себя по-разному на одном и том же коде. Порядок по имени
// модуля так же произволен, но воспроизводим.
func (r *Registry) LoadModules(modules map[string]*ast.Program) {
	flat := make(map[string]*ast.ProcedureDecl)
	byModule := make(map[string]map[string]*ast.ProcedureDecl)
	byFile := make(map[string]map[string]*ast.ProcedureDecl)
	moduleNames := make([]string, 0, len(modules))
	for moduleName := range modules {
		moduleNames = append(moduleNames, moduleName)
	}
	sort.Strings(moduleNames)
	for _, moduleName := range moduleNames {
		prog := modules[moduleName]
		modKey := strings.ToLower(moduleName)
		byModule[modKey] = make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, p := range prog.Procedures {
			procKey := strings.ToLower(p.Name.Literal)
			if _, taken := flat[procKey]; !taken {
				flat[procKey] = p
			}
			byModule[modKey][procKey] = p
			if file := p.Name.File; file != "" {
				if byFile[file] == nil {
					byFile[file] = make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
				}
				byFile[file][procKey] = p
			}
		}
	}
	r.mu.Lock()
	r.moduleProcs = flat
	r.moduleByName = byModule
	r.moduleByFile = byFile
	r.mu.Unlock()
}

// GetModuleNamespacedProc resolves Module.Proc() syntax — например
// Утилиты.ФИФО(...). Это позволяет вызывать процедуры общих модулей
// без коллизий имён между модулями (
func (r *Registry) GetModuleNamespacedProc(moduleName, procName string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mod, ok := r.moduleByName[strings.ToLower(moduleName)]
	if !ok {
		return nil
	}
	return mod[strings.ToLower(procName)]
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
	return r.moduleProcs[strings.ToLower(name)]
}

// GetSiblingProc resolves a helper procedure declared in the same source
// file as currentFile. Lets `.proc.os` (processor entry-point) call
// helpers also defined inside it without flattening every entity proc
// into a global namespace (which would let arbitrary code invoke
// OnWrite/OnPost handlers by name).
//
// currentFile comes from interpreter.curFile (the file:line of the
// last executed statement), so the resolver naturally scopes to the
// currently running source.
//
// Общие модули учитываются наравне с обработками. Без этого правило «своё раньше
// чужого» (#717) на них не распространялось: процедуры модулей лежат отдельно от
// r.procs, поиск сюда не доходил, и неквалифицированный вызов внутри модуля
// разрешался только плоской картой — то есть мог молча уйти в одноимённую
// процедуру ЧУЖОГО модуля. Файл модуля один, поэтому «тот же файл» здесь значит
// «тот же модуль».
func (r *Registry) GetSiblingProc(currentFile, name string) *ast.ProcedureDecl {
	if currentFile == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	low := strings.ToLower(name)
	for _, pm := range r.procs {
		for procLow, decl := range pm {
			if procLow == low && decl.Name.File == currentFile {
				return decl
			}
		}
	}
	if decl, ok := r.moduleByFile[currentFile][low]; ok {
		return decl
	}
	return nil
}

// Processors возвращает список обработок для показа пользователю (меню,
// страница функций, AI-инструменты). Тест-обработки (kind: test, план 136)
// сюда не попадают — они не пользовательские, их гоняет только `onebase test`;
// по имени они по-прежнему доступны через GetProcessor (нужно раннеру).
func (r *Registry) Processors() []*processor.Processor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*processor.Processor, 0, len(r.processors)+len(r.extProcessors))
	for _, p := range r.processors {
		if p.IsTest() {
			continue
		}
		out = append(out, p)
	}
	// внешние обработки, чьё имя не занято конфигурацией
	for name, p := range r.extProcessors {
		if existsCI(r.processors, name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// SetExternalProcessors атомарно заменяет набор внешних обработок (метаданные +
// разобранный код). Каждой выставляется External=true. При коллизии имени с
// обработкой конфигурации пишется предупреждение — приоритет у конфигурации
// (см. GetProcessor/GetProcedure). Хранится отдельно от processors/procs,
// поэтому reload конфигурации внешние обработки не затирает.
func (r *Registry) SetExternalProcessors(procs []*processor.Processor, programs map[string]*ast.Program) {
	pm := make(map[string]*processor.Processor, len(procs))
	for _, p := range procs {
		p.External = true
		pm[p.Name] = p
	}
	codeMap := make(map[string]map[string]*ast.ProcedureDecl, len(programs))
	for name, prog := range programs {
		m := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
		for _, d := range prog.Procedures {
			m[strings.ToLower(d.Name.Literal)] = d
		}
		codeMap[name] = m
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range pm {
		if existsCI(r.processors, name) {
			registryLog().Warn("external processor name collision", "processor", name)
		}
	}
	r.extProcessors = pm
	r.extProcs = codeMap
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
	// внешние обработки — только если в конфигурации такого имени нет
	if p, ok := r.extProcessors[name]; ok {
		return p
	}
	for k, v := range r.extProcessors {
		if strings.ToLower(k) == nl {
			return v
		}
	}
	return nil
}

// LoadHTTPServices регистрирует опубликованные HTTP-сервисы (план 61).
// Ключ — lowercase имя сервиса; роутер ищет сервис по корневому URL через
// HTTPServices(), а обработчик — через GetProcedure(serviceName, handlerName).
func (r *Registry) LoadHTTPServices(services []*httpservice.Service) {
	m := make(map[string]*httpservice.Service, len(services))
	for _, s := range services {
		m[strings.ToLower(s.Name)] = s
	}
	r.mu.Lock()
	r.httpServices = m
	r.mu.Unlock()
}

// HTTPServices возвращает все зарегистрированные HTTP-сервисы.
func (r *Registry) HTTPServices() []*httpservice.Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*httpservice.Service, 0, len(r.httpServices))
	for _, s := range r.httpServices {
		out = append(out, s)
	}
	return out
}

// GetHTTPService ищет сервис по имени (регистронезависимо). nil, если нет.
func (r *Registry) GetHTTPService(name string) *httpservice.Service {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.httpServices[strings.ToLower(name)]
}

// GetHTTPServiceByRoot ищет сервис по корневому URL (регистронезависимо).
func (r *Registry) GetHTTPServiceByRoot(root string) *httpservice.Service {
	root = strings.Trim(root, "/")
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.httpServices {
		if strings.EqualFold(s.RootURL, root) {
			return s
		}
	}
	return nil
}

// LoadPages регистрирует страницы по имени (регистронезависимо, план 66).
// Метаданные хранятся отдельно от обработчиков (.page.os → pageProcs, грузятся
// через Load), как у HTTP-сервисов.
func (r *Registry) LoadPages(pages []*page.Page) {
	m := make(map[string]*page.Page, len(pages))
	for _, p := range pages {
		m[strings.ToLower(p.Name)] = p
	}
	r.mu.Lock()
	r.pages = m
	r.mu.Unlock()
}

// Pages возвращает все зарегистрированные страницы.
func (r *Registry) Pages() []*page.Page {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*page.Page, 0, len(r.pages))
	for _, p := range r.pages {
		out = append(out, p)
	}
	return out
}

// GetPage ищет страницу по имени (регистронезависимо). nil, если нет.
func (r *Registry) GetPage(name string) *page.Page {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pages[strings.ToLower(name)]
}

// GetPageProcedure резолвит обработчик страницы (X.page.os, план 66). Хранится
// отдельно от procs/serviceProcs, поэтому страница и одноимённый документ/сервис
// не конфликтуют. Регистронезависимо по обоим именам.
func (r *Registry) GetPageProcedure(pageName, procName string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return lookupProc(r.pageProcs, pageName, procName)
}

// LoadExchangePlans регистрирует планы обмена по имени (регистронезависимо,
// план 86). Зеркало LoadHTTPServices: движок обмена ищет план через
// GetExchangePlan, а регистрация изменений в entityservice.Save перебирает
// ExchangePlans().
func (r *Registry) LoadExchangePlans(plans []*metadata.ExchangePlan) {
	m := make(map[string]*metadata.ExchangePlan, len(plans))
	for _, p := range plans {
		m[strings.ToLower(p.Name)] = p
	}
	r.mu.Lock()
	r.exchangePlans = m
	r.mu.Unlock()
}

// ExchangePlans возвращает все зарегистрированные планы обмена.
func (r *Registry) ExchangePlans() []*metadata.ExchangePlan {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.ExchangePlan, 0, len(r.exchangePlans))
	for _, p := range r.exchangePlans {
		out = append(out, p)
	}
	return out
}

// GetExchangePlan ищет план обмена по имени (регистронезависимо). nil, если нет.
func (r *Registry) GetExchangePlan(name string) *metadata.ExchangePlan {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.exchangePlans[strings.ToLower(name)]
}

// LoadIntakes регистрирует входные шлюзы по имени (регистронезависимо, план 90).
// Зеркало LoadExchangePlans: транспортный шов ищет шлюз через GetIntake.
func (r *Registry) LoadIntakes(intakes []*metadata.Intake) {
	m := make(map[string]*metadata.Intake, len(intakes))
	for _, in := range intakes {
		m[strings.ToLower(in.Name)] = in
	}
	r.mu.Lock()
	r.intakes = m
	r.mu.Unlock()
}

// Intakes возвращает все зарегистрированные входные шлюзы.
func (r *Registry) Intakes() []*metadata.Intake {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Intake, 0, len(r.intakes))
	for _, in := range r.intakes {
		out = append(out, in)
	}
	return out
}

// GetIntake ищет входной шлюз по имени (регистронезависимо). nil, если нет.
func (r *Registry) GetIntake(name string) *metadata.Intake {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.intakes[strings.ToLower(name)]
}

func (r *Registry) LoadSubsystems(subs []*metadata.Subsystem) {
	r.mu.Lock()
	r.subsystems = subs
	r.mu.Unlock()
}

func (r *Registry) Subsystems() []*metadata.Subsystem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Subsystem, len(r.subsystems))
	copy(out, r.subsystems)
	return out
}

func (r *Registry) GetSubsystem(name string) *metadata.Subsystem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.subsystems {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (r *Registry) LoadJournals(journals []*metadata.Journal) {
	m := make(map[string]*metadata.Journal, len(journals))
	for _, j := range journals {
		m[strings.ToLower(j.Name)] = j
	}
	r.mu.Lock()
	r.journals = m
	r.mu.Unlock()
}

func (r *Registry) GetJournal(name string) *metadata.Journal {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.journals[strings.ToLower(name)]
}

func (r *Registry) Journals() []*metadata.Journal {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.Journal, 0, len(r.journals))
	for _, j := range r.journals {
		out = append(out, j)
	}
	return out
}

func (r *Registry) GetProcedure(entityName, procName string) *ast.ProcedureDecl {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Сначала ищем в коде конфигурации, затем — во внешних обработках (код из
	// БД). Конфигурация приоритетнее: одноимённая внешняя обработка не
	// перехватывает процедуру конфигурации.
	if d := lookupProc(r.procs, entityName, procName); d != nil {
		return d
	}
	return lookupProc(r.extProcs, entityName, procName)
}

// lookupProc находит процедуру в карте procs (entity → procName → decl) с
// регистронезависимым фолбэком по имени сущности и алиасами событий.
func lookupProc(procs map[string]map[string]*ast.ProcedureDecl, entityName, procName string) *ast.ProcedureDecl {
	pm, ok := procs[entityName]
	if !ok {
		nl := strings.ToLower(entityName)
		for k, v := range procs {
			if strings.ToLower(k) == nl {
				pm = v
				break
			}
		}
		if pm == nil {
			return nil
		}
	}
	procLower := strings.ToLower(procName)
	if p, ok := pm[procLower]; ok {
		return p
	}
	if ru, ok := eventAliases[procLower]; ok {
		return pm[ru]
	}
	return nil
}

func (r *Registry) LoadAccountRegisters(regs []*metadata.AccountRegister, charts []*metadata.ChartOfAccounts) {
	newRegs := make(map[string]*metadata.AccountRegister, len(regs))
	for _, ar := range regs {
		newRegs[strings.ToLower(ar.Name)] = ar
	}
	newCharts := make(map[string]*metadata.ChartOfAccounts, len(charts))
	for _, c := range charts {
		newCharts[strings.ToLower(c.Name)] = c
	}
	r.mu.Lock()
	r.accountRegs = newRegs
	r.chartsOfAccount = newCharts
	r.mu.Unlock()
}

func (r *Registry) GetAccountRegister(name string) *metadata.AccountRegister {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.accountRegs[strings.ToLower(name)]
}

func (r *Registry) AccountRegisters() []*metadata.AccountRegister {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.AccountRegister, 0, len(r.accountRegs))
	for _, ar := range r.accountRegs {
		out = append(out, ar)
	}
	return out
}

func (r *Registry) GetChartOfAccounts(name string) *metadata.ChartOfAccounts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.chartsOfAccount[strings.ToLower(name)]
}

func (r *Registry) ChartsOfAccounts() []*metadata.ChartOfAccounts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*metadata.ChartOfAccounts, 0, len(r.chartsOfAccount))
	for _, c := range r.chartsOfAccount {
		out = append(out, c)
	}
	return out
}
