package onec_forms

import (
	"os"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func TestNormalizeForImport_Minimal(t *testing.T) {
	path := writeFixture(t, "Form.xml", minForm)
	form, _, err := ReadFormXML(path)
	if err != nil {
		t.Fatal(err)
	}
	warns := NormalizeForImport(form)

	// Form-level events: OnOpen → ПриОткрытии.
	if form.Events["ПриОткрытии"] != "ПриОткрытии" {
		t.Errorf("form.Events после нормализации = %+v", form.Events)
	}
	if form.Events["ПриСоздании"] != "ПриСозданииНаСервере" {
		t.Errorf("OnCreateAtServer не отмаппился: %+v", form.Events)
	}

	// Корневой Pages должен стать СтраницыФормы
	if form.Elements[0].Kind != string(metadata.FormElementPages) {
		t.Errorf("корень = %q, ожидается %q", form.Elements[0].Kind, metadata.FormElementPages)
	}

	// Внутри Page → UsualGroup → InputField, CheckBoxField
	page := form.Elements[0].Children[0]
	if page.Kind != string(metadata.FormElementPage) {
		t.Errorf("page kind = %q", page.Kind)
	}
	group := page.Children[0]
	if group.Kind != string(metadata.FormElementGroupBox) {
		t.Errorf("group kind = %q", group.Kind)
	}
	input := group.Children[0]
	if input.Kind != string(metadata.FormElementField) {
		t.Errorf("input kind = %q", input.Kind)
	}
	// Событие OnChange → ПриИзменении
	if input.Events["ПриИзменении"] != "НомерПриИзменении" {
		t.Errorf("input events после нормализации = %+v", input.Events)
	}
	check := group.Children[1]
	if check.Kind != string(metadata.FormElementCheckbox) {
		t.Errorf("check kind = %q", check.Kind)
	}

	// Нет ошибок-warnings
	for _, w := range warns {
		if w.Severity == SeverityError {
			t.Errorf("ошибка нормализации: %s", w)
		}
	}
}

func TestToFormModule_Minimal(t *testing.T) {
	path := writeFixture(t, "Form.xml", minForm)
	form, _, err := ReadFormXML(path)
	if err != nil {
		t.Fatal(err)
	}
	NormalizeForImport(form)
	form.Entity = "РеализацияТоваров"
	form.Name = "ФормаОбъекта"
	form.Kind = "object"

	fm := ToFormModule(form)
	if fm == nil {
		t.Fatal("ToFormModule = nil")
		return
	}
	if fm.LayoutKind != metadata.FormLayoutManaged {
		t.Errorf("LayoutKind = %q", fm.LayoutKind)
	}
	if fm.EntityName != "РеализацияТоваров" || fm.Name != "ФормаОбъекта" {
		t.Errorf("EntityName/Name = %q / %q", fm.EntityName, fm.Name)
	}
	if len(fm.Attributes) != 2 {
		t.Fatalf("Attributes = %d", len(fm.Attributes))
	}
	if fm.Attributes[0].TypeRef != "DocumentRef.РеализацияТоваров" {
		t.Errorf("attr[0].TypeRef = %q", fm.Attributes[0].TypeRef)
	}
	if len(fm.Attributes[1].Columns) != 2 {
		t.Errorf("Товары.Columns = %d", len(fm.Attributes[1].Columns))
	}
	if len(fm.Commands) != 1 || fm.Commands[0].Action != "ПровестиКоманда" {
		t.Errorf("commands = %+v", fm.Commands)
	}
	if fm.AutoCommandBar == nil || len(fm.AutoCommandBar.Buttons) != 1 {
		t.Fatalf("AutoCommandBar = %+v", fm.AutoCommandBar)
	}

	// Form-level handlers — через ConvertedFormEventType ключи
	if fm.Handlers[metadata.FormEventOnOpen] != "ПриОткрытии" {
		t.Errorf("handler ПриОткрытии = %q", fm.Handlers[metadata.FormEventOnOpen])
	}

	// Дерево: корневой элемент — Pages, IsContainer=true
	root := fm.Elements[0]
	if !root.IsContainer() {
		t.Error("Pages должен быть IsContainer")
	}
	// глубина: Pages → Page → ГруппаФормы → ПолеВвода
	page := root.Children[0]
	group := page.Children[0]
	input := group.Children[0]
	if input.Kind != metadata.FormElementField {
		t.Errorf("deep input kind = %q", input.Kind)
	}
	// HandlersOnChange: имена должны быть OneBase-канон
	if input.Handlers[metadata.FormEventOnChange] != "НомерПриИзменении" {
		t.Errorf("deep input handlers = %+v", input.Handlers)
	}
	// DataPath перенесён
	if input.DataPath != "Объект.Номер" {
		t.Errorf("input DataPath = %q", input.DataPath)
	}
}

func TestNormalizeForImport_RealFile(t *testing.T) {
	realPath := `C:\Projects\АА5БП3\УТ11УТ11\ПереносДанныхУТ11УТ11_52\Forms\Форма\Ext\Form.xml`
	if _, err := os.Stat(realPath); err != nil {
		t.Skip("real УТ11 Form.xml не найден")
	}
	form, _, err := ReadFormXML(realPath)
	if err != nil {
		t.Fatal(err)
	}
	warns := NormalizeForImport(form)

	// После нормализации Form-level handlers должны быть в OneBase-канон
	if form.Events["ПриОткрытии"] == "" {
		t.Error("ПриОткрытии отсутствует после нормализации")
	}
	if form.Events["ПриСоздании"] == "" {
		t.Error("ПриСоздании отсутствует после нормализации")
	}

	// Не должно быть error-warnings
	errCount := 0
	w010Count := 0
	for _, w := range warns {
		if w.Severity == SeverityError {
			errCount++
		}
		if w.Code == W010_UnknownElement {
			w010Count++
		}
	}
	if errCount > 0 {
		t.Errorf("error-warnings: %d", errCount)
	}
	t.Logf("normalize warnings: total=%d unknown-element=%d", len(warns), w010Count)

	// Проверим что один из элементов стал ОНебейс-каноном:
	// первый элемент в дереве должен быть СтраницыФормы (Pages → СтраницыФормы)
	if form.Elements[0].Kind != string(metadata.FormElementPages) {
		t.Errorf("first element after normalize = %q, ожидается СтраницыФормы", form.Elements[0].Kind)
	}
}
