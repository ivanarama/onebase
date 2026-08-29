package metadata

import (
	"fmt"
	"strings"
)

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
	// ID — устойчивый идентификатор поля (план 81), необязательный.
	//
	// Имя поля меняется при переименовании, а ID — нет; по нему миграция
	// понимает, что колонку надо переименовать, а не заводить новую пустую.
	// Поле без ID мигрирует как раньше, аддитивно: переименование для него
	// по-прежнему выглядит как «удалить старое + добавить новое», то есть
	// данные остаются в осиротевшей колонке.
	ID   string
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
	// PII — реквизит содержит персональные данные (`pii: true`). Признак живёт
	// у ПОЛЯ, а не только в ролях: правило маскирования объявляет роль, но роль,
	// которая про поле промолчала, должна получить маску, а не полное значение.
	// Иначе новый реквизит с телефоном открыт всем, пока про него не вспомнили в
	// каждой роли по отдельности. Полное значение остаётся доступным по явному
	// `read: full` в роли (см. internal/access.FieldDecisions).
	//
	// Field — один тип для реквизита шапки, поля табличной части и поля
	// регистра, а признак работает не везде. Маскирование смотрит на поля
	// синтетической сущности, которую видит FieldDecisions: это шапка объекта
	// (Entity.Fields) и измерения/ресурсы/реквизиты регистров НАКОПЛЕНИЯ и
	// СВЕДЕНИЙ (storage.RegisterPredicateEntity, storage.InfoRegisterPredicateEntity).
	// У них закрыты обе границы чтения — и запросы, и собственные списки
	// регистра (#859, #767).
	//
	// Два места признак не исполняют, и в обоих он отвергается, а не
	// принимается молча:
	//   - поля табличных частей — field_access их не адресует вовсе, маскировать
	//     их платформа не умеет (отказ выдаёт Validate);
	//   - ресурсы и субконто регистра БУХГАЛТЕРИИ — предикатную сущность
	//     FieldDecisions видит, но списки проводок и остатков /ui/accountreg/*
	//     не маскируют полей ни по умолчанию, ни по явному правилу роли, и
	//     закрытое в отчёте значение осталось бы открытым на соседней странице
	//     (отказ выдаёт LoadAccountRegisterFile).
	//
	// Маркер, который по умолчанию не защищает, был бы просто комментарием, а
	// комментарий в поле с телефоном читается как гарантия.
	PII bool
	// Default — значение по умолчанию при СОЗДАНИИ нового объекта (план 153),
	// сырой строкой из YAML: литерал, `сегодня`/`сейчас`, `константа.<Имя>`,
	// `текущийпользователь`, `единственный`. Разбор и проверка — ParseDefault
	// и ValidateDefaults (defaults.go), применение —
	// entityservice.ApplyDefaults. Пустая строка = дефолта нет.
	Default string
	// Required — реквизит обязателен к заполнению (`required: true` в YAML).
	//
	// Раньше ключ читался только у констант, а у реквизита сущности молча
	// игнорировался: конфигурация писала required, линт объявлял ключ
	// неизвестным, а обязательности не было (#1033). Проверяется при записи —
	// там же, где значения перечислений, — чтобы гарантия не зависела от того,
	// какой дверью пришли данные.
	Required bool
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
	// Required — значение обязательно к заполнению (проверяется сервером при
	// сохранении формы «Константы»). Задаётся `required: true` в YAML.
	Required bool
	Label    string
	Labels   map[string]string // переводы подписи по языкам
	Length   int               // разрядность для number(L,P), см. Field
	Scale    int
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
	// BasePrefix включает подстановку префикса ЭТОЙ базы (план 117D). Префикс
	// живёт в данных базы, а не в конфигурации: конфигурация одинакова во всех
	// базах, поэтому «понять, откуда загружено» через неё невозможно by design.
	BasePrefix bool
	// Unique требует уникальности значения (план 117E).
	Unique bool
}

