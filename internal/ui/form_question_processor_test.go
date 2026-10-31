package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
)

// #1683: формы обработок получают те же диалоговые билтины, что и формы
// сущностей. Фаза 1: Нажатие вызывает ПоказатьВопрос — вопрос доезжает до
// клиента; фаза 2: Ответ с _question_answer доставляет ВопросОтвет в
// обработчик.

func TestShowQuestion_ProcessorForm(t *testing.T) {
	formProgram := mustParse(t, `
Процедура ОформитьНажатие()
	ПоказатьВопрос("Продолжить?", "ДаНет", "Подтверждение");
КонецПроцедуры

Процедура ОформитьОтвет()
	Сообщить("Ответ: " + ВопросОтвет);
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Оформить",
			Handlers: map[metadata.FormEventType]string{
				metadata.FormEventOnClick:  "ОформитьНажатие",
				metadata.FormEventOnAnswer: "ОформитьОтвет",
			},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{Name: "ВопросОбработке", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	// Фаза 1: вопрос в ответе form-event обработки.
	body := processorClickBody("Оформить")
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("phase1 ok=false, error=%q", resp.Error)
	}
	if resp.Question == nil {
		t.Fatalf("processor form-event carries no question: %+v", resp)
	}
	if resp.Question.Text != "Продолжить?" || strings.Join(resp.Question.Variants, "|") != "Да|Нет" || resp.Question.Title != "Подтверждение" {
		t.Fatalf("question payload = %+v", resp.Question)
	}

	// Фаза 2: Ответ с _question_answer доставляет ВопросОтвет в обработчик.
	body2 := processorClickBody("Оформить")
	body2.Set("_event", string(metadata.FormEventOnAnswer))
	body2.Set("_question_answer", "Да")
	rec2 := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body2.Encode()))
	resp2 := decodeFormEventResponse(t, rec2.Body.Bytes())
	if !resp2.OK {
		t.Fatalf("phase2 ok=false, error=%q", resp2.Error)
	}
	if !strings.Contains(strings.Join(resp2.Messages, "\n"), "Ответ: Да") {
		t.Fatalf("Ответ handler did not receive ВопросОтвет: %v", resp2.Messages)
	}
}

// Поля диалога в форме обработки: введённое возвращается в ДиалогПоля тем же
// путём, что и ВопросОтвет.
func TestShowQuestion_ProcessorFormDialogFields(t *testing.T) {
	formProgram := mustParse(t, `
Процедура ОформитьОтвет()
	Сообщить("Примечание=" + ДиалогПоля.Примечание);
КонецПроцедуры
`)
	form := processorExecutionForm(
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Оформить",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnAnswer: "ОформитьОтвет"},
		},
	)
	form.ProgramAST = formProgram
	proc := &processor.Processor{Name: "ВопросСПолями", Forms: []*metadata.FormModule{form}}
	srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

	body := processorClickBody("Оформить")
	body.Set("_event", string(metadata.FormEventOnAnswer))
	body.Set("_question_answer", "Сохранить")
	body.Set("_question_fields", `{"Примечание":"перезвонить после 18"}`)
	rec := postProcessorFormEventExecution(t, srv, proc.Name,
		"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode()))
	resp := decodeFormEventResponse(t, rec.Body.Bytes())
	if !resp.OK {
		t.Fatalf("ok=false, error=%q", resp.Error)
	}
	if !strings.Contains(strings.Join(resp.Messages, "\n"), "Примечание=перезвонить после 18") {
		t.Fatalf("ДиалогПоля не дошёл до обработчика обработки: %v", resp.Messages)
	}
}

// GET supplies the browser's service-field mapping; both form-event phases
// must keep dialog input separate from legal processor parameters.
func TestShowQuestion_ProcessorDialogFieldsNameCollision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params []string
	}{
		{name: "no collision"},
		{name: "dialog field parameter", params: []string{"_question_fields"}},
		{name: "auxiliary name also declared", params: []string{"_question_fields", "_ob_service_question_fields"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := `
Процедура ОформитьНажатие()
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("имя, правка, многострочное", "Примечание", Истина, Истина));
	ПоказатьВопрос("Сохранить?", "ДаНет", "", Поля);
КонецПроцедуры

Процедура ОформитьОтвет()
	Сообщить("Ответ=" + ВопросОтвет + "; Примечание=" + ДиалогПоля.Примечание);
`
			var elements []*metadata.FormElement
			var params []processor.Param
			body := processorClickBody("Оформить")
			want := []string{"Ответ=Да; Примечание=строка 1\nстрока 2"}
			for i, name := range tc.params {
				params = append(params, processor.Param{Name: name, Type: "string"})
				elements = append(elements, &metadata.FormElement{
					Kind: metadata.FormElementField, Name: fmt.Sprintf("Параметр%d", i), DataPath: "Объект." + name,
				})
				value := fmt.Sprintf("параметр %d", i)
				body.Set(name, value)
				program += fmt.Sprintf("\tСообщить(Объект.%s);\n", name)
				want = append(want, value)
			}
			program += "КонецПроцедуры\n"
			elements = append(elements, &metadata.FormElement{
				Kind: metadata.FormElementButton, Name: "Оформить",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnClick:  "ОформитьНажатие",
					metadata.FormEventOnAnswer: "ОформитьОтвет",
				},
			})
			form := processorExecutionForm(elements...)
			form.ProgramAST = mustParse(t, program)
			proc := &processor.Processor{Name: "ДиалогИПараметры", Params: params, Forms: []*metadata.FormModule{form}}
			srv, _ := newProcessorFormEventExecutionServer(t, proc, nil)

			page := httptest.NewRecorder()
			srv.processorForm(page, reqWithChi(http.MethodGet, "/ui/processor/"+proc.Name, nil, map[string]string{"name": proc.Name}))
			if page.Code != http.StatusOK {
				t.Fatalf("GET status=%d body=%s", page.Code, page.Body.String())
			}
			config := managedConfigScript.FindStringSubmatch(page.Body.String())
			if config == nil {
				t.Fatal("GET carries no ob-managed-config")
			}
			var cfg struct {
				ServiceFields map[string]string `json:"serviceFields"`
			}
			if err := json.Unmarshal([]byte(config[1]), &cfg); err != nil {
				t.Fatal(err)
			}
			// Match managed.js: an absent mapping falls back to the original key.
			fieldName := cfg.ServiceFields["_question_fields"]
			if fieldName == "" {
				fieldName = "_question_fields"
			}
			resp := decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
				"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode())).Body.Bytes())
			if !resp.OK || resp.Question == nil || len(resp.Question.Fields) != 1 ||
				resp.Question.Fields[0].Name != "Примечание" || !resp.Question.Fields[0].Editable || !resp.Question.Fields[0].Multiline {
				t.Fatalf("phase1: %+v", resp)
			}

			body.Set("_event", string(metadata.FormEventOnAnswer))
			body.Set("_question_answer", "Да")
			body.Add(fieldName, `{"Примечание":"строка 1\nстрока 2"}`)
			resp = decodeFormEventResponse(t, postProcessorFormEventExecution(t, srv, proc.Name,
				"application/x-www-form-urlencoded; charset=utf-8", strings.NewReader(body.Encode())).Body.Bytes())
			if !resp.OK || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("phase2: ok=%v error=%q messages=%q, want=%q", resp.OK, resp.Error, resp.Messages, want)
			}
		})
	}
}
