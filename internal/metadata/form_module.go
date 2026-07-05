package metadata

import (
	"fmt"
	"strings"
	"sync"
)

// FormEventType represents types of form events (1C-style)
type FormEventType string

const (
	FormEventOnOpen         FormEventType = "ПриОткрытии"        // OnOpen (client-side, after render)
	FormEventOnReadAtServer FormEventType = "ПриЧтенииНаСервере" // OnReadAtServer (server-side, before render → может запретить чтение)
	FormEventBeforeWrite    FormEventType = "ПередЗаписью"       // BeforeWrite
	FormEventOnWrite        FormEventType = "ПриЗаписи"          // OnWrite
	FormEventAfterWrite     FormEventType = "ПослеЗаписи"        // AfterWrite
	FormEventBeforeClose    FormEventType = "ПередЗакрытием"     // BeforeClose
	FormEventOnClose        FormEventType = "ПриЗакрытии"        // OnClose
	FormEventOnActivate     FormEventType = "ПриАктивации"       // OnActivate
	FormEventItemChoice     FormEventType = "ОбработкаВыбора"    // ItemChoice
	FormEventStartChoice    FormEventType = "НачалоВыбора"       // StartChoice
	FormEventOnChange       FormEventType = "ПриИзменении"       // OnChange
	FormEventOnCreate       FormEventType = "ПриСоздании"        // OnCreate
	FormEventBeforeDelete   FormEventType = "ПередУдалением"     // BeforeDelete
	FormEventOnDelete       FormEventType = "ПриУдалении"        // OnDelete
	FormEventAfterDelete    FormEventType = "ПослеУдаления"      // AfterDelete
	// События для табличных частей (замечание #15): даём YAML-конфигу
	// возможность их объявлять. UI дёргает их generic-маршрутом через
	// handleManagedFormEvent (resolveHandlerProc + interp.Run) — кастомные
	// триггеры (auto-fill цены при добавлении/удалении строки и т.п.)
	// реализуются в конфиге пользователя.
	FormEventOnRowAdded     FormEventType = "ПриДобавленииСтроки"  // OnRowAdded
	FormEventOnRowChanged   FormEventType = "ПриИзмененииСтроки"   // OnRowChanged
	FormEventOnRowDeleted   FormEventType = "ПриУдаленииСтроки"    // OnRowDeleted
	FormEventOnRowActivated FormEventType = "ПриАктивизацииСтроки" // OnRowActivated
	// События, добавленные для управляемых форм (план 37). Используются
	// конвертером 1С и редактором; интерпретатор вызывает их по той же
	// generic-схеме что и существующие row-события.
	FormEventOnClick         FormEventType = "Нажатие"                // OnClick
	FormEventBeforeRowAdd    FormEventType = "ПередДобавлениемСтроки" // BeforeAddRow
	FormEventAfterRowAdd     FormEventType = "ПослеДобавленияСтроки"  // AfterAddRow
	FormEventBeforeRowDelete FormEventType = "ПередУдалениемСтроки"   // BeforeDeleteRow
	FormEventStartListChoice FormEventType = "НачалоВыбораИзСписка"   // StartListChoice
	FormEventAutoComplete    FormEventType = "АвтоПодбор"             // AutoComplete
	FormEventExecuteCommand  FormEventType = "ВыполнитьКоманду"       // Command
	// Выбор — вторая фаза диалога подбора (план 46): обработчик кнопки
	// показывает диалог через билтин ПоказатьПодбор (фаза 1, событие Нажатие),
	// а результат пользователь возвращает событием Выбор с переменной
	// ПодборРезультат. Generic: годится для любого диалога мультивыбора.
	FormEventOnChoice FormEventType = "Выбор" // OnChoice
)

// FormElementType represents types of form elements
type FormElementType string