// PeriodOrDefault возвращает период сброса счётчика с умолчанием по виду
// объекта: у документа — год (номера принято начинать заново), у справочника
// сброса нет — код элемента живёт с ним всю жизнь.
func (n *Numerator) PeriodOrDefault(kind Kind) string {
	if n == nil {
		return ""
	}
	if n.Period != "" {
		return n.Period
	}
	if kind == KindCatalog {
		return "none"
	}
	return "year"
}

// Стандартный «Код» справочника (план 117B). Имя фиксировано: по нему ищет
// НайтиПоКоду, на него ссылается представление и обмен. ID устойчив, чтобы
// миграция понимала переименование как переименование, а не как «удалить
// колонку и завести новую пустую».
const (
	StandardCodeField   = "Код"
	StandardCodeFieldID = "std_code"
	// «Номер» документа синтезируется той же логикой и по той же причине
	// нуждается в устойчивом ID (#868). Без него сценарий из пересмотра #668
	// заканчивался потерей данных: «Номер» объявлен в YAML с id, строку убрали
	// (numerator остался) → синтез без id → в PlanTableChanges карта wanted
	// строится только по полям с id, сторож коллизии молчит → планируется
	// ChangeDrop, и --allow-destructive сносит все номера документов.
	StandardNumberField   = "Номер"
	StandardNumberFieldID = "std_number"
)

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

// IndexSpec describes a DB index requested by configuration metadata.
// Fields are entity field names as written in YAML. Storage maps them to
// physical column names, including `_id` suffixes for references.
type IndexSpec struct {
	Fields []string
	Unique bool
}

type Entity struct {
	Name string
	// Title — человекочитаемое представление (аналог «Синонима» в 1С).
	// Если пусто, в интерфейсе показывается Name.
	Title string
	// Description — необязательное описание объекта для tooling/AI-карты.
	Description string
	// Titles — переводы синонима по языкам (lang code → перевод). Если для
	// активного языка есть запись, используется она; иначе откатываемся на
	// Title и затем на Name. Пустой map допустим.
	Titles     map[string]string
	Kind       Kind
	Fields     []Field
	TableParts []TablePart
	Indexes    []IndexSpec
	// Posting enables 1C-style posting semantics: movements are written only
	// when the document is explicitly posted, not on every save.
	Posting bool
	// PostCaption переопределяет подпись кнопки проведения («Провести») в
	// формах документа. Пусто → стандартная локализуемая «Провести». Вторая
	// кнопка получает подпись «<PostCaption> и закрыть». Issue #497.
	PostCaption string
	// PostAndCloseHidden скрывает кнопку «Провести и закрыть». По умолчанию
	// (false) обе кнопки видны, как прежде.
	PostAndCloseHidden bool
	Numerator          *Numerator        // nil if auto-numbering is disabled
	Predefined         []*PredefinedItem // nil for most entities; populated from YAML
	Hierarchical       bool              // catalog with parent_id / is_folder tree support
	HierarchyKind      string            // "folders_and_items" (default) | "items_only"
	ListForm           []string          // visible fields in list form (nil = all)
	// ItemForm — состав формы элемента: какие реквизиты видны и в каком
	// порядке (nil = все). Запись может быть помечена «только просмотр»
	// (#1011): служебный реквизит, который пересобирает модуль при записи,
	// показывать надо, а править в нём нечего.
	ItemForm []ItemFormField
	Forms    []*FormModule // form modules (object form, list form, custom forms)
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
	// ListRefreshOn — имена событий шины (план 87, ступень A), при получении
	// которых «живой список» перечитывается. Пусто = список статичный (как
	// сегодня). Событие данные.<lower(имя)> публикуется автоматически при
	// NotifyChanges; прочие имена шлёт конфигурация через ОтправитьУведомление.
	ListRefreshOn []string
	// NotifyChanges — opt-in автопубликации служебного события данные.<lower(имя)>
	// после успешной записи/проведения/отмены/удаления (row-aware адресация по
	// правам). Без него «живому списку» нечего слушать без ручного кода.
	NotifyChanges bool
	// TileView задаёт пользовательскую компоновку плитки списка. Nil означает
	// старое автоправило: картинка из image-поля, заголовок из первого поля,
	// остальные реквизиты ниже.
	TileView *TileView
	// Presentation — реквизиты, которыми объект представляется в списках,
	// пикерах, поиске, REST и DSL, в порядке предпочтения. Пусто — правило по
	// именам (LabelFields).
	//
	// Нужен там, где объект ПРИНЯТО представлять не наименованием: артикул
	// номенклатуры, табельный номер, шифр (#846). Правило по именам такому
	// справочнику помочь не может: если «Наименование» есть, оно и победит.
	Presentation []string

	// DetailPanel — состав боковой панели деталей (план 118C). Nil = автокомпоновка.
	DetailPanel *DetailPanel
	// FullText — реквизиты шапки, попадающие в полнотекстовый индекс (план 82).
	// Nil (блока нет в YAML) означает умолчание: все строковые реквизиты, см.
	// FullTextFields. Явный пустой список — объект исключён из глобального поиска.
	FullText []string
	// FullTextSet отличает отсутствующий ключ fulltext от явного «fulltext: []».
	FullTextSet bool
	// Search — реквизиты, по которым ищет строка поиска списка и подбор ссылки
	// (ввод по строке в ячейке ТЧ, форма выбора). Nil означает умолчание: все
	// строковые реквизиты, как было всегда, — см. SearchFields. Отдельно от
	// FullText: тот управляет ГЛОБАЛЬНЫМ индексом, и `fulltext: []` (объект вне
	// глобального поиска) не должен заодно ломать подбор в форме.
	Search []string
	// SearchSet отличает отсутствующий ключ search_fields от явного пустого
	// списка (поиск по строке для объекта выключен).
	SearchSet bool
	// Stages — этапы объекта (план 121): порядок состояний и допустимые
	// переходы на существующем поле-перечислении. Nil — сущность про этапы
	// ничего не знает и ведёт себя ровно как раньше.
	Stages *Stages
}

