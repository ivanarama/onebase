package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/converter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/version"
	"gopkg.in/yaml.v3"
)

func cfgUpsert(ctx context.Context, db *storage.DB, path string, content []byte) error {
	return configdb.New(db).SaveFile(ctx, path, content)
}

// ── YAML save structs ────────────────────────────────────────────────────────

type saveField struct {
	Name   string            `yaml:"name"`
	Title  string            `yaml:"title,omitempty"`
	Titles map[string]string `yaml:"titles,omitempty"`
	Type   string            `yaml:"type"`
	// AllowInlineCreate — pointer-bool, чтобы omitempty отличал «явно false»
	// от «не задано». nil → дефолт контекста (true в шапке, false в ТЧ).
	AllowInlineCreate *bool `yaml:"allow_inline_create,omitempty"`
}

type saveTP struct {
	Name   string            `yaml:"name"`
	Title  string            `yaml:"title,omitempty"`
	Titles map[string]string `yaml:"titles,omitempty"`
	Fields []saveField       `yaml:"fields"`
}

// saveNumerator зеркалирует metadata.rawNumerator, чтобы при roundtrip
// (Unmarshal через saveEntity → Marshal обратно) не терялась авто-нумерация
// документов после редактирования полей в UI конфигуратора.
type saveNumerator struct {
	Prefix string `yaml:"prefix,omitempty"`
	Length int    `yaml:"length,omitempty"`
	Period string `yaml:"period,omitempty"`
	Scope  string `yaml:"scope,omitempty"`
}

// savePredefined — предопределённые элементы справочника. inline map нужен,
// чтобы поля произвольных типов не терялись (yaml v3 не сохранит interface{}
// через явный тип map, но сохранит через any).
type savePredefined struct {
	Name   string         `yaml:"name"`
	Fields map[string]any `yaml:"fields,omitempty"`
}

// saveEntity отражает все верхнеуровневые ключи rawEntity (см.
// metadata/yaml.go). Раньше структура содержала только Name/Posting/Fields/
// TableParts — поэтому редактирование полей справочника через UI ВЫТИРАЛО
// hierarchical, numerator, predefined и др. Бага: «куда-то исчезли кнопки
// Список/Дерево, Группа» после добавления поля «Поставщик» в Номенклатуру
// (2026-05-25). Теперь поля сохраняются полностью.
type saveEntity struct {
	Name          string            `yaml:"name"`
	Title         string            `yaml:"title,omitempty"`
	Titles        map[string]string `yaml:"titles,omitempty"`
	Hierarchical  bool              `yaml:"hierarchical,omitempty"`
	HierarchyKind string            `yaml:"hierarchy_kind,omitempty"`
	Posting       bool              `yaml:"posting,omitempty"`
	BasedOn       []string          `yaml:"based_on,omitempty"`
	Numerator     *saveNumerator    `yaml:"numerator,omitempty"`
	Predefined    []savePredefined  `yaml:"predefined,omitempty"`
	ListForm      []string          `yaml:"list_form,omitempty"`
	ItemForm      []string          `yaml:"item_form,omitempty"`
	Fields        []saveField       `yaml:"fields"`
	TableParts    []saveTP          `yaml:"tableparts,omitempty"`
}

type saveRegister struct {
	Name       string      `yaml:"name"`
	Dimensions []saveField `yaml:"dimensions,omitempty"`
	Resources  []saveField `yaml:"resources,omitempty"`
	Attributes []saveField `yaml:"attributes,omitempty"`
}

type saveInfoReg struct {
	Name       string      `yaml:"name"`
	Periodic   bool        `yaml:"periodic,omitempty"`
	Dimensions []saveField `yaml:"dimensions,omitempty"`
	Resources  []saveField `yaml:"resources,omitempty"`
}

type saveAccountReg struct {
	Name      string      `yaml:"name"`
	Title     string      `yaml:"title,omitempty"`
	Accounts  string      `yaml:"accounts,omitempty"`
	Resources []saveField `yaml:"resources,omitempty"`
}

// ── view types ────────────────────────────────────────────────────────────────

type cfgField struct {
	Name              string
	Type              string
	RefEntity         string
	EnumName          string
	Length            int // разрядность number(L,P): всего знаков (Длина)
	Scale             int // знаков после запятой (Точность)
	FormListHidden    bool
	FormItemHidden    bool
	AllowInlineCreate *bool // nil = дефолт контекста (true в шапке, false в ТЧ)
}

// InlineAllowChecked — состояние чекбокса «Кнопка + в picker'е» с учётом
// дефолта контекста. Шаблон редактора полей вызывает с inTablePart=true
// для строк ТЧ, false для шапки.
func (f cfgField) InlineAllowChecked(inTablePart bool) bool {
	if f.AllowInlineCreate != nil {
		return *f.AllowInlineCreate
	}
	return !inTablePart
}

type cfgEnum struct {
	Name   string
	Values []string
}

type cfgConstant struct {
	Name      string
	Type      string
	RefEntity string
	Default   string
	Label     string
}

type cfgTablePart struct {
	Name   string
	Fields []cfgField
}

type cfgEntity struct {
	Name             string
	Kind             string // "Справочник" / "Документ"
	Posting          bool
	Hierarchical     bool
	BasedOn          []string // источники для ввода на основании (Plan 38)
	Receivers        []string // обратный список: куда вводится на основании текущего объекта
	Fields           []cfgField
	TableParts       []cfgTablePart
	Source           string // raw .os content (object module)
	PostingSource    string // raw .posting.os content (ОбработкаПроведения)
	ManagerSource    string // raw .manager.os content (модуль менеджера)
	LinkedPrintForms []cfgPrintForm
	Predefined       []cfgPredefined
}

type cfgRegister struct {
	Name       string
	Dimensions []cfgField
	Resources  []cfgField
	Attributes []cfgField
}

type cfgParam struct {
	Name  string
	Type  string
	Label string
}

type cfgReport struct {
	Name        string
	Title       string
	Query       string
	ChartProc   string
	ChartSource string
	Params      []cfgParam
}

// cfgWidget is the configurator-side projection of a dashboard widget. We keep
// the raw YAML alongside parsed fields so the editor can show "источник YAML"
// straight away without re-marshalling.
type cfgWidget struct {
	Name  string
	Type  string
	Title string
	YAML  string
}

type cfgModule struct {
	Name   string
	Source string
}

type cfgProcessor struct {
	Name   string
	Title  string
	Source string
	Params []cfgParam
}

type cfgPrintForm struct {
	Name     string
	Document string
	Source   string
	FileName string
	// Shadowed — для этого entity+name есть одноимённая .os-форма.
	// Runtime в onebase run использует .os-вариант, YAML игнорируется
	// (см. UI конфигуратора показывает оба, но помечает
	// YAML значком ⚠️ — чтобы автор конфигурации не удивлялся, что
	// его правки в YAML «не работают».
	Shadowed bool
}

type cfgDSLPrintForm struct {
	Name          string
	Document      string
	Source        string
	FileName      string
	HasLayout     bool
	LayoutYAML    string
	LayoutPreview template.HTML
	// Overrides — есть одноимённая YAML-форма у того же entity,
	// которую этот .os перебивает.
	Overrides bool
}

type cfgInfoRegister struct {
	Name       string
	Periodic   bool
	Dimensions []cfgField
	Resources  []cfgField
}

type cfgAccountRegister struct {
	Name      string
	Title     string
	Accounts  string
	Resources []cfgField
}

type cfgPredefined struct {
	Name   string
	Fields map[string]string // field name → display value
}

type cfgSubsystem struct {
	Name     string
	Title    string
	Icon     string
	Order    int
	Contents metadata.SubsystemContents
	// HomeWidgets — плоский список отмеченных виджетов (для режима «Авто»).
	// HomeRows — раскладка по рядам (для drag-конструктора «По рядам»).
	// HomeLayout — режим: "auto" (поток по ширине) или "rows" (соблюдение рядов).
	HomeWidgets []string
	HomeRows    [][]string
	HomeLayout  string
}

// widgetOption — лёгкая проекция виджета (имя+заголовок) для JS drag-конструктора.
type widgetOption struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// cfgHomePage — проекция глобальной главной страницы (config/home_page.yaml)
// для визуального редактора конфигуратора (галочки виджетов + раскладка).
type cfgHomePage struct {
	Title   string
	Widgets []string   // отмеченные виджеты (режим «Авто»)
	Rows    [][]string // раскладка по рядам (режим «По рядам»)
	Layout  string     // "auto" | "rows"
}

type configuratorData struct {
	Base             *Base
	AppName          string
	AppVersion       string
	AppLogo          string
	AppLang          string
	AvailableLangs   []i18n.Lang
	DSNMasked        string
	Tab              string // "tree" | "convert" | "files"
	Entities         []cfgEntity
	Catalogs         []cfgEntity
	Docs             []cfgEntity
	Registers        []cfgRegister
	InfoRegisters    []cfgInfoRegister
	AccountRegisters []cfgAccountRegister
	Enums            []cfgEnum
	Constants        []cfgConstant
	Reports          []cfgReport
	Modules          []cfgModule
	Processors       []cfgProcessor
	PrintForms       []cfgPrintForm
	DSLPrintForms    []cfgDSLPrintForm
	// План 37, этап 4: управляемые формы.
	ManagedForms []cfgManagedForm
	EditingForm  *cfgManagedForm
	Subsystems   []cfgSubsystem
	Widgets      []cfgWidget
	// GroupOrder — пользовательский порядок групп дерева (ключи data-group/data-gid)
	// для клиентской перестановки; пусто — порядок по умолчанию из шаблона.
	GroupOrder    []string
	WidgetOptions []widgetOption // имя+заголовок всех виджетов для drag-конструктора
	HomePageYAML  string         // verbatim config/home_page.yaml — для «Расширенно (YAML)»
	GlobalHome    cfgHomePage
	Error         string
	// all entity names for reference picker
	AllEntityNames []string
	// all enum names for enum-type field picker
	AllEnumNames []string
	// query builder schema (JSON for inline query builder)
	QBSchema template.JS
	// converter
	ConvertSrcDir  string
	ConvertResult  string
	ConvertApplied bool
	// module save
	ModuleSaved       bool
	ModuleSavedEntity string
	// fields save
	FieldsSaved       bool
	FieldsSavedEntity string
	// exact tree item to select after save (overrides prefix-search for FieldsSavedEntity)
	SelectedTreeID string
	// platform version
	PlatformVer    string
	UIServerURL    string
	BackupMessage  string
	BackupDir      string
	BackupFiles    []backupFile
	BackupSettings backupSettings
	// session token for passing to UI server (bootstrap auth)
	SessionToken string
	InlineJSYaml template.JS
	// IsRunning: процесс базы запущен сейчас
	IsRunning bool
	// ConfigDirty: на диске есть изменения конфигурации новее, чем запуск базы
	// — пользователю нужно перезапустить базу, чтобы применить.
	ConfigDirty bool
	Lang        string
}

type backupFile struct {
	Name string
	Size string
	Date string
}