const (
	FormElementField      FormElementType = "ПолеВвода"      // InputField
	FormElementLabel      FormElementType = "Надпись"        // Label
	FormElementButton     FormElementType = "Кнопка"         // Button
	FormElementTable      FormElementType = "Таблица"        // Table
	FormElementGroupBox   FormElementType = "ГруппаФормы"    // FormGroup
	FormElementPage       FormElementType = "Страница"       // Page
	FormElementPages      FormElementType = "СтраницыФормы"  // FormPages
	FormElementCheckbox   FormElementType = "Флажок"         // Checkbox
	FormElementSwitch     FormElementType = "Переключатель"  // Switch
	FormElementInputList  FormElementType = "ПолеСписка"     // InputList
	FormElementDatePicker FormElementType = "ПолеДаты"       // DateField
	FormElementFormField  FormElementType = "ПолеФормы"      // FormField
	FormElementTablePart  FormElementType = "ТабличнаяЧасть" // TablePart
	// Управляемые формы (план 37): дополнения для покрытия XML 1С и UI-редактора.
	FormElementColumn           FormElementType = "Колонка"         // Column (Table inner)
	FormElementCommandBar       FormElementType = "КоманднаяПанель" // CommandBar
	FormElementPicture          FormElementType = "ПолеКартинки"    // PictureField
	FormElementCommandBarButton FormElementType = "КнопкаКП"        // CommandBar Button
)

// Layout kinds of a form module. Пустая строка трактуется как "autogen"
// ради backward-compat — все ранее загруженные .form.os формы остаются
// в авто-генерируемом представлении.
const (
	FormLayoutAutogen = "autogen"
	FormLayoutManaged = "managed"
)

// FormElement represents a single form element (field, button, etc.)
type FormElement struct {
	ID   string          `yaml:"id,omitempty"`
	Name string          `yaml:"name,omitempty"`
	Kind FormElementType `yaml:"kind,omitempty"`
	// Title — legacy строковый заголовок. В managed-формах используется
	// TitleMap (поле yaml:"title"); это поле не сериализуется в YAML
	// (тег "-"), и заполняется ToFormModule из TitleMap.Get("ru") как
	// fallback для legacy-рендерера.
	Title     string                   `yaml:"-"`
	FieldName string                   `yaml:"field,omitempty"`
	TablePart string                   `yaml:"table_part,omitempty"`
	Visible   bool                     `yaml:"visible,omitempty"`
	Enabled   bool                     `yaml:"enabled,omitempty"`
	Required  bool                     `yaml:"required,omitempty"`
	Handlers  map[FormEventType]string `yaml:"events,omitempty"`
	Props     map[string]any           `yaml:"props,omitempty"`
	Children  []*FormElement           `yaml:"children,omitempty"`

	// Поля, добавленные планом 37. Все опциональны; YAML-загрузчик
	// заполняет их при чтении managed-формы, конвертер 1С использует
	// для round-trip, рендерер — для отрисовки HTML.
	OriginalID      string            `yaml:"original_id,omitempty"`    // id из Form.xml для round-trip
	TitleMap        map[string]string `yaml:"title,omitempty"`          // локализованный заголовок (ru, en, ...) — основной источник в managed-YAML
	DataPath        string            `yaml:"data_path,omitempty"`      // "Объект.Контрагент", "Список.Цена"
	Picture         string            `yaml:"picture,omitempty"`        // "_resources/.../Picture.png" или "stdpic:Post"
	ValuesPicture   string            `yaml:"values_picture,omitempty"` // палитра выбора (для PictureField/InputField)
	Width           int               `yaml:"width,omitempty"`          // ширина в условных единицах
	Height          int               `yaml:"height,omitempty"`         // высота
	HorizontalAlign string            `yaml:"halign,omitempty"`         // left|center|right|stretch
	VerticalAlign   string            `yaml:"valign,omitempty"`         // top|center|bottom
	Orientation     string            `yaml:"orientation,omitempty"`    // vertical|horizontal для контейнеров
	ReadOnly        bool              `yaml:"readonly,omitempty"`       // только чтение
	UseGrid         bool              `yaml:"use_grid,omitempty"`       // (устар.) SlickGrid теперь включён по умолчанию
	NoGrid          bool              `yaml:"no_grid,omitempty"`        // отключить SlickGrid у ТЧ (вернуть простую таблицу)
	AutoSum         bool              `yaml:"auto_sum,omitempty"`       // ТЧ: авто Сумма = Количество × Цена по именам колонок — opt-in (#215.1)
	Hint            string            `yaml:"hint,omitempty"`           // всплывающая подсказка
	Mask            string            `yaml:"mask,omitempty"`           // маска ввода
	// Format/DisplayFormat распознаются, но для kind: ПолеДаты НЕ применяются в
	// рантайме: нативный <input type=date> показывает дату по локали браузера, а
	// значение всегда ISO (issue #219). Поля заведены, чтобы onebase check мог
	// явно предупредить об этом, а не молча проглатывать неизвестный ключ.
	Format        string `yaml:"format,omitempty"`
	DisplayFormat string `yaml:"display_format,omitempty"`
	Type          string `yaml:"type,omitempty"`   // "file" для файлового поля, и т.п.
	Choice        bool   `yaml:"choice,omitempty"` // включена кнопка выбора у InputField
	// Choices — декларативный список значений для выбора (аналог 1С СписокВыбора).
	// Задаётся в .form.yaml на элементе kind: ПолеСписка; рендерер показывает
	// <select> с этими значениями, а выбор дёргает событие ПриИзменении.
	Choices    []FormChoice `yaml:"choices,omitempty"`
	UnknownXML []byte       `yaml:"unknown_xml,omitempty"` // экзотический XML, сохраняется как есть

	// Набор значений Переключателя/ПолеСписка (план 71b, batch C1). Options —
	// явный список значение→представление; для enum-поля рантайм берёт значения
	// из перечисления автоматически и Options можно не задавать. View управляет
	// представлением: "radio" (по умолчанию) или "select".
	Options []FormOption `yaml:"options,omitempty"`
	View    string       `yaml:"view,omitempty"`
}

