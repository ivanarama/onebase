package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// A browser needs both notify and the collision-free dialog transport name.
// Exercise the rendered JSON and mounted HTTP routes together on both stores.
func TestFormNotifyQuestionFieldsHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, transport string
		fields, attrs   []string
	}{
		{name: "no collision", transport: "_question_fields"},
		{name: "entity field", fields: []string{"_question_fields"}, transport: "_ob_service_question_fields"},
		{name: "form attribute", attrs: []string{"_question_fields"}, transport: "_ob_service_question_fields"},
		{name: "fallback names", fields: []string{"_question_fields", "_ob_service_question_fields"}, attrs: []string{"_ob_service_question_fields_"}, transport: "_ob_service_question_fields__"},
		{name: "case insensitive", fields: []string{"_QUESTION_FIELDS"}, attrs: []string{"_OB_SERVICE_QUESTION_FIELDS"}, transport: "_ob_service_question_fields_"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				form := managedObjectForm(fieldEl("Наименование", "Объект.Наименование"))
				form.Handlers = map[metadata.FormEventType]string{
					metadata.FormEventNotify:      "Получить",
					metadata.FormEventBeforeClose: "Закрыть",
				}
				form.Elements = append(form.Elements,
					&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Вопрос",
						Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Спросить", metadata.FormEventOnAnswer: "Ответить"}},
					&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Отправить",
						Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Отправить"}})
				ent := &metadata.Entity{Name: "ОповещениеИДиалог", Kind: metadata.KindCatalog,
					Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}, Forms: []*metadata.FormModule{form}}
				body := url.Values{"Наименование": {"несохранённая правка"}}
				program := `
Процедура Спросить()
	Поля = Новый Массив;
	Поля.Добавить(Новый Структура("имя, правка, многострочное", "Примечание", Истина, Истина));
	ПоказатьВопрос("Продолжить?", "ДаНет", "Диалог", Поля);
КонецПроцедуры
Процедура Ответить()
	Сообщить("Ответ=" + ВопросОтвет + ";" + ДиалогПоля.Примечание);
	Сообщить("До ответа=" + Объект.Наименование);
`
				names := append(append([]string{}, tc.fields...), tc.attrs...)
				for i, name := range names {
					if i < len(tc.fields) {
						ent.Fields = append(ent.Fields, metadata.Field{Name: name, Type: metadata.FieldTypeString})
					} else {
						form.Attributes = append(form.Attributes, &metadata.FormAttribute{Name: name, TypeRef: "Строка"})
					}
					form.Elements = append(form.Elements, fieldEl(fmt.Sprintf("Поле%d", i), "Объект."+name))
					body.Set(name, fmt.Sprintf("значение %d", i))
					program += fmt.Sprintf("\tСообщить(Объект.%s);\n", name)
				}
				program += `
КонецПроцедуры
Процедура Отправить()
	Оповестить("Совместимость", "параметр", "источник");
КонецПроцедуры
Процедура Получить(Событие, Данные, Откуда)
	Сообщить("Оповещение=" + Событие + ";" + Данные + ";" + Откуда);
	Объект.Наименование = Объект.Наименование + ":получено";
КонецПроцедуры
Процедура Закрыть(Отказ)
	ВызватьИсключение("ПередЗакрытием не должен вызываться");
КонецПроцедуры
`
				form.ProgramAST = mustParse(t, program)
				srv := newSubmitTestServerOn(t, db, []*metadata.Entity{ent})
				srv.ops = newOperationLimiter()
				srv.hub = realtime.NewHub()
				_, events, cancel := srv.hub.Subscribe("", "", nil)
				defer cancel()
				router := chi.NewRouter()
				srv.Mount(router)
				base := "/ui/catalog/" + url.PathEscape(ent.Name)
				config := func() (bool, string) {
					t.Helper()
					page := httptest.NewRecorder()
					router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, base+"/new", nil))
					if page.Code != http.StatusOK {
						t.Fatalf("GET: status=%d body=%s", page.Code, page.Body.String())
					}
					script := managedConfigScript.FindStringSubmatch(page.Body.String())
					if script == nil {
						t.Fatal("GET carries no ob-managed-config")
					}
					var cfg struct {
						Notify        bool              `json:"notify"`
						ServiceFields map[string]string `json:"serviceFields"`
					}
					if err := json.Unmarshal([]byte(script[1]), &cfg); err != nil {
						t.Fatal(err)
					}
					return cfg.Notify, cfg.ServiceFields["_question_fields"]
				}
				post := func(values url.Values) formEventResponse {
					t.Helper()
					req := httptest.NewRequest(http.MethodPost, base+"/form-event", strings.NewReader(values.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						t.Fatalf("POST: status=%d body=%s", rec.Code, rec.Body.String())
					}
					return decodeFormEventResponse(t, rec.Body.Bytes())
				}
				notify, transport := config()
				if transport != tc.transport {
					t.Errorf("GET serviceFields._question_fields=%q, want %q", transport, tc.transport)
				}
				if !notify {
					t.Error("GET must advertise notify=true together with serviceFields")
				}
				if transport == "" {
					t.Fatal("browser has no dialog transport name")
				}
				body.Set("_element", "Вопрос")
				body.Set("_event", string(metadata.FormEventOnClick))
				resp := post(body)
				if !resp.OK || resp.Question == nil || len(resp.Question.Fields) != 1 || resp.Question.Fields[0].Name != "Примечание" {
					t.Fatalf("question: %+v", resp)
				}
				body.Set("_event", string(metadata.FormEventOnAnswer))
				body.Add(transport, `{"Примечание":"строка 1\nстрока 2"}`)
				body.Set("_question_answer", "Да")
				resp = post(body)
				want := []string{"Ответ=Да;строка 1\nстрока 2", "До ответа=несохранённая правка"}
				for i := range names {
					want = append(want, fmt.Sprintf("значение %d", i))
				}
				if !resp.OK || strings.Join(resp.Messages, "\x00") != strings.Join(want, "\x00") {
					t.Fatalf("answer: %+v, want messages=%q", resp, want)
				}
				for i, name := range names {
					if resp.Values[name] != fmt.Sprintf("значение %d", i) {
						t.Errorf("answer changed user field %q: %v", name, resp.Values[name])
					}
				}
				body.Del(transport)
				body.Del("_question_answer")
				body.Set("_element", "Отправить")
				body.Set("_event", string(metadata.FormEventOnClick))
				if resp = post(body); !resp.OK {
					t.Fatalf("sender: %+v", resp)
				}
				id := waitFormNotify(t, events, "Совместимость")
				body.Set("_element", "")
				body.Set("_event", string(metadata.FormEventNotify))
				body.Set("_notify_id", id)
				body.Set("Наименование", "правка после вопроса")
				resp = post(body)
				if !resp.OK || resp.Values["Наименование"] != "правка после вопроса:получено" || strings.Join(resp.Messages, "|") != "Оповещение=Совместимость;параметр;источник" {
					t.Fatalf("receiver lost current values or positional arguments: %+v", resp)
				}
				if resp.SavedID != "" || resp.Close != nil || resp.Navigation != nil || resp.Dirty == nil || !*resp.Dirty {
					t.Fatalf("notification saved or closed the dirty form: %+v", resp)
				}
				rows, err := db.List(context.Background(), ent.Name, ent, storage.ListParams{})
				if err != nil || len(rows) != 0 {
					t.Fatalf("notification wrote data: rows=%v err=%v", rows, err)
				}
				delete(form.Handlers, metadata.FormEventNotify)
				notify, transport = config()
				if notify || transport != tc.transport {
					t.Fatalf("undeclared handler GET: notify=%v transport=%q", notify, transport)
				}
				if resp = post(body); resp.OK || len(resp.Messages) != 0 {
					t.Fatalf("undeclared notification handler executed: %+v", resp)
				}
			})
		})
	}
}
