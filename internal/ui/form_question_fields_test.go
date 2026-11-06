package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Поля диалога ПоказатьВопрос: фаза 1 отдаёт их в question.fields, фаза 2
// возвращает введённое обработчику Ответ в ДиалогПоля. Через публичный
// form-event, как из браузера.
func TestShowQuestion_FieldsRoundTrip(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура КомандаНажатие()
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("заголовок, значение, выделить", "Заявка", "ЗК-17", Истина));
	Поля.Добавить(Новый Структура("имя, заголовок, правка, многострочное", "Ошибка", "Текст ошибки", Истина, Истина));
	ПоказатьВопрос("Уведомить тех. отдел?", "ДаНет", "Ошибка", Поля);
КонецПроцедуры

Процедура КомандаОтвет()
	Сообщить("Ответ: " + ВопросОтвет + "; Ошибка=" + ДиалогПоля.Ошибка);
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind: metadata.FormElementButton,
		Name: "Команда",
		Handlers: map[metadata.FormEventType]string{
			metadata.FormEventOnClick:  "КомандаНажатие",
			metadata.FormEventOnAnswer: "КомандаОтвет",
		},
	}})

	click := url.Values{}
	click.Set("_element", "Команда")
	click.Set("_event", string(metadata.FormEventOnClick))
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, click).Body.Bytes())
	if !resp.OK || resp.Question == nil {
		t.Fatalf("фаза 1: ok=%v error=%q question=%+v", resp.OK, resp.Error, resp.Question)
	}
	f := resp.Question.Fields
	if len(f) != 2 {
		t.Fatalf("fields=%+v", f)
	}
	if f[0].Value != "ЗК-17" || !f[0].Strong || f[0].Editable || f[0].Name == "" {
		t.Fatalf("строка для показа: %+v", f[0])
	}
	if f[1].Name != "Ошибка" || !f[1].Editable || !f[1].Multiline {
		t.Fatalf("многострочное поле ввода: %+v", f[1])
	}

	answer := url.Values{}
	answer.Set("_element", "Команда")
	answer.Set("_event", string(metadata.FormEventOnAnswer))
	answer.Set("_question_answer", "Да")
	answer.Set("_question_fields", `{"Ошибка":"не открывается карточка"}`)
	resp = decodeFormEventResponse(t, executeFormEvent(t, srv, ent, answer).Body.Bytes())
	if !resp.OK {
		t.Fatalf("фаза 2: %q", resp.Error)
	}
	if got := strings.Join(resp.Messages, "\n"); !strings.Contains(got, "Ответ: Да; Ошибка=не открывается карточка") {
		t.Fatalf("обработчик Ответ не получил ДиалогПоля: %v", resp.Messages)
	}
}

// «строк» доезжает до клиента числом видимых строк; больше одной — поле
// многострочное и без «многострочное»; мусор и ноль — по умолчанию.
func TestShowQuestion_FieldRows(t *testing.T) {
	srv, ent := setupManagedEventsServer(t, `
Процедура КомандаНажатие()
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("имя, правка, многострочное, строк", "А", Истина, Истина, 7));
	Поля.Добавить(Новый Структура("имя, правка, строк", "Б", Истина, 3));
	Поля.Добавить(Новый Структура("имя, правка, многострочное, строк", "В", Истина, Истина, "много"));
	Поля.Добавить(Новый Структура("имя, правка, многострочное, строк", "Г", Истина, Истина, 500));
	ПоказатьВопрос("Вопрос", "ОК", "", Поля);
КонецПроцедуры
`, nil, []*metadata.FormElement{{
		Kind:     metadata.FormElementButton,
		Name:     "Команда",
		Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "КомандаНажатие"},
	}})
	click := url.Values{}
	click.Set("_element", "Команда")
	click.Set("_event", string(metadata.FormEventOnClick))
	resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, click).Body.Bytes())
	if !resp.OK || resp.Question == nil || len(resp.Question.Fields) != 4 {
		t.Fatalf("ok=%v error=%q question=%+v", resp.OK, resp.Error, resp.Question)
	}
	f := resp.Question.Fields
	if f[0].Rows != 7 || !f[0].Multiline {
		t.Errorf("строк 7: %+v", f[0])
	}
	if f[1].Rows != 3 || !f[1].Multiline {
		t.Errorf("строк 3 без «многострочное» — поле многострочное: %+v", f[1])
	}
	if f[2].Rows != 0 || !f[2].Multiline {
		t.Errorf("строк не число — по умолчанию: %+v", f[2])
	}
	if f[3].Rows != questionFieldMaxRows {
		t.Errorf("строк 500 — не больше %d: %+v", questionFieldMaxRows, f[3])
	}
}