// ItemFormField — запись блока `item_form:` формы элемента. В YAML это либо
// строка (имя реквизита), либо `{name: X, readonly: true}` — «показывать, но
// не давать править» (#1011). До этого единственным способом защитить
// служебный реквизит от ручного ввода была managed-форма: ради одного-двух
// вычисляемых реквизитов приходилось переписывать в YAML всю форму целиком —
// все поля и все табличные части.
//
// ReadOnly — про интерфейс, а не про доступ: значение по-прежнему уезжает с
// формой при записи (иначе сборка полей обнулила бы реквизит — ровно так же,
// как у скрытых), а ограничение доступа делается политикой поля.
type ItemFormField struct {
	Name     string
	ReadOnly bool
}

// ItemFormNames — имена реквизитов блока item_form в объявленном порядке.
// Нужен всем, кому важен только состав: валидатору, describe, конфигуратору.
func (e *Entity) ItemFormNames() []string {
	if e == nil || len(e.ItemForm) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.ItemForm))
	for _, f := range e.ItemForm {
		names = append(names, f.Name)
	}
	return names
}

// SearchFields возвращает реквизиты, по которым идёт поиск подстроки в списке и
// в подборе ссылки. Без блока `search_fields:` это все строковые реквизиты
// шапки — историческое поведение, менять которое молча нельзя.
//
// Явный список снимает ограничение по типу: артикул или штрихкод часто хранят
// числом, и по умолчанию такой реквизит в поиск не попадал — ровно этого и не
// хватало, чтобы набирать позицию не по наименованию. Приведение к тексту берёт
// на себя диалект (LowerLike), поэтому отдельный CAST не нужен.
//
// Ссылочные реквизиты не участвуют никогда: в колонке лежит UUID, искать по нему
// подстроку бессмысленно (валидация такой список отклоняет).
func SearchFields(e *Entity) []Field {
	if e == nil {
		return nil
	}
	if e.SearchSet {
		out := make([]Field, 0, len(e.Search))
		for _, name := range e.Search {
			if f := findEntityFieldFold(e, name); f != nil && f.RefEntity == "" {
				out = append(out, *f)
			}
		}
		return out
	}
	var out []Field
	for _, f := range e.Fields {
		if f.Type == FieldTypeString && f.RefEntity == "" {
			out = append(out, f)
		}
	}
	return out
}

