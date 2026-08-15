package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
)

const sampleFormYAML = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагенты
  title:
    ru: "Контрагент"
  original_id: "0"
  auto_save_settings: true
  vertical_scroll: auto

attributes:
  - name: Объект
    type: CatalogRef.Контрагенты
    save: true
    original_id: "1"
  - name: Товары
    type: ValueTable
    original_id: "2"
    columns:
      - name: Номенклатура
        type: CatalogRef.Номенклатура
      - name: Цена
        type: "decimal(15,2)"

commands:
  - name: ПровестиКоманда
    title: { ru: "Провести" }
    action: ПровестиОбработчик

elements:
  - kind: ГруппаФормы
    name: Шапка
    title: { ru: "Шапка" }
    children:
      - kind: ПолеВвода
        name: ПолеКонтрагент
        data_path: Объект
        original_id: "132"
        accesskey: "K"
        events:
          ПриИзменении: КонтрагентПриИзменении
      - kind: Флажок
        name: ПолеАктивен
        data_path: Объект.Активен
        accesskey: "A"
      - kind: Кнопка
        name: КнопкаКопировать
        title: { ru: "Создать копию" }
        hotkey: F7
        events:
          Нажатие: Копировать

events:
  ПриОткрытии: ПриОткрытииФормы
`

func TestManagedFormLoader_ParseYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "контрагенты.form.yaml")
	if err := os.WriteFile(yamlPath, []byte(sampleFormYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	mfl := NewManagedFormLoader()
	form, err := mfl.LoadFormFile(yamlPath, "Контрагенты")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}

	if form.Name != "ФормаОбъекта" {
		t.Errorf("Name = %q, want ФормаОбъекта", form.Name)
	}
	if form.LayoutKind != metadata.FormLayoutManaged {
		t.Errorf("LayoutKind = %q, want managed", form.LayoutKind)
	}
	if form.EntityName != "Контрагенты" {
		t.Errorf("EntityName = %q", form.EntityName)
	}
	if form.Title["ru"] != "Контрагент" {
		t.Errorf("Title[ru] = %q", form.Title["ru"])
	}
	if !form.AutoSaveDataInSettings {
		t.Error("AutoSaveDataInSettings should be true")
	}

	// Реквизиты
	if len(form.Attributes) != 2 {
		t.Fatalf("Attributes count = %d, want 2", len(form.Attributes))
	}
	if form.Attributes[1].Name != "Товары" || form.Attributes[1].TypeRef != "ValueTable" {
		t.Errorf("attribute[1] = %+v", form.Attributes[1])
	}
	if len(form.Attributes[1].Columns) != 2 {
		t.Errorf("Товары.Columns = %d, want 2", len(form.Attributes[1].Columns))
	}
	if form.Attributes[1].Columns[1].TypeRef != "decimal(15,2)" {
		t.Errorf("Товары.Columns[1].TypeRef = %q", form.Attributes[1].Columns[1].TypeRef)
	}

	// Команды
	if len(form.Commands) != 1 {
		t.Fatalf("Commands count = %d, want 1", len(form.Commands))
	}
	if form.Commands[0].Name != "ПровестиКоманда" || form.Commands[0].Action != "ПровестиОбработчик" {
		t.Errorf("command = %+v", form.Commands[0])
	}
	// Дерево элементов
	if len(form.Elements) != 1 || form.Elements[0].Kind != metadata.FormElementGroupBox {
		t.Fatalf("root element = %+v", form.Elements)
	}
	root := form.Elements[0]
	if len(root.Children) != 3 {
		t.Fatalf("root.Children = %d, want 3", len(root.Children))
	}
	first := root.Children[0]
	if first.Kind != metadata.FormElementField || first.Name != "ПолеКонтрагент" {
		t.Errorf("first child = %+v", first)
	}
	if first.DataPath != "Объект" || first.OriginalID != "132" {
		t.Errorf("first child datapath/original_id = %q / %q", first.DataPath, first.OriginalID)
	}
	if first.AccessKey != "K" {
		t.Errorf("first child accesskey = %q, want K", first.AccessKey)
	}
	if first.Handlers[metadata.FormEventOnChange] != "КонтрагентПриИзменении" {
		t.Errorf("first child events = %+v", first.Handlers)
	}
	if root.Children[1].AccessKey != "A" {
		t.Errorf("second child accesskey = %q, want A", root.Children[1].AccessKey)
	}
	if root.Children[2].HotKey != "F7" {
		t.Errorf("third child hotkey = %q, want F7", root.Children[2].HotKey)
	}
	if root.Children[2].Handlers[metadata.FormEventOnClick] != "Копировать" {
		t.Errorf("third child events = %+v", root.Children[2].Handlers)
	}

	// Form-level events
	if form.Handlers[metadata.FormEventOnOpen] != "ПриОткрытииФормы" {
		t.Errorf("form events = %+v", form.Handlers)
	}

	// IsManaged
	if !form.IsManaged() {
		t.Error("form.IsManaged() = false")
	}
}

func TestManagedFormLoaderRejectsAmbiguousValueTableNames(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "collision.form.yaml")
	doc := `schema: onebase.form/v1
