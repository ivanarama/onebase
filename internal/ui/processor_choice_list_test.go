package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// Форма обработки с ПолеСписка: варианты списка значений (choices) приходят в
// разметку, и форма отрисовывается целиком. Раньше данные формы обработки не
// несли ChoiceOptions, шаблон падал на первом ПолеСписка («index of untyped
// nil»), и всё, что ниже, — таблица, кнопки — до браузера не доходило.
// Проверяются оба пути рендера: открытие формы и показ после «Выполнить».
func TestProcessorManagedFormRendersChoiceList(t *testing.T) {
	program := mustParse(t, `
Процедура Выполнить() Экспорт
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementInputList, Name: "ПолеВидСписка",
			TitleMap: map[string]string{"ru": "Список"}, DataPath: "Объект.ВидСписка",
			Choices: []metadata.FormChoice{
				{Value: "Мои", Title: map[string]string{"ru": "Мои заявки"}},
				{Value: "ВБригаде", Title: map[string]string{"ru": "В бригаде"}},
			},
		},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "КнОбновить",
			TitleMap: map[string]string{"ru": "Обновить"},
		},
	)
	proc := &processor.Processor{
		Name:   "ВыборСписка",
		Params: []processor.Param{{Name: "ВидСписка", Type: "string", Default: "ВБригаде"}},
		Forms:  []*metadata.FormModule{form},
	}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, program)

	call := func(method string, handler http.HandlerFunc) string {
		t.Helper()
		req := httptest.NewRequest(method, "/ui/processor/"+proc.Name, strings.NewReader("ВидСписка=ВБригаде"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("name", proc.Name)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", method, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	for _, c := range []struct {
		name    string
		method  string
		handler http.HandlerFunc
	}{
		{"открытие формы", http.MethodGet, srv.processorForm},
		{"после «Выполнить»", http.MethodPost, srv.processorRun},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := call(c.method, c.handler)
			if strings.Contains(body, "error calling index") {
				t.Fatalf("шаблон формы обработки упал на ПолеСписка:\n%s", body)
			}
			for _, want := range []string{
				`<option value="Мои" >Мои заявки</option>`,
				`<option value="ВБригаде" selected>В бригаде</option>`,
				`data-ob-el="КнОбновить"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("в разметке нет %s", want)
				}
			}
		})
	}
}
