package onec_forms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/loader"
)

func TestWriteFormYAML_Minimal(t *testing.T) {
	// Берём минимальную фикстуру, парсим её и записываем обратно как YAML.
	path := writeFixture(t, "Form.xml", minForm)
	form, _, err := ReadFormXML(path)
	if err != nil {
		t.Fatal(err)
	}
	NormalizeForImport(form)
	form.Entity = "РеализацияТоваров"
	form.Name = "ФормаОбъекта"
	form.Kind = "object"

	dst := filepath.Join(t.TempDir(), "формаобъекта.form.yaml")
	if err := WriteFormYAML(form, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(data)
	t.Logf("YAML:\n%s", yaml)

	// Базовые проверки содержимого
	expects := []string{
		"schema: onebase.form/v1",
		"name: ФормаОбъекта",
		"kind: object",
		"entity: РеализацияТоваров",
		"vertical_scroll: useIfNecessary",
		"auto_save_settings: true",
		"name: Объект",
		"type: DocumentRef.РеализацияТоваров",
		"main: true",
		"name: Товары",
		"type: ValueTable",
		"type: decimal(15,2)",
		"name: Провести",
		"action: ПровестиКоманда",
		"kind: СтраницыФормы",
		"kind: ПолеВвода",
		"data_path: Объект.Номер",
		"ПриИзменении: НомерПриИзменении",
		"ПриОткрытии: ПриОткрытии",
	}
	for _, e := range expects {
		if !strings.Contains(yaml, e) {
			t.Errorf("YAML не содержит %q", e)
		}
	}
}

// Round-trip: после WriteFormYAML мы должны иметь возможность прочитать
// YAML через существующий ManagedFormLoader и получить эквивалентную FormModule.
func TestRoundTrip_XML_to_YAML_to_FormModule(t *testing.T) {
	path := writeFixture(t, "Form.xml", minForm)
	form, _, err := ReadFormXML(path)
	if err != nil {
		t.Fatal(err)
	}
	NormalizeForImport(form)
	form.Entity = "РеализацияТоваров"
	form.Name = "ФормаОбъекта"
	form.Kind = "object"

	dst := filepath.Join(t.TempDir(), "формаобъекта.form.yaml")
	if err := WriteFormYAML(form, dst); err != nil {
		t.Fatal(err)
	}

	mfl := loader.NewManagedFormLoader()
	fm, err := mfl.LoadFormFile(dst, "РеализацияТоваров")
	if err != nil {
		t.Fatalf("повторное чтение YAML через managed_form_loader: %v", err)
	}

	if fm.Name != "ФормаОбъекта" || fm.Kind != "object" || fm.EntityName != "РеализацияТоваров" {
		t.Errorf("loaded form: %+v", fm)
	}
	if !fm.IsManaged() {
		t.Error("loaded form должна быть managed")
	}
	if len(fm.Attributes) != 2 {
		t.Fatalf("loaded attributes = %d, want 2", len(fm.Attributes))
	}
	if fm.Attributes[1].TypeRef != "ValueTable" || len(fm.Attributes[1].Columns) != 2 {
		t.Errorf("loaded Товары = %+v", fm.Attributes[1])
	}
	if len(fm.Commands) != 1 || fm.Commands[0].Action != "ПровестиКоманда" {
		t.Errorf("loaded commands = %+v", fm.Commands)
	}
	if len(fm.Elements) != 1 {
		t.Fatalf("loaded elements = %d, want 1", len(fm.Elements))
	}
}

func TestRoundTrip_YAMLMultilinePreservesAbsentTrueAndFalse(t *testing.T) {
	form, err := parseFormYAMLBytes([]byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Обращение
elements:
  - kind: ПолеВвода
    name: Наследуемое
    data_path: Объект.Наследуемое
  - kind: ПолеВвода
    name: Многострочное
    data_path: Объект.Многострочное
    multiline: true
  - kind: ПолеВвода
    name: Однострочное
    data_path: Объект.Однострочное
    multiline: false
`))
	if err != nil {
		t.Fatalf("parseFormYAMLBytes: %v", err)
	}
	assertMultilineStates := func(stage string, elements []*IRElement) {
		t.Helper()
		if len(elements) != 3 {
			t.Fatalf("%s: elements=%d, want 3", stage, len(elements))
		}
		if elements[0].Multiline != nil {
			t.Fatalf("%s: отсутствие multiline превратилось в значение", stage)
		}
		if elements[1].Multiline == nil || !*elements[1].Multiline {
			t.Fatalf("%s: multiline:true потерян", stage)
		}
		if elements[2].Multiline == nil || *elements[2].Multiline {
			t.Fatalf("%s: multiline:false потерян", stage)
		}
	}
	assertMultilineStates("read", form.Elements)

	dst := filepath.Join(t.TempDir(), "формаобъекта.form.yaml")
	if err := WriteFormYAML(form, dst); err != nil {
		t.Fatalf("WriteFormYAML: %v", err)
	}
	roundTripped, err := ReadFormYAML(dst)
	if err != nil {
		t.Fatalf("ReadFormYAML: %v", err)
	}
	assertMultilineStates("round-trip", roundTripped.Elements)
}
