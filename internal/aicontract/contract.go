package aicontract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/langref"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"gopkg.in/yaml.v3"
)

type Source struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Ref  string `json:"ref,omitempty"`
	Enum string `json:"enum,omitempty"`
}

type TablePart struct {
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
}

type Form struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind,omitempty"`
	LayoutKind string            `json:"layoutKind,omitempty"`
	Elements   int               `json:"elements,omitempty"`
	Attributes int               `json:"attributes,omitempty"`
	Commands   int               `json:"commands,omitempty"`
	Events     map[string]string `json:"events,omitempty"`
}

type Entity struct {
	Name         string      `json:"name"`
	Title        string      `json:"title,omitempty"`
	Hierarchical bool        `json:"hierarchical,omitempty"`
	Posting      bool        `json:"posting,omitempty"`
	Fields       []Field     `json:"fields"`
	TableParts   []TablePart `json:"tableParts,omitempty"`
	BasedOn      []string    `json:"basedOn,omitempty"`
	ListForm     []string    `json:"listForm,omitempty"`
	ItemForm     []string    `json:"itemForm,omitempty"`
	Forms        []Form      `json:"forms,omitempty"`
	Source       *Source     `json:"source,omitempty"`
}

type Register struct {
	Name       string  `json:"name"`
	Title      string  `json:"title,omitempty"`
	Dimensions []Field `json:"dimensions,omitempty"`
	Resources  []Field `json:"resources,omitempty"`
	Attributes []Field `json:"attributes,omitempty"`
	Source     *Source `json:"source,omitempty"`
}

type InfoRegister struct {
	Name       string  `json:"name"`
	Title      string  `json:"title,omitempty"`
	Periodic   bool    `json:"periodic,omitempty"`
	Dimensions []Field `json:"dimensions,omitempty"`
	Resources  []Field `json:"resources,omitempty"`
	Source     *Source `json:"source,omitempty"`
}

type NamedValues struct {
	Name   string   `json:"name"`
	Values []string `json:"values,omitempty"`
	Source *Source  `json:"source,omitempty"`
}

type Constant struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Ref    string  `json:"ref,omitempty"`
	Enum   string  `json:"enum,omitempty"`
	Source *Source `json:"source,omitempty"`
}

type Param struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Label   string   `json:"label,omitempty"`
	Options []string `json:"options,omitempty"`
}

type Processor struct {
	Name       string      `json:"name"`
	Title      string      `json:"title,omitempty"`
	Params     []Param     `json:"params,omitempty"`
	TableParts []TablePart `json:"tableParts,omitempty"`
	Forms      []Form      `json:"forms,omitempty"`
	Source     *Source     `json:"source,omitempty"`
}

type Procedure struct {
	Name   string   `json:"name"`
	Params []string `json:"params,omitempty"`
	Export bool     `json:"export,omitempty"`
	Source *Source  `json:"source,omitempty"`
}

type Module struct {
	Name       string      `json:"name"`
	Kind       string      `json:"kind,omitempty"`
	Procedures []Procedure `json:"procedures,omitempty"`
	Source     *Source     `json:"source,omitempty"`
}

type Report struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Params      []Param  `json:"params,omitempty"`
	Query       string   `json:"query,omitempty"`
	ChartProc   string   `json:"chartProc,omitempty"`
	Composition bool     `json:"composition,omitempty"`
	Variants    []string `json:"variants,omitempty"`
	External    bool     `json:"external,omitempty"`
	Source      *Source  `json:"source,omitempty"`
}

type WidgetColumn struct {
	Field  string `json:"field"`
	Label  string `json:"label,omitempty"`
	Format string `json:"format,omitempty"`
	Align  string `json:"align,omitempty"`
}

type WidgetAction struct {
	Label  string `json:"label,omitempty"`
	Entity string `json:"entity,omitempty"`
	URL    string `json:"url,omitempty"`
}

