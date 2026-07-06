package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/langref"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var describeCmd = &cobra.Command{
	Use:   "describe",
	Short: "Выгрузить структуру конфигурации в JSON (для ИИ-инструментов)",
	Long: `Сериализует всю метаданность конфигурации (справочники, документы,
регистры, перечисления, константы, отчёты, обработки, подсистемы, журналы,
модули с именами процедур) и список встроенных функций DSL в JSON.
Read-only «рентген» — контракт для ИИ и будущего MCP.

Примеры:
  onebase describe --project C:\Projects\OneBaseConfs\PuT
  onebase describe --id <baseID> | jq .processors`,
	RunE:          runDescribe,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	addBaseFlags(describeCmd)
	describeCmd.Flags().Bool("compact", false, "вывести компактный текстовый контракт для prompt-контекста")
	describeCmd.Flags().Bool("full", false, "вывести полный JSON-контракт (значение по умолчанию)")
	rootCmd.AddCommand(describeCmd)
}

type descSource struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

type descField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Ref  string `json:"ref,omitempty"`
	Enum string `json:"enum,omitempty"`
}

type descTablePart struct {
	Name   string      `json:"name"`
	Fields []descField `json:"fields"`
}

type descForm struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind,omitempty"`
	LayoutKind string            `json:"layoutKind,omitempty"`
	Elements   int               `json:"elements,omitempty"`
	Attributes int               `json:"attributes,omitempty"`
	Commands   int               `json:"commands,omitempty"`
	Events     map[string]string `json:"events,omitempty"`
}

type descEntity struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	Hierarchical bool            `json:"hierarchical,omitempty"`
	Posting      bool            `json:"posting,omitempty"`
	Fields       []descField     `json:"fields"`
	TableParts   []descTablePart `json:"tableParts,omitempty"`
	BasedOn      []string        `json:"basedOn,omitempty"`
	ListForm     []string        `json:"listForm,omitempty"`
	ItemForm     []string        `json:"itemForm,omitempty"`
	Forms        []descForm      `json:"forms,omitempty"`
	Source       *descSource     `json:"source,omitempty"`
}

type descRegister struct {
	Name       string      `json:"name"`
	Title      string      `json:"title,omitempty"`
	Dimensions []descField `json:"dimensions,omitempty"`
	Resources  []descField `json:"resources,omitempty"`
	Attributes []descField `json:"attributes,omitempty"`
	Source     *descSource `json:"source,omitempty"`
}

type descInfoReg struct {
	Name       string      `json:"name"`
	Title      string      `json:"title,omitempty"`
	Periodic   bool        `json:"periodic,omitempty"`
	Dimensions []descField `json:"dimensions,omitempty"`
	Resources  []descField `json:"resources,omitempty"`
	Source     *descSource `json:"source,omitempty"`
}

type descNamedValues struct {
	Name   string      `json:"name"`
	Values []string    `json:"values,omitempty"`
	Source *descSource `json:"source,omitempty"`
}

type descConstant struct {
	Name   string      `json:"name"`
	Type   string      `json:"type"`
	Ref    string      `json:"ref,omitempty"`
	Enum   string      `json:"enum,omitempty"`
	Source *descSource `json:"source,omitempty"`
}

type descParam struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Label   string   `json:"label,omitempty"`
	Options []string `json:"options,omitempty"`
}

type descProcessor struct {
	Name       string          `json:"name"`
	Title      string          `json:"title,omitempty"`
	Params     []descParam     `json:"params,omitempty"`
	TableParts []descTablePart `json:"tableParts,omitempty"`
	Forms      []descForm      `json:"forms,omitempty"`
	Source     *descSource     `json:"source,omitempty"`
}

type descProcedure struct {
	Name   string      `json:"name"`
	Params []string    `json:"params,omitempty"`
	Export bool        `json:"export,omitempty"`
	Source *descSource `json:"source,omitempty"`
}

