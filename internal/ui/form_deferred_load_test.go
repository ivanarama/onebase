package ui

import (
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// ПослеОткрытия у группы: событие разрешено и нередактируемой группе, ответ
// отложенной загрузки (_deferred=1) несёт только изменённое обработчиком —
// неизменённые реквизиты и состояния элементов не возвращаются, чтобы не
// откатить сделанное пользователем за время загрузки. Через form-event.
func TestManagedFormDeferredAfterOpen(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура ЗагрузитьИсторию()
	Объект.История = "3 заявки";
КонецПроцедуры
`, nil, []*metadata.FormElement{
		{Kind: metadata.FormElementField, Name: "ПолеНаим", DataPath: "Объект.Наименование"},
		{Kind: metadata.FormElementGroupBox, Name: "ГруппаИстория", ReadOnly: true,
			Handlers: map[metadata.FormEventType]string{metadata.FormEventAfterOpen: "ЗагрузитьИсторию"},
			Children: []*metadata.FormElement{{Kind: metadata.FormElementField, Name: "ПолеИстория", DataPath: "История"}}},
	})
	ent.Forms[0].Attributes = []*metadata.FormAttribute{{Name: "История", TypeRef: "string"}, {Name: "Другое", TypeRef: "string"}}
	id := insertContragent(t, srv, ent, "Ромашка")

	if body := executeFormEditGET(t, srv, ent, id).Body.String(); !strings.Contains(body, `data-ob-after-open="ГруппаИстория"`) {
		t.Fatalf("у группы нет признака отложенной загрузки:\n%s", body)
	}
	ev := url.Values{
		"_id": {id.String()}, "_element": {"ГруппаИстория"}, "_event": {string(metadata.FormEventAfterOpen)}, "_deferred": {"1"},
		"Наименование": {"Ромашка-правка-пользователя"}, "История": {""}, "Другое": {"введено пока грузилось"},
	}
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, ev).Body.Bytes())
	if !resp.OK || !resp.Deferred {
		t.Fatalf("ответ отложенной загрузки: ok=%v deferred=%v error=%q", resp.OK, resp.Deferred, resp.Error)
	}
	if v := valueCIDeferred(resp.Values, "История"); v != "3 заявки" {
		t.Fatalf("изменённый обработчиком реквизит не пришёл: %v (values=%v)", v, resp.Values)
	}
	for _, k := range []string{"Наименование", "Другое"} {
		if _, ok := lookupCI(resp.Values, k); ok {
			t.Errorf("неизменённое %q вернулось в ответе и откатило бы правку пользователя: %v", k, resp.Values)
		}
	}
	if resp.ElementStates != nil {
		t.Error("ответ отложенной загрузки не должен нести состояния элементов")
	}
}

func lookupCI(values map[string]any, name string) (any, bool) {
	for k, v := range values {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

func valueCIDeferred(values map[string]any, name string) any {
	v, _ := lookupCI(values, name)
	return v
}

func TestDeferredLoadBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нужен node")
	}
	cmd := exec.Command(node, "--test", "static/deferred_load_behavior_test.js") //nolint:gosec // test-only executable resolved by exec.LookPath
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node deferred test: %v\n%s", err, out)
	}
}