form:
  name: Форма
  kind: custom
  entity: Тест
attributes:
  - name: Подбор
    type: ValueTable
  - name: ПОДБОР
    type: valuetable
`
	if err := os.WriteFile(yamlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManagedFormLoader().LoadFormFile(yamlPath, "Тест"); err == nil {
		t.Fatal("case-insensitive ValueTable collision was accepted by loader")
	}
}

func TestManagedFormLoader_ParsesDeleteAction(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "контрагенты.form.yaml")
	doc := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Контрагенты
actions:
  delete:
    visible: false
`
	if err := os.WriteFile(yamlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	mfl := NewManagedFormLoader()
	form, err := mfl.LoadFormFile(yamlPath, "Контрагенты")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	a, ok := form.Actions["delete"]
	if !ok || a == nil {
		t.Fatalf("actions.delete не разобран из YAML; Actions=%v", form.Actions)
	}
	if a.Visible == nil || *a.Visible {
		t.Errorf("actions.delete.visible должно быть false, got %v", a.Visible)
	}
}

func TestManagedFormLoader_ParseConditionalFormatting(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "заказ.form.yaml")
	doc := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
conditional:
  - target: Товары
    when: Количество < 0
    field: Сумма
    style:
      color: "#991b1b"
conditional_formatting:
  - element: ТаблицаТовары
    when: Сумма < 0
    then:
      background: "#fee2e2"
      bold: true
  - table_part: Услуги
    when: Цена = 0
    field: Цена
    then:
      italic: true
`
	if err := os.WriteFile(yamlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	mfl := NewManagedFormLoader()
	form, err := mfl.LoadFormFile(yamlPath, "Заказ")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	if len(form.Conditional) != 3 {
		t.Fatalf("Conditional = %d, want 3", len(form.Conditional))
	}
	if got := form.Conditional[0]; got.Target != "Товары" || got.Field != "Сумма" || got.Style.Color != "#991b1b" {
		t.Fatalf("conditional[0] = %+v", got)
	}
	if got := form.Conditional[1]; got.Target != "ТаблицаТовары" || got.Style.Background != "#fee2e2" || !got.Style.Bold {
		t.Fatalf("conditional_formatting then/element = %+v", got)
	}
	if got := form.Conditional[2]; got.Target != "Услуги" || got.Field != "Цена" || !got.Style.Italic {
		t.Fatalf("conditional_formatting table_part = %+v", got)
	}
}

// Реквизит со списком значений (ПолеСписка + choices) должен разбираться из
// .form.yaml в FormElement.Choices с локализованными подписями.
func TestManagedFormLoader_ParseChoices(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "задача.form.yaml")
	doc := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Задача
elements:
  - kind: ПолеСписка
    name: ПолеПриоритет
    data_path: Объект.Приоритет
    choices:
      - value: high
        title: { ru: "Высокий", en: "High" }
      - value: low
        title: { ru: "Низкий" }
      - value: raw
    events:
      ПриИзменении: ПриоритетПриИзменении
`
	if err := os.WriteFile(yamlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	mfl := NewManagedFormLoader()
	form, err := mfl.LoadFormFile(yamlPath, "Задача")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	if len(form.Elements) != 1 {
		t.Fatalf("elements = %d, want 1", len(form.Elements))
	}
	el := form.Elements[0]
	if el.Kind != metadata.FormElementInputList {
		t.Errorf("kind = %q, want ПолеСписка", el.Kind)
	}
	if len(el.Choices) != 3 {
		t.Fatalf("choices = %d, want 3", len(el.Choices))
	}
	if el.Choices[0].Value != "high" || el.Choices[0].Title["en"] != "High" {
		t.Errorf("choice[0] = %+v", el.Choices[0])
	}
	if got := el.Choices[0].ChoiceLabel("en"); got != "High" {
		t.Errorf("ChoiceLabel(en) = %q, want High", got)
	}
	if got := el.Choices[1].ChoiceLabel("en"); got != "Низкий" { // нет en → откат на ru
		t.Errorf("ChoiceLabel(en) fallback to ru = %q, want Низкий", got)
	}
	if got := el.Choices[2].ChoiceLabel("ru"); got != "raw" { // нет title → откат на value
		t.Errorf("ChoiceLabel(ru) fallback to value = %q, want raw", got)
	}
}