// FormChoice — один пункт списка значений элемента ПолеСписка. Value хранится
// в реквизите (строкой), Title — локализованная подпись (ru, en, ...) по образцу
// Enum.ValueTitles; если перевода нет, в качестве подписи используется Value.
type FormChoice struct {
	Value string            `yaml:"value"`
	Title map[string]string `yaml:"title,omitempty"`
}

// ChoiceLabel возвращает подпись пункта для указанного языка с откатом на Value.
func (c FormChoice) ChoiceLabel(lang string) string {
	if c.Title != nil {
		if t, ok := c.Title[lang]; ok && t != "" {
			return t
		}
		if t, ok := c.Title["ru"]; ok && t != "" {
			return t
		}
	}
	return c.Value
}

// FormOption — один элемент набора значений Переключателя/ПолеСписка (C1).
// Value хранит значение под тип поля (число/строка); Label — локализованное
// представление (ключ "ru" и т.п., как TitleMap у элементов).
type FormOption struct {
	Value  any               `yaml:"value"`
	Labels map[string]string `yaml:"label,omitempty"`
}

// ValueStr возвращает значение опции как строку (для атрибута value у radio/
// option и для сравнения с текущим значением формы, которое всегда строковое).
func (o FormOption) ValueStr() string {
	if o.Value == nil {
		return ""
	}
	return fmt.Sprintf("%v", o.Value)
}

// Label возвращает представление опции: ru-локаль → любая непустая локаль →
// строковое значение как fallback.
func (o FormOption) Label() string {
	if o.Labels != nil {
		if v := o.Labels["ru"]; v != "" {
			return v
		}
		for _, v := range o.Labels {
			if v != "" {
				return v
			}
		}
	}
	return o.ValueStr()
}

// FormAction — переопределение стандартного действия формы объекта (issue #151).
type FormAction struct {
	// Visible — показывать ли кнопку действия. nil = поведение по умолчанию
	// (для delete — по праву CanDelete). false = скрыть кнопку.
	Visible *bool `yaml:"visible,omitempty"`
}

// FormCondRule описывает декларативное условное оформление строк и ячеек
// табличных частей managed-формы. Target — имя табличной части/ValueTable или
// элемента формы. Пустой Field означает оформление всей строки.
type FormCondRule struct {
	When   string
	Target string
	Field  string
	Style  FormCellStyle
}

