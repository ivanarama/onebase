package ui

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestTabularPayloadHTTPMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		for _, mode := range []string{"nested-roundtrip", "tampered-client-data", "current-rls-denied", "foreign-session", "wrong-cell", "expired", "callback-copy", "export-does-not-call", "question-answer", "question-current-rls-denied"} {
			t.Run(mode, func(t *testing.T) {
				ctx := context.Background()
				s, user, _ := tabularFixture(t, db)
				target := &metadata.Entity{Name: "PayloadMaster", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Name", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}}}
				if err := db.Migrate(ctx, []*metadata.Entity{target}); err != nil {
					t.Fatal(err)
				}
				id := uuid.New()
				name := "own-" + id.String()
				fields := map[string]any{"Name": name, "Owner": user.Login}
				if err := db.Upsert(ctx, target.Name, id, fields, target); err != nil {
					t.Fatal(err)
				}
				s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{target}})
				user.Roles[0].Permissions.Catalogs = map[string][]string{target.Name: {"read"}}
				user.Roles[0].Permissions.RowAccess = auth.RowAccess{Catalogs: map[string]auth.RowPolicies{target.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}}}}
				form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Build", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Build"}}, &metadata.FormElement{Kind: metadata.FormElementSpreadsheet, Name: "Sheet", DataPath: "Объект.Result", Handlers: map[metadata.FormEventType]string{metadata.FormEventDrilldown: "Expand"}})
				form.Attributes = []*metadata.FormAttribute{{Name: "Result", TypeRef: "SpreadsheetDocument"}}
				form.Elements[1].ReadOnly = true
				source := `Процедура Build()
      Д=Новый ТабличныйДокумент;
      Д.Ячейка(1,1).Текст="Detail";
      М=Новый Массив;
      С=Новый Структура("Amount,Master,PrivateMarker",7,Справочники.PayloadMaster.НайтиПоРеквизиту("Name","OWNNAME"),"server_private_context");
      Д.Ячейка(1,1).Расшифровка=М;
      Вложенный=Новый Массив;Вложенный.Добавить(С);М.Добавить(Вложенный);
      С.Amount=8;
      Объект.Result=Д;
     КонецПроцедуры
     Процедура Expand()
      Если Расшифровка[0][0].Amount<>8 Тогда ВызватьИсключение "wrong frozen amount";КонецЕсли;
      Если Расшифровка[0][0].PrivateMarker<>"server_private_context" Тогда ВызватьИсключение "lost context";КонецЕсли;
      Мастер=Расшифровка[0][0].Master;
      Расшифровка[0][0].Amount=0;
      ОткрытьФорму(Мастер);
     КонецПроцедуры`
				if mode == "export-does-not-call" {
					source = strings.ReplaceAll(source, "Мастер=Расшифровка[0][0].Master;", "ВызватьИсключение \"callback reached\";")
				}
				if strings.HasPrefix(mode, "question-") {
					form.Elements[1].Handlers[metadata.FormEventOnAnswer] = "Expand"
					source = strings.ReplaceAll(source, "Процедура Expand()", "Процедура Expand()\nЕсли ВопросОтвет=\"\" Тогда ПоказатьВопрос(\"Создать документ?\",\"ДаНет\");Возврат;КонецЕсли;\nЕсли ВопросОтвет<>\"Да\" Тогда Возврат;КонецЕсли;")
				}
				form.ProgramAST = mustParse(t, strings.ReplaceAll(source, "OWNNAME", name))
				proc := &processor.Processor{Name: "PayloadProcessor", Forms: []*metadata.FormModule{form}}
				s.reg.LoadProcessors([]*processor.Processor{proc})
				user.Roles[0].Permissions.Processors = map[string][]string{proc.Name: {"run"}}
				endpoint := "/ui/processor/" + proc.Name + "/form-event"
				build := tabularHTTP(s, user, "own-session", "POST", endpoint, url.Values{"_element": {"Build"}, "_event": {"Нажатие"}})
				generated := decodeFormEventResponse(t, build.Body.Bytes())
				path := generated.TabularDocuments["result"]
				if !generated.OK || path == "" {
					t.Fatalf("build failed: %s", build.Body.String())
				}
				if strings.Contains(build.Body.String(), "server_private_context") {
					t.Fatal("payload leaked in JSON")
				}
				view := tabularHTTP(s, user, "own-session", "GET", path, nil)
				if view.Code != 200 || strings.Contains(view.Body.String(), "server_private_context") || !strings.Contains(view.Body.String(), "data-sheet-drilldown") {
					t.Fatal("native private drilldown viewer")
				}
				body := url.Values{"_element": {"Sheet"}, "_event": {"ОбработкаРасшифровки"}, "_tabular_token": {strings.TrimPrefix(path, "/ui/tabular/")}, "_tabular_row": {"0"}, "_tabular_col": {"0"}}
				session := "own-session"
				if strings.HasPrefix(mode, "question-") {
					first := tabularHTTP(s, user, session, "POST", endpoint, body)
					q := decodeFormEventResponse(t, first.Body.Bytes())
					if !q.OK || q.Question == nil || q.Navigation != nil {
						t.Fatalf("question bridge failed: %s", first.Body.String())
					}
					body.Set("_event", "Ответ")
					body.Set("_question_answer", "Да")
					if mode == "question-current-rls-denied" {
						fields["Owner"] = "other"
						if err := db.Upsert(ctx, target.Name, id, fields, target); err != nil {
							t.Fatal(err)
						}
					}
				}
				switch mode {
				case "tampered-client-data":
					body.Set("_tabular_payload", `{"Amount":1000000}`)
				case "current-rls-denied":
					fields["Owner"] = "other"
					if err := db.Upsert(ctx, target.Name, id, fields, target); err != nil {
						t.Fatal(err)
					}
				case "foreign-session":
					session = "foreign-session"
				case "wrong-cell":
					body.Set("_tabular_col", "100")
				case "expired":
					s.tabularResults.items[strings.TrimPrefix(path, "/ui/tabular/")].expires = time.Now().Add(-time.Second)
				case "export-does-not-call":
					for _, format := range []string{"pdf", "xlsx"} {
						w := tabularHTTP(s, user, "own-session", "GET", path+"/"+format, nil)
						if w.Code != 200 {
							t.Fatalf("export executed callback: %d", w.Code)
						}
					}
					return
				}
				click := tabularHTTP(s, user, session, "POST", endpoint, body)
				reply := decodeFormEventResponse(t, click.Body.Bytes())
				positive := mode == "nested-roundtrip" || mode == "tampered-client-data" || mode == "callback-copy" || mode == "question-answer"
				if positive {
					if !reply.OK || reply.Navigation == nil || !strings.Contains(reply.Navigation.URL, id.String()) {
						t.Fatalf("callback failed: %s", click.Body.String())
					}
					if reply.Dirty == nil || *reply.Dirty {
						t.Fatal("payload raised dirty")
					}
					if mode == "callback-copy" {
						again := tabularHTTP(s, user, session, "POST", endpoint, body)
						v := decodeFormEventResponse(t, again.Body.Bytes())
						if !v.OK || v.Navigation == nil {
							t.Fatal("callback changed cached payload")
						}
					}
				} else if reply.OK || reply.Navigation != nil {
					t.Fatal("invalid callback reached DSL")
				}
			})
		}
	})
}
