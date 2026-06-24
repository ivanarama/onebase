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
	"github.com/ivantit66/onebase/internal/backup"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/converter"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/ui"
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
	Name       string            `yaml:"name"`
	Title      string            `yaml:"title,omitempty"`
	Titles     map[string]string `yaml:"titles,omitempty"`
	Dimensions []saveField       `yaml:"dimensions,omitempty"`
	Resources  []saveField       `yaml:"resources,omitempty"`
	Attributes []saveField       `yaml:"attributes,omitempty"`
}

type saveInfoReg struct {
	Name       string            `yaml:"name"`
	Title      string            `yaml:"title,omitempty"`
	Titles     map[string]string `yaml:"titles,omitempty"`
	Periodic   bool              `yaml:"periodic,omitempty"`
	Dimensions []saveField       `yaml:"dimensions,omitempty"`
	Resources  []saveField       `yaml:"resources,omitempty"`
}

type saveAccountReg struct {
	Name      string            `yaml:"name"`
	Title     string            `yaml:"title,omitempty"`
	Titles    map[string]string `yaml:"titles,omitempty"`
	Accounts  string            `yaml:"accounts,omitempty"`
	Resources []saveField       `yaml:"resources,omitempty"`
}

// applyAccountRegFields точечно правит редактируемые в форме ключи плана счетов
// прямо в дереве YAML, сохраняя блок subconto, многоязычные titles и любые
// прочие поля (раньше свежий marshal saveAccountReg стирал subconto и titles).
// setTitles=true означает, что форма содержала блок titles и его нужно записать
// (включая удаление ключа при пустом значении); false — не трогать.
func applyAccountRegFields(raw []byte, reg saveAccountReg, setTitles bool) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("applyAccountRegFields: ожидалось YAML-отображение в корне")
	}
	doc := root.Content[0]
	if err := setYAMLMapField(doc, "name", reg.Name); err != nil {
		return nil, err
	}
	var t, a, rv any
	if reg.Title != "" {
		t = reg.Title
	}
	if reg.Accounts != "" {
		a = reg.Accounts
	}
	if len(reg.Resources) > 0 {
		rv = reg.Resources
	}
	for _, kv := range []struct {
		k string
		v any
	}{{"title", t}, {"accounts", a}, {"resources", rv}} {
		if err := setYAMLMapField(doc, kv.k, kv.v); err != nil {
			return nil, err
		}
	}
	if setTitles {
		if err := setYAMLMapField(doc, "titles", anyOrNil(reg.Titles)); err != nil {
			return nil, err
		}
	}
	return yaml.Marshal(&root)
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
	AllowInlineCreate *bool             // nil = дефолт контекста (true в шапке, false в ТЧ)
	Titles            map[string]string // переводы синонима поля
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

type cfgEnumValue struct {
	Name   string
	Titles map[string]string
}

type cfgEnum struct {
	Name       string
	Values     []string
	EnumValues []cfgEnumValue
}

type cfgConstant struct {
	Name      string
	Type      string
	RefEntity string
	Default   string
	Label     string
	Labels    map[string]string // переводы подписи по языкам
	Length    int               // разрядность number(L,P): всего знаков (Длина)
	Scale     int               // знаков после запятой (Точность)
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
	Titles           map[string]string // переводы синонима объекта
}

type cfgRegister struct {
	Name       string
	Title      string
	Titles     map[string]string
	Dimensions []cfgField
	Resources  []cfgField
	Attributes []cfgField
}

type cfgParam struct {
	Name   string
	Type   string
	Label  string
	Labels map[string]string
}

type cfgReport struct {
	Name        string
	Title       string
	Titles      map[string]string
	Query       string
	ChartProc   string
	ChartSource string
	Params      []cfgParam
	Composition *report.Composition // пред-заполнение конструктора компоновки (план 59)
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
	Titles map[string]string
	Source string
	Params []cfgParam
}

// cfgPage — проекция объекта «страница» (план 66) для конфигуратора: метаданные
// pages/<имя>.yaml + исходник обработчика src/<имя>.page.os (ПриФормировании).
// Структурный близнец cfgProcessor (yaml + .os в src/).
type cfgPage struct {
	Name   string
	Title  string
	Titles map[string]string
	Icon   string
	Roles  []string
	Params []string
	Source string // raw .page.os
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
	Title      string
	Titles     map[string]string
	Periodic   bool
	Dimensions []cfgField
	Resources  []cfgField
}

type cfgAccountRegister struct {
	Name      string
	Title     string
	Titles    map[string]string
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
	Titles   map[string]string
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
	Titles  map[string]string // переводы заголовка (titles в YAML)
	Widgets []string          // отмеченные виджеты (режим «Авто»)
	Rows    [][]string        // раскладка по рядам (режим «По рядам»)
	Layout  string            // "auto" | "rows"
}

