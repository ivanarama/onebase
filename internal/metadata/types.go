package metadata

import "strings"

type Kind string

const (
	KindCatalog  Kind = "catalog"
	KindDocument Kind = "document"
)

type FieldType string

const (
	FieldTypeString   FieldType = "string"
	FieldTypeDate     FieldType = "date"
	FieldTypeNumber   FieldType = "number"
	FieldTypeBool     FieldType = "bool"
	FieldTypeRichText FieldType = "richtext"
	// FieldTypeImage — поле-картинка. В колонке хранится ссылка (UUID бинарника
	// в хранилище), сам файл лежит на диске или в БД (см. storage blob backend).
	// Отдаётся отдельным HTTP-обработчиком; показывается превью на форме и плитке.
	FieldTypeImage FieldType = "image"
)

type Field struct {
	Name string
	// Title — синоним поля по умолчанию (показывается в UI). Пустой Title →
	// в интерфейсе используется Name.
	Title string
	// Titles — переводы синонима по языкам (lang code → перевод).
	Titles    map[string]string
	Type      FieldType
	RefEntity string // non-empty when Type starts with "reference:"
	EnumName  string // non-empty when Type starts with "enum:"
	// Length и Scale задают разрядность числового реквизита (Type=number),
	// по аналогии с 1С (Длина, Точность) и SQL NUMERIC(precision, scale).
	// Length — всего знаков, Scale — знаков после запятой. Непустые только
	// когда тип задан как "number(L,P)" / "decimal(L,P)". 0,0 = без ограничения.
	Length int
	Scale  int
	// AllowInlineCreate управляет показом кнопки «+» (создать новый элемент
	// справочника, не покидая формы) у ссылочного поля. nil = дефолт по
	// контексту: для полей шапки true, для полей ТЧ false. Переопределяется
	// в metadata YAML (`allow_inline_create: true/false`). Для не-ref полей
	// игнорируется. Управляемая форма может перекрыть дефолт на уровне
	// элемента формы (см. FormElement.AllowInlineCreate).
	AllowInlineCreate *bool
}

// InlineCreateEnabled — итоговое значение «показывать ли «+»» для поля.
// inTablePart=true означает, что поле принадлежит табличной части (дефолт
// false), иначе шапка (дефолт true). Используется шаблонами рендера формы.
func (f Field) InlineCreateEnabled(inTablePart bool) bool {
	if f.AllowInlineCreate != nil {
		return *f.AllowInlineCreate
	}
	return !inTablePart
}

// DisplayName возвращает представление поля для интерфейса: Titles[lang] →
// Title → Name. Name всегда остаётся идентификатором (БД, URL, форма).
func (f Field) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := f.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if f.Title != "" {
		return f.Title
	}
	return f.Name
}

type Enum struct {
	Name        string
	Values      []string                     // имена значений (идентификаторы)
	ValueTitles map[string]map[string]string // value → lang → перевод
}

// ValueTitle — перевод значения для интерфейса: Titles[lang] → само имя.
// Name остаётся идентификатором (БД/форма).
func (e *Enum) ValueTitle(value, lang string) string {
	if lang != "" {
		if v, ok := e.ValueTitles[value][lang]; ok && v != "" {
			return v
		}
	}
	return value
}

type Constant struct {
	Name      string
	Type      FieldType
	RefEntity string
	EnumName  string
	Default   string
	Label     string
	Labels    map[string]string // переводы подписи по языкам
	Length    int               // разрядность для number(L,P), см. Field
	Scale     int
}

// DisplayLabel возвращает подпись константы с учётом языка.
func (c *Constant) DisplayLabel(lang string) string {
	if lang != "" {
		if v, ok := c.Labels[lang]; ok && v != "" {
			return v
		}
	}
	if c.Label != "" {
		return c.Label
	}
	return c.Name
}

type TablePart struct {
	Name   string
	Title  string
	Titles map[string]string
	Fields []Field
}

// DisplayName возвращает представление табличной части для интерфейса.
func (tp TablePart) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := tp.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if tp.Title != "" {
		return tp.Title
	}
	return tp.Name
}

// Numerator describes automatic document numbering.
type Numerator struct {
	Prefix string // e.g. "ПОС-"
	Length int    // digits in numeric part, padded with leading zeros
	Period string // "year" | "month" | "none"
	// Scope — имя поля документа, значение которого включается в ключ
	// нумерации. Например, scope: "Организация" даст отдельные счётчики
	// для каждой организации.
	Scope string
}

// PredefinedItem describes a catalog record that is always present in the DB
// and cannot be deleted. Synced from YAML on every startup.
type PredefinedItem struct {
	Name   string         // identifier used in DSL: ПредопределённыеЗначения.Валюта.Рубль
	Fields map[string]any // initial field values
}

const (
	ActivityScopeActive   = "active"
	ActivityScopeInactive = "inactive"
	ActivityScopeAll      = "all"
)

// ActivityConfig describes opt-in catalog activity semantics. The referenced
// bool field remains a normal requisit; the platform only standardizes list
// scopes and reference-choice filtering when this block is configured.
type ActivityConfig struct {
	Field          string
	DefaultScope   string
	HideFromChoice bool
}

