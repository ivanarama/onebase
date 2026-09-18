package metadata

import "strings"

// Реестр ключей FormElement.Props: единственное место, где написано, кто из
// потребителей знает про ключ.
//
// Props — свободный map[string]any, и до появления реестра это означало, что
// любое имя принималось молча (#1492). Рендерер управляемой формы не читает
// Props ни разу; смысл ключам придаёт только конвертер 1С. Отличить «свойство
// есть, но не применилось» от «такого свойства не существует» изнутри
// конфигурации было нельзя.
//
// Реестр держит этот список рядом с флагом потребителя, чтобы проверка
// configcheck не разошлась с кодом: сторож TestFormPropsRegistryCoversConsumers
// сверяет реестр с исходниками конвертера и рендерера.

// FormPropUse — потребители ключа props. Ноль означает «ключ не нужен никому»:
// именно за это configcheck и предупреждает.
type FormPropUse uint8

const (
	// FormPropManagedForm — ключ читает рендерер управляемой формы.
	// Сейчас таких ключей нет: managed-рендерер к Props не обращается.
	FormPropManagedForm FormPropUse = 1 << iota
	// FormPropOneCXML — экспорт в 1С пишет ключ отдельным элементом Form.xml.
	FormPropOneCXML
	// FormPropOneCService — служебный ключ конвертера 1С: его ставит импорт,
	// а читает экспорт либо хранит для обратной сборки формы.
	FormPropOneCService
)

// Имена ключей, на которые ссылается код конвертера. Реестр и конвертер
// пишут ключ одной константой — иначе опечатка в одном из мест снова
// осталась бы молчаливой.
const (
	FormPropKeyType         = "Type"
	FormPropKeyDecoration   = "decoration"
	FormPropKeyInCommandBar = "in_command_bar"
	FormPropKeyPopup        = "popup"
)

// FormPropSpec — одна запись реестра.
type FormPropSpec struct {
	Key  string
	Uses FormPropUse
	// Note — чем ключ занят. Уходит в подсказку configcheck, поэтому пишется
	// так, чтобы её можно было прочитать без чтения кода.
	Note string
}

// formPropRegistry перечислен в порядке вывода в Form.xml: writeKnownProps
// берёт порядок отсюда, чтобы diff выгрузки был стабильным.
var formPropRegistry = []FormPropSpec{
	{FormPropKeyType, FormPropOneCXML | FormPropOneCService, "вид элемента 1С (например CommandBarButton)"},
	{"Representation", FormPropOneCXML | FormPropOneCService, "представление элемента 1С"},
	{"CommandName", FormPropOneCXML | FormPropOneCService, "имя команды 1С у кнопки"},
	{"Group", FormPropOneCXML | FormPropOneCService, "группировка элемента 1С"},
	{"Behavior", FormPropOneCXML | FormPropOneCService, "поведение элемента 1С"},
	{"ShowTitle", FormPropOneCXML | FormPropOneCService, "показывать заголовок (1С)"},
	{"PagesRepresentation", FormPropOneCXML | FormPropOneCService, "представление страниц (1С)"},
	{"TitleLocation", FormPropOneCXML | FormPropOneCService, "положение заголовка (1С)"},
	{"EditMode", FormPropOneCXML | FormPropOneCService, "режим редактирования таблицы (1С)"},
	{"ChoiceFoldersAndItems", FormPropOneCXML | FormPropOneCService, "выбор групп и элементов (1С)"},
	{"AutoInsertNewRow", FormPropOneCXML | FormPropOneCService, "автодобавление строки (1С)"},
	{"HeightInTableRows", FormPropOneCXML | FormPropOneCService, "высота в строках таблицы (1С)"},
	{"HorizontalStretch", FormPropOneCXML | FormPropOneCService, "растягивание по горизонтали в 1С; на управляемую форму не влияет"},
	{"VerticalStretch", FormPropOneCXML | FormPropOneCService, "растягивание по вертикали в 1С; на управляемую форму не влияет"},

	// Пометки конвертера: ставит импорт (mapping_in), читает экспорт
	// (mapping_out) и удаляет перед записью Form.xml.
	{FormPropKeyDecoration, FormPropOneCService, "пометка конвертера: элемент пришёл как <Decoration>"},
	{FormPropKeyInCommandBar, FormPropOneCService, "пометка конвертера: кнопка командной панели"},
	// popup ставит импорт; обратную сборку <Popup> экспорт пока не делает —
	// ключ остаётся носителем сведений о форме и за него не предупреждаем.
	{FormPropKeyPopup, FormPropOneCService, "пометка конвертера: группа пришла как <Popup>"},

	// Носители round-trip: импорт запоминает служебные узлы 1С, чтобы при
	// обратной сборке формы их не потерять.
	{"ContextMenu_name", FormPropOneCService, "round-trip: имя узла <ContextMenu> из Form.xml"},
	{"ContextMenu_id", FormPropOneCService, "round-trip: id узла <ContextMenu> из Form.xml"},
	{"ExtendedTooltip_name", FormPropOneCService, "round-trip: имя узла <ExtendedTooltip> из Form.xml"},
	{"ExtendedTooltip_id", FormPropOneCService, "round-trip: id узла <ExtendedTooltip> из Form.xml"},
	{"ChoiceList_xml", FormPropOneCService, "round-trip: сериализованный <ChoiceList> из Form.xml"},
}

var formPropByKey = func() map[string]FormPropSpec {
	m := make(map[string]FormPropSpec, len(formPropRegistry))
	for _, s := range formPropRegistry {
		m[s.Key] = s
	}
	return m
}()

// FormPropRegistry возвращает реестр в порядке объявления.
func FormPropRegistry() []FormPropSpec {
	out := make([]FormPropSpec, len(formPropRegistry))
	copy(out, formPropRegistry)
	return out
}

// LookupFormProp ищет ключ реестра. Регистр важен: 1С-имена пишутся
// PascalCase, и writeKnownProps сравнивает их точно.
func LookupFormProp(key string) (FormPropSpec, bool) {
	s, ok := formPropByKey[key]
	return s, ok
}

// LookupFormPropFold ищет ключ без учёта регистра — только для подсказки
// «проверьте написание». Возвращает найденный ключ реестра.
func LookupFormPropFold(key string) (FormPropSpec, bool) {
	if s, ok := formPropByKey[key]; ok {
		return s, true
	}
	for _, s := range formPropRegistry {
		if strings.EqualFold(s.Key, key) {
			return s, true
		}
	}
	return FormPropSpec{}, false
}

// FormPropOneCXMLKeys — ключи, которые экспорт пишет в Form.xml, в порядке
// реестра.
func FormPropOneCXMLKeys() []string {
	var out []string
	for _, s := range formPropRegistry {
		if s.Uses&FormPropOneCXML != 0 {
			out = append(out, s.Key)
		}
	}
	return out
}