type descModule struct {
	Name       string          `json:"name"`
	Kind       string          `json:"kind,omitempty"`
	Procedures []descProcedure `json:"procedures,omitempty"`
	Source     *descSource     `json:"source,omitempty"`
}

type descReport struct {
	Name        string      `json:"name"`
	Title       string      `json:"title,omitempty"`
	Params      []descParam `json:"params,omitempty"`
	Query       string      `json:"query,omitempty"`
	ChartProc   string      `json:"chartProc,omitempty"`
	Composition bool        `json:"composition,omitempty"`
	Variants    []string    `json:"variants,omitempty"`
	External    bool        `json:"external,omitempty"`
	Source      *descSource `json:"source,omitempty"`
}

type descWidgetColumn struct {
	Field  string `json:"field"`
	Label  string `json:"label,omitempty"`
	Format string `json:"format,omitempty"`
	Align  string `json:"align,omitempty"`
}

type descWidgetAction struct {
	Label  string `json:"label,omitempty"`
	Entity string `json:"entity,omitempty"`
	URL    string `json:"url,omitempty"`
}

type descWidget struct {
	Name      string             `json:"name"`
	Type      string             `json:"type"`
	Title     string             `json:"title,omitempty"`
	Query     string             `json:"query,omitempty"`
	Params    map[string]string  `json:"params,omitempty"`
	Format    string             `json:"format,omitempty"`
	Limit     int                `json:"limit,omitempty"`
	Columns   []descWidgetColumn `json:"columns,omitempty"`
	ChartKind string             `json:"chartKind,omitempty"`
	XField    string             `json:"xField,omitempty"`
	YFields   []string           `json:"yFields,omitempty"`
	Items     []descWidgetAction `json:"items,omitempty"`
	Entities  []string           `json:"entities,omitempty"`
	Scope     string             `json:"scope,omitempty"`
	Source    *descSource        `json:"source,omitempty"`
}

type descJournal struct {
	Name      string                   `json:"name"`
	Title     string                   `json:"title,omitempty"`
	Documents []string                 `json:"documents,omitempty"`
	Columns   []metadata.JournalColumn `json:"columns,omitempty"`
	Filters   []metadata.JournalFilter `json:"filters,omitempty"`
	Source    *descSource              `json:"source,omitempty"`
}

type descSubsystem struct {
	Name     string                      `json:"name"`
	Title    string                      `json:"title,omitempty"`
	Icon     string                      `json:"icon,omitempty"`
	Order    int                         `json:"order,omitempty"`
	Contents *metadata.SubsystemContents `json:"contents,omitempty"`
	HomePage bool                        `json:"homePage,omitempty"`
	Source   *descSource                 `json:"source,omitempty"`
}

type descPage struct {
	Name   string      `json:"name"`
	Title  string      `json:"title,omitempty"`
	Icon   string      `json:"icon,omitempty"`
	Roles  []string    `json:"roles,omitempty"`
	Params []string    `json:"params,omitempty"`
	Source *descSource `json:"source,omitempty"`
}

type descHTTPService struct {
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
	Source *descSource `json:"source,omitempty"`
}

type descPermission struct {
	Catalogs   map[string][]string `json:"catalogs,omitempty"`
	Documents  map[string][]string `json:"documents,omitempty"`
	Registers  map[string][]string `json:"registers,omitempty"`
	InfoRegs   map[string][]string `json:"inforegs,omitempty"`
	Reports    map[string][]string `json:"reports,omitempty"`
	Processors map[string][]string `json:"processors,omitempty"`
}

type descRole struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Permissions descPermission `json:"permissions,omitempty"`
	Source      *descSource    `json:"source,omitempty"`
}

