package launcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// «Создать форму» в лаунчере всегда писала kind: object, поэтому форму списка
// через UI создать было нельзя: как бы форма ни называлась, список сущности
// продолжал показывать все поля — состав колонок берётся из формы вида list
// (issue #572).
func TestNewFormYAMLTemplateListKind(t *testing.T) {
	attrs := []formScaffoldAttr{
		{Name: "Наименование", Type: "string"},
		{Name: "ТипГТП", Title: "Тип ГТП", Type: "string"},
	}
	y := newFormYAMLTemplate("сайт_атс", "ФормаСписка", "list", attrs)

	if !strings.Contains(y, "kind: list") {
		t.Fatalf("вид формы не попал в заготовку:\n%s", y)
	}
	if strings.Contains(y, "ГруппаФормы") {
		t.Errorf("форма списка описывает колонки, группе там места нет:\n%s", y)
	}
	for _, want := range []string{
		`ru: "Список"`,
		"data_path: Объект.Наименование",
		"data_path: Объект.ТипГТП",
		`ru: "Тип ГТП"`,
	} {
		if !strings.Contains(y, want) {
			t.Errorf("в заготовке формы списка нет %q:\n%s", want, y)
		}
	}
	mustLoadForm(t, y, "сайт_атс")
}

// Объектная форма остаётся прежней: группа «Реквизиты» с полями внутри.
func TestNewFormYAMLTemplateObjectKindUnchanged(t *testing.T) {
	attrs := []formScaffoldAttr{{Name: "Наименование", Type: "string"}}
	y := newFormYAMLTemplate("Контрагент", "ФормаОбъекта", "object", attrs)
	for _, want := range []string{
		"kind: object",
		`ru: "Карточка"`,
		"kind: ГруппаФормы",
		"data_path: Объект.Наименование",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("в заготовке карточки нет %q:\n%s", want, y)
		}
	}
	mustLoadForm(t, y, "Контрагент")
}

// Форма списка без реквизитов (например, у объекта их не нашли) не должна
// давать битый YAML: elements: [] грузится загрузчиком.
func TestNewFormYAMLTemplateListKindNoAttrs(t *testing.T) {
	y := newFormYAMLTemplate("Контрагент", "ФормаСписка", "list", nil)
	if !strings.Contains(y, "elements: []") {
		t.Fatalf("ожидался пустой список элементов:\n%s", y)
	}
	mustLoadForm(t, y, "Контрагент")
}

func TestNormFormKind(t *testing.T) {
	cases := map[string]string{
		"":         "object",
		"object":   "object",
		"list":     "list",
		"List":     "list",
		" choice ": "choice",
		"folder":   "folder",
		"custom":   "custom",
		"мусор":    "object",
	}
	for in, want := range cases {
		if got := normFormKind(in); got != want {
			t.Errorf("normFormKind(%q) = %q, ожидали %q", in, got, want)
		}
	}
}

// Страница «Все управляемые формы» предлагает выбрать вид создаваемой формы.
func TestFormsListRendersKindSelect(t *testing.T) {
	rec := httptest.NewRecorder()
	renderFormsList(rec, &configuratorData{Base: &Base{ID: "b", Name: "b"}, Lang: "ru"})
	body := rec.Body.String()
	for _, want := range []string{`name="kind"`, `value="list"`, `value="object"`} {
		if !strings.Contains(body, want) {
			t.Errorf("в форме создания нет %q", want)
		}
	}
}

// Вид формы из списка на странице создания доезжает до заготовки редактора.
func TestConfiguratorFormsEditNewFormTakesKindFromQuery(t *testing.T) {
	store := newTestStore(t)
	if err := store.Add(&Base{ID: "b", Name: "b", ConfigSource: "file", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(CloseAuthPools)

	req := httptest.NewRequest(http.MethodGet,
		"/bases/b/configurator/forms/edit?entity=Контрагент&name=ФормаСписка&kind=list", nil)
	req = requestWithBaseID(req, "b")
	rec := httptest.NewRecorder()

	(&handler{store: store, runner: NewRunner()}).configuratorFormsEdit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("статус %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "kind: list") {
		t.Errorf("в заготовке новой формы нет kind: list")
	}
	if !strings.Contains(body, "· list") {
		t.Errorf("вид формы не показан в шапке редактора")
	}
}