type backupSettings struct {
	Enabled   bool
	Schedule  string
	KeepLast  int
	Directory string
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (h *handler) configuratorPage(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tab := r.URL.Query().Get("tab")
	if tab == "" {
		tab = "tree"
	}
	data := h.loadCfgData(r.Context(), b, tab)
	if cookie, cerr := r.Cookie("onebase_session"); cerr == nil {
		data.SessionToken = cookie.Value
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorConvert(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	srcDir := strings.TrimSpace(r.FormValue("src_dir"))
	apply := r.FormValue("apply") == "1"

	data := h.loadCfgData(r.Context(), b, "convert")
	data.ConvertSrcDir = srcDir

	if srcDir == "" {
		data.Error = tr(lang, "Укажите путь к папке XML-выгрузки конфигурации")
		renderCfg(w, r, data)
		return
	}

	outDir, err := workspacePath(b.ID + "-convert")
	if err != nil {
		data.Error = err.Error()
		renderCfg(w, r, data)
		return
	}
	// clean previous conversion
	os.RemoveAll(outDir)

	rep, err := converter.Convert(converter.Options{SourceDir: srcDir, OutDir: outDir})
	if err != nil {
		data.Error = tr(lang, "Ошибка конвертации") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	data.ConvertResult = rep.String()

	if apply {
		if b.ConfigSource == "database" {
			db, cerr := OpenDB(r.Context(), b)
			if cerr != nil {
				data.Error = tr(lang, "Ошибка подключения к БД") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
			defer db.Close()
			repo := configdb.New(db)
			repo.EnsureSchema(r.Context())
			if cerr := repo.ImportFromDir(r.Context(), outDir); cerr != nil {
				data.Error = tr(lang, "Ошибка импорта") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
		} else {
			// file mode — copy files into base path
			if cerr := copyDir(outDir, b.Path); cerr != nil {
				data.Error = tr(lang, "Ошибка копирования") + ": " + cerr.Error()
				renderCfg(w, r, data)
				return
			}
		}
		data.ConvertApplied = true
		// reload tree with new data
		fresh := h.loadCfgData(r.Context(), b, "convert")
		fresh.ConvertSrcDir = srcDir
		fresh.ConvertResult = data.ConvertResult
		fresh.ConvertApplied = true
		data = fresh
	}

	renderCfg(w, r, data)
}

// ── data loading ──────────────────────────────────────────────────────────────

// configDirtyAfter возвращает true, если в rootDir есть .os/.yaml/.yml файл
// с mtime новее threshold. Используется для отображения «звёздочки» в дереве
// метаданных — конфигурация на диске изменилась с момента запуска базы.
func configDirtyAfter(rootDir string, threshold time.Time) bool {
	dirty := false
	filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == "backups" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".os" && ext != ".yaml" && ext != ".yml" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.ModTime().After(threshold) {
			dirty = true
			return filepath.SkipAll
		}
		return nil
	})
	return dirty
}

func (h *handler) loadCfgData(ctx context.Context, b *Base, tab string, lang ...string) *configuratorData {
	l := "ru"
	if len(lang) > 0 {
		l = lang[0]
	}
	data := &configuratorData{Base: b, Tab: tab, PlatformVer: version.String(), UIServerURL: fmt.Sprintf("http://localhost:%d", b.Port), DSNMasked: maskDSN(b.DB), InlineJSYaml: InlineJSYaml}

	if startedAt, ok := h.runner.StartedAt(b.ID); ok {
		data.IsRunning = true
		if b.ConfigSource == "file" {
			// Звёздочка означает «БД/сессия отстала от метаданных»: сравниваем с
			// моментом последней миграции, а при её отсутствии — с моментом запуска.
			threshold := startedAt
			if t, ok := migratedAt(b.ID); ok && t.After(threshold) {
				threshold = t
			}
			data.ConfigDirty = configDirtyAfter(b.Path, threshold)
		}
	}

	var proj *project.Project
	var err error

	if b.ConfigSource == "database" {
		db, cerr := OpenDB(ctx, b)
		if cerr != nil {
			data.Error = tr(l, "Нет подключения к БД") + ": " + cerr.Error()
			return data
		}
		defer db.Close()
		repo := configdb.New(db)
		if cerr := repo.EnsureSchema(ctx); cerr != nil {
			data.Error = cerr.Error()
			return data
		}
		empty, _ := repo.IsEmpty(ctx)
		if empty {
			data.Error = tr(l, "Конфигурация не загружена в базу данных. Воспользуйтесь вкладкой «Файлы».")
			return data
		}
		proj, err = project.LoadFromDB(ctx, repo)
	} else {
		proj, err = project.Load(b.Path)
	}

	if err != nil {
		data.Error = tr(l, "Ошибка загрузки конфигурации") + ": " + err.Error()
		return data
	}
	defer proj.Close()

	if appCfg, _ := project.LoadConfig(proj.Dir); appCfg != nil {
		data.AppName = appCfg.Name
		data.AppVersion = appCfg.Version
		data.AppLogo = appCfg.Logo
		data.AppLang = appCfg.Lang
	}
	if launcherBundle != nil {
		data.AvailableLangs = launcherBundle.Available()
	}

	sources, postingSources, managerSources := readOSSources(proj.Dir)

	for _, e := range proj.Entities {
		ev := cfgEntity{
			Name:          e.Name,
			Posting:       e.Posting,
			Hierarchical:  e.Hierarchical,
			BasedOn:       append([]string(nil), e.BasedOn...),
			Source:        sources[strings.ToLower(e.Name)],
			PostingSource: postingSources[strings.ToLower(e.Name)],
			ManagerSource: managerSources[strings.ToLower(e.Name)],
		}
		if e.Kind == metadata.KindCatalog {
			ev.Kind = "Справочник"
		} else {
			ev.Kind = "Документ"
		}
		for _, f := range e.Fields {
			ev.Fields = append(ev.Fields, toCfgField(f))
		}
		for _, tp := range e.TableParts {
			tpv := cfgTablePart{Name: tp.Name}
			for _, f := range tp.Fields {
				tpv.Fields = append(tpv.Fields, toCfgField(f))
			}
			ev.TableParts = append(ev.TableParts, tpv)
		}
		for _, pd := range e.Predefined {
			pv := cfgPredefined{Name: pd.Name, Fields: make(map[string]string, len(pd.Fields))}
			for k, v := range pd.Fields {
				pv.Fields[k] = fmt.Sprintf("%v", v)
			}
			ev.Predefined = append(ev.Predefined, pv)
		}
		// Mark fields hidden based on form config
		if len(e.ListForm) > 0 {
			lfSet := make(map[string]bool, len(e.ListForm))
			for _, n := range e.ListForm {
				lfSet[n] = true
			}
			for i := range ev.Fields {
				if !lfSet[ev.Fields[i].Name] {
					ev.Fields[i].FormListHidden = true
				}
			}
		}
		if len(e.ItemForm) > 0 {
			ifSet := make(map[string]bool, len(e.ItemForm))
			for _, n := range e.ItemForm {
				ifSet[n] = true
			}
			for i := range ev.Fields {
				if !ifSet[ev.Fields[i].Name] {
					ev.Fields[i].FormItemHidden = true
				}
			}
		}
		data.Entities = append(data.Entities, ev)
	}

	sort.Slice(data.Entities, func(i, j int) bool {
		return data.Entities[i].Name < data.Entities[j].Name
	})

	// Обратный индекс: какие документы/справочники вводятся НА ОСНОВАНИИ
	// текущего объекта. Показывается на форме источника как read-only —
	// аналог одноимённой вкладки в 1С:Конфигураторе.
	for i := range data.Entities {
		src := data.Entities[i].Name
		for j := range data.Entities {
			for _, b := range data.Entities[j].BasedOn {
				if strings.EqualFold(b, src) {
					data.Entities[i].Receivers = append(data.Entities[i].Receivers, data.Entities[j].Name)
					break
				}
			}
		}
	}

	// load print forms and link to entities
	pfSources := readPrintFormSources(proj.Dir)

	// индекс «(doc,name)→true» по .os формам, чтобы пометить
	// YAML-формы, которые runtime тихо игнорирует.
	dslIndex := make(map[string]bool, len(proj.DSLPrintForms))
	for _, df := range proj.DSLPrintForms {
		dslIndex[strings.ToLower(df.Document)+"|"+strings.ToLower(df.Name)] = true
	}
	// И обратный индекс для пометки .os-форм, которые перебивают YAML.
	yamlIndex := make(map[string]bool, len(proj.PrintForms))
	for _, pf := range proj.PrintForms {
		yamlIndex[strings.ToLower(pf.Document)+"|"+strings.ToLower(pf.Name)] = true
	}

	pfByDoc := make(map[string][]cfgPrintForm)
	for _, pf := range proj.PrintForms {
		entry := pfSources[pf.Name]
		cpf := cfgPrintForm{
			Name:     pf.Name,
			Document: pf.Document,
			Source:   entry.source,
			FileName: entry.filename,
			Shadowed: dslIndex[strings.ToLower(pf.Document)+"|"+strings.ToLower(pf.Name)],
		}
		data.PrintForms = append(data.PrintForms, cpf)
		pfByDoc[strings.ToLower(pf.Document)] = append(pfByDoc[strings.ToLower(pf.Document)], cpf)
	}

	// load DSL print forms (.os)
	for _, df := range proj.DSLPrintForms {
		cpf := cfgDSLPrintForm{
			Name:      df.Name,
			Document:  df.Document,
			Source:    df.Source,
			FileName:  df.Name + ".os",
			Overrides: yamlIndex[strings.ToLower(df.Document)+"|"+strings.ToLower(df.Name)],
		}
		if df.Layout != nil {
			cpf.HasLayout = true
			cpf.LayoutPreview = template.HTML(df.Layout.PreviewHTML())
			if df.LayoutPath != "" {
				if raw, err := os.ReadFile(df.LayoutPath); err == nil {
					cpf.LayoutYAML = string(raw)
				}
			}
		}
		data.DSLPrintForms = append(data.DSLPrintForms, cpf)
	}
	for _, e := range data.Entities {
		data.AllEntityNames = append(data.AllEntityNames, e.Name)
		e.LinkedPrintForms = pfByDoc[strings.ToLower(e.Name)]
		if e.Kind == "Справочник" {
			data.Catalogs = append(data.Catalogs, e)
		} else {
			data.Docs = append(data.Docs, e)
		}
	}

	for _, reg := range proj.Registers {
		rv := cfgRegister{Name: reg.Name}
		for _, f := range reg.Dimensions {
			rv.Dimensions = append(rv.Dimensions, toCfgField(f))
		}
		for _, f := range reg.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		for _, f := range reg.Attributes {
			rv.Attributes = append(rv.Attributes, toCfgField(f))
		}
		data.Registers = append(data.Registers, rv)
	}

	for _, ir := range proj.InfoRegisters {
		rv := cfgInfoRegister{Name: ir.Name, Periodic: ir.Periodic}
		for _, f := range ir.Dimensions {
			rv.Dimensions = append(rv.Dimensions, toCfgField(f))
		}
		for _, f := range ir.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		data.InfoRegisters = append(data.InfoRegisters, rv)
	}

	for _, ar := range proj.AccountRegisters {
		rv := cfgAccountRegister{Name: ar.Name, Title: ar.Title, Accounts: ar.Accounts}
		for _, f := range ar.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		data.AccountRegisters = append(data.AccountRegisters, rv)
	}

	for _, en := range proj.Enums {
		data.Enums = append(data.Enums, cfgEnum{Name: en.Name, Values: en.Values})
		data.AllEnumNames = append(data.AllEnumNames, en.Name)
	}

	for _, c := range proj.Constants {
		typ := string(c.Type)
		if c.RefEntity != "" {
			typ = "reference"
		}
		data.Constants = append(data.Constants, cfgConstant{
			Name:      c.Name,
			Type:      typ,
			RefEntity: c.RefEntity,
			Default:   c.Default,
			Label:     c.Label,
		})
	}

	repSources := readReportSources(proj.Dir)

	for _, rep := range proj.Reports {
		rv := cfgReport{Name: rep.Name, Title: rep.Title, Query: rep.Query, ChartProc: rep.ChartProc}
		if src, ok := repSources[strings.ToLower(rep.Name)]; ok {
			rv.ChartSource = src
		}
		for _, p := range rep.Params {
			rv.Params = append(rv.Params, cfgParam{Name: p.Name, Type: p.Type, Label: p.Label})
		}
		data.Reports = append(data.Reports, rv)
	}

	moduleSources, procSources := readModuleAndProcSources(proj.Dir)

	for name := range proj.Modules {
		data.Modules = append(data.Modules, cfgModule{
			Name:   name,
			Source: moduleSources[strings.ToLower(name)],
		})
	}
	sort.Slice(data.Modules, func(i, j int) bool { return data.Modules[i].Name < data.Modules[j].Name })

	for _, proc := range proj.Processors {
		rv := cfgProcessor{
			Name:   proc.Name,
			Title:  proc.Title,
			Source: procSources[strings.ToLower(proc.Name)],
		}
		for _, p := range proc.Params {
			rv.Params = append(rv.Params, cfgParam{Name: p.Name, Type: p.Type, Label: p.Label})
		}
		data.Processors = append(data.Processors, rv)
	}
	sort.Slice(data.Processors, func(i, j int) bool { return data.Processors[i].Name < data.Processors[j].Name })

	// Subsystems
	for _, sub := range proj.Subsystems {
		var rows [][]string
		if sub.HomePage != nil {
			rows = sub.HomePage.RowGroups()
		}
		data.Subsystems = append(data.Subsystems, cfgSubsystem{
			Name:        sub.Name,
			Title:       sub.Title,
			Icon:        sub.Icon,
			Order:       sub.Order,
			Contents:    sub.Contents,
			HomeWidgets: homeWidgetsNames(sub.HomePage),
			HomeRows:    rows,
			HomeLayout:  homeLayoutMode(sub.HomePage),
		})
	}

	// Widgets + home page
	widgetSources := readWidgetSources(proj.Dir)
	for _, wMeta := range proj.Widgets {
		yamlText, ok := widgetSources[wMeta.Name]
		if !ok {
			// Fallback: re-marshal from parsed metadata so the editor still
			// shows something even when the source file was lost.
			b, _ := yaml.Marshal(wMeta)
			yamlText = string(b)
		}
		data.Widgets = append(data.Widgets, cfgWidget{
			Name:  wMeta.Name,
			Type:  string(wMeta.Type),
			Title: wMeta.Title,
			YAML:  yamlText,
		})
	}
	sort.Slice(data.Widgets, func(i, j int) bool { return data.Widgets[i].Name < data.Widgets[j].Name })
	for _, wc := range data.Widgets {
		data.WidgetOptions = append(data.WidgetOptions, widgetOption{Name: wc.Name, Title: wc.Title})
	}
	data.HomePageYAML = readHomePageYAML(proj.Dir)

	// Глобальная главная для визуального редактора (галочки / drag-конструктор).
	ghTitle := ""
	var ghRows [][]string
	if proj.HomePage != nil {
		ghRows = proj.HomePage.RowGroups()
		if proj.HomePage.Title != "" && proj.HomePage.Title != "Главная" {
			ghTitle = proj.HomePage.Title
		}
	}
	data.GlobalHome = cfgHomePage{
		Title:   ghTitle,
		Widgets: homeWidgetsNames(proj.HomePage),
		Rows:    ghRows,
		Layout:  homeLayoutMode(proj.HomePage),
	}

	// Generate query builder schema
	data.QBSchema = buildQBSchema(data)

	// Backup dir & files
	data.BackupDir = h.backupDir(b)
	backupDir := data.BackupDir
	if files, err := filepath.Glob(filepath.Join(backupDir, "backup_*.sql.gz")); err == nil {
		for _, f := range files {
			info, _ := os.Stat(f)
			if info == nil {
				continue
			}
			data.BackupFiles = append(data.BackupFiles, backupFile{
				Name: filepath.Base(f),
				Size: fmt.Sprintf("%.1f KB", float64(info.Size())/1024),
				Date: info.ModTime().Format("2006-01-02 15:04"),
			})
		}
		for i, j := 0, len(data.BackupFiles)-1; i < j; i, j = i+1, j-1 {
			data.BackupFiles[i], data.BackupFiles[j] = data.BackupFiles[j], data.BackupFiles[i]
		}
	}

	// Backup settings from app.yaml
	{
		cfgPath := ""
		if b.ConfigSource == "database" {
			// read from DB config
		} else {
			cfgPath = filepath.Join(b.Path, "config", "app.yaml")
		}
		if cfgPath != "" {
			if raw, err := os.ReadFile(cfgPath); err == nil {
				var tmp struct {
					Backup struct {
						Enabled   bool   `yaml:"enabled"`
						Schedule  string `yaml:"schedule"`
						KeepLast  int    `yaml:"keep_last"`
						Directory string `yaml:"directory"`
					} `yaml:"backup"`
				}
				yaml.Unmarshal(raw, &tmp)
				data.BackupSettings = backupSettings{
					Enabled:   tmp.Backup.Enabled,
					Schedule:  tmp.Backup.Schedule,
					KeepLast:  tmp.Backup.KeepLast,
					Directory: tmp.Backup.Directory,
				}
			}
		}
	}

	// Управляемые формы (план 37, этап 4): подгружаем для бокового дерева.
	// listManagedForms требует http.Request только для DB-режима; в file-mode
	// nil-Request не используется. В случае ошибки оставляем срез пустым —
	// дерево просто не покажет раздел.
	if forms, err := listManagedFormsFromFS(b); err == nil {
		data.ManagedForms = forms
	}
	if b.ConfigSource == "database" {
		if forms, err := h.listManagedFormsFromDBNoRequest(ctx, b); err == nil {
			data.ManagedForms = forms
		}
	}

	// Пользовательский порядок объектов и групп в дереве (ручное перемещение,
	// как в 1С) — одинаково для file- и database-режимов.
	applyTreeOrder(data, h.loadTreeOrderFor(ctx, b))

	return data
}

// listManagedFormsFromDBNoRequest — версия для loadCfgData, где нет
// http.Request. Использует ctx напрямую вместо r.Context().
func (h *handler) listManagedFormsFromDBNoRequest(ctx context.Context, b *Base) ([]cfgManagedForm, error) {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	repo := configdb.New(db)
	files, err := repo.ListByPrefix(ctx, "forms/")
	if err != nil {
		return nil, err
	}
	type pair struct{ yaml, os string }
	groups := map[string]*cfgManagedForm{}
	for _, f := range files {
		parts := strings.Split(strings.TrimPrefix(f.Path, "forms/"), "/")
		if len(parts) < 2 {
			continue
		}
		entityLower := parts[0]
		base := parts[1]
		var name string
		var isYAML, isOS bool
		if strings.HasSuffix(base, ".form.yaml") {
			name = strings.TrimSuffix(base, ".form.yaml")
			isYAML = true
		} else if strings.HasSuffix(base, ".form.os") {
			name = strings.TrimSuffix(base, ".form.os")
			isOS = true
		} else {
			continue
		}
		key := entityLower + "/" + name
		g, ok := groups[key]
		if !ok {
			g = &cfgManagedForm{
				Entity:   entityLower,
				Name:     name,
				YAMLPath: "forms/" + entityLower + "/" + name + ".form.yaml",
				OSPath:   "forms/" + entityLower + "/" + name + ".form.os",
			}
			groups[key] = g
		}
		if isYAML {
			g.YAML = string(f.Content)
			g.Kind = extractFormKindFromYAML(g.YAML)
		}
		if isOS {
			g.OS = string(f.Content)
			g.HasOS = true
		}
	}
	out := make([]cfgManagedForm, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	return out, nil
}

// ── query builder schema ────────────────────────────────────────────────────

type cfgQBField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type cfgQBSource struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Group   string       `json:"group"`
	VTParam string       `json:"vtParam,omitempty"`
	Fields  []cfgQBField `json:"fields"`
}

func cfgQBFieldType(t string) string {
	switch t {
	case "number":
		return "number"
	case "date":
		return "date"
	case "bool", "boolean":
		return "bool"
	case "reference":
		return "ref"
	default:
		return "string"
	}
}

func buildQBSchema(d *configuratorData) template.JS {
	var sources []cfgQBSource

	for _, e := range d.Catalogs {
		src := cfgQBSource{ID: "catalog:" + e.Name, Label: "Справочник." + e.Name, Group: "Справочники"}
		for _, f := range e.Fields {
			src.Fields = append(src.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, src)
	}

	for _, e := range d.Docs {
		src := cfgQBSource{ID: "document:" + e.Name, Label: "Документ." + e.Name, Group: "Документы"}
		for _, f := range e.Fields {
			src.Fields = append(src.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, src)
	}

	for _, reg := range d.Registers {
		raw := cfgQBSource{ID: "register:" + reg.Name, Label: "РегистрНакопления." + reg.Name, Group: "Регистры накопления"}
		raw.Fields = append(raw.Fields, cfgQBField{Name: "период", Label: "Период", Type: "date"})
		raw.Fields = append(raw.Fields, cfgQBField{Name: "вид_движения", Label: "ВидДвижения", Type: "string"})
		for _, f := range reg.Dimensions {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range reg.Resources {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, raw)

		bal := cfgQBSource{ID: "vt_balances:" + reg.Name, Label: "РегистрНакопления." + reg.Name + ".Остатки(&НаДату)", Group: "Виртуальные таблицы", VTParam: "&НаДату"}
		for _, f := range reg.Dimensions {
			bal.Fields = append(bal.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range reg.Resources {
			bal.Fields = append(bal.Fields, cfgQBField{Name: f.Name + "Остаток", Label: f.Name + "Остаток", Type: "res"})
		}
		sources = append(sources, bal)

		trn := cfgQBSource{ID: "vt_turnovers:" + reg.Name, Label: "РегистрНакопления." + reg.Name + ".Обороты(&Начало, &Конец)", Group: "Виртуальные таблицы", VTParam: "&Начало, &Конец"}
		for _, f := range reg.Dimensions {
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range reg.Resources {
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name + "Приход", Label: f.Name + "Приход", Type: "res"})
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name + "Расход", Label: f.Name + "Расход", Type: "res"})
			trn.Fields = append(trn.Fields, cfgQBField{Name: f.Name + "Оборот", Label: f.Name + "Оборот", Type: "res"})
		}
		sources = append(sources, trn)
	}

	for _, ir := range d.InfoRegisters {
		raw := cfgQBSource{ID: "inforeg:" + ir.Name, Label: "РегистрСведений." + ir.Name, Group: "Регистры сведений"}
		if ir.Periodic {
			raw.Fields = append(raw.Fields, cfgQBField{Name: "period", Label: "Период", Type: "date"})
		}
		for _, f := range ir.Dimensions {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		for _, f := range ir.Resources {
			raw.Fields = append(raw.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
		}
		sources = append(sources, raw)

		if ir.Periodic {
			sl := cfgQBSource{ID: "vt_slice:" + ir.Name, Label: "РегистрСведений." + ir.Name + ".СрезПоследних(&НаДату)", Group: "Виртуальные таблицы", VTParam: "&НаДату"}
			for _, f := range ir.Dimensions {
				sl.Fields = append(sl.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
			}
			for _, f := range ir.Resources {
				sl.Fields = append(sl.Fields, cfgQBField{Name: f.Name, Label: f.Name, Type: cfgQBFieldType(f.Type)})
			}
			sources = append(sources, sl)
		}
	}

	if sources == nil {
		sources = []cfgQBSource{}
	}
	b, _ := json.Marshal(sources)
	return template.JS(b)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toCfgField(f metadata.Field) cfgField {
	typ := string(f.Type)
	if f.RefEntity != "" {
		typ = "reference"
	} else if f.EnumName != "" {
		typ = "enum"
	}
	return cfgField{Name: f.Name, Type: typ, RefEntity: f.RefEntity, EnumName: f.EnumName, Length: f.Length, Scale: f.Scale, AllowInlineCreate: f.AllowInlineCreate}
}

func readOSSources(dir string) (sources, postingSources, managerSources map[string]string) {
	sources = make(map[string]string)
	postingSources = make(map[string]string)
	managerSources = make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".posting.os"):
			base := strings.ToLower(strings.TrimSuffix(name, ".posting.os"))
			postingSources[base] = string(raw)
		case strings.HasSuffix(name, ".manager.os"):
			base := strings.ToLower(strings.TrimSuffix(name, ".manager.os"))
			managerSources[base] = string(raw)
		case strings.HasSuffix(name, ".module.os"), strings.HasSuffix(name, ".proc.os"), strings.HasSuffix(name, ".rep.os"):
			// skip — handled by readModuleAndProcSources / report editor
		default:
			base := strings.ToLower(strings.TrimSuffix(name, ".os"))
			sources[base] = string(raw)
		}
	}
	return
}

func readReportSources(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rep.os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(e.Name(), ".rep.os"))
		result[base] = string(raw)
	}
	return result
}

// readWidgetSources reads widgets/*.yaml from the exported project dir and maps
// raw YAML content by widget name (extracted from the "name:" key, falling back
// to the file's basename). This lets the configurator show the source text in
// the editor without re-marshalling parsed metadata.
func readWidgetSources(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "widgets"))
	if err != nil {
		return result
	}
	type nameOnly struct {
		Name string `yaml:"name"`
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "widgets", e.Name()))
		if err != nil {
			continue
		}
		var no nameOnly
		key := strings.TrimSuffix(e.Name(), ".yaml")
		if yaml.Unmarshal(raw, &no) == nil && no.Name != "" {
			key = no.Name
		}
		result[key] = string(raw)
	}
	return result
}

// loadProjectFor загружает конфигурацию базы из файлов или из БД, повторяя
// ветвление loadCfgData. Используется обработчиками сохранения, которым нужны
// уже распарсенные метаданные (например, существующая главная страница).
// Вызывающий обязан вызвать proj.Close().
func (h *handler) loadProjectFor(ctx context.Context, b *Base) (*project.Project, error) {
	if b.ConfigSource == "database" {
		db, err := OpenDB(ctx, b)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		repo := configdb.New(db)
		if err := repo.EnsureSchema(ctx); err != nil {
			return nil, err
		}
		return project.LoadFromDB(ctx, repo)
	}
	return project.Load(b.Path)
}

// readHomePageYAML reads config/home_page.yaml verbatim. Empty string when the
// file is missing — caller decides whether to show a placeholder.
func readHomePageYAML(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "config", "home_page.yaml"))
	if err != nil {
		return ""
	}
	return string(raw)
}