// FormCellStyle — безопасное подмножество CSS-оформления ячейки формы.
type FormCellStyle struct {
	Color      string
	Background string
	Bold       bool
	Italic     bool
}

// FormModule represents a form module with event handlers
type FormModule struct {
	EntityName string                    `yaml:"entity,omitempty"`
	Name       string                    `yaml:"name,omitempty"`
	Kind       string                    `yaml:"kind,omitempty"` // "object", "list", "choice", "folder", "custom"
	Elements   []*FormElement            `yaml:"elements,omitempty"`
	Handlers   map[FormEventType]string  `yaml:"events,omitempty"`
	Procedures map[string]*FormProcedure `yaml:"-"`

	// Actions — переопределение стандартных действий формы объекта (issue #151).
	// Пока поддерживается ключ "delete": actions.delete.visible=false скрывает
	// платформенную кнопку «Удалить», чтобы конфиг мог увести удаление в свой
	// процессор. Платформенное удаление и так пишется в _audit и закрыто правом
	// delete — это про управление UI-кнопкой.
	Actions map[string]*FormAction `yaml:"actions,omitempty"`

	// Conditional — декларативное условное оформление табличных частей формы.
	// YAML-загрузчик принимает ключи conditional и conditional_formatting.
	Conditional []FormCondRule `yaml:"conditional,omitempty"`

	// Поля, добавленные планом 37 для управляемых форм.
	LayoutKind             string            `yaml:"layout_kind,omitempty"`      // "managed"|"autogen" (пусто=autogen)
	Title                  map[string]string `yaml:"title,omitempty"`            // локализованный заголовок формы
	OriginalID             string            `yaml:"original_id,omitempty"`      // id корневого узла из 1С
	Attributes             []*FormAttribute  `yaml:"attributes,omitempty"`       // реквизиты формы
	Commands               []*FormCommand    `yaml:"commands,omitempty"`         // команды формы
	AutoCommandBar         *FormCommandBar   `yaml:"auto_command_bar,omitempty"` // авто-командная панель
	AutoSaveDataInSettings bool              `yaml:"auto_save_data_in_settings,omitempty"`
	VerticalScroll         string            `yaml:"vertical_scroll,omitempty"` // auto|never|always
	// OneCMeta — служебный блок, используемый только конвертером 1С,
	// рантайм его игнорирует. Может содержать version, unknown_xml и т.п.
	OneCMeta map[string]any `yaml:"oneC_meta,omitempty"`

	// ProgramAST — распарсенный AST модуля .form.os (тип *dsl/ast.Program).
	// Хранится через any, чтобы пакет metadata не зависел от пакета ast
	// (избегаем циклической зависимости). Заполняется FormLoader при
	// загрузке модуля; рантайм событий формы достаёт отсюда конкретные
	// *ast.ProcedureDecl по имени для запуска через interp.Run(...).
	// Если поле nil — обработчики событий не запускаются (loader не
	// сохранил AST или модуль не загружен).
	ProgramAST any `yaml:"-"`

	// idCounter — приватный счётчик для GenerateID. Стартует с 10000,
	// чтобы новые id из редактора не пересекались с диапазоном 1С.
	idCounter int        `yaml:"-"`
	idMu      sync.Mutex `yaml:"-"`
}

// FormProcedure represents a procedure in form module
type FormProcedure struct {
	Name     string          `yaml:"name,omitempty"`
	Params   []FormProcParam `yaml:"params,omitempty"`
	Body     string          `yaml:"body,omitempty"`
	IsExport bool            `yaml:"export,omitempty"`
}

// FormProcParam represents a procedure parameter
type FormProcParam struct {
	Name string `yaml:"name,omitempty"`
	Type string `yaml:"type,omitempty"`
}