type Widget struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Title     string            `json:"title,omitempty"`
	Query     string            `json:"query,omitempty"`
	Params    map[string]string `json:"params,omitempty"`
	Format    string            `json:"format,omitempty"`
	Limit     int               `json:"limit,omitempty"`
	Columns   []WidgetColumn    `json:"columns,omitempty"`
	ChartKind string            `json:"chartKind,omitempty"`
	XField    string            `json:"xField,omitempty"`
	YFields   []string          `json:"yFields,omitempty"`
	Items     []WidgetAction    `json:"items,omitempty"`
	Entities  []string          `json:"entities,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	Source    *Source           `json:"source,omitempty"`
}

type Journal struct {
	Name      string                   `json:"name"`
	Title     string                   `json:"title,omitempty"`
	Documents []string                 `json:"documents,omitempty"`
	Columns   []metadata.JournalColumn `json:"columns,omitempty"`
	Filters   []metadata.JournalFilter `json:"filters,omitempty"`
	Source    *Source                  `json:"source,omitempty"`
}

type Subsystem struct {
	Name     string                      `json:"name"`
	Title    string                      `json:"title,omitempty"`
	Icon     string                      `json:"icon,omitempty"`
	Order    int                         `json:"order,omitempty"`
	Contents *metadata.SubsystemContents `json:"contents,omitempty"`
	HomePage bool                        `json:"homePage,omitempty"`
	Source   *Source                     `json:"source,omitempty"`
}

type Page struct {
	Name   string   `json:"name"`
	Title  string   `json:"title,omitempty"`
	Icon   string   `json:"icon,omitempty"`
	Roles  []string `json:"roles,omitempty"`
	Params []string `json:"params,omitempty"`
	Source *Source  `json:"source,omitempty"`
}

type HTTPService struct {
	Name      string   `json:"name"`
	Title     string   `json:"title,omitempty"`
	RootURL   string   `json:"rootURL,omitempty"`
	Auth      string   `json:"auth,omitempty"`
	RateLimit int      `json:"rateLimit,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Templates []struct {
		Template string            `json:"template"`
		Methods  map[string]string `json:"methods,omitempty"`
	} `json:"templates,omitempty"`
	Source *Source `json:"source,omitempty"`
}

type Permission struct {
	Catalogs   map[string][]string `json:"catalogs,omitempty"`
	Documents  map[string][]string `json:"documents,omitempty"`
	Registers  map[string][]string `json:"registers,omitempty"`
	InfoRegs   map[string][]string `json:"inforegs,omitempty"`
	Reports    map[string][]string `json:"reports,omitempty"`
	Processors map[string][]string `json:"processors,omitempty"`
}

type Role struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Permissions Permission `json:"permissions,omitempty"`
	Source      *Source    `json:"source,omitempty"`
}

