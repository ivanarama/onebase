package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// hint_from на форме обработки (cc2, А_СозданиеЗадачи): реквизит подсказки
// не имеет элемента на форме, и в values ответа события его нет. Значение
// после обработчика едет в elementStates.hints — клиент обновляет подсказку
// «ⓘ» так же, как у формы документа. Путь — публичный POST form-event.
func TestProcessorForm_HintFromAfterEvent(t *testing.T) {
	program := mustParse(t, `
Процедура Заполнить()
 Объект.Подсказка = "Только после 18:00";
КонецПроцедуры
Процедура Очистить()
 Объект.Подсказка = "";
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "КнПримечание", Hint: "Примечание направления", HintFrom: "Подсказка"},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Заполнить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Заполнить"}},
		&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Очистить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Очистить"}},
	)
	form.Attributes = []*metadata.FormAttribute{{Name: "Подсказка", TypeRef: "string"}}
	form.ProgramAST = program
	proc := &processor.Processor{Name: "СозданиеЗадачи", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	router := chi.NewRouter()
	srv.Mount(router)
	fire := func(button string) formEventResponse {
		t.Helper()
		values := url.Values{"_element": {button}, "_event": {string(metadata.FormEventOnClick)}}
		req := httptest.NewRequest(http.MethodPost, "/ui/processor/"+proc.Name+"/form-event", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
		resp := decodeFormEventResponse(t, rec.Body.Bytes())
		if !resp.OK {
			t.Fatalf("событие %s: %s", button, resp.Error)
		}
		return resp
	}
	resp := fire("Заполнить")
	if resp.ElementStates == nil || resp.ElementStates.Hints["Подсказка"] != "Только после 18:00" {
		t.Fatalf("после обработчика нет подсказки в elementStates.hints: %+v", resp.ElementStates)
	}
	resp = fire("Очистить")
	if resp.ElementStates == nil {
		t.Fatal("нет elementStates после очистки")
	}
	if v, ok := resp.ElementStates.Hints["Подсказка"]; !ok || v != "" {
		t.Fatalf("пустой реквизит должен приехать пустой строкой (клиент вернёт статический hint): %q %v", v, ok)
	}
}