func readModuleAndProcSources(dir string) (moduleSources, procSources map[string]string) {
	moduleSources = make(map[string]string)
	procSources = make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".os") {
			continue
		}
		name := e.Name()
		raw, err := os.ReadFile(filepath.Join(dir, "src", name))
		if err != nil {
			continue
		}
		if strings.HasSuffix(name, ".module.os") {
			base := strings.ToLower(strings.TrimSuffix(name, ".module.os"))
			moduleSources[base] = string(raw)
		} else if strings.HasSuffix(name, ".proc.os") {
			base := strings.ToLower(strings.TrimSuffix(name, ".proc.os"))
			procSources[base] = string(raw)
		}
	}
	return
}

type pfSourceEntry struct {
	source   string
	filename string
}

func readPrintFormSources(dir string) map[string]pfSourceEntry {
	result := make(map[string]pfSourceEntry)
	entries, err := os.ReadDir(filepath.Join(dir, "printforms"))
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "printforms", e.Name()))
		if err != nil {
			continue
		}
		var hdr struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(raw, &hdr) == nil && hdr.Name != "" {
			result[hdr.Name] = pfSourceEntry{source: string(raw), filename: e.Name()}
		}
	}
	return result
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func (h *handler) configuratorSaveModule(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	entityName := r.FormValue("entity")
	moduleType := r.FormValue("module_type")
	source := r.FormValue("source")

	var filename string
	switch moduleType {
	case "posting":
		filename = entityToPostingFilename(entityName)
	case "manager":
		filename = entityToManagerFilename(entityName)
	default:
		filename = entityToFilename(entityName)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			saveErr = err
		} else {
			defer db.Close()
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, "src/"+filename, []byte(source))
		}
	} else {
		srcDir := filepath.Join(b.Path, "src")
		os.MkdirAll(srcDir, 0o755)
		saveErr = os.WriteFile(filepath.Join(srcDir, filename), []byte(source), 0o644)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.ModuleSaved = true
		data.ModuleSavedEntity = entityName
	}
	renderCfg(w, r, data)
}

// entityToFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.os"
func entityToFilename(name string) string {
	if name == "" {
		return ".os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".os"
}

// entityToPostingFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.posting.os"
func entityToPostingFilename(name string) string {
	if name == "" {
		return ".posting.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".posting.os"
}

// entityToManagerFilename converts "ПоступлениеТоваров" → "поступлениеТоваров.manager.os"
func entityToManagerFilename(name string) string {
	if name == "" {
		return ".manager.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".manager.os"
}

// ── field-type save ───────────────────────────────────────────────────────────

func findEntityFilePath(dir, entityName string) (string, error) {
	// Сканируем все папки с YAML-объектами, чтобы контекстное удаление работало
	// не только для справочников/документов, но и для регистров, перечислений,
	// отчётов и подсистем (раньше находились лишь catalogs/documents).
	for _, sub := range []string{"catalogs", "documents", "registers", "inforegisters", "accountregisters", "enums", "reports", "subsystems"} {
		items, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, sub, item.Name())
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var hdr struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(data, &hdr) == nil && hdr.Name == entityName {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("entity %q not found", entityName)
}

func applyFieldEdits(ent *saveEntity, fields []saveField, tpFields map[string][]saveField, posting *bool, hierarchical *bool, basedOn *[]string) {
	ent.Fields = fields
	for i, tp := range ent.TableParts {
		if f, ok := tpFields[tp.Name]; ok {
			ent.TableParts[i].Fields = f
		}
	}
	if posting != nil {
		ent.Posting = *posting
	}
	if hierarchical != nil {
		ent.Hierarchical = *hierarchical
		// При сбросе иерархии — стираем и hierarchy_kind, чтобы в YAML
		// не оставался «фантомный» ключ без эффекта.
		if !*hierarchical {
			ent.HierarchyKind = ""
		}
	}
	if basedOn != nil {
		// nil-slice → based_on удаляется из YAML (omitempty); пустой
		// явный slice трактуем так же.
		if len(*basedOn) == 0 {
			ent.BasedOn = nil
		} else {
			ent.BasedOn = append([]string(nil), (*basedOn)...)
		}
	}
}

func saveEntityFieldsToFile(dir, entityName string, fields []saveField, tpFields map[string][]saveField, posting *bool, hierarchical *bool, basedOn *[]string) error {
	filePath, err := findEntityFilePath(dir, entityName)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var ent saveEntity
	if err := yaml.Unmarshal(raw, &ent); err != nil {
		return err
	}
	applyFieldEdits(&ent, fields, tpFields, posting, hierarchical, basedOn)
	out, err := yaml.Marshal(&ent)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, out, 0o644)
}

func (h *handler) saveEntityFieldsToDB(ctx context.Context, b *Base, entityName string, fields []saveField, tpFields map[string][]saveField, posting *bool, hierarchical *bool, basedOn *[]string) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(ctx,
		`SELECT path, content FROM _onebase_config WHERE path LIKE 'catalogs/%.yaml' OR path LIKE 'documents/%.yaml'`)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var targetPath string
	var ent saveEntity
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var e saveEntity
		if yaml.Unmarshal(content, &e) == nil && e.Name == entityName {
			targetPath = p
			ent = e
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("entity %q not found in DB config", entityName)
	}

	applyFieldEdits(&ent, fields, tpFields, posting, hierarchical, basedOn)
	out, err := yaml.Marshal(&ent)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO _onebase_config (path, content, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
	`, targetPath, out)
	return err
}

func (h *handler) configuratorSaveForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	entityName := r.FormValue("entity")
	dir := b.Path
	if b.ConfigSource == "database" {
		dir, err = workspacePath(b.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	// Find entity YAML file
	entityDir := ""
	for _, sub := range []string{"catalogs", "documents"} {
		p := filepath.Join(dir, sub, nameToFilename(entityName)+".yaml")
		if _, e := os.Stat(p); e == nil {
			entityDir = sub
			break
		}
	}
	if entityDir == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Файл сущности не найден") + ": " + entityName
		renderCfg(w, r, data)
		return
	}

	filePath := filepath.Join(dir, entityDir, nameToFilename(entityName)+".yaml")
	raw, err := os.ReadFile(filePath)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка чтения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Parse as generic map, update list_form and item_form
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка YAML") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	// Build list_form from lf.N.name + lf.N.vis
	var listForm []string
	for i := 0; ; i++ {
		name := r.FormValue(fmt.Sprintf("lf.%d.name", i))
		if name == "" {
			break
		}
		vis := r.FormValue(fmt.Sprintf("lf.%d.vis", i))
		if vis == "1" {
			listForm = append(listForm, name)
		}
	}
	if len(listForm) > 0 {
		doc["list_form"] = listForm
	} else {
		delete(doc, "list_form")
	}

	// Build item_form from ef.N.name + ef.N.vis
	var itemForm []string
	for i := 0; ; i++ {
		name := r.FormValue(fmt.Sprintf("ef.%d.name", i))
		if name == "" {
			// Try table part fields
			name = r.FormValue(fmt.Sprintf("ef.tp0.%d.name", i))
		}
		if name == "" {
			break
		}
		vis := r.FormValue(fmt.Sprintf("ef.%d.vis", i))
		if vis == "" {
			vis = r.FormValue(fmt.Sprintf("ef.tp0.%d.vis", i))
		}
		if vis == "1" {
			itemForm = append(itemForm, name)
		}
	}
	// Also check table part fields with any index
	for tpJ := 0; ; tpJ++ {
		foundAny := false
		for fi := 0; ; fi++ {
			name := r.FormValue(fmt.Sprintf("ef.tp%d.%d.name", tpJ, fi))
			if name == "" {
				break
			}
			foundAny = true
			vis := r.FormValue(fmt.Sprintf("ef.tp%d.%d.vis", tpJ, fi))
			if vis == "1" {
				itemForm = append(itemForm, name)
			}
		}
		if !foundAny {
			break
		}
	}
	if len(itemForm) > 0 {
		doc["item_form"] = itemForm
	} else {
		delete(doc, "item_form")
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка сериализации") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if err := os.WriteFile(filePath, out, 0o644); err != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	fresh := h.loadCfgData(r.Context(), b, "tree")
	fresh.FieldsSaved = true
	fresh.FieldsSavedEntity = entityName
	renderCfg(w, r, fresh)
}

// numberTypeWithSpec превращает тип "number" в инлайн-нотацию "number(L,P)",
// если на форме заданы разрядность (Длина) и точность. Для остальных типов
// (включая уже собранные "reference:X"/"enum:X") возвращает typ без изменений.
func numberTypeWithSpec(typ, lengthVal, scaleVal string) string {
	if typ != "number" {
		return typ
	}
	l, _ := strconv.Atoi(strings.TrimSpace(lengthVal))
	if l <= 0 {
		return typ
	}
	s, _ := strconv.Atoi(strings.TrimSpace(scaleVal))
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("number(%d,%d)", l, s)
}

func (h *handler) configuratorSaveFields(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	entityName := r.FormValue("entity")
	tpNames := r.Form["tp_names"]
	entityKind := r.FormValue("entity_kind")

	// read posting checkbox (only meaningful for documents)
	var posting *bool
	if entityKind == "Документ" {
		v := r.FormValue("posting") == "true"
		posting = &v
	}
	// «Иерархический» имеет смысл только для справочников. Передаём
	// указатель — nil означает «не трогать поле в YAML», иначе
	// applyFieldEdits перепишет ent.Hierarchical явным значением.
	var hierarchical *bool
	if entityKind == "Справочник" {
		v := r.FormValue("hierarchical") == "true"
		hierarchical = &v
	}
	// Ввод на основании (Plan 38): список источников приходит как
	// based_on[] = ИмяСущности — multi-select на форме. Маркер «based_on_present»
	// отличает «поле не было на форме» (не трогать YAML) от «оба чекбокса
	// сняты» (очистить based_on). Без маркера было бы невозможно очистить
	// based_on через UI — пустой slice выглядел бы так же, как отсутствующее
	// поле.
	var basedOn *[]string
	if r.FormValue("based_on_present") == "1" {
		vals := r.Form["based_on"]
		clean := make([]string, 0, len(vals))
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v != "" {
				clean = append(clean, v)
			}
		}
		basedOn = &clean
	}

	var fields []saveField
	for i := 0; i < 500; i++ {
		name := r.FormValue(fmt.Sprintf("field.%d.name", i))
		if name == "" {
			break
		}
		typ := r.FormValue(fmt.Sprintf("field.%d.type", i))
		ref := r.FormValue(fmt.Sprintf("field.%d.ref", i))
		if typ == "reference" || typ == "enum" {
			if ref == "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				kind := "объект для ссылки"
				if typ == "enum" {
					kind = "перечисление"
				}
				data.Error = fmt.Sprintf(tr(lang, "Поле «%s»: выберите %s"), name, kind)
				renderCfg(w, r, data)
				return
			}
			typ = typ + ":" + ref
		}
		typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("field.%d.length", i)), r.FormValue(fmt.Sprintf("field.%d.scale", i)))
		sf := saveField{Name: name, Type: typ}
		// allow_inline_create — пишем только если значение отличается от дефолта
		// контекста (true в шапке). Шаблон отрисовывает hidden inline_present=1
		// только для ссылочных полей, поэтому маркер косвенно фильтрует тип.
		if r.FormValue(fmt.Sprintf("field.%d.inline_present", i)) == "1" {
			allow := r.FormValue(fmt.Sprintf("field.%d.inline_allow", i)) == "1"
			if !allow {
				f := false
				sf.AllowInlineCreate = &f
			}
		}
		fields = append(fields, sf)
	}
	// new fields added via "+ Добавить поле"
	for i := 1; i <= 500; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("new_field.%d.name", i)))
		if name == "" {
			continue
		}
		typ := r.FormValue(fmt.Sprintf("new_field.%d.type", i))
		ref := r.FormValue(fmt.Sprintf("new_field.%d.ref", i))
		if typ == "reference" || typ == "enum" {
			if ref == "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				kind := "объект для ссылки"
				if typ == "enum" {
					kind = "перечисление"
				}
				data.Error = fmt.Sprintf(tr(lang, "Поле «%s»: выберите %s"), name, kind)
				renderCfg(w, r, data)
				return
			}
			typ = typ + ":" + ref
		}
		typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("new_field.%d.length", i)), r.FormValue(fmt.Sprintf("new_field.%d.scale", i)))
		fields = append(fields, saveField{Name: name, Type: typ})
	}

	tpFields := make(map[string][]saveField)
	for _, tpName := range tpNames {
		var f []saveField
		for i := 0; i < 500; i++ {
			name := r.FormValue(fmt.Sprintf("tp.%s.field.%d.name", tpName, i))
			if name == "" {
				break
			}
			typ := r.FormValue(fmt.Sprintf("tp.%s.field.%d.type", tpName, i))
			ref := r.FormValue(fmt.Sprintf("tp.%s.field.%d.ref", tpName, i))
			if typ == "reference" || typ == "enum" {
				if ref == "" {
					data := h.loadCfgData(r.Context(), b, "tree")
					kind := "объект для ссылки"
					if typ == "enum" {
						kind = "перечисление"
					}
					data.Error = fmt.Sprintf(tr(lang, "Поле «%s.%s»: выберите %s"), tpName, name, kind)
					renderCfg(w, r, data)
					return
				}
				typ = typ + ":" + ref
			}
			typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("tp.%s.field.%d.length", tpName, i)), r.FormValue(fmt.Sprintf("tp.%s.field.%d.scale", tpName, i)))
			sf := saveField{Name: name, Type: typ}
			// В ТЧ дефолт allow_inline_create = false; пишем только если
			// чекбокс установлен (отличие от дефолта).
			if r.FormValue(fmt.Sprintf("tp.%s.field.%d.inline_present", tpName, i)) == "1" {
				if r.FormValue(fmt.Sprintf("tp.%s.field.%d.inline_allow", tpName, i)) == "1" {
					t := true
					sf.AllowInlineCreate = &t
				}
			}
			f = append(f, sf)
		}
		tpFields[tpName] = f
	}
	// new table parts added via "+ Добавить табличную часть"
	for _, tpIdx := range r.Form["new_tp_name"] {
		tpName := strings.TrimSpace(tpIdx)
		if tpName == "" {
			continue
		}
		tpFields[tpName] = nil
	}
	for ntp := 1; ntp <= 100; ntp++ {
		tpKey := strings.TrimSpace(r.FormValue(fmt.Sprintf("new_tp.%d.idx", ntp)))
		if tpKey == "" {
			continue
		}
		var f []saveField
		for i := 1; i <= 500; i++ {
			name := strings.TrimSpace(r.FormValue(fmt.Sprintf("new_tp.%d.field.%d.name", ntp, i)))
			if name == "" {
				continue
			}
			typ := r.FormValue(fmt.Sprintf("new_tp.%d.field.%d.type", ntp, i))
			ref := r.FormValue(fmt.Sprintf("new_tp.%d.field.%d.ref", ntp, i))
			if typ == "reference" || typ == "enum" {
				if ref == "" {
					data := h.loadCfgData(r.Context(), b, "tree")
					kind := "объект для ссылки"
					if typ == "enum" {
						kind = "перечисление"
					}
					data.Error = fmt.Sprintf(tr(lang, "Поле «%s.%s»: выберите %s"), tpKey, name, kind)
					renderCfg(w, r, data)
					return
				}
				typ = typ + ":" + ref
			}
			typ = numberTypeWithSpec(typ, r.FormValue(fmt.Sprintf("new_tp.%d.field.%d.length", ntp, i)), r.FormValue(fmt.Sprintf("new_tp.%d.field.%d.scale", ntp, i)))
			f = append(f, saveField{Name: name, Type: typ})
		}
		tpFields[tpKey] = f
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveEntityFieldsToDB(r.Context(), b, entityName, fields, tpFields, posting, hierarchical, basedOn)
	} else {
		saveErr = saveEntityFieldsToFile(b.Path, entityName, fields, tpFields, posting, hierarchical, basedOn)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = entityName
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorDeleteEntity(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	entityName := r.FormValue("entity")
	if entityName == "" {
		http.Error(w, "entity name required", 400)
		return
	}

	var delErr error
	if b.ConfigSource == "database" {
		delErr = h.deleteEntityFromDB(r.Context(), b, entityName)
	} else {
		delErr = deleteEntityFromFile(b.Path, entityName)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if delErr != nil {
		data.Error = tr(lang, "Ошибка удаления") + ": " + delErr.Error()
	} else {
		data.Error = tr(lang, "Сущность «") + entityName + tr(lang, "» удалена")
	}
	renderCfg(w, r, data)
}

func deleteEntityFromFile(dir, entityName string) error {
	path, err := findEntityFilePath(dir, entityName)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (h *handler) deleteEntityFromDB(ctx context.Context, b *Base, entityName string) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	repo := configdb.New(db)

	// Scan all YAML-object folders so deletion works for catalogs, documents,
	// registers, enums, reports and subsystems alike (not just catalogs/documents).
	rows, err := db.Query(ctx,
		`SELECT path, content FROM _onebase_config WHERE path LIKE 'catalogs/%.yaml' OR path LIKE 'documents/%.yaml' OR path LIKE 'registers/%.yaml' OR path LIKE 'inforegisters/%.yaml' OR path LIKE 'accountregisters/%.yaml' OR path LIKE 'enums/%.yaml' OR path LIKE 'reports/%.yaml' OR path LIKE 'subsystems/%.yaml'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var hdr struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(content, &hdr) == nil && hdr.Name == entityName {
			return repo.DeleteFile(ctx, p)
		}
	}
	return fmt.Errorf("сущность %q не найдена", entityName)
}

func renderCfg(w http.ResponseWriter, r *http.Request, data *configuratorData) {
	if data.Lang == "" {
		data.Lang = resolveLang(r)
	}
	// AJAX-сохранение форм редактирования объектов: вместо полной перерисовки
	// страницы возвращаем компактный JSON, который клиент показывает тостом. Это
	// убирает полностраничную перезагрузку (и связанный с ней «разрыв кадра» в
	// WebView2) и позволяет иметь единую кнопку «Сохранить» в шапке.
	if r != nil && r.Header.Get("X-Onebase-Ajax") == "1" {
		entity := data.FieldsSavedEntity
		if entity == "" {
			entity = data.ModuleSavedEntity
		}
		msg := tr(data.Lang, "Сохранено")
		switch {
		case entity == "" || entity == "panel-backup" || entity == "__app__":
			// generic
		default:
			msg = "✓ " + entity + " — " + tr(data.Lang, "сохранено")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      data.Error == "",
			"error":   data.Error,
			"message": msg,
			"running": data.IsRunning,
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cfgTmpl.ExecuteTemplate(w, "cfg-main", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// ── Register field save ───────────────────────────────────────────────────────

func findRegisterFilePath(dir, regName string) (string, error) {
	items, _ := os.ReadDir(filepath.Join(dir, "registers"))
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, "registers", item.Name())
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var hdr struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(data, &hdr) == nil && hdr.Name == regName {
			return p, nil
		}
	}
	return "", fmt.Errorf("register %q not found", regName)
}

func saveRegisterFieldsToFile(dir, regName string, dims, res, attrs []saveField) error {
	filePath, err := findRegisterFilePath(dir, regName)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	var reg saveRegister
	if err := yaml.Unmarshal(raw, &reg); err != nil {
		return err
	}
	reg.Dimensions = dims
	reg.Resources = res
	reg.Attributes = attrs
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, out, 0o644)
}

func (h *handler) saveRegisterFieldsToDB(ctx context.Context, b *Base, regName string, dims, res, attrs []saveField) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(ctx,
		`SELECT path, content FROM _onebase_config WHERE path LIKE 'registers/%.yaml'`)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var targetPath string
	var reg saveRegister
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var r saveRegister
		if yaml.Unmarshal(content, &r) == nil && r.Name == regName {
			targetPath = p
			reg = r
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("register %q not found in DB config", regName)
	}

	reg.Dimensions = dims
	reg.Resources = res
	reg.Attributes = attrs
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO _onebase_config (path, content, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
	`, targetPath, out)
	return err
}

func (h *handler) configuratorSaveRegisterFields(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	regName := r.FormValue("register")

	parseSection := func(prefix string) []saveField {
		var fields []saveField
		for i := 0; i < 500; i++ {
			name := r.FormValue(fmt.Sprintf("%s.%d.name", prefix, i))
			if name == "" {
				break
			}
			typ := r.FormValue(fmt.Sprintf("%s.%d.type", prefix, i))
			ref := r.FormValue(fmt.Sprintf("%s.%d.ref", prefix, i))
			if typ == "reference" && ref != "" {
				typ = "reference:" + ref
			}
			fields = append(fields, saveField{Name: name, Type: typ})
		}
		return fields
	}

	dims := parseSection("dim")
	res := parseSection("res")
	attrs := parseSection("attr")

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveRegisterFieldsToDB(r.Context(), b, regName, dims, res, attrs)
	} else {
		saveErr = saveRegisterFieldsToFile(b.Path, regName, dims, res, attrs)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = regName
	}
	renderCfg(w, r, data)
}

// ── New object creation ───────────────────────────────────────────────────────

func (h *handler) configuratorNewObject(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	kind := r.FormValue("kind")
	name := strings.TrimSpace(r.FormValue("name"))

	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Укажите имя объекта")
		renderCfg(w, r, data)
		return
	}

	subdir, content := newObjectContent(kind, name)
	if subdir == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Неизвестный тип объекта") + ": " + kind
		renderCfg(w, r, data)
		return
	}

	filename := nameToFilename(name) + ".yaml"
	if kind == "module" {
		filename = nameToFilename(name) + ".module.os"
		content = "// " + name + "\n// Общий модуль\n\nПроцедура Главная()\nКонецПроцедуры\n"
		subdir = "src"
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			repo := configdb.New(db)
			repo.EnsureSchema(r.Context())
			path := subdir + "/" + filename
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, path, []byte(content))
		}
	} else {
		dir := filepath.Join(b.Path, subdir)
		os.MkdirAll(dir, 0o755)
		saveErr = os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644)
	}

	if saveErr != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка создания") + ": " + saveErr.Error()
		renderCfg(w, r, data)
		return
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	data.FieldsSavedEntity = name
	data.FieldsSaved = true
	renderCfg(w, r, data)
}

func newObjectContent(kind, name string) (subdir, content string) {
	switch kind {
	case "catalog":
		return "catalogs", "name: " + name + "\nfields:\n  - name: Наименование\n    type: string\n"
	case "document":
		return "documents", "name: " + name + "\nfields:\n  - name: Дата\n    type: date\n"
	case "register":
		return "registers", "name: " + name + "\ndimensions:\n  - name: Измерение1\n    type: string\nresources:\n  - name: Ресурс1\n    type: number\n"
	case "inforeg":
		return "inforegs", "name: " + name + "\nperiodic: false\ndimensions:\n  - name: Ключ\n    type: string\nresources:\n  - name: Значение\n    type: string\n"
	case "enum":
		return "enums", "name: " + name + "\nvalues:\n  - Значение1\n  - Значение2\n"
	case "subsystem":
		return "subsystems", "name: " + name + "\ntitle: " + name + "\norder: 10\ncontents:\n  catalogs: []\n  documents: []\n  registers: []\n"
	case "widget":
		return "widgets", "name: " + name + "\ntype: kpi\ntitle: " + name + "\nformat: number\nquery: |\n  ВЫБРАТЬ КОЛИЧЕСТВО(*) КАК Значение ИЗ Документ.ИмяДокумента\n"
	case "accountreg":
		return "accountregs", "name: " + name + "\ntitle: " + name + "\naccounts: ПланСчетов\nresources:\n  - name: Сумма\n    type: number\n"
	case "processor":
		return "processors", "name: " + name + "\ntitle: " + name + "\nparams: []\n"
	}
	return "", ""
}

func nameToFilename(name string) string {
	return strings.ToLower(name)
}

// ── Enum save ─────────────────────────────────────────────────────────────────

func (h *handler) configuratorSaveEnum(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	enumName := r.FormValue("enum_name")
	rawValues := r.FormValue("values")

	var values []string
	for _, line := range strings.Split(rawValues, "\n") {
		v := strings.TrimSpace(line)
		if v != "" {
			values = append(values, v)
		}
	}

	type saveEnum struct {
		Name   string   `yaml:"name"`
		Values []string `yaml:"values"`
	}
	out, _ := yaml.Marshal(saveEnum{Name: enumName, Values: values})

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			path := "enums/" + nameToFilename(enumName) + ".yaml"
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, path, out)
		}
	} else {
		dir := filepath.Join(b.Path, "enums")
		os.MkdirAll(dir, 0o755)
		// find existing file by name field, fallback to name-based filename
		files, _ := os.ReadDir(dir)
		targetFile := filepath.Join(dir, nameToFilename(enumName)+".yaml")
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			raw, _ := os.ReadFile(p)
			var hdr struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(raw, &hdr) == nil && hdr.Name == enumName {
				targetFile = p
				break
			}
		}
		saveErr = os.WriteFile(targetFile, out, 0o644)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = enumName
	}
	renderCfg(w, r, data)
}

// ── Constant save ─────────────────────────────────────────────────────────────

func (h *handler) configuratorSaveConstant(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	constName := r.FormValue("const_name")
	label := strings.TrimSpace(r.FormValue("label"))
	typ := strings.TrimSpace(r.FormValue("type"))
	ref := strings.TrimSpace(r.FormValue("ref"))
	def := strings.TrimSpace(r.FormValue("default"))
	if typ == "reference" && ref != "" {
		typ = "reference:" + ref
	}

	type rawConst struct {
		Name    string `yaml:"name"`
		Type    string `yaml:"type"`
		Default string `yaml:"default,omitempty"`
		Label   string `yaml:"label,omitempty"`
	}
	type rawConstsFile struct {
		Constants []rawConst `yaml:"constants"`
	}

	updateConstantsFile := func(raw []byte) ([]byte, error) {
		var cf rawConstsFile
		if err := yaml.Unmarshal(raw, &cf); err != nil {
			return nil, err
		}
		for i, c := range cf.Constants {
			if c.Name == constName {
				cf.Constants[i].Label = label
				cf.Constants[i].Type = typ
				cf.Constants[i].Default = def
				break
			}
		}
		return yaml.Marshal(&cf)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			rows, _ := db.Query(r.Context(),
				`SELECT path, content FROM _onebase_config WHERE path LIKE 'constants/%.yaml'`)
			var targetPath string
			var targetContent []byte
			for rows.Next() {
				var p string
				var content []byte
				rows.Scan(&p, &content)
				var cf rawConstsFile
				if yaml.Unmarshal(content, &cf) == nil {
					for _, c := range cf.Constants {
						if c.Name == constName {
							targetPath = p
							targetContent = content
							break
						}
					}
				}
				if targetPath != "" {
					break
				}
			}
			rows.Close()
			if targetPath != "" {
				if out, err := updateConstantsFile(targetContent); err == nil {
					_, saveErr = db.Exec(r.Context(), `
						INSERT INTO _onebase_config (path, content, updated_at)
						VALUES ($1, $2, CURRENT_TIMESTAMP)
						ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
					`, targetPath, out)
				}
			}
		}
	} else {
		dir := filepath.Join(b.Path, "constants")
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			raw, _ := os.ReadFile(p)
			var cf rawConstsFile
			if yaml.Unmarshal(raw, &cf) != nil {
				continue
			}
			found := false
			for _, c := range cf.Constants {
				if c.Name == constName {
					found = true
					break
				}
			}
			if !found {
				continue
			}
			out, err := updateConstantsFile(raw)
			if err == nil {
				saveErr = os.WriteFile(p, out, 0o644)
			} else {
				saveErr = err
			}
			break
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = constName
	}
	renderCfg(w, r, data)
}

// ── Report save ───────────────────────────────────────────────────────────────

func (h *handler) configuratorSaveReport(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	repName := r.FormValue("report_name")
	query := r.FormValue("query")
	title := strings.TrimSpace(r.FormValue("title"))
	chartProc := strings.TrimSpace(r.FormValue("chart_proc"))
	chartSource := r.FormValue("chart_source")

	type saveParam struct {
		Name  string `yaml:"name"`
		Type  string `yaml:"type"`
		Label string `yaml:"label,omitempty"`
	}
	type saveReport struct {
		Name      string      `yaml:"name"`
		Title     string      `yaml:"title,omitempty"`
		Params    []saveParam `yaml:"params,omitempty"`
		Query     string      `yaml:"query"`
		ChartProc string      `yaml:"chart_proc,omitempty"`
	}

	// Parse params from form: param.0.name, param.0.type, param.0.label, ...
	var newParams []saveParam
	for i := 0; i < 50; i++ {
		pname := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.name", i)))
		if pname == "" {
			break
		}
		ptype := r.FormValue(fmt.Sprintf("param.%d.type", i))
		plabel := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.label", i)))
		newParams = append(newParams, saveParam{Name: pname, Type: ptype, Label: plabel})
	}

	updateReportFile := func(raw []byte) ([]byte, error) {
		var rep saveReport
		if err := yaml.Unmarshal(raw, &rep); err != nil {
			return nil, err
		}
		rep.Query = query
		if title != "" {
			rep.Title = title
		}
		rep.Params = newParams
		rep.ChartProc = chartProc
		return yaml.Marshal(&rep)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			rows, _ := db.Query(r.Context(),
				`SELECT path, content FROM _onebase_config WHERE path LIKE 'reports/%.yaml'`)
			var targetPath string
			var targetContent []byte
			for rows.Next() {
				var p string
				var content []byte
				rows.Scan(&p, &content)
				var rep saveReport
				if yaml.Unmarshal(content, &rep) == nil && rep.Name == repName {
					targetPath = p
					targetContent = content
					break
				}
			}
			rows.Close()
			if targetPath != "" {
				if out, err := updateReportFile(targetContent); err == nil {
					_, saveErr = db.Exec(r.Context(), `
						INSERT INTO _onebase_config (path, content, updated_at)
						VALUES ($1, $2, CURRENT_TIMESTAMP)
						ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
					`, targetPath, out)
				}
			}
		}
	} else {
		dir := filepath.Join(b.Path, "reports")
		files, _ := os.ReadDir(dir)
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, f.Name())
			raw, _ := os.ReadFile(p)
			var rep saveReport
			if yaml.Unmarshal(raw, &rep) != nil || rep.Name != repName {
				continue
			}
			out, err := updateReportFile(raw)
			if err == nil {
				saveErr = os.WriteFile(p, out, 0o644)
			} else {
				saveErr = err
			}
			break
		}
	}

	// Save chart .rep.os source if provided
	if chartSource != "" && b.ConfigSource == "file" {
		os.MkdirAll(filepath.Join(b.Path, "src"), 0o755)
		repOSPath := filepath.Join(b.Path, "src", repName+".rep.os")
		saveErr = os.WriteFile(repOSPath, []byte(chartSource), 0o644)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = repName
	}
	renderCfg(w, r, data)
}

// ── Common module save ────────────────────────────────────────────────────────

func (h *handler) configuratorSaveCommonModule(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	moduleName := r.FormValue("module_name")
	source := r.FormValue("source")

	filename := moduleNameToFilename(moduleName)

	var saveErr error
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			saveErr = err
		} else {
			defer db.Close()
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, "src/"+filename, []byte(source))
		}
	} else {
		srcDir := filepath.Join(b.Path, "src")
		os.MkdirAll(srcDir, 0o755)
		saveErr = os.WriteFile(filepath.Join(srcDir, filename), []byte(source), 0o644)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.ModuleSaved = true
		data.ModuleSavedEntity = moduleName
	}
	renderCfg(w, r, data)
}

func moduleNameToFilename(name string) string {
	if name == "" {
		return ".module.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".module.os"
}

// ── Processor save ────────────────────────────────────────────────────────────

func (h *handler) configuratorSaveProcessor(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	procName := r.FormValue("processor_name")
	title := strings.TrimSpace(r.FormValue("title"))
	source := r.FormValue("source")

	type saveParam struct {
		Name  string `yaml:"name"`
		Type  string `yaml:"type"`
		Label string `yaml:"label,omitempty"`
	}
	type saveProcessor struct {
		Name   string      `yaml:"name"`
		Title  string      `yaml:"title,omitempty"`
		Params []saveParam `yaml:"params,omitempty"`
	}

	var newParams []saveParam
	for i := 0; i < 50; i++ {
		pname := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.name", i)))
		if pname == "" {
			break
		}
		ptype := r.FormValue(fmt.Sprintf("param.%d.type", i))
		plabel := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.label", i)))
		newParams = append(newParams, saveParam{Name: pname, Type: ptype, Label: plabel})
	}

	yamlData, _ := yaml.Marshal(saveProcessor{Name: procName, Title: title, Params: newParams})
	yamlFilename := "processors/" + nameToFilename(procName) + ".yaml"
	srcFilename := "src/" + processorSrcFilename(procName)

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			if _, err := db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, yamlFilename, yamlData); err != nil {
				saveErr = err
			}
			if saveErr == nil {
				_, saveErr = db.Exec(r.Context(), `
					INSERT INTO _onebase_config (path, content, updated_at)
					VALUES ($1, $2, CURRENT_TIMESTAMP)
					ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
				`, srcFilename, []byte(source))
			}
		}
	} else {
		procDir := filepath.Join(b.Path, "processors")
		os.MkdirAll(procDir, 0o755)
		if err := os.WriteFile(filepath.Join(b.Path, yamlFilename), yamlData, 0o644); err != nil {
			saveErr = err
		}
		if saveErr == nil {
			srcDir := filepath.Join(b.Path, "src")
			os.MkdirAll(srcDir, 0o755)
			saveErr = os.WriteFile(filepath.Join(b.Path, srcFilename), []byte(source), 0o644)
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = procName
	}
	renderCfg(w, r, data)
}

func processorSrcFilename(name string) string {
	if name == "" {
		return ".proc.os"
	}
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes) + ".proc.os"
}

// ── Print form save ───────────────────────────────────────────────────────────

func (h *handler) configuratorSavePrintForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	filename := strings.TrimSpace(r.FormValue("printform_filename"))
	source := r.FormValue("source")

	if filename == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя файла печатной формы не указано")
		renderCfg(w, r, data)
		return
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, "printforms/"+filename, []byte(source))
		}
	} else {
		pfDir := filepath.Join(b.Path, "printforms")
		os.MkdirAll(pfDir, 0o755)
		saveErr = os.WriteFile(filepath.Join(pfDir, filename), []byte(source), 0o644)
	}

	var hdr struct {
		Name string `yaml:"name"`
	}
	yaml.Unmarshal([]byte(source), &hdr) //nolint
	pfName := hdr.Name
	isDSL := r.FormValue("printform_dsl") == "1"
	if isDSL {
		pfName = strings.TrimSuffix(filename, ".os")
	} else if pfName == "" {
		pfName = strings.TrimSuffix(filename, ".yaml")
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = pfName
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorNewPrintForm(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	document := strings.TrimSpace(r.FormValue("document"))

	if name == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя печатной формы обязательно")
		renderCfg(w, r, data)
		return
	}

	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	filename := string(runes) + ".yaml"

	source := fmt.Sprintf("name: %s\ndocument: %s\ntitle: \"{{Номер}} от {{Дата | date}}\"\n\nheader: |\n  ## %s\n\ntable:\n  source: Товары\n  columns:\n    - field: \"@row\"\n      label: \"№\"\n      width: 36px\n      align: center\n", name, document, name)

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, "printforms/"+filename, []byte(source))
		}
	} else {
		pfDir := filepath.Join(b.Path, "printforms")
		os.MkdirAll(pfDir, 0o755)
		fullPath := filepath.Join(pfDir, filename)
		if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			saveErr = os.WriteFile(fullPath, []byte(source), 0o644)
		}
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка создания") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = name
	}
	renderCfg(w, r, data)
}

func (h *handler) configuratorSaveLayout(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	r.ParseForm()
	layoutName := strings.TrimSpace(r.FormValue("layout_name"))
	source := r.FormValue("source")

	if layoutName == "" {
		http.Error(w, "layout name required", 400)
		return
	}

	filename := layoutName + ".layout.yaml"

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, "printforms/"+filename, []byte(source))
		}
	} else {
		pfDir := filepath.Join(b.Path, "printforms")
		layoutPath := ""
		filepath.Walk(pfDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Base(path), filename) {
				layoutPath = path
			}
			return nil
		})
		if layoutPath == "" {
			filepath.Walk(pfDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if strings.EqualFold(filepath.Base(path), layoutName+".os") {
					layoutPath = filepath.Join(filepath.Dir(path), filename)
				}
				return nil
			})
		}
		if layoutPath == "" {
			layoutPath = filepath.Join(pfDir, filename)
		}
		saveErr = os.WriteFile(layoutPath, []byte(source), 0o644)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения макета") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = layoutName
		data.SelectedTreeID = "mkt-" + layoutName
	}
	renderCfg(w, r, data)
}

// ── Subsystem save ──────────────────────────────────────────────────────────

// homeWidgetsNames разворачивает раскладку рабочего стола в плоский список имён
// виджетов (в порядке вывода) — для отметки галочек в режиме «Авто».
func homeWidgetsNames(hp *metadata.HomePage) []string {
	if hp == nil {
		return nil
	}
	var names []string
	for _, row := range hp.Rows {
		names = append(names, row.Widgets...)
	}
	for _, w := range hp.Widgets {
		names = append(names, w.Name)
	}
	return names
}

// homeLayoutMode возвращает режим раскладки для селектора: "rows" или "auto".
func homeLayoutMode(hp *metadata.HomePage) string {
	if hp != nil && hp.Layout == "rows" {
		return "rows"
	}
	return "auto"
}

// rowsFromForm строит ряды виджетов и режим раскладки из формы редактора.
// Режим «По рядам» (home_layout=rows) читает JSON home_rows из drag-конструктора;
// иначе отмеченные галочками виджеты (home_widgets) складываются в один ряд.
func rowsFromForm(r *http.Request) ([]metadata.HomePageRow, string) {
	clean := func(in []string) []string {
		var out []string
		for _, n := range in {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	if r.FormValue("home_layout") == "rows" {
		var raw [][]string
		_ = json.Unmarshal([]byte(r.FormValue("home_rows")), &raw)
		var rows []metadata.HomePageRow
		for _, names := range raw {
			if c := clean(names); len(c) > 0 {
				rows = append(rows, metadata.HomePageRow{Widgets: c})
			}
		}
		return rows, "rows"
	}
	if names := clean(r.Form["home_widgets"]); len(names) > 0 {
		return []metadata.HomePageRow{{Widgets: names}}, "auto"
	}
	return nil, "auto"
}

func (h *handler) configuratorSaveSubsystem(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	subName := strings.TrimSpace(r.FormValue("subsystem_name"))
	title := r.FormValue("title")
	icon := r.FormValue("icon")
	orderStr := r.FormValue("order")
	var order int
	if orderStr != "" {
		fmt.Sscanf(orderStr, "%d", &order)
	}

	// Без имени подсистему не сохраняем — иначе на диске появляется битый
	// файл «.yaml» с пустым name (пустая подсистема в дереве).
	if subName == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Укажите имя подсистемы")
		renderCfg(w, r, data)
		return
	}

	if title == "" {
		title = subName
	}

	dir := b.Path
	if b.ConfigSource == "database" {
		dir, err = workspacePath(b.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	subDir := filepath.Join(dir, "subsystems")
	os.MkdirAll(subDir, 0o755)

	type yamlContents struct {
		Catalogs   []string `yaml:"catalogs,omitempty"`
		Documents  []string `yaml:"documents,omitempty"`
		Registers  []string `yaml:"registers,omitempty"`
		InfoRegs   []string `yaml:"inforegs,omitempty"`
		Reports    []string `yaml:"reports,omitempty"`
		Processors []string `yaml:"processors,omitempty"`
		Journals   []string `yaml:"journals,omitempty"`
	}
	type yamlHomePage struct {
		Title   string                    `yaml:"title,omitempty"`
		Titles  map[string]string         `yaml:"titles,omitempty"`
		Layout  string                    `yaml:"layout,omitempty"`
		Rows    []metadata.HomePageRow    `yaml:"rows,omitempty"`
		Widgets []metadata.HomePageWidget `yaml:"widgets,omitempty"`
	}
	type yamlSubsystem struct {
		Name     string            `yaml:"name"`
		Title    string            `yaml:"title"`
		Titles   map[string]string `yaml:"titles,omitempty"`
		Icon     string            `yaml:"icon,omitempty"`
		Order    int               `yaml:"order"`
		Contents yamlContents      `yaml:"contents"`
		HomePage *yamlHomePage     `yaml:"home_page,omitempty"`
	}

	targetFile := filepath.Join(subDir, nameToFilename(subName)+".yaml")

	ys := yamlSubsystem{
		Name:  subName,
		Title: title,
		Icon:  icon,
		Order: order,
	}
	ys.Contents.Catalogs = r.Form["catalogs"]
	ys.Contents.Documents = r.Form["documents"]
	ys.Contents.Registers = r.Form["registers"]
	ys.Contents.InfoRegs = r.Form["inforegs"]
	ys.Contents.Reports = r.Form["reports"]
	ys.Contents.Processors = r.Form["processors"]

	// Сохраняем переводы (titles) и метаданные рабочего стола из уже
	// существующего файла, чтобы перезапись не теряла данные, которых нет в форме.
	if existing, lerr := metadata.LoadSubsystemFile(targetFile); lerr == nil && existing != nil {
		ys.Titles = existing.Titles
		if existing.HomePage != nil {
			ys.HomePage = &yamlHomePage{
				Title:   existing.HomePage.Title,
				Titles:  existing.HomePage.Titles,
				Layout:  existing.HomePage.Layout,
				Widgets: existing.HomePage.Widgets,
			}
		}
	}

	// Раскладка виджетов рабочего стола из формы: режим «Авто» — отмеченные
	// галочками виджеты одним рядом; «По рядам» — ряды из drag-конструктора.
	// Перезаписывает rows/layout, сохраняя title/titles рабочего стола.
	rows, layout := rowsFromForm(r)
	if len(rows) > 0 {
		if ys.HomePage == nil {
			ys.HomePage = &yamlHomePage{}
		}
		ys.HomePage.Rows = rows
		ys.HomePage.Layout = layout
		ys.HomePage.Widgets = nil // rows и flat widgets взаимоисключающи
	} else if ys.HomePage != nil {
		ys.HomePage.Rows = nil
		// Если от рабочего стола ничего не осталось — убираем секцию целиком.
		if ys.HomePage.Title == "" && len(ys.HomePage.Titles) == 0 &&
			ys.HomePage.Layout == "" && len(ys.HomePage.Widgets) == 0 {
			ys.HomePage = nil
		}
	}

	out, _ := yaml.Marshal(&ys)

	data := h.loadCfgData(r.Context(), b, "tree")
	if err := os.WriteFile(targetFile, out, 0o644); err != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}
	data.FieldsSaved = true
	data.FieldsSavedEntity = subName
	renderCfg(w, r, data)
}

// ── App config save ───────────────────────────────────────────────────────────

func (h *handler) configuratorSaveApp(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Parse multipart form (up to 2MB for logo)
	lang := resolveLang(r)
	r.ParseMultipartForm(2 << 20)
	newName := strings.TrimSpace(r.FormValue("app_name"))
	newVersion := strings.TrimSpace(r.FormValue("app_version"))
	newLang := strings.TrimSpace(r.FormValue("app_lang"))
	existingLogo := strings.TrimSpace(r.FormValue("app_logo_existing"))
	removeLogo := r.FormValue("app_logo_remove") == "1"

	if newName == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя конфигурации не может быть пустым")
		renderCfg(w, r, data)
		return
	}

	// Determine logo path
	logoPath := existingLogo
	if removeLogo {
		logoPath = ""
	}

	// Handle uploaded logo file
	file, header, ferr := r.FormFile("app_logo_file")
	if ferr == nil {
		defer file.Close()
		// Read file content
		logoData, rerr := io.ReadAll(file)
		if rerr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Ошибка чтения логотипа") + ": " + rerr.Error()
			renderCfg(w, r, data)
			return
		}
		if len(logoData) > 2<<20 {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Логотип слишком большой (максимум 2 МБ)")
			renderCfg(w, r, data)
			return
		}
		// Determine storage path
		ext := strings.ToLower(filepath.Ext(header.Filename))
		if ext == "" {
			ext = ".png"
		}
		logoPath = "config/logo" + ext

		// Save logo file
		if b.ConfigSource == "database" {
			db, cerr := OpenDB(r.Context(), b)
			if cerr == nil {
				defer db.Close()
				db.Exec(r.Context(), `
					INSERT INTO _onebase_config (path, content, updated_at)
					VALUES ($1, $2, CURRENT_TIMESTAMP)
					ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
				`, logoPath, logoData)
			}
		} else {
			os.MkdirAll(filepath.Join(b.Path, "config"), 0o755)
			os.WriteFile(filepath.Join(b.Path, logoPath), logoData, 0o644)
		}
	}

	// Remove old logo file if changed
	if removeLogo && existingLogo != "" {
		if b.ConfigSource == "database" {
			db, cerr := OpenDB(r.Context(), b)
			if cerr == nil {
				defer db.Close()
				db.Exec(r.Context(), `DELETE FROM _onebase_config WHERE path = $1`, existingLogo)
			}
		} else {
			os.Remove(filepath.Join(b.Path, existingLogo))
		}
	}

	type saveAppConfig struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version,omitempty"`
		Lang    string `yaml:"lang,omitempty"`
		Logo    string `yaml:"logo,omitempty"`
	}
	out, _ := yaml.Marshal(saveAppConfig{Name: newName, Version: newVersion, Lang: newLang, Logo: logoPath})

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			_, saveErr = db.Exec(r.Context(), `
				INSERT INTO _onebase_config (path, content, updated_at)
				VALUES ($1, $2, CURRENT_TIMESTAMP)
				ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
			`, "config/app.yaml", out)
		}
	} else {
		dir := filepath.Join(b.Path, "config")
		os.MkdirAll(dir, 0o755)
		saveErr = os.WriteFile(filepath.Join(dir, "app.yaml"), out, 0o644)
	}

	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = "__app__"
	}
	renderCfg(w, r, data)
}