// FullTextFields возвращает реквизиты шапки, которые индексируются глобальным
// поиском (план 82). Без блока `fulltext:` в YAML это все строковые реквизиты —
// у документа сюда попадает и синтезированный Номер. Явный `fulltext: []`
// выключает объект из индекса, поэтому пустой результат — валидное состояние,
// а не «список не задан».
func FullTextFields(e *Entity) []Field {
	if e == nil {
		return nil
	}
	if e.FullTextSet {
		out := make([]Field, 0, len(e.FullText))
		for _, name := range e.FullText {
			if f := findEntityFieldFold(e, name); f != nil {
				out = append(out, *f)
			}
		}
		return out
	}
	var out []Field
	for _, f := range e.Fields {
		if f.Type == FieldTypeString {
			out = append(out, f)
		}
	}
	return out
}

// findEntityFieldFold ищет реквизит шапки по имени без учёта регистра: DSL и
// метаданные регистронезависимы, поэтому `fulltext: [наименование]` должен
// находить поле «Наименование».
func findEntityFieldFold(e *Entity, name string) *Field {
	if e == nil {
		return nil
	}
	for i := range e.Fields {
		if strings.EqualFold(e.Fields[i].Name, name) {
			return &e.Fields[i]
		}
	}
	return nil
}

// DetailPanel описывает состав боковой панели деталей списка (план 118C).
// Nil — автокомпоновка: все реквизиты шапки, картинки и размеченный текст на
// своих закладках. Блок задаёт СОСТАВ, а не факт включения: панель включает
// пользователь кнопкой, и молча показывать её всем нельзя.
type DetailPanel struct {
	Title string // реквизит-заголовок карточки; пусто — представление записи
	Width int    // ширина по умолчанию, px; 0 — 320
	// Fields — короткая форма без закладок, как tile_view.fields.
	Fields []string
	// FieldsSet отличает отсутствующий ключ fields от явного fields: [].
	FieldsSet bool
	Tabs      []DetailPanelTab
	// TabsSet отличает отсутствующий ключ tabs (автокомпоновка) от tabs: [].
	// Явная пустая раскладка не должна fail-open'ом показывать все поля.
	TabsSet bool
}

// DetailPanelTab — закладка панели с явным составом.
type DetailPanelTab struct {
	Name   string
	Titles map[string]string
	Fields []string
	// Reserved for plan 118D. Set flags preserve even explicit empty/false YAML
	// declarations so validation can reject unsupported promises instead of
	// silently ignoring them.
	TableParts     []string
	TablePartsSet  bool
	Attachments    bool
	AttachmentsSet bool
}

const (
	DetailPanelDefaultWidth = 320
	DetailPanelMinWidth     = 220
	DetailPanelMaxWidth     = 640
)

