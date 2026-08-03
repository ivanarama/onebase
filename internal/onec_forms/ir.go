package onec_forms

// IR — нейтральное промежуточное представление управляемой формы.
// К нему сводятся оба формата: 1С Form.xml (через reader_xml.go) и
// OneBase .form.yaml (через reader_yaml.go). Обратно — writer_xml.go и
// writer_yaml.go соответственно.
//
// Структуры намеренно «широкие»: одно поле IRElement покрывает все
// типы элементов через Kind + Props. Это упрощает round-trip без потерь:
// неузнанные поля XML складываются в Props или UnknownXML.

// IRForm — корневой узел IR.
type IRForm struct {
	Name                   string  // имя формы ("ФормаОбъекта", "Форма")
	Kind                   string  // object|list|choice|folder|custom
	Entity                 string  // имя сущности, к которой привязана форма
	Title                  IRTitle // локализованный заголовок
	OriginalID             string  // id корневого узла из Form.xml (round-trip)
	Version                string  // <Form version="2.20">
	AutoSaveDataInSettings bool    // <AutoSaveDataInSettings>
	VerticalScroll         string  // "auto"|"never"|"always"
	AutoCommandBar         *IRCommandBar
	Attributes             []*IRAttribute
	Commands               []*IRCommand
	Parameters             []*IRParameter
	Elements               []*IRElement      // дерево ChildItems
	Events                 map[string]string // form-level events (1С имя → процедура)
	Resources              []IRResource      // бинарные файлы из Items/
	// UnknownTopLevel — XML-узлы верхнего уровня, не имеющие IR-семантики
	// (ДКС, Conditional Appearance, расширения). Хранятся «как есть» для
	// round-trip; сериализуются в oneC_meta.unknown_xml в YAML.
	UnknownTopLevel []*IRUnknownXML
}

// IRElement — элемент формы (поле/таблица/группа/страница/кнопка/…).
// Kind хранит каноническое имя из 1С (InputField, Table, UsualGroup, Page, …)
// либо OneBase-имя (ПолеВвода, Таблица, …) — нормализация в mapping_in/out.
type IRElement struct {
	ID         string  // внутренний id (мб совпадать с OriginalID)
	OriginalID string  // id из Form.xml
	Name       string  // имя элемента (как в XML attribute name=)
	Kind       string  // тип элемента (см. elements_map.go)
	Title      IRTitle // локализованный заголовок
	DataPath   string  // "Объект.Контрагент", "Список.Цена"
	Picture    string  // относительный путь к ресурсу или "stdpic:Post"
	Values     string  // для PictureField — ValuesPicture
	Visible    bool
	Enabled    bool
	Required   bool
	ReadOnly   bool
	Choice     bool
	Width      int
	Height     int
	HAlign     string
	VAlign     string
	Hint       string
	Mask       string
	Events     map[string]string // 1С имя события → имя процедуры
	Props      map[string]any    // прочие свойства элемента (TitleLocation, EditMode, …)
	Children   []*IRElement
	UnknownXML []byte // экзотический XML, не разобранный в IR (round-trip)
}

// IRAttribute — реквизит формы (Form.xml / Attributes).
type IRAttribute struct {
	ID            string
	OriginalID    string
	Name          string
	Title         IRTitle
	TypeRef       string // канонический тип в нейтральной нотации, см. types_map.go
	Length        int    // для строк/чисел
	Precision     int    // для decimal
	AllowedLength string // "Variable"|"Fixed"
	Save          bool
	FillingValue  string
	MainAttribute bool
	Columns       []*IRAttributeColumn // для ValueTable
	Props         map[string]any
}

// IRAttributeColumn — колонка ValueTable-реквизита.
type IRAttributeColumn struct {
	ID         string
	OriginalID string
	Name       string
	Title      IRTitle
	TypeRef    string
	Length     int
	Precision  int
	Props      map[string]any
}

// IRCommand — команда формы (Form.xml / Commands).
type IRCommand struct {
	ID         string
	OriginalID string
	Name       string
	Title      IRTitle
	Group      string
	Picture    string
	Action     string // имя процедуры
	Props      map[string]any
}

// IRCommandBar — командная панель формы.
type IRCommandBar struct {
	ID         string
	OriginalID string
	Name       string
	Visible    bool
	Buttons    []*IRCommandBarButton
}

// IRCommandBarButton — кнопка в командной панели.
type IRCommandBarButton struct {
	ID             string
	OriginalID     string
	Name           string
	Title          IRTitle
	CommandName    string
	Representation string
	Picture        string
	Props          map[string]any
}

// IRParameter — параметр формы (Form.xml / Parameters).
type IRParameter struct {
	ID           string
	OriginalID   string
	Name         string
	TypeRef      string
	KeyParameter bool
	Props        map[string]any
}

// IRResource — бинарный файл из Forms/<Form>/Ext/Form/Items/<ElementName>/.
// При импорте Data заполнен (для копирования в проект OneBase);
// при экспорте берётся из проекта по Path.
type IRResource struct {
	ElementName  string // папка в Items/
	Path         string // относительный путь от .form.yaml (например "_resources/Логотип/Picture.png")
	OriginalName string // оригинальное имя файла в Items/ (Picture.png, ValuesPicture.png, ...)
	Data         []byte // содержимое (опционально — может загружаться лениво)
}

// IRUnknownXML — фрагмент XML, не разобранный в IR.
type IRUnknownXML struct {
	OwnerElement string // имя элемента-владельца или "" для top-level
	XML          []byte // сырой XML «как есть»
}

// IRTitle — локализованный заголовок: map "ru" → "Контрагент".
// В 1С формате — это <v8:item><v8:lang>ru</v8:lang>…</v8:item> повторами.
type IRTitle map[string]string

// Get возвращает значение по lang, либо первое непустое, либо пустую строку.
func (t IRTitle) Get(lang string) string {
	if t == nil {
		return ""
	}
	if v, ok := t[lang]; ok && v != "" {
		return v
	}
	for _, v := range t {
		if v != "" {
			return v
		}
	}
	return ""
}

// ImportReport / ExportReport — структуры результата фасадных функций.
// Содержат списки предупреждений и пути созданных файлов.
type ImportReport struct {
	YAMLPath     string
	ModulePath   string
	ResourcesDir string
	Warnings     []Warning
}

type ExportReport struct {
	FormDir  string // Forms/<FormName>/Ext/
	Warnings []Warning
}