// ── InfoRegister field save ───────────────────────────────────────────────────

func findInfoRegFilePath(dir, name string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "inforegs"))
	if err != nil {
		return "", fmt.Errorf("inforegs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, "inforegs", e.Name())
		raw, _ := os.ReadFile(p)
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(raw, &tmp) == nil && tmp.Name == name {
			return p, nil
		}
	}
	return "", fmt.Errorf("inforeg %q not found", name)
}

func saveInfoRegToFile(dir string, reg saveInfoReg) error {
	p, err := findInfoRegFilePath(dir, reg.Name)
	if err != nil {
		return err
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}

func (h *handler) saveInfoRegToDB(ctx context.Context, b *Base, reg saveInfoReg) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path LIKE 'inforegs/%.yaml'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var targetPath string
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(content, &tmp) == nil && tmp.Name == reg.Name {
			targetPath = p
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("inforeg %q not found in DB config", reg.Name)
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO _onebase_config (path, content, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
	`, targetPath, out)
	return err
}

func (h *handler) configuratorSaveInfoRegFields(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	parseSection := func(prefix string) []saveField {
		var fields []saveField
		for i := 0; i < 500; i++ {
			name := r.FormValue(fmt.Sprintf("%s.%d.name", prefix, i))
			if name == "" {
				break
			}
			typ := r.FormValue(fmt.Sprintf("%s.%d.type", prefix, i))
			ref := r.FormValue(fmt.Sprintf("%s.%d.ref", prefix, i))
			if typ == "reference" && ref != "" {
				typ = "reference:" + ref
			}
			fields = append(fields, saveField{Name: name, Type: typ})
		}
		return fields
	}
	reg := saveInfoReg{
		Name:       r.FormValue("inforeg"),
		Periodic:   r.FormValue("periodic") == "true",
		Dimensions: parseSection("dim"),
		Resources:  parseSection("res"),
	}
	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveInfoRegToDB(r.Context(), b, reg)
	} else {
		saveErr = saveInfoRegToFile(b.Path, reg)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = reg.Name
	}
	renderCfg(w, r, data)
}

// ── AccountRegister save ───────────────────────────────────────────────────────

func findAccountRegFilePath(dir, name string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "accountregs"))
	if err != nil {
		return "", fmt.Errorf("accountregs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, "accountregs", e.Name())
		raw, _ := os.ReadFile(p)
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(raw, &tmp) == nil && tmp.Name == name {
			return p, nil
		}
	}
	return "", fmt.Errorf("accountreg %q not found", name)
}

func saveAccountRegToFile(dir string, reg saveAccountReg) error {
	p, err := findAccountRegFilePath(dir, reg.Name)
	if err != nil {
		// create new file
		os.MkdirAll(filepath.Join(dir, "accountregs"), 0o755)
		p = filepath.Join(dir, "accountregs", nameToFilename(reg.Name)+".yaml")
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}

func (h *handler) saveAccountRegToDB(ctx context.Context, b *Base, reg saveAccountReg) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path LIKE 'accountregs/%.yaml'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	targetPath := "accountregs/" + nameToFilename(reg.Name) + ".yaml"
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var tmp struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(content, &tmp) == nil && tmp.Name == reg.Name {
			targetPath = p
			break
		}
	}
	rows.Close()
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO _onebase_config (path, content, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
	`, targetPath, out)
	return err
}

func (h *handler) configuratorSaveAccountRegister(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	parseSection := func(prefix string) []saveField {
		var fields []saveField
		for i := 0; i < 500; i++ {
			name := r.FormValue(fmt.Sprintf("%s.%d.name", prefix, i))
			if name == "" {
				break
			}
			typ := r.FormValue(fmt.Sprintf("%s.%d.type", prefix, i))
			fields = append(fields, saveField{Name: name, Type: typ})
		}
		return fields
	}
	reg := saveAccountReg{
		Name:      r.FormValue("accountreg"),
		Title:     strings.TrimSpace(r.FormValue("title")),
		Accounts:  strings.TrimSpace(r.FormValue("accounts")),
		Resources: parseSection("res"),
	}
	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveAccountRegToDB(r.Context(), b, reg)
	} else {
		saveErr = saveAccountRegToFile(b.Path, reg)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = reg.Name
	}
	renderCfg(w, r, data)
}

