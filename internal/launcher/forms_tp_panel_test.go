package launcher

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/formdoc"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Панель свойств табличной части предлагала одно событие из шести и не знала
// про auto_sum. Настройки существовали в рантайме и документации, но ставились
// руками в YAML — поэтому их и не находили (план 154, Р1/Р2).
func formsEditorScript(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	data := &configuratorData{
		Base: &Base{ID: "b1"},
		EditingForm: &cfgManagedForm{
			Entity: "Заказ", Name: "ФормаОбъекта", Kind: "object",
			YAML: "schema: onebase.form/v1\n",
		},
	}
	if err := formsTmpl.ExecuteTemplate(&buf, "forms-editor", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func TestПанельСвойствТЧ_ПредлагаетВсеСобытияТабличнойЧасти(t *testing.T) {
	script := formsEditorScript(t)
	// Источник правды о том, какие события элемент реально отправляет, —
	// browserEventAllowedForElement на сервере. Панель обязана предлагать их
	// все: событие, которого в ней нет, пишется руками и потому не пишется.
	for _, event := range []string{
		"ПриИзменении", "ПриИзмененииСтроки", "ПриДобавленииСтроки",
		"ПослеДобавленияСтроки", "ПриУдаленииСтроки", "ПриАктивизацииСтроки",
	} {
		if !strings.Contains(script, "'"+event+"'") {
			t.Errorf("панель не предлагает событие %q табличной части", event)
		}
	}
}

func TestПанельСвойствТЧ_ГалочкаAutoSum(t *testing.T) {
	script := formsEditorScript(t)
	if !strings.Contains(script, "setProp('auto_sum'") {
		t.Errorf("в панели свойств ТЧ нет настройки auto_sum:\n%s",
			extractContext(script, "no_grid", 400))
	}
}

func TestПанельСвойствТЧ_КолонкаПолучаетСобытие(t *testing.T) {
	script := formsEditorScript(t)
	if !strings.Contains(script, "case 'Колонка':") {
		t.Errorf("колонке ТЧ не предлагается ни одного события:\n%s",
			extractContext(script, "applicableEvents", 500))
	}
}

// auto_sum — булев ключ: чекбокс шлёт "true"/"", и в YAML обязан лечь скаляром
// true, а не строкой. Строка "true" в auto_sum сломала бы декод FormElement.
func TestРедакторФормы_AutoSumПишетсяБулевым(t *testing.T) {
	src := []byte(`schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТабСтроки
    data_path: Объект.Строки
`)
	doc, err := formdoc.Load(src)
	if err != nil {
		t.Fatalf("formdoc.Load: %v", err)
	}
	nodes, err := doc.Elements()
	if err != nil {
		t.Fatalf("Elements: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("элементов %d, ожидался 1", len(nodes))
	}

	result, err := applyEditOp(src, editOpRequest{
		Op: "setProp", Node: nodes[0].NodeID, Key: "auto_sum", Value: "true",
	})
	if err != nil {
		t.Fatalf("applyEditOp: %v", err)
	}
	if !strings.Contains(result.YAML, "auto_sum: true") {
		t.Fatalf("auto_sum записан не булевым скаляром:\n%s", result.YAML)
	}

	reloaded, err := formdoc.Load([]byte(result.YAML))
	if err != nil {
		t.Fatalf("перечитывание YAML: %v", err)
	}
	els, err := reloaded.Elements()
	if err != nil {
		t.Fatalf("Elements после правки: %v", err)
	}
	if len(els) != 1 || els[0].El.Kind != metadata.FormElementTablePart || !els[0].El.AutoSum {
		t.Fatalf("после перечитывания auto_sum не взведён: %+v", els[0].El)
	}
}