// DisplayName возвращает заголовок закладки с учётом языка.
func (t DetailPanelTab) DisplayName(lang string) string {
	if lang != "" {
		if v, ok := t.Titles[lang]; ok && v != "" {
			return v
		}
	}
	return t.Name
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

// Режимы гейта переходов между этапами (план 121).
const (
	// StageEnforceWarn — нарушение переходов пишется в лог и в `onebase check`,
	// но запись проходит. Умолчание: включение блока `stages` в работающей
	// конфигурации не должно ломать уже накопленные данные и обработки.
	StageEnforceWarn = "warn"
	// StageEnforceStrict — недопустимый переход отвергается на записи.
	StageEnforceStrict = "strict"
)

// Stages описывает этапы объекта (план 121): упорядоченные состояния на
// существующем поле-перечислении и допустимые переходы между ними. Нового вида
// объекта метаданных не заводится — значения берутся из перечисления, на
// которое ссылается Field, а блок задаёт только порядок и правила.
type Stages struct {
	// Field — имя поля-перечисления, ведущего этапы.
	Field string
	// Order — порядок этапов для отчёта и схемы. Первый этап считается
	// начальным: именно с него по умолчанию начинается маршрут объекта.
	Order []string
	// Transitions — допустимые переходы. Всё, чего в списке нет, запрещено.
	Transitions []StageTransition
	// DeadlineDays — сколько дней объект вправе висеть на этапе; этапы без
	// записи не имеют срока. Просрочка попадает в отчёт «где застряло».
	DeadlineDays map[string]int
	// Enforce — StageEnforceWarn (умолчание) или StageEnforceStrict.
	Enforce string
}

// StageTransition — разрешённые переходы из одного этапа.
type StageTransition struct {
	From string
	To   []string
}

// Strict сообщает, отвергается ли недопустимый переход на записи.
func (s *Stages) Strict() bool { return s != nil && s.Enforce == StageEnforceStrict }

// Initial возвращает начальный этап — первый в Order. Пусто, если порядок не
// задан.
func (s *Stages) Initial() string {
	if s == nil || len(s.Order) == 0 {
		return ""
	}
	return s.Order[0]
}

// Canonical приводит значение этапа к написанию из Order. Сравнение
// регистронезависимо (DSL регистронезависим, значение может прийти из формы,
// пакета обмена или запроса). Пустая строка — значение не объявлено.
func (s *Stages) Canonical(stage string) string {
	if s == nil {
		return ""
	}
	stage = strings.TrimSpace(stage)
	if stage == "" {
		return ""
	}
	for _, st := range s.Order {
		if strings.EqualFold(st, stage) {
			return st
		}
	}
	return ""
}

// Known сообщает, объявлен ли этап в Order.
func (s *Stages) Known(stage string) bool { return s.Canonical(stage) != "" }

// Allowed проверяет переход from → to.
//
// Пустой from — объект ещё не на маршруте (только создаётся либо реквизит не
// заполняли) — считается стоящим на НАЧАЛЬНОМ этапе. Иначе пришлось бы выбирать
// между двумя одинаково неверными крайностями: разрешить из пустого что угодно
// (тогда объект заводится сразу «Утверждённым» — ровно тот перескок, ради
// запрета которого всё и делается) или разрешить только начальный этап (тогда
// законное «пусто → НаСогласовании», с которого начинается согласование в
// прикладных конфигурациях, оказалось бы нарушением).
//
// Переход в то же значение — не переход.
func (s *Stages) Allowed(from, to string) bool {
	if s == nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(from), strings.TrimSpace(to)) {
		return true
	}
	if strings.TrimSpace(from) == "" {
		from = s.Initial()
		if strings.EqualFold(from, strings.TrimSpace(to)) {
			return true
		}
	}
	for _, tr := range s.Transitions {
		if !strings.EqualFold(tr.From, from) {
			continue
		}
		for _, t := range tr.To {
			if strings.EqualFold(t, to) {
				return true
			}
		}
	}
	return false
}

// Deadline возвращает срок этапа в днях; 0 — срока нет.
func (s *Stages) Deadline(stage string) int {
	if s == nil || len(s.DeadlineDays) == 0 {
		return 0
	}
	for st, d := range s.DeadlineDays {
		if strings.EqualFold(st, stage) {
			return d
		}
	}
	return 0
}

// StageField возвращает описание поля-этапа сущности (nil, если этапы не
// объявлены или поле не найдено).
func (e *Entity) StageField() *Field {
	if e == nil || e.Stages == nil {
		return nil
	}
	for i := range e.Fields {
		if strings.EqualFold(e.Fields[i].Name, e.Stages.Field) {
			return &e.Fields[i]
		}
	}
	return nil
}