func TestManagedFormLoader_LoadEntityForms_NoDir(t *testing.T) {
	dir := t.TempDir()
	mfl := NewManagedFormLoader()
	forms, err := mfl.LoadEntityForms(dir, "Контрагенты")
	if err != nil {
		t.Fatalf("LoadEntityForms на отсутствующий каталог должен вернуть nil, nil: %v", err)
	}
	if forms != nil {
		t.Errorf("forms = %v, want nil", forms)
	}
}

// Модуль формы должен сохранять физический путь .form.os в AST. Это не только
// диагностика: interpreter использует identity текущего модуля, чтобы
// Вычислить и обычные неквалифицированные вызовы видели соседние процедуры.
func TestManagedFormLoader_PreservesModuleSourcePath(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "заказ.form.yaml")
	osPath := filepath.Join(dir, "заказ.form.os")
	if err := os.WriteFile(yamlPath, []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(osPath, []byte(`
Функция Локальная()
	Возврат "форма";
КонецФункции
`), 0o644); err != nil {
		t.Fatal(err)
	}

	form, err := NewManagedFormLoader().LoadFormFile(yamlPath, "Заказ")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	program, ok := form.ProgramAST.(*ast.Program)
	if !ok || program == nil || len(program.Procedures) != 1 {
		t.Fatalf("ProgramAST не содержит процедуру формы: %#v", form.ProgramAST)
	}
	if got := filepath.Clean(program.Procedures[0].Name.File); got != filepath.Clean(osPath) {
		t.Fatalf("source identity = %q, want %q", got, osPath)
	}
}

func TestFormLoader_OnlyKnownProcedureNamesBecomeFormEvents(t *testing.T) {
	form, err := NewFormLoader().LoadFormModuleFromSource(`
Процедура ПриОткрытии()
КонецПроцедуры
Процедура Вспомогательная()
КонецПроцедуры
`, "Заказ", "ФормаОбъекта", "object")
	if err != nil {
		t.Fatalf("LoadFormModuleFromSource: %v", err)
	}
	if got := form.Handlers[metadata.FormEventOnOpen]; got != "ПриОткрытии" {
		t.Fatalf("known event handler = %q, want ПриОткрытии", got)
	}
	if _, exists := form.Handlers[metadata.FormEventType("Вспомогательная")]; exists {
		t.Fatalf("helper procedure was exposed as form event: %#v", form.Handlers)
	}
	if form.Procedures["Вспомогательная"] == nil {
		t.Fatal("helper procedure must remain callable from the form module")
	}
}

