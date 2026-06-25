package launcher

import (
	"context"
	"fmt"
	"html/template"

	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/i18n"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/storage"
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

type saveActivity struct {
	Field          string `yaml:"field"`
	DefaultScope   string `yaml:"default_scope,omitempty"`
	HideFromChoice *bool  `yaml:"hide_from_choice,omitempty"`
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
	Activity      *saveActivity     `yaml:"activity,omitempty"`
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

type cfgActivity struct {
	Field          string
	DefaultScope   string
	HideFromChoice bool
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
	Activity         *cfgActivity
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
