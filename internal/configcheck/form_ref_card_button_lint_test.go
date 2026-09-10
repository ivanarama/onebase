package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// `ref_card_button` в корне документа формы (issue #1450).
//
// Ключ читается загрузчиком только внутри блока `form:`
// (`internal/dsl/loader/managed_form_loader.go`, поле RefCardButton у тега
// yaml:"form"), но в белом списке линта он стоял ДВАЖДЫ — и в блоке, и в корне.
// Конфигурация с ключом в корне проходила проверку зелёной, а кнопка молча
// оставалась на месте: ровно та «тихая потеря», от которой линт и заведён.
//
// Тест идёт через RunFullWithOptions — тот самый вызов, которым `onebase check
// --lint` проверяет проект (`internal/cli/check.go:56,70`), а не через разбор
// схемы напрямую.

func refCardButtonProject(t *testing.T, formYAML string) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Номер
    type: string
`)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), formYAML)
	return dir
}

// unvalidatedKeyMentions ищет находку линта про неизвестный ключ с этим именем.
// Смотрим и Issues, и Warnings: класс находки — деталь реализации, а тест
// говорит о том, названа ли проблема вообще.
func unvalidatedKeyMentions(res Result, key string) bool {
	for _, list := range [][]Issue{res.Issues, res.Warnings} {
		for _, is := range list {
			if is.Code == "metadata.unvalidated-key" && strings.Contains(is.Message, key) {
				return true
			}
		}
	}
	return false
}

func TestLint_RefCardButtonInFormRootReported(t *testing.T) {
	dir := refCardButtonProject(t, `schema: onebase.form/v1
ref_card_button: false
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: Номер
    data_path: Объект.Номер
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	if !unvalidatedKeyMentions(res, "ref_card_button") {
		t.Errorf("ключ в корне формы не назван линтом; находки: %+v %+v", res.Issues, res.Warnings)
	}
}

func TestLint_RefCardButtonInsideFormAccepted(t *testing.T) {
	// Правильное место ключа обязано остаться зелёным: иначе починка «тихой
	// потери» превратилась бы в ложную тревогу на рабочих конфигурациях.
	dir := refCardButtonProject(t, `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
  ref_card_button: false
elements:
  - kind: ПолеВвода
    name: Номер
    data_path: Объект.Номер
`)
	res := RunFullWithOptions(dir, Options{Lint: true})
	if unvalidatedKeyMentions(res, "ref_card_button") {
		t.Errorf("ключ внутри form: назван неизвестным; находки: %+v %+v", res.Issues, res.Warnings)
	}
}

func TestLint_RefCardButtonInRootSilentWithoutLint(t *testing.T) {
	// Обычный `onebase check` без --lint поведения не меняет: находка
	// рекомендательная и приходит только с флагом.
	dir := refCardButtonProject(t, `schema: onebase.form/v1
ref_card_button: false
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ПолеВвода
    name: Номер
    data_path: Объект.Номер
`)
	res := RunFull(dir)
	if unvalidatedKeyMentions(res, "ref_card_button") {
		t.Errorf("lint-находка попала в обычный check; находки: %+v %+v", res.Issues, res.Warnings)
	}
}