func TestParseQuestionFields_DropsNonStrings(t *testing.T) {
	if parseQuestionFields(`{"a": 1, "": "x"}`) != nil {
		t.Fatal("нестроковые значения и пустые имена отбрасываются")
	}
	if parseQuestionFields(`не json`) != nil || parseQuestionFields("") != nil {
		t.Fatal("мусор не даёт структуры")
	}
}

// The browser must carry dialog JSON separately from both entity fields and
// form-only attributes, even when a fallback transport name is also declared.
func TestShowQuestion_EntityDialogFieldsNameCollision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
		attrs  []string
	}{
		{name: "no collision"},
		{name: "entity field", fields: []string{"_question_fields"}},
		{name: "form attribute", attrs: []string{"_question_fields"}},
		{name: "fallback names", fields: []string{"_question_fields", "_ob_service_question_fields"}, attrs: []string{"_ob_service_question_fields_"}},
		{name: "case insensitive", fields: []string{"_QUESTION_FIELDS"}, attrs: []string{"_OB_SERVICE_QUESTION_FIELDS"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := `
Процедура КомандаНажатие()
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("имя, правка, многострочное", "Примечание", Истина, Истина));
	ПоказатьВопрос("Сохранить?", "ДаНет", "", Поля);
КонецПроцедуры

Процедура КомандаОтвет()
	Сообщить("Ответ=" + ВопросОтвет + "; Примечание=" + ДиалогПоля.Примечание);
`
			ent := &metadata.Entity{Name: "ДиалогИРеквизиты", Kind: metadata.KindCatalog}
			form := managedObjectForm()
			ent.Forms = []*metadata.FormModule{form}
			body := url.Values{"_element": {"Команда"}, "_event": {string(metadata.FormEventOnClick)}}
			want := []string{"Ответ=Да; Примечание=строка 1\nстрока 2"}
			names := append(append([]string{}, tc.fields...), tc.attrs...)
			for i, name := range names {
				if i < len(tc.fields) {
					ent.Fields = append(ent.Fields, metadata.Field{Name: name, Type: metadata.FieldTypeString})
				} else {
					form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: name, TypeRef: "Строка"})
				}
				form.Elements = append(form.Elements, fieldEl(fmt.Sprintf("Поле%d", i), "Объект."+name))
				value := fmt.Sprintf("реквизит %d", i)
				body.Set(name, value)
				program += fmt.Sprintf("\tСообщить(Объект.%s);\n", name)
				want = append(want, value)
			}
			program += "КонецПроцедуры\n"
			form.ProgramAST = mustParse(t, program)
			form.Elements = append(form.Elements, &metadata.FormElement{
				Kind: metadata.FormElementButton, Name: "Команда",
				Handlers: map[metadata.FormEventType]string{
					metadata.FormEventOnClick:  "КомандаНажатие",
					metadata.FormEventOnAnswer: "КомандаОтвет",
				},
			})
			srv, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
			page := httptest.NewRecorder()
			srv.form(page, reqWithChi(http.MethodGet, "/ui/catalog/"+ent.Name+"/new", nil,
				map[string]string{"kind": "catalog", "entity": ent.Name}))
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
			fieldName := cfg.ServiceFields["_question_fields"]
			if fieldName == "" {
				fieldName = "_question_fields"
			}
			resp := decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
			if !resp.OK || resp.Question == nil || len(resp.Question.Fields) != 1 || !resp.Question.Fields[0].Multiline {
				t.Fatalf("phase1: %+v", resp)
			}
			body.Set("_event", string(metadata.FormEventOnAnswer))
			body.Set("_question_answer", "Да")
			// managed.js appends extra parameters after serializing the form.
			body.Add(fieldName, `{"Примечание":"строка 1\nстрока 2"}`)
			resp = decodeFormEventResponse(t, executeFormEvent(t, srv, ent, body).Body.Bytes())
			if !resp.OK || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("phase2: ok=%v error=%q messages=%q, want=%q", resp.OK, resp.Error, resp.Messages, want)
			}
		})
	}
}
