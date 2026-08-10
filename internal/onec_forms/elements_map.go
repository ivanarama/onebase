package onec_forms

import "github.com/ivantit66/onebase/internal/metadata"

// elementMap — соответствие между именем элемента в Form.xml (1С)
// и каноническим типом OneBase (FormElementType из internal/metadata).
//
// Заполняется в виде {1С → OneBase}; обратное направление получаем
// инверсией в init(). Несимметричные случаи (Decoration → Надпись с пометкой,
// CommandBarButton → Кнопка с флагом) решаются явными хуками в mapping_in/out
// и здесь не отражены.
var elementMap = map[string]metadata.FormElementType{
	"InputField":       metadata.FormElementField,
	"LabelField":       metadata.FormElementLabel,
	"Button":           metadata.FormElementButton,
	"CheckBoxField":    metadata.FormElementCheckbox,
	"RadioButtonField": metadata.FormElementSwitch,
	"PictureField":     metadata.FormElementPicture,
	"Table":            metadata.FormElementTable,
	"ColumnGroup":      metadata.FormElementColumn,
	"UsualGroup":       metadata.FormElementGroupBox,
	"Pages":            metadata.FormElementPages,
	"Page":             metadata.FormElementPage,
	"AutoCommandBar":   metadata.FormElementCommandBar,
	"CommandBar":       metadata.FormElementCommandBar,
	// Поле текстового документа — многострочный редактор кода/текста. В таких
	// полях обычно правят правила обмена, шаблоны и запросы, поэтому ближайший
	// аналог — ПолеКода с подсветкой, а не обычная textarea.
	"TextDocumentField": metadata.FormElementCodeField,
	// "Decoration" — мапится в LabelField + props.decoration=true (см. mapping_in).
}

var elementMapInverse map[metadata.FormElementType]string

func init() {
	elementMapInverse = make(map[metadata.FormElementType]string, len(elementMap))
	for k, v := range elementMap {
		// если несколько ключей дают одно значение (CommandBar+AutoCommandBar
		// → КоманднаяПанель), оставляем первый встретившийся
		if _, exists := elementMapInverse[v]; !exists {
			elementMapInverse[v] = k
		}
	}
}

// Element1CToOneBase возвращает FormElementType OneBase по имени элемента 1С.
// Если соответствия нет — возвращает "" и ok=false (вызывающий код обычно
// складывает такой узел в UnknownXML и эмитит W010).
func Element1CToOneBase(name1c string) (metadata.FormElementType, bool) {
	v, ok := elementMap[name1c]
	return v, ok
}

// ElementOneBaseTo1C — обратное направление.
func ElementOneBaseTo1C(kind metadata.FormElementType) (string, bool) {
	v, ok := elementMapInverse[kind]
	return v, ok
}