type ScheduledJob struct {
	Name      string         `json:"name"`
	Title     string         `json:"title,omitempty"`
	Schedule  string         `json:"schedule,omitempty"`
	Processor string         `json:"processor,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	Enabled   bool           `json:"enabled,omitempty"`
	OnError   string         `json:"onError,omitempty"`
	Timeout   int            `json:"timeout,omitempty"`
	Source    *Source        `json:"source,omitempty"`
}

type ChartOfAccounts struct {
	Name     string             `json:"name"`
	Title    string             `json:"title,omitempty"`
	Accounts []metadata.Account `json:"accounts,omitempty"`
	Source   *Source            `json:"source,omitempty"`
}

type AccountRegister struct {
	Name      string  `json:"name"`
	Title     string  `json:"title,omitempty"`
	Accounts  string  `json:"accounts,omitempty"`
	Resources []Field `json:"resources,omitempty"`
	Subconto  []Field `json:"subconto,omitempty"`
	Source    *Source `json:"source,omitempty"`
}

type Contract struct {
	SchemaVersion    int                  `json:"schemaVersion"`
	Catalogs         []Entity             `json:"catalogs"`
	Documents        []Entity             `json:"documents"`
	Registers        []Register           `json:"registers"`
	InfoRegisters    []InfoRegister       `json:"infoRegisters"`
	AccountRegisters []AccountRegister    `json:"accountRegisters,omitempty"`
	ChartsOfAccounts []ChartOfAccounts    `json:"chartsOfAccounts,omitempty"`
	Enums            []NamedValues        `json:"enums"`
	Constants        []Constant           `json:"constants"`
	Reports          []Report             `json:"reports"`
	Processors       []Processor          `json:"processors"`
	Subsystems       []Subsystem          `json:"subsystems"`
	Journals         []Journal            `json:"journals"`
	Widgets          []Widget             `json:"widgets"`
	Pages            []Page               `json:"pages,omitempty"`
	HTTPServices     []HTTPService        `json:"httpServices,omitempty"`
	Roles            []Role               `json:"roles,omitempty"`
	ScheduledJobs    []ScheduledJob       `json:"scheduledJobs,omitempty"`
	HomePage         bool                 `json:"homePage,omitempty"`
	Modules          []Module             `json:"modules"`
	Builtins         []langref.Descriptor `json:"builtins"`
	Language         []langref.Descriptor `json:"language"`
}

// Build returns the full schemaVersion=2 AI contract for a loaded project.
func Build(dir string, proj *project.Project) (*Contract, error) {
	out := &Contract{
		SchemaVersion:    2,
		Catalogs:         []Entity{},
		Documents:        []Entity{},
		Registers:        []Register{},
		InfoRegisters:    []InfoRegister{},
		AccountRegisters: []AccountRegister{},
		ChartsOfAccounts: []ChartOfAccounts{},
		Enums:            []NamedValues{},
		Constants:        []Constant{},
		Reports:          []Report{},
		Processors:       []Processor{},
		Subsystems:       []Subsystem{},
		Journals:         []Journal{},
		Widgets:          []Widget{},
		Pages:            []Page{},
		HTTPServices:     []HTTPService{},
		Roles:            []Role{},
		ScheduledJobs:    []ScheduledJob{},
		Modules:          []Module{},
		Builtins:         []langref.Descriptor{},
		Language:         []langref.Descriptor{},
	}
	src := newSourceLookup(dir)

	for _, e := range proj.Entities {
		subdir := "catalogs"
		if e.Kind == metadata.KindDocument {
			subdir = "documents"
		}
		de := Entity{
			Name: e.Name, Title: e.Title,
			Hierarchical: e.Hierarchical, Posting: e.Posting,
			Fields:   toFields(e.Fields),
			BasedOn:  e.BasedOn,
			ListForm: e.ListForm,
			ItemForm: e.ItemForm,
			Forms:    toForms(e.Forms),
			Source:   src.yaml(subdir, e.Name),
		}
		for _, tp := range e.TableParts {
			de.TableParts = append(de.TableParts, TablePart{Name: tp.Name, Fields: toFields(tp.Fields)})
		}
		if e.Kind == metadata.KindDocument {
			out.Documents = append(out.Documents, de)
		} else {
			out.Catalogs = append(out.Catalogs, de)
		}
	}
	for _, r := range proj.Registers {
		out.Registers = append(out.Registers, Register{
			Name: r.Name, Title: r.Title, Dimensions: toFields(r.Dimensions),
			Resources: toFields(r.Resources), Attributes: toFields(r.Attributes),
			Source: src.yaml("registers", r.Name),
		})
	}
	for _, ir := range proj.InfoRegisters {
		out.InfoRegisters = append(out.InfoRegisters, InfoRegister{
			Name: ir.Name, Title: ir.Title, Periodic: ir.Periodic,
			Dimensions: toFields(ir.Dimensions), Resources: toFields(ir.Resources),
			Source: src.yaml("inforegs", ir.Name),
		})
	}
	for _, ar := range proj.AccountRegisters {
		out.AccountRegisters = append(out.AccountRegisters, AccountRegister{
			Name: ar.Name, Title: ar.Title, Accounts: ar.Accounts,
			Resources: toFields(ar.Resources), Subconto: toFields(ar.Subconto),
			Source: src.yaml("accountregs", ar.Name),
		})
	}
	for _, ch := range proj.ChartsOfAccounts {
		out.ChartsOfAccounts = append(out.ChartsOfAccounts, ChartOfAccounts{
			Name: ch.Name, Title: ch.Title, Accounts: ch.Accounts, Source: src.yaml("accounts", ch.Name),
		})
	}
	for _, en := range proj.Enums {
		out.Enums = append(out.Enums, NamedValues{Name: en.Name, Values: en.Values, Source: src.yaml("enums", en.Name)})
	}
	for _, c := range proj.Constants {
		out.Constants = append(out.Constants, Constant{
			Name: c.Name, Type: string(c.Type), Ref: c.RefEntity, Enum: c.EnumName,
			Source: src.yaml("constants", "constants"),
		})
	}
	for _, rep := range proj.Reports {
		dr := Report{
			Name: rep.Name, Title: rep.Title, Query: rep.Query, ChartProc: rep.ChartProc,
			Composition: rep.Composition != nil, External: rep.External,
			Source: src.yaml("reports", rep.Name),
		}
		for _, p := range rep.Params {
			dr.Params = append(dr.Params, Param{Name: p.Name, Type: p.Type, Label: p.Label, Options: p.Options})
		}
		for _, v := range rep.Variants {
			dr.Variants = append(dr.Variants, v.Name)
		}
		out.Reports = append(out.Reports, dr)
	}
	for _, p := range proj.Processors {
		dp := Processor{Name: p.Name, Title: p.Title, Forms: toForms(p.Forms), Source: src.yaml("processors", p.Name)}
		for _, par := range p.Params {
			dp.Params = append(dp.Params, Param{Name: par.Name, Type: par.Type, Label: par.Label, Options: par.Options})
		}
		for _, tp := range p.TableParts {
			dp.TableParts = append(dp.TableParts, TablePart{Name: tp.Name, Fields: toFields(tp.Fields)})
		}
		out.Processors = append(out.Processors, dp)
	}
	for _, s := range proj.Subsystems {
		ds := Subsystem{
			Name: s.Name, Title: s.Title, Icon: s.Icon, Order: s.Order,
			HomePage: s.HomePage != nil, Source: src.yaml("subsystems", s.Name),
		}
		if !s.Contents.IsEmpty() {
			c := s.Contents
			ds.Contents = &c
		}
		out.Subsystems = append(out.Subsystems, ds)
	}
	for _, j := range proj.Journals {
		out.Journals = append(out.Journals, Journal{
			Name: j.Name, Title: j.Title, Documents: j.Documents,
			Columns: j.Columns, Filters: j.Filters, Source: src.yaml("journals", j.Name),
		})
	}
	for _, w := range proj.Widgets {
		dw := Widget{
			Name: w.Name, Type: string(w.Type), Title: w.Title, Query: w.Query,
			Params: w.Params, Format: w.Format, Limit: w.Limit, ChartKind: w.ChartKind,
			XField: w.XField, YFields: w.YFields, Entities: w.Entities, Scope: w.Scope,
			Source: src.yaml("widgets", w.Name),
		}
		for _, c := range w.Columns {
			dw.Columns = append(dw.Columns, WidgetColumn{Field: c.Field, Label: c.Label, Format: c.Format, Align: c.Align})
		}
		for _, it := range w.Items {
			dw.Items = append(dw.Items, WidgetAction{Label: it.Label, Entity: it.Entity, URL: it.URL})
		}
		out.Widgets = append(out.Widgets, dw)
	}
	for _, p := range proj.Pages {
		out.Pages = append(out.Pages, Page{
			Name: p.Name, Title: p.Title, Icon: p.Icon, Roles: p.Roles, Params: p.Params,
			Source: src.yaml("pages", p.Name),
		})
	}
	for _, s := range proj.HTTPServices {
		ds := HTTPService{
			Name: s.Name, Title: s.Title, RootURL: s.RootURL, Auth: s.Auth,
			RateLimit: s.RateLimit, Roles: s.Roles, Source: src.yaml("services", s.Name),
		}
		for _, t := range s.Templates {
			ds.Templates = append(ds.Templates, struct {
				Template string            `json:"template"`
				Methods  map[string]string `json:"methods,omitempty"`
			}{Template: t.Template, Methods: t.Methods})
		}
		out.HTTPServices = append(out.HTTPServices, ds)
	}
	roles, err := auth.LoadRolesYAML(filepath.Join(dir, "roles"))
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		out.Roles = append(out.Roles, Role{
			Name:        r.Name,
			Description: r.Description,
			Permissions: permissionFromAuth(r.Permissions),
			Source:      src.yaml("roles", r.Name),
		})
	}
	for _, j := range proj.ScheduledJobs {
		out.ScheduledJobs = append(out.ScheduledJobs, ScheduledJob{
			Name: j.Name, Title: j.Title, Schedule: j.Schedule, Processor: j.Processor,
			Params: j.Params, Enabled: j.Enabled, OnError: j.OnError, Timeout: j.Timeout,
			Source: src.yaml("scheduled", j.Name),
		})
	}
	out.HomePage = proj.HomePage != nil

	for name, prog := range proj.Modules {
		out.Modules = append(out.Modules, Module{Name: name, Kind: "module", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.Programs {
		out.Modules = append(out.Modules, Module{Name: name, Kind: "object", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.ManagerPrograms {
		out.Modules = append(out.Modules, Module{Name: name, Kind: "manager", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.ServicePrograms {
		out.Modules = append(out.Modules, Module{Name: name, Kind: "service", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.PagePrograms {
		out.Modules = append(out.Modules, Module{Name: name, Kind: "page", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	sort.Slice(out.Modules, func(i, j int) bool { return out.Modules[i].Name < out.Modules[j].Name })

	for _, d := range langref.All() {
		out.Language = append(out.Language, d)
		if d.Kind == langref.KindFunc {
			out.Builtins = append(out.Builtins, d)
		}
	}
	sort.Slice(out.Language, func(i, j int) bool { return langrefSortKey(out.Language[i]) < langrefSortKey(out.Language[j]) })
	sort.Slice(out.Builtins, func(i, j int) bool {
		return strings.ToLower(out.Builtins[i].Name) < strings.ToLower(out.Builtins[j].Name)
	})
	return out, nil
}

// TextInputFromProject returns compact prompt input from a loaded project.
func TextInputFromProject(proj *project.Project) TextInput {
	reports := make([]NamedTitle, 0, len(proj.Reports))
	for _, rp := range proj.Reports {
		reports = append(reports, NamedTitle{Name: rp.Name, Title: rp.Title})
	}
	procs := make([]NamedTitle, 0, len(proj.Processors))
	for _, p := range proj.Processors {
		procs = append(procs, NamedTitle{Name: p.Name, Title: p.Title})
	}
	return TextInput{
		Entities:         proj.Entities,
		Registers:        proj.Registers,
		InfoRegisters:    proj.InfoRegisters,
		AccountRegisters: proj.AccountRegisters,
		ChartsOfAccounts: proj.ChartsOfAccounts,
		Enums:            proj.Enums,
		Constants:        proj.Constants,
		Reports:          reports,
		Processors:       procs,
		Journals:         proj.Journals,
		Subsystems:       proj.Subsystems,
	}
}

func ProjectSchemaText(proj *project.Project) string {
	return SchemaText(TextInputFromProject(proj))
}

func toFields(fields []metadata.Field) []Field {
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		out = append(out, Field{Name: f.Name, Type: string(f.Type), Ref: f.RefEntity, Enum: f.EnumName})
	}
	return out
}

func toForms(forms []*metadata.FormModule) []Form {
	out := make([]Form, 0, len(forms))
	for _, f := range forms {
		if f == nil {
			continue
		}
		df := Form{
			Name:       f.Name,
			Kind:       f.Kind,
			LayoutKind: f.LayoutKind,
			Elements:   countFormElements(f.Elements),
			Attributes: len(f.Attributes),
			Commands:   len(f.Commands),
		}
		if len(f.Handlers) > 0 {
			df.Events = map[string]string{}
			for ev, proc := range f.Handlers {
				df.Events[string(ev)] = proc
			}
		}
		out = append(out, df)
	}
	return out
}

func permissionFromAuth(p auth.Permission) Permission {
	return Permission{
		Catalogs:   p.Catalogs,
		Documents:  p.Documents,
		Registers:  p.Registers,
		InfoRegs:   p.InfoRegs,
		Reports:    p.Reports,
		Processors: p.Processors,
	}
}

func countFormElements(items []*metadata.FormElement) int {
	n := 0
	for _, el := range items {
		if el == nil {
			continue
		}
		n++
		n += countFormElements(el.Children)
	}
	return n
}

func procDescs(prog *ast.Program, src sourceLookup) []Procedure {
	if prog == nil {
		return nil
	}
	out := make([]Procedure, 0, len(prog.Procedures))
	for _, p := range prog.Procedures {
		dp := Procedure{Name: p.Name.Literal, Export: p.Export}
		for _, par := range p.Params {
			dp.Params = append(dp.Params, par.Literal)
		}
		dp.Source = src.source(p.Name.File, p.Name.Line)
		out = append(out, dp)
	}
	return out
}

func moduleSource(prog *ast.Program, src sourceLookup) *Source {
	if prog == nil {
		return nil
	}
	for _, p := range prog.Procedures {
		if p.Name.File != "" {
			return src.source(p.Name.File, 1)
		}
	}
	return nil
}

func langrefSortKey(d langref.Descriptor) string {
	return string(d.Kind) + "|" + strings.ToLower(d.Object) + "|" + strings.ToLower(d.Name)
}

type sourceLookup struct {
	dir   string
	files map[string]string
}

func newSourceLookup(dir string) sourceLookup {
	return sourceLookup{dir: dir, files: map[string]string{}}
}

func (s sourceLookup) yaml(subdir, name string) *Source {
	file := s.lookupYAML(subdir, name)
	if file == "" {
		return nil
	}
	return &Source{File: file, Line: 1}
}

func (s sourceLookup) source(file string, line int) *Source {
	if file == "" {
		return nil
	}
	rel := file
	if absDir, errDir := filepath.Abs(s.dir); errDir == nil {
		if absFile, errFile := filepath.Abs(file); errFile == nil {
			if r, err := filepath.Rel(absDir, absFile); err == nil && r != ".." &&
				!strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r) {
				rel = r
			}
		}
	}
	if line <= 0 {
		line = 1
	}
	return &Source{File: filepath.ToSlash(rel), Line: line}
}

func (s sourceLookup) lookupYAML(subdir, name string) string {
	key := subdir + "|" + strings.ToLower(name)
	if v, ok := s.files[key]; ok {
		return v
	}
	dir := filepath.Join(s.dir, subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		rel := filepath.ToSlash(filepath.Join(subdir, e.Name()))
		s.files[subdir+"|"+strings.ToLower(stem)] = rel
		if yamlName := topLevelYAMLName(filepath.Join(dir, e.Name())); yamlName != "" {
			s.files[subdir+"|"+strings.ToLower(yamlName)] = rel
		}
		if strings.EqualFold(stem, name) || strings.EqualFold(stem, strings.ToLower(name)) {
			s.files[key] = rel
			return rel
		}
	}
	return s.files[key]
}

func topLevelYAMLName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var v struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v.Name)
}
