package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestProcessorPhoneComparison(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		testProcessorPhoneComparison(t, db)
	})
}

func testProcessorPhoneComparison(t *testing.T, db *storage.DB) {
	doc := &metadata.Entity{Name: "Звонок", Kind: metadata.KindDocument, Fields: []metadata.Field{
		{Name: "Телефон", Type: metadata.FieldTypeString, PII: true},
		{Name: "Филиал", Type: metadata.FieldTypeString},
	}}
	s := newSubmitTestServerOn(t, db, []*metadata.Entity{doc})
	s.ops = newOperationLimiter()
	first, second, repeat, hidden := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fixtures := []struct {
		id            uuid.UUID
		phone, branch string
	}{
		{first, "79120000001", "МСК"}, {second, "79120000002", "СПБ"},
		{repeat, "9120000001", "СПБ"}, {hidden, "79120000003", "ЛПЦ"},
	}
	for _, f := range fixtures {
		if err := db.Upsert(context.Background(), doc.Name, f.id, map[string]any{"Телефон": f.phone, "Филиал": f.branch}, doc); err != nil {
			t.Fatal(err)
		}
	}

	form := processorExecutionForm(
		&metadata.FormElement{Kind: metadata.FormElementField, Name: "Result", DataPath: "Объект.Результат"},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "Calculate",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Рассчитать"},
			Props:    map[string]any{phoneComparisonProp: "Звонок.Телефон"},
		},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "WithoutOptIn",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Рассчитать"},
		},
		&metadata.FormElement{
			Kind: metadata.FormElementButton, Name: "HiddenRecord",
			Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "НедоступнаяЗапись"},
			Props:    map[string]any{phoneComparisonProp: "Звонок.Телефон"},
		},
	)
	form.ProgramAST = mustParse(t, fmt.Sprintf(`Процедура Рассчитать()
    Ссылки = Новый Массив;
    Ссылки.Добавить("%[1]s");
    Ссылки.Добавить("%[2]s");
    Ссылки.Добавить("%[3]s");
    Ключи = СопоставлениеТелефонов.КлючиДокументов(Ссылки);
    Первый = Ключи.Получить("%[1]s");
    Другой = Ключи.Получить("%[2]s");
    ТотЖе = Ключи.Получить("%[3]s");
    Callista = СопоставлениеТелефонов.Ключ("9120000001");
    Если Первый = Другой ИЛИ Первый <> ТотЖе ИЛИ Первый <> Callista Тогда
        ВызватьИсключение "сопоставление нарушено";
    КонецЕсли;
    З = Новый Запрос;
    З.Текст = "ВЫБРАТЬ Телефон ИЗ Документ.Звонок";
    Для Каждого Стр Из З.Выполнить() Цикл
        Если Стр.Телефон = "79120000001" ИЛИ Стр.Телефон = "79120000002" ИЛИ Стр.Телефон = "9120000001" Тогда
            ВызватьИсключение "маскирование нарушено";
        КонецЕсли;
    КонецЦикла;
    Объект.Результат = "2|1|1";
КонецПроцедуры

Процедура НедоступнаяЗапись()
    Ссылки = Новый Массив;
    Ссылки.Добавить("%[4]s");
    Ключи = СопоставлениеТелефонов.КлючиДокументов(Ссылки);
КонецПроцедуры`, first, second, repeat, hidden))

	proc := &processor.Processor{Name: "PhoneTotals", Params: []processor.Param{{Name: "Результат", Type: "string"}}, Forms: []*metadata.FormModule{form}}
	s.reg.LoadProcessors([]*processor.Processor{proc})
	user := &auth.User{Login: "exporter", Roles: []*auth.Role{{
		Name: "ВыгрузкаКЦ",
		Permissions: auth.Permission{
			Documents:   map[string][]string{doc.Name: {"read"}},
			Processors:  map[string][]string{proc.Name: {"run"}},
			FieldAccess: auth.FieldAccess{Documents: map[string]auth.FieldPolicies{doc.Name: {"Телефон": {Read: "mask_all"}}}},
			RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{
				doc.Name: {"read": {Field: "Филиал", Op: "ne", Value: auth.RowValue{Literal: "ЛПЦ"}}},
			}},
		},
	}}}

	for _, tc := range []struct{ button, want string }{
		{"Calculate", "2|1|1"},
		{"WithoutOptIn", ""},
		{"HiddenRecord", ""},
	} {
		t.Run(tc.button, func(t *testing.T) {
			body := processorClickBody(tc.button)
			req := httptest.NewRequest(http.MethodPost, "/ui/processor/PhoneTotals/form-event", strings.NewReader(body.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			route := chi.NewRouteContext()
			route.URLParams.Add("name", proc.Name)
			req = req.WithContext(context.WithValue(auth.ContextWithUser(req.Context(), user), chi.RouteCtxKey, route))
			rec := httptest.NewRecorder()
			s.handleProcessorFormEvent(rec, req)
			response := decodeFormEventResponse(t, rec.Body.Bytes())
			if tc.want == "" {
				if response.OK {
					t.Fatalf("сравнение разрешено: %s", rec.Body)
				}
			} else if !response.OK || response.Values["Результат"] != tc.want {
				t.Fatalf("числа сравнения: %s", rec.Body)
			}
			for _, f := range fixtures {
				if strings.Contains(rec.Body.String(), f.phone) {
					t.Fatal("номер попал в ответ формы")
				}
			}
		})
	}
}
