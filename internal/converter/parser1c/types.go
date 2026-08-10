package parser1c

// Numbering — свойства кода справочника или номера документа из выгрузки 1С.
// Раньше не читались вовсе и терялись молча (план 117, Д8).
type Numbering struct {
	Auto        bool   // Autonumbering
	Length      int    // CodeLength / NumberLength
	Type        string // CodeType / NumberType: String | Number
	CheckUnique bool   // контроль уникальности кода
	Periodicity string // NumberPeriodicity: Year | Month | Day | Nonperiodical
}

// CatalogMeta — справочник из Metadata.xml
type CatalogMeta struct {
	Name            string
	Synonym         string
	Attributes      []Attribute
	TabularSections []TabularSection
	Forms           []FormSource // управляемые формы объекта (Forms/<X>/Ext/Form.xml)
	Hierarchical    bool
	Code            Numbering
}

// DocumentMeta — документ из Metadata.xml
type DocumentMeta struct {
	Name            string
	Synonym         string
	Attributes      []Attribute
	TabularSections []TabularSection
	Forms           []FormSource
	Posting         bool
	Number          Numbering
}

// FormSource — управляемая форма объекта, найденная в выгрузке 1С.
// Импортируется отдельным шагом через пакет onec_forms.
type FormSource struct {
	Entity   string // имя объекта-владельца OneBase (справочник/документ/обработка)
	FormName string // имя формы (имя каталога Forms/<FormName>)
	ExtDir   string // путь к каталогу Ext (содержит Form.xml, Form/Module.bsl, Form/Items)
}

// RegisterMeta — регистр накопления из Metadata.xml
type RegisterMeta struct {
	Name       string
	Synonym    string
	Dimensions []Attribute
	Resources  []Attribute
	Attributes []Attribute
}

// TabularSection — табличная часть документа
type TabularSection struct {
	Name       string
	Synonym    string
	Attributes []Attribute
}

// Attribute — реквизит (поле)
type Attribute struct {
	Name    string
	Synonym string
	Type    FieldType
}

// FieldType — тип реквизита в формате 1С
type FieldType struct {
	// Основной тип, если один
	Primary string
	// Ссылочный тип: имя объекта (справочника/документа) без префикса
	RefObject string
	// Истина если тип составной (несколько вариантов)
	Composite bool
	// Имена всех типов при составном
	AllTypes []string
}

// EnumMeta — перечисление
type EnumMeta struct {
	Name    string
	Synonym string
	Values  []string
}

// ConstantMeta — константа
type ConstantMeta struct {
	Name    string
	Synonym string
	Type    FieldType
}

// InfoRegMeta — регистр сведений
type InfoRegMeta struct {
	Name       string
	Synonym    string
	Periodic   bool
	Dimensions []Attribute
	Resources  []Attribute
	Attributes []Attribute
}

// AccountRegMeta — регистр бухгалтерии
type AccountRegMeta struct {
	Name       string
	Synonym    string
	Dimensions []Attribute
	Resources  []Attribute
	Attributes []Attribute
}

// ChartOfAccountsMeta — план счетов
type ChartOfAccountsMeta struct {
	Name       string
	Synonym    string
	Attributes []Attribute
}

// ScheduledJobMeta — регламентное задание
type ScheduledJobMeta struct {
	Name     string
	Synonym  string
	Schedule string
	Handler  string
}

// ModuleMeta — общий модуль
type ModuleMeta struct {
	Name   string
	Source string
}

// ProcessorMeta — обработка
type ProcessorMeta struct {
	Name       string
	Synonym    string
	Attributes []Attribute
	Source     string
	Forms      []FormSource // управляемые формы объекта (Forms/<X>/Ext/Form.xml)
}

// ConfigDump — всё содержимое выгрузки конфигурации
type ConfigDump struct {
	Catalogs         []*CatalogMeta
	Documents        []*DocumentMeta
	Registers        []*RegisterMeta
	Enums            []*EnumMeta
	Constants        []*ConstantMeta
	InfoRegisters    []*InfoRegMeta
	AccountRegisters []*AccountRegMeta
	ChartsOfAccounts []*ChartOfAccountsMeta
	ScheduledJobs    []*ScheduledJobMeta
	Modules          []*ModuleMeta
	Processors       []*ProcessorMeta
	SkippedDirs      []SkippedItem
}

// SkippedItem — объект, который не конвертируется
type SkippedItem struct {
	Kind string // Enumerations, ChartOfAccounts, etc.
	Name string
}