// ── Predefined items save ─────────────────────────────────────────────────────

func (h *handler) configuratorSavePredefined(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lang := resolveLang(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	entityName := r.FormValue("entity")
	// collect predefined items
	type rawPD struct {
		Name   string                 `yaml:"name"`
		Fields map[string]interface{} `yaml:"fields,omitempty"`
	}
	var predefined []rawPD
	fieldNames := r.Form["pre_field_names"]
	for i := 0; i < 500; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("pre.%d.name", i)))
		if name == "" {
			break
		}
		fields := make(map[string]interface{})
		for _, fn := range fieldNames {
			if v := r.FormValue(fmt.Sprintf("pre.%d.field.%s", i, fn)); v != "" {
				fields[fn] = v
			}
		}
		pd := rawPD{Name: name}
		if len(fields) > 0 {
			pd.Fields = fields
		}
		predefined = append(predefined, pd)
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.savePredefinedToDB(r.Context(), b, entityName, predefined)
	} else {
		saveErr = savePredefinedToFile(b.Path, entityName, predefined)
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	if saveErr != nil {
		data.Error = tr(lang, "Ошибка сохранения") + ": " + saveErr.Error()
	} else {
		data.FieldsSaved = true
		data.FieldsSavedEntity = entityName
	}
	renderCfg(w, r, data)
}