// Виды регистра накопления (план 151). Балансовый (остатки) — по умолчанию;
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
	// период; свернуть его в один остаток нельзя — свёртка (план 151) его минует.
	Kind       string
	Dimensions []Field        // form the grouping key for balances
	Resources  []Field        // accumulated (summed with sign based on movement type)
	Attributes []Field        // extra data, stored but not aggregated
	Totals     RegisterTotals // предрасчёт итогов (план 80)
}

// RegisterTotals — настройки предрасчёта итогов регистра накопления (план 80).
// Enabled включает таблицу текущих итогов итоги_<рег>: чистый знаковый остаток
// ресурсов по каждому набору измерений, поддерживаемый в той же транзакции, что
// и движения (см. storage.WriteMovements). Ускоряет текущие Остатки() с
// O(все движения) до O(число комбинаций измерений). Периодические итоги (для
// Остатки(&Момент)/ОстаткиИОбороты) — следующий этап плана 80.
type RegisterTotals struct {
	Enabled bool
}

// IsTurnover сообщает, что регистр оборотный (его нельзя сворачивать в остаток).
func (r *Register) IsTurnover() bool {
	return r.Kind == RegisterKindTurnover
}

// TotalsEnabled сообщает, что итоги включены пользователем и регистр балансовый
// (оборотные остатков не имеют).
func (r *Register) TotalsEnabled() bool {
	return r.Totals.Enabled && !r.IsTurnover()
}

// TotalsUsable сообщает, применимы ли итоги к регистру на текущем этапе плана 80:
// включены, регистр балансовый и без атрибутов. Атрибуты (MIN(attr) в остатках)
// таблица итогов не хранит, поэтому регистр с атрибутами использует расчёт на
// лету — и итоги для него не ведутся, чтобы не платить за поддержку без пользы.
func (r *Register) TotalsUsable() bool {
	return r.TotalsEnabled() && len(r.Attributes) == 0
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
	Name     string
	Title    string
	Titles   map[string]string
	Periodic bool // if true, (period, dim...) is PK; otherwise just (dim...)
	// Recorder — регистр подчинён регистратору: записи в него делает проведение
	// документа (Движения.X.Добавить), а не прикладной код. Программная запись
	// такого регистра отклоняется: она разошлась бы с проведением, и ближайшее
	// перепроведение документа снесло бы её без предупреждения. Умолчание —
	// НЕЗАВИСИМЫЙ регистр: так ведут себя все существующие конфигурации, и
	// молчаливая смена смысла у них недопустима.
	Recorder   bool
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

// RegisterTotalsTableName — таблица предрасчитанных итогов регистра (план 80).
func RegisterTotalsTableName(regName string) string {
	return "итоги_" + strings.ToLower(regName)
}

// RegisterTotalsMonthCol — колонка месяц-ключа (YYYY-MM) в таблице итогов.
// Итоги хранят помесячный оборот; ключ совпадает по формату с
// time.Format("2006-01"), чтобы границу момента можно было вычислить в Go.
const RegisterTotalsMonthCol = "месяц"

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

// FieldSignature — диалект-независимая подпись типа поля (план 81).
//
// По ней миграция понимает, что тип реквизита изменился и колонку надо
// преобразовать. Диалект-независимая намеренно: одна и та же конфигурация
// живёт и на PostgreSQL, и на SQLite, а подпись хранится в базе и должна
// значить одно и то же в обеих.
func FieldSignature(f Field) string {
	switch {
	case f.RefEntity != "":
		return "ref:" + f.RefEntity
	case f.EnumName != "":
		return "enum:" + f.EnumName
	case f.Type == FieldTypeNumber && (f.Length > 0 || f.Scale > 0):
		return fmt.Sprintf("number(%d,%d)", f.Length, f.Scale)
	default:
		return string(f.Type)
	}
}

func ColumnName(f Field) string {
	col := strings.ToLower(f.Name)
	if f.RefEntity != "" {
		return col + "_id"
	}
	return col
}