type descScheduledJob struct {
	Name      string         `json:"name"`
	Title     string         `json:"title,omitempty"`
	Schedule  string         `json:"schedule,omitempty"`
	Processor string         `json:"processor,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	Enabled   bool           `json:"enabled,omitempty"`
	OnError   string         `json:"onError,omitempty"`
	Timeout   int            `json:"timeout,omitempty"`
	Source    *descSource    `json:"source,omitempty"`
}

type descChartOfAccounts struct {
	Name     string             `json:"name"`
	Title    string             `json:"title,omitempty"`
	Accounts []metadata.Account `json:"accounts,omitempty"`
	Source   *descSource        `json:"source,omitempty"`
}

type descAccountReg struct {
	Name      string      `json:"name"`
	Title     string      `json:"title,omitempty"`
	Accounts  string      `json:"accounts,omitempty"`
	Resources []descField `json:"resources,omitempty"`
	Subconto  []descField `json:"subconto,omitempty"`
	Source    *descSource `json:"source,omitempty"`
}

type describeOutput struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	Catalogs         []descEntity          `json:"catalogs"`
	Documents        []descEntity          `json:"documents"`
	Registers        []descRegister        `json:"registers"`
	InfoRegisters    []descInfoReg         `json:"infoRegisters"`
	AccountRegisters []descAccountReg      `json:"accountRegisters,omitempty"`
	ChartsOfAccounts []descChartOfAccounts `json:"chartsOfAccounts,omitempty"`
	Enums            []descNamedValues     `json:"enums"`
	Constants        []descConstant        `json:"constants"`
	Reports          []descReport          `json:"reports"`
	Processors       []descProcessor       `json:"processors"`
	Subsystems       []descSubsystem       `json:"subsystems"`
	Journals         []descJournal         `json:"journals"`
	Widgets          []descWidget          `json:"widgets"`
	Pages            []descPage            `json:"pages,omitempty"`
	HTTPServices     []descHTTPService     `json:"httpServices,omitempty"`
	Roles            []descRole            `json:"roles,omitempty"`
	ScheduledJobs    []descScheduledJob    `json:"scheduledJobs,omitempty"`
	HomePage         bool                  `json:"homePage,omitempty"`
	Modules          []descModule          `json:"modules"`
	Builtins         []langref.Descriptor  `json:"builtins"`
	Language         []langref.Descriptor  `json:"language"`
}

func runDescribe(cmd *cobra.Command, _ []string) error {
	bc, err := resolveBase(cmd)
	if err != nil {
		return err
	}
	defer bc.Cleanup()

	proj, err := project.Load(bc.Dir)
	if err != nil {
		return err
	}
	defer proj.Close()

	compact, _ := cmd.Flags().GetBool("compact")
	if compact {
		fmt.Fprint(os.Stdout, projectAIContext(proj))
		return nil
	}

	out := describeOutput{
		SchemaVersion:    2,
		Catalogs:         []descEntity{},
		Documents:        []descEntity{},
		Registers:        []descRegister{},
		InfoRegisters:    []descInfoReg{},
		AccountRegisters: []descAccountReg{},
		ChartsOfAccounts: []descChartOfAccounts{},
		Enums:            []descNamedValues{},
		Constants:        []descConstant{},
		Reports:          []descReport{},
		Processors:       []descProcessor{},
		Subsystems:       []descSubsystem{},
		Journals:         []descJournal{},
		Widgets:          []descWidget{},
		Pages:            []descPage{},
		HTTPServices:     []descHTTPService{},
		Roles:            []descRole{},
		ScheduledJobs:    []descScheduledJob{},
		Modules:          []descModule{},
		Builtins:         []langref.Descriptor{},
		Language:         []langref.Descriptor{},
	}
	src := newSourceLookup(bc.Dir)

	for _, e := range proj.Entities {
		subdir := "catalogs"
		if e.Kind == metadata.KindDocument {
			subdir = "documents"
		}
		de := descEntity{
			Name: e.Name, Title: e.Title, Description: e.Description,
			Hierarchical: e.Hierarchical, Posting: e.Posting,
			Fields:   toDescFields(e.Fields),
			BasedOn:  e.BasedOn,
			ListForm: e.ListForm,
			ItemForm: e.ItemForm,
			Forms:    toDescForms(e.Forms),
			Source:   src.yaml(subdir, e.Name),
		}
		for _, tp := range e.TableParts {
			de.TableParts = append(de.TableParts, descTablePart{Name: tp.Name, Fields: toDescFields(tp.Fields)})
		}
		if e.Kind == metadata.KindDocument {
			out.Documents = append(out.Documents, de)
		} else {
			out.Catalogs = append(out.Catalogs, de)
		}
	}
	for _, r := range proj.Registers {
		out.Registers = append(out.Registers, descRegister{
			Name: r.Name, Title: r.Title, Dimensions: toDescFields(r.Dimensions),
			Resources: toDescFields(r.Resources), Attributes: toDescFields(r.Attributes),
			Source: src.yaml("registers", r.Name),
		})
	}
	for _, ir := range proj.InfoRegisters {
		out.InfoRegisters = append(out.InfoRegisters, descInfoReg{
			Name: ir.Name, Title: ir.Title, Periodic: ir.Periodic,
			Dimensions: toDescFields(ir.Dimensions), Resources: toDescFields(ir.Resources),
			Source: src.yaml("inforegs", ir.Name),
		})
	}
	for _, ar := range proj.AccountRegisters {
		out.AccountRegisters = append(out.AccountRegisters, descAccountReg{
			Name: ar.Name, Title: ar.Title, Accounts: ar.Accounts,
			Resources: toDescFields(ar.Resources), Subconto: toDescFields(ar.Subconto),
			Source: src.yaml("accountregs", ar.Name),
		})
	}
	for _, ch := range proj.ChartsOfAccounts {
		out.ChartsOfAccounts = append(out.ChartsOfAccounts, descChartOfAccounts{
			Name: ch.Name, Title: ch.Title, Accounts: ch.Accounts, Source: src.yaml("accounts", ch.Name),
		})
	}
	for _, en := range proj.Enums {
		out.Enums = append(out.Enums, descNamedValues{Name: en.Name, Values: en.Values, Source: src.yaml("enums", en.Name)})
	}
	for _, c := range proj.Constants {
		out.Constants = append(out.Constants, descConstant{
			Name: c.Name, Type: string(c.Type), Ref: c.RefEntity, Enum: c.EnumName,
			Source: src.yaml("constants", "constants"),
		})
	}
	for _, rep := range proj.Reports {
		dr := descReport{
			Name: rep.Name, Title: rep.Title, Query: rep.Query, ChartProc: rep.ChartProc,
			Composition: rep.Composition != nil, External: rep.External,
			Source: src.yaml("reports", rep.Name),
		}
		for _, p := range rep.Params {
			dr.Params = append(dr.Params, descParam{Name: p.Name, Type: p.Type, Label: p.Label, Options: p.Options})
		}
		for _, v := range rep.Variants {
			dr.Variants = append(dr.Variants, v.Name)
		}
		out.Reports = append(out.Reports, dr)
	}
	for _, p := range proj.Processors {
		dp := descProcessor{Name: p.Name, Title: p.Title, Forms: toDescForms(p.Forms), Source: src.yaml("processors", p.Name)}
		for _, par := range p.Params {
			dp.Params = append(dp.Params, descParam{Name: par.Name, Type: par.Type, Label: par.Label, Options: par.Options})
		}
		for _, tp := range p.TableParts {
			dp.TableParts = append(dp.TableParts, descTablePart{Name: tp.Name, Fields: toDescFields(tp.Fields)})
		}
		out.Processors = append(out.Processors, dp)
	}
	for _, s := range proj.Subsystems {
		ds := descSubsystem{
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
		out.Journals = append(out.Journals, descJournal{
			Name: j.Name, Title: j.Title, Documents: j.Documents,
			Columns: j.Columns, Filters: j.Filters, Source: src.yaml("journals", j.Name),
		})
	}
	for _, w := range proj.Widgets {
		dw := descWidget{
			Name: w.Name, Type: string(w.Type), Title: w.Title, Query: w.Query,
			Params: w.Params, Format: w.Format, Limit: w.Limit, ChartKind: w.ChartKind,
			XField: w.XField, YFields: w.YFields, Entities: w.Entities, Scope: w.Scope,
			Source: src.yaml("widgets", w.Name),
		}
		for _, c := range w.Columns {
			dw.Columns = append(dw.Columns, descWidgetColumn{Field: c.Field, Label: c.Label, Format: c.Format, Align: c.Align})
		}
		for _, it := range w.Items {
			dw.Items = append(dw.Items, descWidgetAction{Label: it.Label, Entity: it.Entity, URL: it.URL})
		}
		out.Widgets = append(out.Widgets, dw)
	}
	for _, p := range proj.Pages {
		out.Pages = append(out.Pages, descPage{
			Name: p.Name, Title: p.Title, Icon: p.Icon, Roles: p.Roles, Params: p.Params,
			Source: src.yaml("pages", p.Name),
		})
	}
	for _, s := range proj.HTTPServices {
		ds := descHTTPService{
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
	roles, err := auth.LoadRolesYAML(filepath.Join(bc.Dir, "roles"))
	if err != nil {
		return err
	}
	for _, r := range roles {
		out.Roles = append(out.Roles, descRole{
			Name:        r.Name,
			Description: r.Description,
			Permissions: toDescPermission(r.Permissions),
			Source:      src.yaml("roles", r.Name),
		})
	}
	for _, j := range proj.ScheduledJobs {
		out.ScheduledJobs = append(out.ScheduledJobs, descScheduledJob{
			Name: j.Name, Title: j.Title, Schedule: j.Schedule, Processor: j.Processor,
			Params: j.Params, Enabled: j.Enabled, OnError: j.OnError, Timeout: j.Timeout,
			Source: src.yaml("scheduled", j.Name),
		})
	}
	out.HomePage = proj.HomePage != nil

	// Модули: общие модули + модули объектов/обработок/сервисов/страниц.
	for name, prog := range proj.Modules {
		out.Modules = append(out.Modules, descModule{Name: name, Kind: "module", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.Programs {
		out.Modules = append(out.Modules, descModule{Name: name, Kind: "object", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.ManagerPrograms {
		out.Modules = append(out.Modules, descModule{Name: name, Kind: "manager", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.ServicePrograms {
		out.Modules = append(out.Modules, descModule{Name: name, Kind: "service", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
	}
	for name, prog := range proj.PagePrograms {
		out.Modules = append(out.Modules, descModule{Name: name, Kind: "page", Procedures: procDescs(prog, src), Source: moduleSource(prog, src)})
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

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func toDescFields(fields []metadata.Field) []descField {
	out := make([]descField, 0, len(fields))
	for _, f := range fields {
		out = append(out, descField{Name: f.Name, Type: string(f.Type), Ref: f.RefEntity, Enum: f.EnumName})
	}
	return out
}

func toDescForms(forms []*metadata.FormModule) []descForm {
	out := make([]descForm, 0, len(forms))
	for _, f := range forms {
		if f == nil {
			continue
		}
		df := descForm{
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

func toDescPermission(p auth.Permission) descPermission {
	return descPermission{
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

func procDescs(prog *ast.Program, src sourceLookup) []descProcedure {
	if prog == nil {
		return nil
	}
	out := make([]descProcedure, 0, len(prog.Procedures))
	for _, p := range prog.Procedures {
		dp := descProcedure{Name: p.Name.Literal, Export: p.Export}
		for _, par := range p.Params {
			dp.Params = append(dp.Params, par.Literal)
		}
		dp.Source = src.source(p.Name.File, p.Name.Line)
		out = append(out, dp)
	}
	return out
}

func moduleSource(prog *ast.Program, src sourceLookup) *descSource {
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

func (s sourceLookup) yaml(subdir, name string) *descSource {
	file := s.lookupYAML(subdir, name)
	if file == "" {
		return nil
	}
	return &descSource{File: file, Line: 1}
}

func (s sourceLookup) source(file string, line int) *descSource {
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
	return &descSource{File: filepath.ToSlash(rel), Line: line}
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