type configuratorData struct {
	Base             *Base
	AppName          string
	AppVersion       string
	AppLogo          string
	AppLang          string
	AppAuthor        string
	AppCopyright     string
	AppLicense       string
	AvailableLangs   []i18n.Lang
	DSNMasked        string
	Tab              string             // "tree" | "convert" | "files"
	ConfigFileTree   []fileTreeCategory // дерево файлов для вкладки «Файлы» (issue #132)
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
	Pages            []cfgPage
	PrintForms       []cfgPrintForm
	DSLPrintForms    []cfgDSLPrintForm
	// План 37, этап 4: управляемые формы.
	ManagedForms []cfgManagedForm
	EditingForm  *cfgManagedForm
	// EditingFormAttrs — реквизиты редактируемого объекта для палитры
	// перетаскивания реквизитов на форму (issue #134).
	EditingFormAttrs []formScaffoldAttr
	// FormEditFrom — node-id узла дерева, из которого открыт редактор формы
	// (?from=…). Back-link «← В конфигуратор» ведёт обратно на этот узел, а не
	// в корень конфигуратора (follow-up #164, слайс A). Пусто → фолбэк на
	// e-<Entity> редактируемой формы.
	FormEditFrom string
	// EditingFormTableParts — табличные части редактируемого объекта с составом
	// колонок: палитра ТЧ на холсте и редактор состава колонок (follow-up #164,
	// слайсы D1/D2).
	EditingFormTableParts []formTablePart
	Subsystems            []cfgSubsystem
	Widgets               []cfgWidget
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
	// LayoutMeta — JSON для панели «Данные» редактора макетов (план 64, этап 5b):
	// метаданные сущностей (реквизиты/ТЧ) + константы + карта «макет → документ».
	LayoutMeta template.JS
	// Bootstrap — JSON-блоб данных для window.__cfg (план 55, фаза 2b-1): имена
	// сущностей/перечислений, выбор дерева, флаги сохранения, база/порт/сессия.
	// Главный скрипт читает значения отсюда вместо серверной интерполяции.
	Bootstrap template.JS
	// I18n — JSON-словарь переводов для window.__cfgI18n (ключ→перевод языка
	// рендера; null для базового ru). Главный скрипт переводит через T(k).
	I18n template.JS
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
	// «Открыть в редакторе» из дерева файлов (issue #132, фаза 2): ?select=<data-id>
	// узла → выделить объект в дереве (через SelectedTreeID/bootstrap).
	if sel := strings.TrimSpace(r.URL.Query().Get("select")); sel != "" && data.SelectedTreeID == "" {
		data.SelectedTreeID = sel
	}
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

	// Вкладка «Файлы»: дерево файлов конфигурации (issue #132).
	if tab == "files" {
		data.ConfigFileTree = h.buildConfigFileTree(ctx, b, proj)
	}

	if appCfg, _ := project.LoadConfig(proj.Dir); appCfg != nil {
		data.AppName = appCfg.Name
		data.AppVersion = appCfg.Version
		data.AppLogo = appCfg.Logo
		data.AppLang = appCfg.Lang
		data.AppAuthor = appCfg.Author
		data.AppCopyright = appCfg.Copyright
		data.AppLicense = appCfg.License
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
			Titles:        e.Titles,
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
		rv := cfgRegister{Name: reg.Name, Title: reg.Title, Titles: reg.Titles}
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
		rv := cfgInfoRegister{Name: ir.Name, Title: ir.Title, Titles: ir.Titles, Periodic: ir.Periodic}
		for _, f := range ir.Dimensions {
			rv.Dimensions = append(rv.Dimensions, toCfgField(f))
		}
		for _, f := range ir.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		data.InfoRegisters = append(data.InfoRegisters, rv)
	}

	for _, ar := range proj.AccountRegisters {
		rv := cfgAccountRegister{Name: ar.Name, Title: ar.Title, Titles: ar.Titles, Accounts: ar.Accounts}
		for _, f := range ar.Resources {
			rv.Resources = append(rv.Resources, toCfgField(f))
		}
		data.AccountRegisters = append(data.AccountRegisters, rv)
	}

	for _, en := range proj.Enums {
		ev := cfgEnum{Name: en.Name, Values: en.Values}
		for _, v := range en.Values {
			ev.EnumValues = append(ev.EnumValues, cfgEnumValue{
				Name:   v,
				Titles: en.ValueTitles[v],
			})
		}
		data.Enums = append(data.Enums, ev)
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
			Labels:    c.Labels,
			Length:    c.Length,
			Scale:     c.Scale,
		})
	}

	repSources := readReportSources(proj.Dir)

	for _, rep := range proj.Reports {
		rv := cfgReport{Name: rep.Name, Title: rep.Title, Titles: rep.Titles, Query: rep.Query, ChartProc: rep.ChartProc, Composition: rep.Composition}
		if src, ok := repSources[strings.ToLower(rep.Name)]; ok {
			rv.ChartSource = src
		}
		for _, p := range rep.Params {
			rv.Params = append(rv.Params, cfgParam{Name: p.Name, Type: p.Type, Label: p.Label, Labels: p.Labels})
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
			Titles: proc.Titles,
			Source: procSources[strings.ToLower(proc.Name)],
		}
		for _, p := range proc.Params {
			rv.Params = append(rv.Params, cfgParam{Name: p.Name, Type: p.Type, Label: p.Label, Labels: p.Labels})
		}
		data.Processors = append(data.Processors, rv)
	}
	sort.Slice(data.Processors, func(i, j int) bool { return data.Processors[i].Name < data.Processors[j].Name })

	// Pages (план 66): метаданные pages/*.yaml + обработчик src/*.page.os.
	// proj.Pages уже отсортирован по имени (page.LoadDir).
	pageSources := readPageSources(proj.Dir)
	for _, pg := range proj.Pages {
		data.Pages = append(data.Pages, cfgPage{
			Name:   pg.Name,
			Title:  pg.Title,
			Titles: pg.Titles,
			Icon:   pg.Icon,
			Roles:  append([]string(nil), pg.Roles...),
			Params: append([]string(nil), pg.Params...),
			Source: pageSources[strings.ToLower(pg.Name)],
		})
	}

	// Subsystems
	for _, sub := range proj.Subsystems {
		var rows [][]string
		if sub.HomePage != nil {
			rows = sub.HomePage.RowGroups()
		}
		data.Subsystems = append(data.Subsystems, cfgSubsystem{
			Name:        sub.Name,
			Title:       sub.Title,
			Titles:      sub.Titles,
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
	var ghTitles map[string]string
	if proj.HomePage != nil {
		ghRows = proj.HomePage.RowGroups()
		if proj.HomePage.Title != "" && proj.HomePage.Title != "Главная" {
			ghTitle = proj.HomePage.Title
		}
		ghTitles = proj.HomePage.Titles
	}
	data.GlobalHome = cfgHomePage{
		Title:   ghTitle,
		Titles:  ghTitles,
		Widgets: homeWidgetsNames(proj.HomePage),
		Rows:    ghRows,
		Layout:  homeLayoutMode(proj.HomePage),
	}

	// Generate query builder schema
	data.QBSchema = buildQBSchema(data)

	// Generate layout designer data-binding metadata
	data.LayoutMeta = buildLayoutMeta(data)

	// Backup dir & files
	data.BackupDir = h.backupDir(b)
	backupDir := data.BackupDir
	if files, err := backup.BackupFiles(backupDir); err == nil {
		for _, f := range files {
			data.BackupFiles = append(data.BackupFiles, backupFile{
				Name: filepath.Base(f.Path),
				Size: fmt.Sprintf("%.1f KB", float64(f.Info.Size())/1024),
				Date: f.Info.ModTime().Format("2006-01-02 15:04"),
			})
		}
	}

	// Backup settings from app.yaml
	{
		var raw []byte
		var readErr error
		if b.ConfigSource == "database" {
			if db, err := OpenDB(ctx, b); err == nil {
				readErr = db.QueryRow(ctx,
					"SELECT content FROM _onebase_config WHERE path='config/app.yaml'").Scan(&raw)
				db.Close()
			}
		} else {
			raw, readErr = os.ReadFile(filepath.Join(b.Path, "config", "app.yaml"))
		}
		if readErr == nil && len(raw) > 0 {
			var tmp struct {
				Backup struct {
					Enabled   bool   `yaml:"enabled"`
					Schedule  string `yaml:"schedule"`
					KeepLast  int    `yaml:"keep_last"`
					Directory string `yaml:"directory"`
				} `yaml:"backup"`
			}
			if yaml.Unmarshal(raw, &tmp) == nil {
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

// populateBootstrap собирает JSON-блоб window.__cfg и словарь window.__cfgI18n
// из уже заполненных полей configuratorData. Вынесено отдельно, чтобы тест мог
// эмулировать продакшен-заполнение. lang — язык рендера (как в renderCfg).
func populateBootstrap(data *configuratorData, lang string) {
	// nil-срезы кодируем как [] (а не null): фронт делает .map()/.length по этим
	// массивам без guard'ов — старый {{range}} тоже отдавал [] для пустого среза.
	boot := map[string]any{
		"entityNames":    orEmpty(data.AllEntityNames),
		"enumNames":      orEmpty(data.AllEnumNames),
		"selectedTreeId": data.SelectedTreeID,
		"fieldsSaved":    data.FieldsSavedEntity,
		"moduleSaved":    data.ModuleSavedEntity,
		"baseId":         baseIDOf(data.Base),
		"basePort":       basePortOf(data.Base),
		"hasSession":     data.SessionToken != "",
		"groupOrder":     orEmpty(data.GroupOrder),
	}
	if bb, err := json.Marshal(boot); err == nil {
		data.Bootstrap = template.JS(bb)
	}
	var dict map[string]string
	if launcherBundle != nil {
		dict = launcherBundle.Dict(lang)
	}
	if ib, err := json.Marshal(dict); err == nil {
		data.I18n = template.JS(ib) // nil-словарь → "null"; в шаблоне `|| {}`
	}
}

// orEmpty гарантирует non-nil срез, чтобы json.Marshal дал [] вместо null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// baseIDOf/basePortOf — безопасные геттеры (data.Base может быть nil в тестах
// минимального рендера, например renderCfgFoot).
func baseIDOf(b *Base) string {
	if b == nil {
		return ""
	}
	return b.ID
}

func basePortOf(b *Base) int {
	if b == nil {
		return 0
	}
	return b.Port
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
	return cfgField{Name: f.Name, Type: typ, RefEntity: f.RefEntity, EnumName: f.EnumName, Length: f.Length, Scale: f.Scale, AllowInlineCreate: f.AllowInlineCreate, Titles: f.Titles}
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
		case strings.HasSuffix(name, ".module.os"), strings.HasSuffix(name, ".proc.os"), strings.HasSuffix(name, ".rep.os"), strings.HasSuffix(name, ".page.os"):
			// skip — handled by readModuleAndProcSources / report / page editor
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

// readPageSources читает src/*.page.os (обработчики страниц, план 66) и
// раскладывает их по имени страницы в нижнем регистре — так же, как лукапит
// loadCfgData (pageSources[ToLower(pg.Name)]).
func readPageSources(dir string) map[string]string {
	result := make(map[string]string)
	entries, err := os.ReadDir(filepath.Join(dir, "src"))
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".page.os") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "src", e.Name()))
		if err != nil {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(e.Name(), ".page.os"))
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
	if !validObjectName(entityName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя объекта")
		renderCfg(w, r, data)
		return
	}

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
			saveErr = cfgUpsert(r.Context(), db, "src/"+filename, []byte(source))
		}
	} else {
		saveErr = h.writeConfigFileRaw(r.Context(), b, "src/"+filename, []byte(source))
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
	existingTP := make(map[string]bool, len(ent.TableParts))
	for i, tp := range ent.TableParts {
		existingTP[tp.Name] = true
		if f, ok := tpFields[tp.Name]; ok {
			ent.TableParts[i].Fields = f
		}
	}
	// Новые табличные части — ключи tpFields, которых ещё нет среди
	// существующих ТЧ. Раньше они молча терялись (цикл шёл только по
	// ent.TableParts), из-за чего «Добавить табличную часть» в конфигураторе не
	// сохранялось. Имена сортируем для детерминированного YAML (порядок обхода
	// ключей карты в Go не определён).
	var newTP []string
	for name := range tpFields {
		if !existingTP[name] {
			newTP = append(newTP, name)
		}
	}
	sort.Strings(newTP)
	for _, name := range newTP {
		ent.TableParts = append(ent.TableParts, saveTP{Name: name, Fields: tpFields[name]})
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

func saveEntityFieldsToFile(dir, entityName string, fields []saveField, tpFields map[string][]saveField, posting *bool, hierarchical *bool, basedOn *[]string, objTitles *map[string]string) error {
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
	if objTitles != nil {
		ent.Titles = *objTitles
	}
	out, err := yaml.Marshal(&ent)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, out, 0o644)
}

func (h *handler) saveEntityFieldsToDB(ctx context.Context, b *Base, entityName string, fields []saveField, tpFields map[string][]saveField, posting *bool, hierarchical *bool, basedOn *[]string, objTitles *map[string]string) error {
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
	if objTitles != nil {
		ent.Titles = *objTitles
	}
	out, err := yaml.Marshal(&ent)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
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

// formRowIndices возвращает отсортированные числовые индексы строк, присланных
// под базовым ключом base: ключи вида <base>.<idx>.name. Индексы на фронте
// выдаёт глобальный счётчик (_cfgNewFieldIdx), поэтому непрерывности нет —
// собираем фактически присланные. Точка после индекса исключает ложные
// совпадения имён-префиксов (например «new_tp.Цен.field» против «...Цены...»).
func formRowIndices(r *http.Request, base string) []int {
	prefix := base + "."
	var idxs []int
	for k := range r.Form {
		if !strings.HasPrefix(k, prefix) || !strings.HasSuffix(k, ".name") {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(k, prefix), ".name")
		if n, err := strconv.Atoi(mid); err == nil {
			idxs = append(idxs, n)
		}
	}
	sort.Ints(idxs)
	return idxs
}

// anyOrNil превращает nil-карту в нетипизированный nil, чтобы setYAMLMapField
// удалил ключ (типизированный nil map, обёрнутый в any, != nil).
func anyOrNil(m map[string]string) any {
	if m == nil {
		return nil
	}
	return m
}

// formHasMapField сообщает, содержала ли форма блок ключей "<prefix>.*"
// (хотя бы один, пусть и с пустым значением). Отличает «блок переводов был
// отрендерен и отправлен» (применяем, включая очистку) от «блока не было»
// (не трогаем существующее). Используется для round-trip/точечных сохранений.
func formHasMapField(r *http.Request, prefix string) bool {
	pfx := prefix + "."
	for key := range r.Form {
		if strings.HasPrefix(key, pfx) {
			return true
		}
	}
	return false
}

// parseMapForm читает значения формы вида "<prefix>.<lang>" в map[lang]value.
// Скан по ключам формы (а не по списку языков) — чтобы не зависеть от бандла
// локализации. Пропускает: базовый язык ru (он в title/label), пустые значения
// и остатки с точкой (защита от пересечения с вложенными префиксами, напр.
// "titles." не должен ловить "field.0.titles.en"). nil при пустом результате —
// тогда omitempty / setYAMLMapField(nil) убирают ключ из YAML.
func parseMapForm(r *http.Request, prefix string) map[string]string {
	pfx := prefix + "."
	out := map[string]string{}
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, pfx) || len(vals) == 0 {
			continue
		}
		lang := strings.TrimPrefix(key, pfx)
		if lang == "" || lang == "ru" || strings.Contains(lang, ".") {
			continue
		}
		if v := strings.TrimSpace(vals[0]); v != "" {
			out[lang] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseRegSection читает секцию полей регистра/плана счетов из формы. Существующие
// строки приходят как <prefix>.<i>.{name,type,length,scale,ref} (обрыв на первом
// пустом), добавленные кнопкой «+ Добавить» — как new_<prefix>.<idx>.* (пропуск
// пустых, индексы из глобального счётчика). Тип числа кодируется number(L,P) через
// numberTypeWithSpec; reference/enum → typ:ref — как в редакторе реквизитов
// сущности. Раньше точность и добавленные строки терялись.
func parseRegSection(r *http.Request, prefix string) []saveField {
	var fields []saveField
	add := func(keyBase string) {
		name := strings.TrimSpace(r.FormValue(keyBase + ".name"))
		if name == "" {
			return
		}
		typ := r.FormValue(keyBase + ".type")
		ref := r.FormValue(keyBase + ".ref")
		if (typ == "reference" || typ == "enum") && ref != "" {
			typ = typ + ":" + ref
		}
		typ = numberTypeWithSpec(typ, r.FormValue(keyBase+".length"), r.FormValue(keyBase+".scale"))
		sf := saveField{Name: name, Type: typ}
		sf.Titles = parseMapForm(r, keyBase+".titles")
		fields = append(fields, sf)
	}
	for i := 0; i < 500; i++ {
		if strings.TrimSpace(r.FormValue(fmt.Sprintf("%s.%d.name", prefix, i))) == "" {
			break
		}
		add(fmt.Sprintf("%s.%d", prefix, i))
	}
	for _, i := range formRowIndices(r, "new_"+prefix) {
		add(fmt.Sprintf("new_%s.%d", prefix, i))
	}
	return fields
}

// buildTPSaveField собирает saveField строки ТЧ из значений формы с базовым
// ключом keyBase: тип, разрядность числа number(L,P), ссылку/перечисление.
// Возвращает непустую строку ошибки, если для reference/enum не выбран объект.
func buildTPSaveField(r *http.Request, lang, keyBase, tpName, name string) (saveField, string) {
	typ := r.FormValue(keyBase + ".type")
	ref := r.FormValue(keyBase + ".ref")
	if typ == "reference" || typ == "enum" {
		if ref == "" {
			kind := "объект для ссылки"
			if typ == "enum" {
				kind = "перечисление"
			}
			return saveField{}, fmt.Sprintf(tr(lang, "Поле «%s.%s»: выберите %s"), tpName, name, kind)
		}
		typ = typ + ":" + ref
	}
	typ = numberTypeWithSpec(typ, r.FormValue(keyBase+".length"), r.FormValue(keyBase+".scale"))
	sf := saveField{Name: name, Type: typ}
	sf.Titles = parseMapForm(r, keyBase+".titles")
	return sf, ""
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
		sf.Titles = parseMapForm(r, fmt.Sprintf("field.%d.titles", i))
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
		fields = append(fields, saveField{Name: name, Type: typ, Titles: parseMapForm(r, fmt.Sprintf("new_field.%d.titles", i))})
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
			sf.Titles = parseMapForm(r, fmt.Sprintf("tp.%s.field.%d.titles", tpName, i))
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
	// Новые табличные части («+ Добавить табличную часть») приходят маркером
	// new_tp_name=<Имя>. Поля, добавленные кнопкой «+ Добавить поле» — и в
	// новые, и в СУЩЕСТВУЮЩИЕ ТЧ — приходят под именным префиксом
	// new_tp.<Имя>.field.<idx>.* (idx — глобальный счётчик фронта, непрерывности
	// нет). Раньше бэкенд читал new_tp.<число>.idx, а имя клал отдельным ключом —
	// поэтому и новые ТЧ, и реквизиты, дописанные в существующую ТЧ, терялись.
	var newTPOrder []string
	seenNewTP := make(map[string]bool)
	for _, raw := range r.Form["new_tp_name"] {
		name := strings.TrimSpace(raw)
		if name == "" || seenNewTP[name] {
			continue
		}
		seenNewTP[name] = true
		newTPOrder = append(newTPOrder, name)
		if _, ok := tpFields[name]; !ok {
			tpFields[name] = nil // пустая новая ТЧ всё равно должна создаться
		}
	}
	// Дочитываем добавленные строки и дописываем к нужной ТЧ — существующей (из
	// tp_names) или только что созданной (из new_tp_name).
	appendDone := make(map[string]bool)
	for _, tpName := range append(append([]string{}, tpNames...), newTPOrder...) {
		if appendDone[tpName] {
			continue
		}
		appendDone[tpName] = true
		for _, i := range formRowIndices(r, "new_tp."+tpName+".field") {
			keyBase := fmt.Sprintf("new_tp.%s.field.%d", tpName, i)
			name := strings.TrimSpace(r.FormValue(keyBase + ".name"))
			if name == "" {
				continue
			}
			sf, errMsg := buildTPSaveField(r, lang, keyBase, tpName, name)
			if errMsg != "" {
				data := h.loadCfgData(r.Context(), b, "tree")
				data.Error = errMsg
				renderCfg(w, r, data)
				return
			}
			tpFields[tpName] = append(tpFields[tpName], sf)
		}
	}

	var objTitles *map[string]string
	if formHasMapField(r, "titles") {
		tm := parseMapForm(r, "titles")
		objTitles = &tm
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveEntityFieldsToDB(r.Context(), b, entityName, fields, tpFields, posting, hierarchical, basedOn, objTitles)
	} else {
		saveErr = saveEntityFieldsToFile(b.Path, entityName, fields, tpFields, posting, hierarchical, basedOn, objTitles)
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
		data.Error = tr(lang, "Ошибка удаления") + ": " + errText(r, delErr)
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
	return i18nerr.Errorf("сущность %q не найдена", entityName)
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
	// Bootstrap-блоб window.__cfg + словарь window.__cfgI18n собираем здесь, а не
	// в loadCfgData: флаги сохранения (FieldsSavedEntity/ModuleSavedEntity) и
	// SelectedTreeID проставляются вызывающими ПОСЛЕ loadCfgData, поэтому их
	// финальные значения известны лишь к моменту рендера (план 55, фаза 2b-1).
	populateBootstrap(data, data.Lang)

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

func saveRegisterFieldsToFile(dir, regName string, dims, res, attrs []saveField, objTitles *map[string]string) error {
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
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, out, 0o644)
}

func (h *handler) saveRegisterFieldsToDB(ctx context.Context, b *Base, regName string, dims, res, attrs []saveField, objTitles *map[string]string) error {
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
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
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

	dims := parseRegSection(r, "dim")
	res := parseRegSection(r, "res")
	attrs := parseRegSection(r, "attr")

	var regTitles *map[string]string
	if formHasMapField(r, "titles") {
		t := parseMapForm(r, "titles")
		regTitles = &t
	}

	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveRegisterFieldsToDB(r.Context(), b, regName, dims, res, attrs, regTitles)
	} else {
		saveErr = saveRegisterFieldsToFile(b.Path, regName, dims, res, attrs, regTitles)
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
	if !validObjectName(name) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя объекта")
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
		// subdir/content приходят из newObjectContent; здесь только расширение.
		filename = nameToFilename(name) + ".module.os"
	}
	if kind == "page" {
		// Имя файла страницы — в исходном регистре (как демо pages/Панель.yaml),
		// чтобы правка существующей страницы перезаписывала тот же файл, а не
		// плодила дубль на регистрозависимых ФС.
		filename = name + ".yaml"
	}

	path := subdir + "/" + filename
	files := []configFileEntry{{relPath: path, content: []byte(content)}}
	if kind == "page" {
		// Страница — это пара файлов: сначала обработчик, затем метаданные, чтобы
		// файловый watcher при reload по .yaml уже видел готовый .os.
		files = []configFileEntry{
			{relPath: pageSrcRelPath(name), content: []byte(newPageOSSkeleton(name))},
			{relPath: path, content: []byte(content)},
		}
	}
	saveErr := saveConfigFiles(r, h, b, files)

	if saveErr != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Ошибка создания") + ": " + saveErr.Error()
		renderCfg(w, r, data)
		return
	}
	data := h.loadCfgData(r.Context(), b, "tree")
	data.FieldsSavedEntity = name
	data.FieldsSaved = true
	// issue #127: спозиционировать дерево на только что созданном объекте —
	// передаём точный data-id его узла, дальше клиент раскроет группу,
	// проскроллит и выделит его.
	data.SelectedTreeID = treeNodeID(kind, name)
	renderCfg(w, r, data)
}

// treeNodeID возвращает data-id узла дерева конфигуратора для (вид, имя). Набор
// префиксов обязан совпадать с разметкой дерева (configurator_tmpl.go) и
// перечнем видов в newObjectContent. Пустая строка — для видов без узла в дереве.
func treeNodeID(kind, name string) string {
	prefix := map[string]string{
		"catalog":    "e-",
		"document":   "e-",
		"register":   "r-",
		"inforeg":    "ir-",
		"accountreg": "ar-",
		"enum":       "en-",
		"subsystem":  "sub-",
		"widget":     "wdg-",
		"processor":  "proc-",
		"page":       "page-",
		"module":     "mod-",
	}[kind]
	if prefix == "" {
		return ""
	}
	return prefix + name
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
	case "page":
		return "pages", "name: " + name + "\ntitle: " + name + "\n"
	case "module":
		// Общий модуль — файл src/<имя>.module.os со стартовой экспортной
		// процедурой (расширение файла проставляется в configuratorNewObject).
		return "src", "// " + name + "\n// Общий модуль\n\nПроцедура Главная() Экспорт\nКонецПроцедуры\n"
	}
	return "", ""
}

// newPageOSSkeleton — стартовый обработчик src/<имя>.page.os для новой страницы
// (план 66): пустая процедура ПриФормировании с парой блоков-примеров, чтобы
// onebase check сразу проходил, а автор видел, куда писать.
func newPageOSSkeleton(name string) string {
	return "// Страница " + name + " (план 66) — произвольное представление на встроенном языке.\n" +
		"// Обработчик наполняет построитель «Страница» структурными блоками.\n\n" +
		"Процедура ПриФормировании(Страница, Параметры) Экспорт\n" +
		"    Страница.Заголовок(\"" + name + "\");\n" +
		"    Страница.Абзац(\"Произвольное представление на встроенном языке.\");\n" +
		"КонецПроцедуры\n"
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

	type enumValueOut struct {
		Name   string            `yaml:"name"`
		Titles map[string]string `yaml:"titles,omitempty"`
	}
	var values []any
	for i := 0; i < 500; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("value.%d.name", i)))
		if name == "" {
			continue
		}
		titles := parseMapForm(r, fmt.Sprintf("value.%d.titles", i))
		if len(titles) == 0 {
			values = append(values, name)
		} else {
			values = append(values, enumValueOut{Name: name, Titles: titles})
		}
	}

	type saveEnumOut struct {
		Name   string `yaml:"name"`
		Values []any  `yaml:"values"`
	}
	out, _ := yaml.Marshal(saveEnumOut{Name: enumName, Values: values})

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			path := "enums/" + nameToFilename(enumName) + ".yaml"
			saveErr = cfgUpsert(r.Context(), db, path, out)
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
	// Разрядность числовой константы number(L,P) — как у реквизитов сущности.
	typ = numberTypeWithSpec(typ, r.FormValue("length"), r.FormValue("scale"))

	type rawConst struct {
		Name    string            `yaml:"name"`
		Type    string            `yaml:"type"`
		Default string            `yaml:"default,omitempty"`
		Label   string            `yaml:"label,omitempty"`
		Labels  map[string]string `yaml:"labels,omitempty"`
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
				if formHasMapField(r, "labels") {
					cf.Constants[i].Labels = parseMapForm(r, "labels")
				}
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
					saveErr = cfgUpsert(r.Context(), db, targetPath, out)
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
		Name   string            `yaml:"name"`
		Type   string            `yaml:"type"`
		Label  string            `yaml:"label,omitempty"`
		Labels map[string]string `yaml:"labels,omitempty"`
	}
	type saveReport struct {
		Name        string              `yaml:"name"`
		Title       string              `yaml:"title,omitempty"`
		Params      []saveParam         `yaml:"params,omitempty"`
		Query       string              `yaml:"query"`
		ChartProc   string              `yaml:"chart_proc,omitempty"`
		Composition *report.Composition `yaml:"composition,omitempty"`
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
		newParams = append(newParams, saveParam{
			Name:   pname,
			Type:   ptype,
			Label:  plabel,
			Labels: parseMapForm(r, fmt.Sprintf("param.%d.labels", i)),
		})
	}

	// Переводы объекта: вычисляем до updateReportFile — нужен гейт, чтобы
	// отличить «форма не имела блока переводов» (AvailableLangs пуст) от
	// «пользователь очистил все переводы». Только во втором случае ключ titles: удаляется.
	var newTitles map[string]string
	hasTitlesBlock := formHasMapField(r, "titles")
	if hasTitlesBlock {
		newTitles = parseMapForm(r, "titles")
	}

	// Правим только редактируемые в форме ключи прямо в дереве YAML, чтобы не
	// терять прочие поля отчёта — многоязычные titles и любые будущие (раньше
	// round-trip через типизированную saveReport стирал titles, issue #86).
	updateReportFile := func(raw []byte) ([]byte, error) {
		var root yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
		if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("updateReportFile: ожидалось YAML-отображение в корне отчёта")
		}
		doc := root.Content[0]
		if err := setYAMLMapField(doc, "query", query); err != nil {
			return nil, err
		}
		if title != "" { // пустой title не трогаем — сохраняем существующий
			if err := setYAMLMapField(doc, "title", title); err != nil {
				return nil, err
			}
		}
		if hasTitlesBlock {
			if err := setYAMLMapField(doc, "titles", anyOrNil(newTitles)); err != nil {
				return nil, err
			}
		}
		var cp any
		if chartProc != "" {
			cp = chartProc // пусто → ключ удаляется (как omitempty)
		}
		if err := setYAMLMapField(doc, "chart_proc", cp); err != nil {
			return nil, err
		}
		var pv any
		if len(newParams) > 0 {
			pv = newParams
		}
		if err := setYAMLMapField(doc, "params", pv); err != nil {
			return nil, err
		}
		if c, present := parseCompositionForm(r.Form); present {
			var cv any
			if c != nil {
				cv = c
			}
			if err := setYAMLMapField(doc, "composition", cv); err != nil {
				return nil, err
			}
		}
		return yaml.Marshal(&root)
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
					saveErr = cfgUpsert(r.Context(), db, targetPath, out)
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
		saveErr = h.writeConfigFileRaw(r.Context(), b, "src/"+repName+".rep.os", []byte(chartSource))
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
	if !validObjectName(moduleName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя модуля")
		renderCfg(w, r, data)
		return
	}

	filename := moduleNameToFilename(moduleName)

	var saveErr error
	if b.ConfigSource == "database" {
		db, err := OpenDB(r.Context(), b)
		if err != nil {
			saveErr = err
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, "src/"+filename, []byte(source))
		}
	} else {
		saveErr = h.writeConfigFileRaw(r.Context(), b, "src/"+filename, []byte(source))
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
	if !validObjectName(procName) {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя обработки")
		renderCfg(w, r, data)
		return
	}

	type saveParam struct {
		Name   string            `yaml:"name"`
		Type   string            `yaml:"type"`
		Label  string            `yaml:"label,omitempty"`
		Labels map[string]string `yaml:"labels,omitempty"`
	}
	type saveProcessor struct {
		Name   string            `yaml:"name"`
		Title  string            `yaml:"title,omitempty"`
		Titles map[string]string `yaml:"titles,omitempty"`
		Params []saveParam       `yaml:"params,omitempty"`
	}

	var newParams []saveParam
	for i := 0; i < 50; i++ {
		pname := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.name", i)))
		if pname == "" {
			break
		}
		ptype := r.FormValue(fmt.Sprintf("param.%d.type", i))
		plabel := strings.TrimSpace(r.FormValue(fmt.Sprintf("param.%d.label", i)))
		newParams = append(newParams, saveParam{
			Name: pname, Type: ptype, Label: plabel,
			Labels: parseMapForm(r, fmt.Sprintf("param.%d.labels", i)),
		})
	}

	// Переводы заголовка: гейт отличает «блок переводов не отрендерен» (не трогаем
	// существующее) от «пользователь очистил все переводы» (удаляем ключ titles).
	var newTitles map[string]string
	hasTitlesBlock := formHasMapField(r, "titles")
	if hasTitlesBlock {
		newTitles = parseMapForm(r, "titles")
	}

	// Round-trip node-edit (issue #3): правим только редактируемые в форме ключи
	// прямо в дереве YAML, чтобы не терять отсутствующие в форме блоки обработки —
	// table_parts и поля default/options параметров. Усечённый yaml.Marshal
	// типизированной saveProcessor молча их стирал (потеря данных). Параметры
	// сливаем по имени с существующими узлами, сохраняя их default/options.
	//
	// procMapField возвращает узел-значение ключа в mapping-узле (или nil).
	procMapField := func(m *yaml.Node, key string) *yaml.Node {
		if m == nil || m.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				return m.Content[i+1]
			}
		}
		return nil
	}
	// procSetSeq устанавливает (или удаляет при seq==nil) ключ key со значением-
	// последовательностью seq прямо в mapping-узле, сохраняя порядок прочих ключей.
	procSetSeq := func(m *yaml.Node, key string, seq *yaml.Node) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				if seq == nil {
					m.Content = append(m.Content[:i], m.Content[i+2:]...)
					return
				}
				m.Content[i+1] = seq
				return
			}
		}
		if seq != nil {
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: key}, seq)
		}
	}
	updateProcessorFile := func(raw []byte) ([]byte, error) {
		var root yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return nil, err
		}
		if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("updateProcessorFile: ожидалось YAML-отображение в корне обработки")
		}
		doc := root.Content[0]
		if title != "" { // пустой title не трогаем — сохраняем существующий
			if err := setYAMLMapField(doc, "title", title); err != nil {
				return nil, err
			}
		}
		if hasTitlesBlock {
			if err := setYAMLMapField(doc, "titles", anyOrNil(newTitles)); err != nil {
				return nil, err
			}
		}
		// Существующие узлы параметров по имени (lowercase) — для сохранения
		// неотображаемых в форме ключей (default/options).
		existing := map[string]*yaml.Node{}
		if old := procMapField(doc, "params"); old != nil && old.Kind == yaml.SequenceNode {
			for _, pn := range old.Content {
				if name := procMapField(pn, "name"); name != nil {
					existing[strings.ToLower(name.Value)] = pn
				}
			}
		}
		var paramSeq *yaml.Node
		if len(newParams) > 0 {
			paramSeq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, p := range newParams {
				node := existing[strings.ToLower(p.Name)]
				if node == nil {
					node = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
					if err := setYAMLMapField(node, "name", p.Name); err != nil {
						return nil, err
					}
				}
				if err := setYAMLMapField(node, "type", p.Type); err != nil {
					return nil, err
				}
				var label any
				if p.Label != "" {
					label = p.Label
				}
				if err := setYAMLMapField(node, "label", label); err != nil {
					return nil, err
				}
				if err := setYAMLMapField(node, "labels", anyOrNil(p.Labels)); err != nil {
					return nil, err
				}
				paramSeq.Content = append(paramSeq.Content, node)
			}
		}
		procSetSeq(doc, "params", paramSeq)
		return yaml.Marshal(&root)
	}

	yamlFilename := "processors/" + nameToFilename(procName) + ".yaml"
	srcFilename := "src/" + processorSrcFilename(procName)

	// Готовим YAML: round-trip из существующего файла (сохраняет table_parts и
	// прочее), либо полная сборка для новой обработки (терять нечего).
	buildYAML := func(raw []byte) []byte {
		if len(raw) > 0 {
			if out, err := updateProcessorFile(raw); err == nil {
				return out
			}
		}
		out, _ := yaml.Marshal(saveProcessor{
			Name: procName, Title: title, Params: newParams, Titles: newTitles,
		})
		return out
	}

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			existingYAML, _, _ := configdb.New(db).ReadFile(r.Context(), yamlFilename)
			yamlData := buildYAML(existingYAML)
			if err := saveConfigFiles(r, h, b, []configFileEntry{
				{relPath: yamlFilename, content: yamlData},
				{relPath: srcFilename, content: []byte(source)},
			}); err != nil {
				saveErr = err
			}
		}
	} else {
		existingYAML, _ := h.readConfigFileRaw(r.Context(), b, yamlFilename)
		yamlData := buildYAML(existingYAML)
		saveErr = saveConfigFiles(r, h, b, []configFileEntry{
			{relPath: yamlFilename, content: yamlData},
			{relPath: srcFilename, content: []byte(source)},
		})
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
	relPath := "printforms/" + filename

	if filename == "" {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Имя файла печатной формы не указано")
		renderCfg(w, r, data)
		return
	}
	if err := configdb.ValidatePath(relPath); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя файла печатной формы") + ": " + err.Error()
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
			saveErr = cfgUpsert(r.Context(), db, relPath, []byte(source))
		}
	} else {
		saveErr = h.writeConfigFileRaw(r.Context(), b, relPath, []byte(source))
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
	relPath := "printforms/" + filename
	if err := configdb.ValidatePath(relPath); err != nil {
		data := h.loadCfgData(r.Context(), b, "tree")
		data.Error = tr(lang, "Недопустимое имя файла печатной формы") + ": " + err.Error()
		renderCfg(w, r, data)
		return
	}

	source := fmt.Sprintf("name: %s\ndocument: %s\ntitle: \"{{Номер}} от {{Дата | date}}\"\n\nheader: |\n  ## %s\n\ntable:\n  source: Товары\n  columns:\n    - field: \"@row\"\n      label: \"№\"\n      width: 36px\n      align: center\n", name, document, name)

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, relPath, []byte(source))
		}
	} else {
		fullPath, jerr := configdb.SafeJoin(b.Path, relPath)
		if jerr != nil {
			saveErr = jerr
		} else if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			saveErr = h.writeConfigFileRaw(r.Context(), b, relPath, []byte(source))
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
	if !validLayoutName(layoutName) {
		http.Error(w, "bad layout name", http.StatusBadRequest)
		return
	}

	filename := layoutName + ".layout.yaml"
	relPath := "printforms/" + filename

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			saveErr = cfgUpsert(r.Context(), db, relPath, []byte(source))
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
			layoutPath, saveErr = configdb.SafeJoin(b.Path, relPath)
		}
		if saveErr != nil {
			// keep saveErr for the common response below
		} else {
			saveErr = os.WriteFile(layoutPath, []byte(source), 0o644)
		}
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
	icon := ui.NormalizeIconName(r.FormValue("icon"))
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
		if formHasMapField(r, "titles") {
			ys.Titles = parseMapForm(r, "titles")
		} else {
			ys.Titles = existing.Titles
		}
		if existing.HomePage != nil {
			ys.HomePage = &yamlHomePage{
				Title:   existing.HomePage.Title,
				Titles:  existing.HomePage.Titles,
				Layout:  existing.HomePage.Layout,
				Widgets: existing.HomePage.Widgets,
			}
		}
	} else if formHasMapField(r, "titles") {
		ys.Titles = parseMapForm(r, "titles")
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
	newAuthor := strings.TrimSpace(r.FormValue("app_author"))
	newCopyright := strings.TrimSpace(r.FormValue("app_copyright"))
	newLicense := strings.TrimSpace(r.FormValue("app_license"))
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

	// Handle uploaded logo file. The write itself is delayed so app.yaml and
	// logo changes become one config version in database mode.
	var (
		logoData      []byte
		hasLogoUpload bool
	)
	file, header, ferr := r.FormFile("app_logo_file")
	if ferr == nil {
		defer file.Close()
		// Read file content
		data, rerr := io.ReadAll(file)
		if rerr != nil {
			data := h.loadCfgData(r.Context(), b, "tree")
			data.Error = tr(lang, "Ошибка чтения логотипа") + ": " + rerr.Error()
			renderCfg(w, r, data)
			return
		}
		if len(data) > 2<<20 {
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
		logoData = data
		hasLogoUpload = true
	}

	type saveAppConfig struct {
		Name      string `yaml:"name"`
		Version   string `yaml:"version,omitempty"`
		Lang      string `yaml:"lang,omitempty"`
		Logo      string `yaml:"logo,omitempty"`
		Author    string `yaml:"author,omitempty"`
		Copyright string `yaml:"copyright,omitempty"`
		License   string `yaml:"license,omitempty"`
	}
	out, _ := yaml.Marshal(saveAppConfig{
		Name: newName, Version: newVersion, Lang: newLang, Logo: logoPath,
		Author: newAuthor, Copyright: newCopyright, License: newLicense,
	})

	var saveErr error
	if b.ConfigSource == "database" {
		db, cerr := OpenDB(r.Context(), b)
		if cerr != nil {
			saveErr = cerr
		} else {
			defer db.Close()
			repo := configdb.New(db)
			if err := repo.EnsureSchema(r.Context()); err != nil {
				saveErr = err
			} else {
				saves := []configdb.ConfigFile{{Path: "config/app.yaml", Content: out}}
				if hasLogoUpload {
					saves = append([]configdb.ConfigFile{{Path: logoPath, Content: logoData}}, saves...)
				}
				var deletes []string
				if removeLogo && existingLogo != "" {
					deletes = append(deletes, existingLogo)
				}
				saveErr = repo.ApplyFiles(r.Context(), saves, deletes, configdb.VersionOptions{
					AuthorLogin: cfgLogin(r.Context()),
					Message:     "save app settings",
				})
			}
		}
	} else {
		if removeLogo && existingLogo != "" {
			full, err := configdb.SafeJoin(b.Path, existingLogo)
			if err != nil {
				saveErr = err
			} else if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				saveErr = err
			}
		}
		if saveErr == nil && hasLogoUpload {
			saveErr = h.writeConfigFileRaw(r.Context(), b, logoPath, logoData)
		}
		if saveErr == nil {
			saveErr = h.writeConfigFileRaw(r.Context(), b, "config/app.yaml", out)
		}
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

func saveInfoRegToFile(dir string, reg saveInfoReg, objTitles *map[string]string) error {
	p, err := findInfoRegFilePath(dir, reg.Name)
	if err != nil {
		return err
	}
	// Round-trip: читаем существующий файл, чтобы сохранить поля, которые
	// форма не редактирует. Titles объекта перезаписываем только если форма
	// прислала блок переводов (objTitles != nil).
	if raw, rerr := os.ReadFile(p); rerr == nil {
		var existing saveInfoReg
		if yaml.Unmarshal(raw, &existing) == nil {
			reg.Title = existing.Title
			if objTitles == nil {
				reg.Titles = existing.Titles
			}
		}
	}
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}

func (h *handler) saveInfoRegToDB(ctx context.Context, b *Base, reg saveInfoReg, objTitles *map[string]string) error {
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
		var existing saveInfoReg
		if yaml.Unmarshal(content, &existing) == nil && existing.Name == reg.Name {
			targetPath = p
			// Round-trip: сохраняем поля, которые форма не редактирует.
			// Titles перезаписываем только если форма прислала блок переводов.
			reg.Title = existing.Title
			if objTitles == nil {
				reg.Titles = existing.Titles
			}
			break
		}
	}
	rows.Close()
	if targetPath == "" {
		return fmt.Errorf("inforeg %q not found in DB config", reg.Name)
	}
	if objTitles != nil {
		reg.Titles = *objTitles
	}
	out, err := yaml.Marshal(&reg)
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
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
	reg := saveInfoReg{
		Name:       r.FormValue("inforeg"),
		Periodic:   r.FormValue("periodic") == "true",
		Dimensions: parseRegSection(r, "dim"),
		Resources:  parseRegSection(r, "res"),
	}
	var objTitles *map[string]string
	if formHasMapField(r, "titles") {
		t := parseMapForm(r, "titles")
		objTitles = &t
	}
	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveInfoRegToDB(r.Context(), b, reg, objTitles)
	} else {
		saveErr = saveInfoRegToFile(b.Path, reg, objTitles)
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

func saveAccountRegToFile(dir string, reg saveAccountReg, setTitles bool) error {
	p, err := findAccountRegFilePath(dir, reg.Name)
	if err != nil {
		// новый файл — subconto/titles сохранять неоткуда, marshal свежего reg
		os.MkdirAll(filepath.Join(dir, "accountregs"), 0o755)
		p = filepath.Join(dir, "accountregs", nameToFilename(reg.Name)+".yaml")
		out, merr := yaml.Marshal(&reg)
		if merr != nil {
			return merr
		}
		return os.WriteFile(p, out, 0o644)
	}
	raw, rerr := os.ReadFile(p)
	if rerr != nil {
		return rerr
	}
	out, merr := applyAccountRegFields(raw, reg, setTitles)
	if merr != nil {
		return merr
	}
	return os.WriteFile(p, out, 0o644)
}

func (h *handler) saveAccountRegToDB(ctx context.Context, b *Base, reg saveAccountReg, setTitles bool) error {
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
	var existingContent []byte
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
			existingContent = content
			break
		}
	}
	rows.Close()
	// Сохраняем subconto/titles из существующей записи через node-редактирование;
	// для новой записи marshal свежего reg.
	var out []byte
	if len(existingContent) > 0 {
		out, err = applyAccountRegFields(existingContent, reg, setTitles)
	} else {
		out, err = yaml.Marshal(&reg)
	}
	if err != nil {
		return err
	}
	return cfgUpsert(ctx, db, targetPath, out)
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
	reg := saveAccountReg{
		Name:      r.FormValue("accountreg"),
		Title:     strings.TrimSpace(r.FormValue("title")),
		Accounts:  strings.TrimSpace(r.FormValue("accounts")),
		Resources: parseRegSection(r, "res"),
	}
	setTitles := formHasMapField(r, "titles")
	if setTitles {
		reg.Titles = parseMapForm(r, "titles")
	}
	var saveErr error
	if b.ConfigSource == "database" {
		saveErr = h.saveAccountRegToDB(r.Context(), b, reg, setTitles)
	} else {
		saveErr = saveAccountRegToFile(b.Path, reg, setTitles)
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
	return cfgUpsert(ctx, db, targetPath, out)
}

// ── one-time code proxy ──────────────────────────────────────────────────────

// oneTimeCodeProxy запрашивает у процесса базы одноразовый bootstrap-код для
// текущей сессии (план 53): конфигуратор больше не вшивает сессионный токен в
// URL пользовательского режима (?_tk=) — JS дёргает этот эндпоинт (same-origin,
// без CORS) и открывает /auth/bootstrap?code=<одноразовый>.
func (h *handler) oneTimeCodeProxy(w http.ResponseWriter, r *http.Request) {
	b, err := h.store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "base not found"})
		return
	}
	if !h.cfgAdminAuthorized(r, b) {
		writeJSON(w, 401, map[string]string{"error": "Требуется вход администратора"})
		return
	}
	cookie, err := r.Cookie("onebase_session")
	if err != nil || cookie.Value == "" {
		// Нет сессии пользовательского режима — клиент откроет /ui без bootstrap.
		writeJSON(w, 200, map[string]string{"code": ""})
		return
	}

	url := fmt.Sprintf("http://localhost:%d/auth/one-time-code", b.Port)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, nil)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	req.AddCookie(&http.Cookie{Name: "onebase_session", Value: cookie.Value})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "UI server unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
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