type Entity struct {
	Name string
	// Title — человекочитаемое представление (аналог «Синонима» в 1С).
	// Если пусто, в интерфейсе показывается Name.
	Title string
	// Titles — переводы синонима по языкам (lang code → перевод). Если для
	// активного языка есть запись, используется она; иначе откатываемся на
	// Title и затем на Name. Пустой map допустим.
	Titles     map[string]string
	Kind       Kind
	Fields     []Field
	TableParts []TablePart
	// Posting enables 1C-style posting semantics: movements are written only
	// when the document is explicitly posted, not on every save.
	Posting       bool
	Numerator     *Numerator        // nil if auto-numbering is disabled
	Predefined    []*PredefinedItem // nil for most entities; populated from YAML
	Hierarchical  bool              // catalog with parent_id / is_folder tree support
	HierarchyKind string            // "folders_and_items" (default) | "items_only"
	ListForm      []string          // visible fields in list form (nil = all)
	ItemForm      []string          // visible fields in item form (nil = all)
	Forms         []*FormModule     // form modules (object form, list form, custom forms)
	// BasedOn — типы источников, на основании которых разрешено вводить эту
	// сущность (аналог «Вводится на основании» в 1С). Имена сущностей —
	// catalog или document. Проверяются Validate. Пустой/nil — ввод на
	// основании запрещён.
	BasedOn []string
	// Activity — opt-in механизм активности справочника. Nil означает, что
	// справочник не получает специальных фильтров и скрытия из выбора.
	Activity *ActivityConfig
	// ListMode — режим загрузки списка по умолчанию: "" / "pages" (постранично)
	// или "feed" (лента с догрузкой по скроллу, как динамический список 1С).
	// Пользователь может переопределить тумблером (запоминается per-сущность).
	ListMode string
	// TileView задаёт пользовательскую компоновку плитки списка. Nil означает
	// старое автоправило: картинка из image-поля, заголовок из первого поля,
	// остальные реквизиты ниже.
	TileView *TileView
}

// TileView описывает, какие реквизиты использовать в плиточной карточке списка.
// Имена ссылаются на поля Entity.Fields.
type TileView struct {
	Image    string
	Title    string
	Subtitle string
	Fields   []string
	// FieldsSet отличает отсутствующий ключ fields от явного fields: [].
	FieldsSet bool
}

// Виды регистра накопления (план 74). Балансовый (остатки) — по умолчанию;
// оборотный нельзя сворачивать в остаток, поэтому свёртка его не предлагает.
const (
	RegisterKindBalance  = "balance"
	RegisterKindTurnover = "turnover"
)

type Register struct {
	Name   string
	Title  string
	Titles map[string]string
	// Kind — вид регистра: "" / RegisterKindBalance (остатки, по умолчанию) или
	// RegisterKindTurnover (обороты). Оборотный регистр накапливает обороты за
	// период; свернуть его в один остаток нельзя — свёртка (план 74) его минует.
	Kind       string
	Dimensions []Field // form the grouping key for balances
	Resources  []Field // accumulated (summed with sign based on movement type)
	Attributes []Field // extra data, stored but not aggregated
}

// IsTurnover сообщает, что регистр оборотный (его нельзя сворачивать в остаток).
func (r *Register) IsTurnover() bool {
	return r.Kind == RegisterKindTurnover
}

// DisplayName возвращает заголовок регистра накопления с учётом языка.
func (r *Register) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := r.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

type InfoRegister struct {
	Name       string
	Title      string
	Titles     map[string]string
	Periodic   bool    // if true, (period, dim...) is PK; otherwise just (dim...)
	Dimensions []Field // key fields
	Resources  []Field // value fields
}

// DisplayName возвращает заголовок регистра сведений с учётом языка.
func (ir *InfoRegister) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := ir.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if ir.Title != "" {
		return ir.Title
	}
	return ir.Name
}

func RegisterTableName(regName string) string {
	return "рег_" + strings.ToLower(regName)
}

func InfoRegTableName(regName string) string {
	return "инфо_" + strings.ToLower(regName)
}

func TablePartTableName(entityName, tpName string) string {
	return strings.ToLower(entityName) + "_" + strings.ToLower(tpName)
}

// DisplayName возвращает представление объекта для интерфейса с учётом языка:
// сначала пробуется Titles[lang], затем Title (синоним по умолчанию), затем
// Name. Пустой lang пропускает первый шаг — используется как Name всегда
// остаётся идентификатором (URL, DSL).
func (e *Entity) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := e.Titles[lang]; ok && v != "" {
			return v
		}
	}
	if e.Title != "" {
		return e.Title
	}
	return e.Name
}

func IsReference(ft FieldType) bool {
	return strings.HasPrefix(string(ft), "reference:")
}

func RefName(ft FieldType) string {
	return strings.TrimPrefix(string(ft), "reference:")
}

func IsEnum(ft FieldType) bool {
	return strings.HasPrefix(string(ft), "enum:")
}

// IsRichText сообщает, что поле хранит форматированный HTML (тип richtext).
// Такие поля санитизируются на записи и выводе и не допускаются в табличных
// частях (см. Validate).
func IsRichText(ft FieldType) bool {
	return ft == FieldTypeRichText
}

// IsImage сообщает, что поле хранит картинку (тип image). В колонке лежит ссылка
// на бинарник; файл раздаётся отдельным обработчиком, на формах/плитке — превью.
func IsImage(ft FieldType) bool {
	return ft == FieldTypeImage
}

func EnumTypeName(ft FieldType) string {
	return strings.TrimPrefix(string(ft), "enum:")
}

func TableName(entityName string) string {
	return strings.ToLower(entityName)
}

func ColumnName(f Field) string {
	col := strings.ToLower(f.Name)
	if f.RefEntity != "" {
		return col + "_id"
	}
	return col
}