// FormAttribute — реквизит формы (живёт только в форме, отдельно от полей сущности).
// При импорте из 1С строится из Form.xml/Attributes.
type FormAttribute struct {
	ID            string                 `yaml:"id,omitempty"`          // ID из IR, опционально
	OriginalID    string                 `yaml:"original_id,omitempty"` // id из 1С
	Name          string                 `yaml:"name"`
	Title         map[string]string      `yaml:"title,omitempty"`
	TypeRef       string                 `yaml:"type"` // "string(40)", "decimal(15,2)", "CatalogRef.Контрагенты", "ValueTable"
	Length        int                    `yaml:"length,omitempty"`
	Precision     int                    `yaml:"precision,omitempty"`
	AllowedLength string                 `yaml:"allowed_length,omitempty"` // "Variable"|"Fixed"
	Save          bool                   `yaml:"save,omitempty"`
	FillingValue  string                 `yaml:"filling_value,omitempty"`
	MainAttribute bool                   `yaml:"main,omitempty"`    // соответствует <MainAttribute>true</MainAttribute>
	Columns       []*FormAttributeColumn `yaml:"columns,omitempty"` // для ValueTable
	Props         map[string]any         `yaml:"props,omitempty"`
}

// FormAttributeColumn — колонка реквизита-таблицы (ValueTable).
type FormAttributeColumn struct {
	ID         string            `yaml:"id,omitempty"`
	OriginalID string            `yaml:"original_id,omitempty"`
	Name       string            `yaml:"name"`
	Title      map[string]string `yaml:"title,omitempty"`
	TypeRef    string            `yaml:"type"`
	Length     int               `yaml:"length,omitempty"`
	Precision  int               `yaml:"precision,omitempty"`
	Props      map[string]any    `yaml:"props,omitempty"`
}

// FormCommand — команда формы (соответствует <Commands>/<Command> из 1С).
type FormCommand struct {
	ID         string            `yaml:"id,omitempty"`
	OriginalID string            `yaml:"original_id,omitempty"`
	Name       string            `yaml:"name"`
	Title      map[string]string `yaml:"title,omitempty"`
	Group      string            `yaml:"group,omitempty"`   // form_command_bar|...
	Picture    string            `yaml:"picture,omitempty"` // "_resources/..." или "stdpic:Post"
	Action     string            `yaml:"action,omitempty"`  // имя процедуры-обработчика
	Props      map[string]any    `yaml:"props,omitempty"`
}

// FormCommandBar — описание авто-командной панели формы.
type FormCommandBar struct {
	ID         string                  `yaml:"id,omitempty"`
	OriginalID string                  `yaml:"original_id,omitempty"`
	Name       string                  `yaml:"name,omitempty"`
	Visible    bool                    `yaml:"visible,omitempty"`
	Buttons    []*FormCommandBarButton `yaml:"buttons,omitempty"`
}

// FormCommandBarButton — кнопка в командной панели.
type FormCommandBarButton struct {
	ID             string            `yaml:"id,omitempty"`
	OriginalID     string            `yaml:"original_id,omitempty"`
	Name           string            `yaml:"name"`
	Title          map[string]string `yaml:"title,omitempty"`
	CommandName    string            `yaml:"command,omitempty"`        // ссылка на FormCommand.Name
	Representation string            `yaml:"representation,omitempty"` // PictureAndText|Picture|Text
	Picture        string            `yaml:"picture,omitempty"`
}

// EventHandlerInfo contains information about event handler
type EventHandlerInfo struct {
	ElementName string        // element name (empty for form-level events)
	EventType   FormEventType // event type
	ProcName    string        // procedure name to call
}

// GetEventHandler returns handler for element event
func (fm *FormModule) GetEventHandler(elementName string, eventType FormEventType) (string, bool) {
	// First check element handlers
	if elementName != "" {
		for _, el := range fm.Elements {
			if handler := findElementHandler(el, eventNameToID(elementName), eventType); handler != "" {
				return handler, true
			}
		}
	}
	// Then check form-level handlers
	if fm.Handlers != nil {
		if handler, ok := fm.Handlers[eventType]; ok {
			return handler, true
		}
	}
	return "", false
}

// findElementHandler recursively searches for element handler
func findElementHandler(el *FormElement, elementID string, eventType FormEventType) string {
	if el.ID == elementID {
		if el.Handlers != nil {
			if handler, ok := el.Handlers[eventType]; ok {
				return handler
			}
		}
		return ""
	}
	for _, child := range el.Children {
		if handler := findElementHandler(child, elementID, eventType); handler != "" {
			return handler
		}
	}
	return ""
}

