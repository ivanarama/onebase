package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Параметр обработки типа enum:<Перечисление> на форме — Переключатель
// (view: select и радио) выводит все значения перечисления с их подписями
// (titles). Раньше виртуальная сущность обработки превращала такой параметр в
// строку: в списке оставалось «— выбрать —» и текущее значение подписью-именем
// (создание задачи в колл-центре, «Кому задача»). Путь: открытие формы
// обработки processorForm.
func TestProcessorFormEnumParamSwitchShowsEnumValues(t *testing.T) {
	program := mustParse(t, "Процедура Выполнить() Экспорт\nКонецПроцедуры\n")
	enum := &metadata.Enum{Name: "ВариантыЗадачи", Values: []string{"Консультация", "Мастеру"},
		ValueTitles: map[string]map[string]string{
			"Консультация": {"ru": "Консультация оператора"},
			"Мастеру":      {"ru": "Задача мастеру"},
		}}
	for _, view := range []string{"select", ""} {
		t.Run("view="+view, func(t *testing.T) {
			form := processorExecutionForm(&metadata.FormElement{
				Kind: metadata.FormElementSwitch, Name: "ПолеВариант", View: view,
				TitleMap: map[string]string{"ru": "Кому задача"}, DataPath: "Объект.Вариант",
			})
			proc := &processor.Processor{
				Name:   "СозданиеЗадачи",
				Params: []processor.Param{{Name: "Вариант", Type: "enum:ВариантыЗадачи", Default: "Мастеру"}},
				Forms:  []*metadata.FormModule{form},
			}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, program)
			srv.reg.Load(runtime.LoadOptions{Programs: map[string]*ast.Program{proc.Name: program}, Enums: []*metadata.Enum{enum}})
			srv.reg.LoadProcessors([]*processor.Processor{proc})

			req := httptest.NewRequest(http.MethodGet, "/ui/processor/"+proc.Name, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("name", proc.Name)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			srv.processorForm(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			want := []string{`value="Консультация"`, `value="Мастеру"`, "Консультация оператора", "Задача мастеру"}
			for _, w := range want {
				if !strings.Contains(body, w) {
					t.Errorf("в разметке переключателя нет %s", w)
				}
			}
			if view == "select" && !strings.Contains(body, `<option value="Мастеру" selected>Задача мастеру</option>`) {
				t.Error("значение по умолчанию не выбрано в списке с подписью перечисления")
			}
		})
	}
}
