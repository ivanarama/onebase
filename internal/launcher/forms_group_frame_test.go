package launcher

import (
	"strings"
	"testing"
)

// groupFrameSample — группа с заголовком: на ней проверяется снятие галок
// «Отображать заголовок» и «Выделять группу».
const groupFrameSample = `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ГруппаФормы
    name: Реквизиты
    title:
      ru: Реквизиты
    children:
      - kind: ПолеВвода
        name: ПолеНомер
        data_path: Объект.Номер
`

// Снятая галка «Отображать заголовок» пишется в YAML булевым false (строка
// "false" не декодируется в *bool и уронила бы загрузку формы), а холст сразу
// перестаёт рисовать legend — иначе настройку нельзя проверить до сохранения.
func TestApplyEditOp_GroupShowTitle(t *testing.T) {
	res, err := applyEditOp([]byte(groupFrameSample), editOpRequest{
		Op: "setProp", Node: "elements.0", Key: "show_title", Value: "false",
	})
	if err != nil {
		t.Fatalf("setProp show_title: %v", err)
	}
	if !strings.Contains(res.YAML, "show_title: false") {
		t.Errorf("ключ show_title не записан булевым false:\n%s", res.YAML)
	}
	if strings.Contains(res.YAML, `show_title: "false"`) {
		t.Errorf("значение записано строкой — загрузка формы упадёт:\n%s", res.YAML)
	}
	if info := res.Model["elements.0"]; info.ShowTitle {
		t.Errorf("модель сообщает заголовок включённым: %+v", info)
	}
	if strings.Contains(res.CanvasHTML, "<legend") {
		t.Errorf("холст рисует заголовок группы после снятия галки:\n%s", res.CanvasHTML)
	}
}

// Поставленная галка удаляет ключ, а не пишет true: форма без ключа — это и есть
// «включено», и обрастать show_title: true при каждом сохранении она не должна.
func TestApplyEditOp_GroupShowTitleRestoredByDeletingKey(t *testing.T) {
	off, err := applyEditOp([]byte(groupFrameSample), editOpRequest{
		Op: "setProp", Node: "elements.0", Key: "show_title", Value: "false",
	})
	if err != nil {
		t.Fatalf("setProp show_title: %v", err)
	}
	on, err := applyEditOp([]byte(off.YAML), editOpRequest{
		Op: "delProp", Node: "elements.0", Key: "show_title",
	})
	if err != nil {
		t.Fatalf("delProp show_title: %v", err)
	}
	if strings.Contains(on.YAML, "show_title") {
		t.Errorf("ключ остался в YAML после включения галки:\n%s", on.YAML)
	}
	if !strings.Contains(on.CanvasHTML, `>Реквизиты</legend>`) {
		t.Errorf("холст не вернул заголовок группы:\n%s", on.CanvasHTML)
	}
	if !on.Model["elements.0"].ShowTitle {
		t.Error("модель не вернула заголовок включённым")
	}
}

// Снятая галка «Выделять группу» пишется bordered: false и сразу видна на
// холсте: рамки и внутренних отступов у группы нет.
func TestApplyEditOp_GroupBordered(t *testing.T) {
	res, err := applyEditOp([]byte(groupFrameSample), editOpRequest{
		Op: "setProp", Node: "elements.0", Key: "bordered", Value: "false",
	})
	if err != nil {
		t.Fatalf("setProp bordered: %v", err)
	}
	if !strings.Contains(res.YAML, "bordered: false") {
		t.Errorf("ключ bordered не записан булевым false:\n%s", res.YAML)
	}
	if info := res.Model["elements.0"]; info.Bordered {
		t.Errorf("модель сообщает рамку включённой: %+v", info)
	}
	if !strings.Contains(res.CanvasHTML, "ob-group-plain") {
		t.Errorf("холст не пометил группу без рамки:\n%s", res.CanvasHTML)
	}
	// По умолчанию рамка есть: без ключа группа остаётся обычной.
	if res, err = applyEditOp([]byte(groupFrameSample), editOpRequest{Op: "render"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(res.CanvasHTML, "ob-group-plain") {
		t.Errorf("без ключа bordered холст рисует группу без рамки:\n%s", res.CanvasHTML)
	}
	if !res.Model["elements.0"].Bordered {
		t.Error("по умолчанию модель обязана сообщать рамку включённой")
	}
}
