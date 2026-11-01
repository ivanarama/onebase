package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// ref_card_button_admin_only (#1876) и ref_card_button у поля. Тесты идут через
// RunFull/RunFullWithOptions — те же вызовы, которыми `onebase check` и
// `onebase check --lint` проверяют проект.

func refCardAdminProject(t *testing.T, formYAML string) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n")
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Номер
    type: string
  - name: Клиент
    type: reference:Клиент
`)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), formYAML)
	return dir
}

func refCardPlacementWarned(res Result) []string {
	var found []string
	for _, list := range [][]Issue{res.Issues, res.Warnings} {
		for _, is := range list {
			if is.Code == "form.ref-card-button" {
				found = append(found, is.Message)
			}
		}
	}
	return found
}

func TestLint_RefCardButtonAdminOnlyAccepted(t *testing.T) {
	dir := refCardAdminProject(t, `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
  ref_card_button_admin_only: true
attributes:
  - name: Склад
    type: CatalogRef.Клиент
elements:
  - kind: ПолеВвода
    name: ПолеКлиент
    data_path: Объект.Клиент
    ref_card_button: true
  - kind: ПолеВвода
    name: ПолеСклад
    data_path: Склад
    ref_card_button: false
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	for _, key := range []string{"ref_card_button_admin_only", "ref_card_button"} {
		if unvalidatedKeyMentions(res, key) {
			t.Errorf("ключ %s назван неизвестным; находки: %+v %+v", key, res.Issues, res.Warnings)
		}
	}
	// Ссылочное поле объекта и голый реквизит формы — правильные места ключа.
	if warned := refCardPlacementWarned(res); len(warned) > 0 {
		t.Errorf("ложная тревога о месте ref_card_button: %v", warned)
	}
}

func TestLint_RefCardButtonAdminOnlyInFormRootReported(t *testing.T) {
	// Загрузчик читает ключ только внутри form: — в корне он тихо потерялся бы.
	dir := refCardAdminProject(t, `schema: onebase.form/v1
ref_card_button_admin_only: true
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеКлиент
    data_path: Объект.Клиент
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	if !unvalidatedKeyMentions(res, "ref_card_button_admin_only") {
		t.Errorf("ключ в корне формы не назван линтом; находки: %+v %+v", res.Issues, res.Warnings)
	}
}

func TestCheck_RefCardButtonOnNonReferenceFieldWarned(t *testing.T) {
	dir := refCardAdminProject(t, `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: ПолеНомер
    data_path: Объект.Номер
    ref_card_button: false
`)
	warned := refCardPlacementWarned(RunFull(dir))
	if len(warned) != 1 || !strings.Contains(warned[0], "Объект.Номер") {
		t.Fatalf("ref_card_button у строкового поля: ожидалось одно предупреждение про Объект.Номер, получено %v", warned)
	}
}

// Голый data_path ссылочного поля сущности (ревью #1915): шаблон берёт имя
// поля из последнего сегмента и сначала ищет поле сущности, поэтому кнопка у
// такого поля есть и ключ действует — предупреждать не о чем. У голого имени
// строкового поля кнопки нет, предупреждение остаётся.
func TestCheck_RefCardButtonBareDataPath(t *testing.T) {
	element := func(dataPath string) string {
		return `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: Поле
    data_path: ` + dataPath + `
    ref_card_button: false
`
	}
	if warned := refCardPlacementWarned(RunFull(refCardAdminProject(t, element("Клиент")))); len(warned) != 0 {
		t.Errorf("голый data_path ссылочного поля: ложное предупреждение %v", warned)
	}
	if warned := refCardPlacementWarned(RunFull(refCardAdminProject(t, element("Номер")))); len(warned) != 1 {
		t.Errorf("голый data_path строкового поля: предупреждений %v, ожидалось одно", warned)
	}
}
