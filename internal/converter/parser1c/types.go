package parser1c

// CatalogMeta — справочник из Metadata.xml
type CatalogMeta struct {
	Name       string
	Synonym    string
	Attributes []Attribute
	Forms      []string // пропускаются при конвертации
}

// DocumentMeta — документ из Metadata.xml
type DocumentMeta struct {
	Name            string
	Synonym         string
	Attributes      []Attribute
	TabularSections []TabularSection
	Forms           []string
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
	Type    FieldType1C
}

// FieldType1C — тип реквизита в формате 1С
type FieldType1C struct {
	// Основной тип, если один
	Primary string
	// Ссылочный тип: имя объекта (справочника/документа) без префикса
	RefObject string
	// Истина если тип составной (несколько вариантов)
	Composite bool
	// Имена всех типов при составном
	AllTypes []string
}

// ConfigDump — всё содержимое выгрузки конфигурации
type ConfigDump struct {
	Catalogs   []*CatalogMeta
	Documents  []*DocumentMeta
	Registers  []*RegisterMeta
	SkippedDirs []SkippedItem
}

// SkippedItem — объект, который не конвертируется
type SkippedItem struct {
	Kind string // Enumerations, ChartOfAccounts, etc.
	Name string
}
