package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Блок item_form принимает две формы записи: строку (как было всегда) и
// `{name: X, readonly: true}` — «показывать, но не давать править» (#1011).
// Смешивать их в одном списке обязательно: помечают обычно один-два служебных
// реквизита, переписывать из-за этого весь блок незачем.
func TestLoadFile_ItemFormReadonlyEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Клиенты
fields:
  - name: Наименование
    type: string
  - name: ТелефоныНорм
    type: string
item_form:
  - Наименование
  - name: ТелефоныНорм
    readonly: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	want := []ItemFormField{{Name: "Наименование"}, {Name: "ТелефоныНорм", ReadOnly: true}}
	if len(e.ItemForm) != len(want) {
		t.Fatalf("item_form = %+v, ожидалось %+v", e.ItemForm, want)
	}
	for i := range want {
		if e.ItemForm[i] != want[i] {
			t.Errorf("item_form[%d] = %+v, ожидалось %+v", i, e.ItemForm[i], want[i])
		}
	}
	if names := e.ItemFormNames(); strings.Join(names, ",") != "Наименование,ТелефоныНорм" {
		t.Errorf("ItemFormNames() = %v", names)
	}
	// Состав формы проходит валидацию так же, как список строк: имена
	// проверяются по реквизитам сущности.
	if err := Validate([]*Entity{e}, nil); err != nil {
		t.Fatalf("валидация отвергла расширенную запись item_form: %v", err)
	}
}

// Запись без имени — ошибка загрузки, а не молча пропущенный реквизит:
// «readonly без name» почти наверняка опечатка автора конфигурации.
func TestLoadFile_ItemFormEntryWithoutName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.yaml")
	yaml := `name: Клиенты
fields:
  - name: Наименование
    type: string
item_form:
  - readonly: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, KindCatalog); err == nil || !strings.Contains(err.Error(), "item_form") {
		t.Fatalf("запись без name принята: %v", err)
	}
}

// Опечатка в имени реквизита ловится валидацией и в расширенной записи —
// иначе «readonly» молча не срабатывал бы, а поле оставалось редактируемым.
func TestValidate_ItemFormReadonlyUnknownField(t *testing.T) {
	e := &Entity{
		Name: "Клиенты", Kind: KindCatalog,
		Fields:   []Field{{Name: "Наименование", Type: FieldTypeString}},
		ItemForm: []ItemFormField{{Name: "ТелефонНорм", ReadOnly: true}},
	}
	err := Validate([]*Entity{e}, nil)
	if err == nil || !strings.Contains(err.Error(), "item_form") {
		t.Fatalf("неизвестный реквизит в item_form принят: %v", err)
	}
}
