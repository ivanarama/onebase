package launcher

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/formdoc"
)

// Галка «Ввод по строке» в панели свойств конструктора форм: ключ
// input_by_string пишется в YAML булевым скаляром, читается обратно в модель
// холста (иначе галка не отобразится), а снятие удаляет ключ — умолчание и есть
// «выключено», как у show_title/bordered.
func TestCanvasModel_InputByString(t *testing.T) {
	src := `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
elements:
  - kind: ПолеВвода
    name: ПолеСклад
    data_path: Объект.Склад
    input_by_string: true
`
	doc, err := formdoc.Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	model, err := canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	if !model["elements.0"].InputByString {
		t.Fatalf("input_by_string не попал в модель панели свойств: %+v", model["elements.0"])
	}

	disabled := strings.Replace(src, "    input_by_string: true\n", "", 1)
	doc, err = formdoc.Load([]byte(disabled))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	model, err = canvasModel(doc)
	if err != nil {
		t.Fatalf("canvasModel: %v", err)
	}
	if model["elements.0"].InputByString {
		t.Fatalf("без ключа галка должна быть снята: %+v", model["elements.0"])
	}
}
