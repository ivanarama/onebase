package ui

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/msgstatus"
)

// Сообщить(Текст, СтатусСообщения.Важное) — как в 1С: ошибку выгрузки в 1С
// оператор должен увидеть красным. Путь пользовательский: событие формы по
// HTTP, затем панель сообщений /ui/messages. Статус доезжает до ответа формы
// (тост красным) и до панели, а текст не несёт видимых служебных символов.
func TestFormEventMessageStatusReachesFormAndPanel(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура НаименованиеПриИзменении()
	Сообщить("обычное");
	Сообщить("Заявка в 1С НЕ выгружена", СтатусСообщения.Важное);
	Сообщить("очень важное", MessageStatus.VeryImportant);
	Сообщить("внимание", СтатусСообщения.Внимание);
	Сообщить("опечатка в статусе", "НетТакогоСтатуса");
КонецПроцедуры
`, nil,
		[]*metadata.FormElement{{
			Kind:     metadata.FormElementField,
			Name:     "ПолеНаименование",
			DataPath: "Объект.Наименование",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnChange: "НаименованиеПриИзменении"},
		}})

	body := url.Values{}
	body.Set("_element", "ПолеНаименование")
	body.Set("_event", string(metadata.FormEventOnChange))
	body.Set("_kind", "object")
	body.Set("Наименование", "x")
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
	if !resp.OK {
		t.Fatalf("событие не выполнилось: %q", resp.Error)
	}

	want := []struct{ text, status string }{
		{"обычное", msgstatus.Ordinary},
		{"Заявка в 1С НЕ выгружена", msgstatus.Important},
		{"очень важное", msgstatus.VeryImportant},
		{"внимание", msgstatus.Attention},
		{"опечатка в статусе", msgstatus.Ordinary},
	}
	if len(resp.Messages) != len(want) {
		t.Fatalf("messages=%q, ждали %d", resp.Messages, len(want))
	}
	for i, w := range want {
		text, status := msgstatus.Split(resp.Messages[i])
		if text != w.text || status != w.status {
			t.Errorf("сообщение %d: текст %q статус %q, ждали %q %q", i, text, status, w.text, w.status)
		}
	}
	if resp.Messages[0] != "обычное" {
		t.Errorf("сообщение без статуса изменилось: %q", resp.Messages[0])
	}

	rec := httptest.NewRecorder()
	srv.messagesList(rec, httptest.NewRequest("GET", "/ui/messages", nil))
	var panel struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &panel); err != nil {
		t.Fatalf("панель: %v: %s", err, rec.Body.String())
	}
	if len(panel.Messages) != len(want) {
		t.Fatalf("панель: %+v", panel.Messages)
	}
	for i, w := range want {
		if panel.Messages[i].Text != w.text || panel.Messages[i].Status != w.status {
			t.Errorf("панель %d: %+v, ждали %q %q", i, panel.Messages[i], w.text, w.status)
		}
	}
	if strings.ContainsAny(rec.Body.String(), "⁣⁤") {
		t.Error("в JSON панели осталась служебная метка статуса")
	}
}

// Сообщение, показанное прямо на странице формы (например, после неудачной
// записи), со статусом Важное рисуется блоком ошибки, обычное — как раньше.
func TestManagedFormPageRendersImportantMessageAsError(t *testing.T) {
	form := &metadata.FormModule{Name: "ФормаОбъекта", Kind: "object", EntityName: "Задача", LayoutKind: metadata.FormLayoutManaged}
	ent := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Forms: []*metadata.FormModule{form}}
	data := map[string]any{
		"Entity": ent, "Form": form, "IsNew": true, "Lang": "ru",
		"Values": map[string]string{}, "RefOptions": map[string]any{}, "EnumOptions": map[string]any{},
		"TPRefOptions": map[string]any{}, "TablePartRows": map[string][]map[string]any{},
		"Messages": []string{"записано", msgstatus.Encode("в 1С НЕ выгружена", msgstatus.Important)},
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	if !strings.Contains(page, `<div class="msg-info">записано</div>`) {
		t.Error("обычное сообщение изменило вид")
	}
	if !strings.Contains(page, `<div class="error msg-important">в 1С НЕ выгружена</div>`) {
		t.Error("важное сообщение не показано блоком ошибки")
	}
	if strings.ContainsAny(page, "⁣⁤") {
		t.Error("на странице осталась служебная метка статуса")
	}
}