// eventNameToID converts element name to ID format
func eventNameToID(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

// GetElementByName finds element by name
func (fm *FormModule) GetElementByName(name string) *FormElement {
	for _, el := range fm.Elements {
		if el := findElementByName(el, name); el != nil {
			return el
		}
	}
	return nil
}

func findElementByName(el *FormElement, name string) *FormElement {
	if el.Name == name {
		return el
	}
	for _, child := range el.Children {
		if found := findElementByName(child, name); found != nil {
			return found
		}
	}
	return nil
}

// StandardFormNames returns standard form names for 1C compatibility
func StandardFormNames() []string {
	return []string{
		"ФормаОбъекта", // ObjectForm
		"ФормаСписка",  // ListForm
		"ФормаВыбора",  // ChoiceForm
		"ФормаГруппы",  // FolderForm (for hierarchical catalogs)
	}
}

// IsStandardForm checks if form name is standard
func IsStandardForm(name string) bool {
	for _, std := range StandardFormNames() {
		if strings.EqualFold(name, std) {
			return true
		}
	}
	return false
}

// FormModuleFileName returns .os filename for form module
func FormModuleFileName(entityName, formName string) string {
	base := strings.ToLower(entityName)
	if formName != "" && !IsStandardForm(formName) {
		return base + "_" + strings.ToLower(formName) + ".form.os"
	}
	return base + ".form.os"
}

// ObjectFormFileName returns .os filename for object form
func ObjectFormFileName(entityName string) string {
	return strings.ToLower(entityName) + ".form.os"
}

// IsManaged возвращает true для управляемой формы (LayoutKind=managed).
// Пустой LayoutKind трактуется как autogen — backward-compat для существующих
// форм, загружаемых из src/*.form.os.
func (fm *FormModule) IsManaged() bool {
	return fm != nil && fm.LayoutKind == FormLayoutManaged
}

// GenerateID выдаёт стабильный id для новых элементов формы.
// Счётчик стартует с 10000 чтобы не пересекаться с диапазоном id 1С (обычно <10000).
func (fm *FormModule) GenerateID() string {
	fm.idMu.Lock()
	defer fm.idMu.Unlock()
	if fm.idCounter < 10000 {
		fm.idCounter = 10000
	}
	fm.idCounter++
	return fmt.Sprintf("fm-%d", fm.idCounter)
}

// FindByID находит элемент в дереве по идентификатору (ID или OriginalID).
func (fm *FormModule) FindByID(id string) *FormElement {
	if fm == nil || id == "" {
		return nil
	}
	for _, el := range fm.Elements {
		if found := findElementByID(el, id); found != nil {
			return found
		}
	}
	return nil
}

func findElementByID(el *FormElement, id string) *FormElement {
	if el == nil {
		return nil
	}
	if el.ID == id || el.OriginalID == id {
		return el
	}
	for _, child := range el.Children {
		if found := findElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

// Walk вызывает fn для каждого элемента дерева в порядке pre-order.
// Если fn возвращает false, обход поддерева прерывается.
func (fm *FormModule) Walk(fn func(*FormElement) bool) {
	if fm == nil || fn == nil {
		return
	}
	for _, el := range fm.Elements {
		walkElement(el, fn)
	}
}

func walkElement(el *FormElement, fn func(*FormElement) bool) {
	if el == nil {
		return
	}
	if !fn(el) {
		return
	}
	for _, child := range el.Children {
		walkElement(child, fn)
	}
}

// IsContainer возвращает true для элементов, способных содержать дочерние.
// Используется UI-редактором и рендерером для рекурсивной отрисовки.
func (el *FormElement) IsContainer() bool {
	if el == nil {
		return false
	}
	switch el.Kind {
	case FormElementGroupBox,
		FormElementPage,
		FormElementPages,
		FormElementTable,
		FormElementTablePart,
		FormElementCommandBar:
		return true
	}
	return false
}
