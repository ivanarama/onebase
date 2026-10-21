package ui

import (
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