func TestManagedFormLoader_FiltersUnknownYAMLAndInferredEvents(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "заказ.form.yaml")
	osPath := filepath.Join(dir, "заказ.form.os")
	if err := os.WriteFile(yamlPath, []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
events:
  ПриОткрытии: Открыть
  СекретныйПомощник: СекретныйПомощник
elements:
  - kind: Кнопка
    name: Кнопка
    events:
      Нажатие: Нажать
      ПроизвольноеСобытие: СекретныйПомощник
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(osPath, []byte(`
Процедура СекретныйПомощник()
КонецПроцедуры
Процедура ПриОткрытии()
КонецПроцедуры
`), 0o644); err != nil {
		t.Fatal(err)
	}
	form, err := NewManagedFormLoader().LoadFormFile(yamlPath, "Заказ")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	if _, ok := form.Handlers[metadata.FormEventType("СекретныйПомощник")]; ok {
		t.Fatalf("unknown YAML/inferred form event survived merge: %#v", form.Handlers)
	}
	if _, ok := form.Elements[0].Handlers[metadata.FormEventType("ПроизвольноеСобытие")]; ok {
		t.Fatalf("unknown element event survived YAML load: %#v", form.Elements[0].Handlers)
	}
	if form.Procedures["СекретныйПомощник"] == nil {
		t.Fatal("filtered helper procedure must remain available for internal calls")
	}
}

func TestManagedFormLoader_LoadEntityForms_TwoForms(t *testing.T) {
	dir := t.TempDir()
	entityDir := filepath.Join(dir, "forms", "контрагенты")
	if err := os.MkdirAll(entityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entityDir, "объекта.form.yaml"), []byte(sampleFormYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	listYAML := `schema: onebase.form/v1
form:
  name: ФормаСписка
  kind: list
  entity: Контрагенты
elements: []
`
	if err := os.WriteFile(filepath.Join(entityDir, "списка.form.yaml"), []byte(listYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	mfl := NewManagedFormLoader()
	forms, err := mfl.LoadEntityForms(dir, "Контрагенты")
	if err != nil {
		t.Fatalf("LoadEntityForms: %v", err)
	}
	if len(forms) != 2 {
		t.Fatalf("forms count = %d, want 2", len(forms))
	}
	// Все должны быть managed
	for _, f := range forms {
		if !f.IsManaged() {
			t.Errorf("form %q is not managed", f.Name)
		}
	}
}

func TestManagedFormLoader_RejectsUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "x.form.yaml")
	body := `schema: weird/v999
form:
  name: X
  entity: E
`
	if err := os.WriteFile(yamlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mfl := NewManagedFormLoader()
	_, err := mfl.LoadFormFile(yamlPath, "E")
	if err == nil {
		t.Fatal("expected error on unknown schema")
	}
}

// `table_part:` у элемента ТЧ — рабочий ключ, а не украшение (#830).
//
// Элемент, описанный через него, молча не рендерился: имя ТЧ все потребители
// (рендер, событие формы, частичная запись) берут из data_path. При этом ключ
// объявлен в модели, загрузчик его читает, а check и forms validate проходят
// зелёными — человек видел зелёную проверку и пустую форму.
func TestManagedFormLoader_TablePartKeyFillsDataPath(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "заказ.form.yaml")
	doc := `schema: onebase.form/v1
form:
  name: ФормаЗаказа
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: Строки
    table_part: Строки
`
	if err := os.WriteFile(yamlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	form, err := NewManagedFormLoader().LoadFormFile(yamlPath, "Заказ")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	el := form.GetElementByName("Строки")
	if el == nil {
		t.Fatal("элемент ТЧ не загружен")
	}
	if el.DataPath != "Объект.Строки" {
		t.Fatalf("data_path = %q, ожидался «Объект.Строки» — без него таблица не отрисуется", el.DataPath)
	}
}

// Явный data_path сильнее: если автор написал оба ключа, побеждает тот, что
// уже работает, — иначе правка меняла бы поведение существующих форм.
func TestManagedFormLoader_ExplicitDataPathWins(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "заказ2.form.yaml")
	doc := `schema: onebase.form/v1
form:
  name: ФормаЗаказа
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: Строки
    table_part: Другая
    data_path: Объект.Строки
`
	if err := os.WriteFile(yamlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	form, err := NewManagedFormLoader().LoadFormFile(yamlPath, "Заказ")
	if err != nil {
		t.Fatalf("LoadFormFile: %v", err)
	}
	if el := form.GetElementByName("Строки"); el == nil || el.DataPath != "Объект.Строки" {
		t.Fatalf("явный data_path перезаписан: %+v", el)
	}
}
