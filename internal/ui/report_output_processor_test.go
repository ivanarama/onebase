package ui

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
)

func TestReportOutputProcessorPublicPaths(t *testing.T) {
	entity := &metadata.Entity{Name: "Clients", Kind: metadata.KindCatalog, Presentation: []string{"Label"}, Fields: []metadata.Field{
		{Name: "Label", Type: metadata.FieldTypeString}, {Name: "Phone", Type: metadata.FieldTypeString, PII: true},
		{Name: "House", Type: metadata.FieldTypeString, ReportPII: true}, {Name: "City", Type: metadata.FieldTypeString}, {Name: "Amount", Type: metadata.FieldTypeNumber},
	}}
	program := mustParse(t, `
Процедура Выполнить()
    Запрос = Новый Запрос;
    Запрос.Текст = "ВЫБРАТЬ Phone, House, City, Amount, Ссылка КАК Link ИЗ Справочник.Clients";
    Для Каждого Выборка Из Запрос.Выполнить() Цикл
        Сообщить("Query=" + Строка(Выборка.Phone) + "|" + Строка(Выборка.House) + "|" + Строка(Выборка.City) + "|" + Строка(Выборка.Amount));
        СсылкаКлиента = Справочники.Clients.НайтиПоИдентификатору(Выборка.Link.УникальныйИдентификатор());
        Об = СсылкаКлиента.ПолучитьОбъект();
        Сообщить("Object=" + Строка(Об.Phone) + "|" + Строка(Об.House) + "|" + Строка(Об.City));
        Сообщить("Reference=" + Строка(СсылкаКлиента));
    КонецЦикла;
КонецПроцедуры
`)
	proc := &processor.Processor{Name: "PrivacyProcessor", Kind: "report", Forms: []*metadata.FormModule{processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Выполнить"})}}
	s, db := newProcessorFormEventExecutionServer(t, proc, program)
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Programs: map[string]*ast.Program{proc.Name: program}})
	s.reg.LoadProcessors([]*processor.Processor{proc})
	ctx := context.Background()
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Label": "79990009987 HOUSE_987", "Phone": "79990009987", "House": "HOUSE_987", "City": "SAFE_CITY", "Amount": 12345}, entity); err != nil {
		t.Fatal(err)
	}
	permission := auth.Permission{
		Catalogs: map[string][]string{entity.Name: {"read", "disclose"}}, Processors: map[string][]string{proc.Name: {"run"}},
		FieldAccess: auth.FieldAccess{Catalogs: map[string]auth.FieldPolicies{entity.Name: {"Phone": {Read: "full"}}}},
	}
	for _, user := range []*auth.User{{IsAdmin: true}, {Roles: []*auth.Role{{Permissions: permission}}}} {
		for _, kind := range []string{"report", ""} {
			proc.Kind = kind
			for _, channel := range []string{"run", "form-event", "offline"} {
				var output string
				if channel == "offline" {
					messages, runErr, err := s.RunProcessor(auth.ContextWithUser(ctx, user), s.reg, proc.Name, nil, nil, nil)
					if err != nil || runErr != nil {
						t.Fatalf("offline: %v %v", err, runErr)
					}
					output = strings.Join(messages, "\n")
				} else {
					body := url.Values{}
					if channel == "form-event" {
						body = processorClickBody("Выполнить")
					}
					r := reqWithChi("POST", "/ui/processor/"+proc.Name+"/"+channel, body, map[string]string{"name": proc.Name})
					r = r.WithContext(auth.ContextWithUser(r.Context(), user))
					w := httptest.NewRecorder()
					if channel == "run" {
						s.processorRun(w, r)
					} else {
						s.handleProcessorFormEvent(w, r)
					}
					if w.Code != 200 {
						t.Fatalf("%s %s: %d %s", kind, channel, w.Code, w.Body.String())
					}
					output = w.Body.String()
				}
				if !strings.Contains(output, "SAFE_CITY") || !strings.Contains(output, "12345") {
					t.Fatalf("%s %s lost ordinary result", kind, channel)
				}
				for _, secret := range []string{"79990009987", "HOUSE_987"} {
					if strings.Contains(output, secret) != (kind == "") {
						t.Fatalf("%s %s unexpected privacy boundary for %s: %s", kind, channel, secret, output)
					}
				}
			}
		}
	}
}