func savePredefinedToFile(dir, entityName string, predefined interface{}) error {
	// find entity file in catalogs/ or documents/
	for _, subdir := range []string{"catalogs", "documents"} {
		entries, _ := os.ReadDir(filepath.Join(dir, subdir))
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			p := filepath.Join(dir, subdir, e.Name())
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var top struct {
				Name string `yaml:"name"`
			}
			if yaml.Unmarshal(raw, &top) != nil || top.Name != entityName {
				continue
			}
			var node map[string]interface{}
			if err := yaml.Unmarshal(raw, &node); err != nil {
				return err
			}
			if predefined == nil {
				delete(node, "predefined")
			} else {
				node["predefined"] = predefined
			}
			out, err := yaml.Marshal(node)
			if err != nil {
				return err
			}
			return os.WriteFile(p, out, 0o644)
		}
	}
	return fmt.Errorf("entity %q not found", entityName)
}

func (h *handler) savePredefinedToDB(ctx context.Context, b *Base, entityName string, predefined interface{}) error {
	db, err := OpenDB(ctx, b)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := db.Query(ctx, `SELECT path, content FROM _onebase_config WHERE path ~ '^(catalogs|documents)/[^/]+\.yaml$'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var targetPath string
	var rawContent []byte
	for rows.Next() {
		var p string
		var content []byte
		if err := rows.Scan(&p, &content); err != nil {
			continue
		}
		var top struct {
			Name string `yaml:"name"`
		}
		if yaml.Unmarshal(content, &top) == nil && top.Name == entityName {
			targetPath = p
			rawContent = content
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("entity %q not found in DB config", entityName)
	}
	var node map[string]interface{}
	if err := yaml.Unmarshal(rawContent, &node); err != nil {
		return err
	}
	if predefined == nil {
		delete(node, "predefined")
	} else {
		node["predefined"] = predefined
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO _onebase_config (path, content, updated_at)
		VALUES ($1, $2, CURRENT_TIMESTAMP)
		ON CONFLICT (path) DO UPDATE SET content=EXCLUDED.content, updated_at=CURRENT_TIMESTAMP
	`, targetPath, out)
	return err
}

// ── debug proxy ──────────────────────────────────────────────────────────────

// debugProxy forwards debug API requests from the configurator (launcher server)
// to the UI server, avoiding CORS issues in the webview.
func (h *handler) debugProxy(w http.ResponseWriter, r *http.Request) {
	baseID := chi.URLParam(r, "id")
	action := chi.URLParam(r, "action")

	b, err := h.store.Get(baseID)
	if err != nil {
		http.Error(w, "base not found", 404)
		return
	}

	// Требуем сессию админа конфигуратора. 401 JSON (не 302), т.к. это API для JS.
	if !h.cfgAdminAuthorized(r, b) {
		writeJSON(w, 401, map[string]string{"error": "Требуется вход администратора"})
		return
	}

	uiURL := fmt.Sprintf("http://localhost:%d/debug/global/%s", b.Port, action)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, uiURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Forward Content-Type from original request
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	// Внутренний токен — процесс базы примет debug-запрос только с ним.
	if tok := h.runner.DebugToken(baseID); tok != "" {
		req.Header.Set("X-OneBase-Debug-Token", tok)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
