package ui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// These regressions execute the same form-event and DSL calls as a user button.
// The OnFill hook must load the saved source, never the pending form/writer data.
func TestDSLFill_ObjectSourcesThroughFormEvent(t *testing.T) {
	for _, mode := range []string{"form", "writer", "reference"} {
		for _, state := range []string{"saved", "new", "deleted", "based-on", "row-access", "transaction"} {
			t.Run(mode+"/"+state, func(t *testing.T) {
				if mode == "reference" && state != "saved" {
					return
				} // existing reference success contract
				source := &metadata.Entity{Name: "FillSource", Kind: metadata.KindDocument,
					Fields:     []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}, {Name: "Текст", Type: metadata.FieldTypeString}, {Name: "Owner", Type: metadata.FieldTypeString}},
					TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}}},
				}
				receiver := &metadata.Entity{Name: "FillReceiver", Kind: metadata.KindDocument, BasedOn: []string{source.Name},
					Fields: []metadata.Field{{Name: "Текст", Type: metadata.FieldTypeString}}, TableParts: source.TableParts,
				}
				if state == "based-on" {
					receiver.BasedOn = []string{"OtherSource"}
				}
				srv, ctx := newSubmitTestServer(t, []*metadata.Entity{source, receiver})
				sourceID := uuid.New()
				if state != "new" {
					if err := srv.store.Upsert(ctx, source.Name, sourceID, map[string]any{"Номер": "SRC-1", "Текст": "saved header", "Owner": "u"}, source); err != nil {
						t.Fatal(err)
					}
					if err := srv.store.UpsertTablePartRows(ctx, source.Name, "Строки", sourceID, []map[string]any{{"Текст": "saved row"}}, source.TableParts[0]); err != nil {
						t.Fatal(err)
					}
				}
				onFill := mustParse(t, `Процедура ОбработкаЗаполнения(Основание)
     ЭтотОбъект.Текст = Основание.Текст;
     Для Каждого Стр Из Основание.Строки Цикл
      Нов = ЭтотОбъект.Строки.Добавить();
      Нов.Текст = Стр.Текст;
     КонецЦикла;
    КонецПроцедуры`)
				before := ""
				arg := "Объект"
				switch mode {
				case "writer":
					if state == "new" {
						before = `Осн = Документы.FillSource.Создать();`
					} else {
						// Write a new source in the handler to cover Создать→Записать→Заполнить.
						before = `Осн = Документы.FillSource.Создать();
       Осн.Номер = "DSL-1";
       Осн.Текст = "saved header";
       Осн.Owner = "u";
       Стр = Осн.Строки.Добавить(); Стр.Текст = "saved row";
       Осн.Записать();`
					}
					arg = "Осн"
				case "reference":
					arg = "Объект.Ссылка"
				}
				if state == "transaction" {
					before = "НачатьТранзакцию();\n" + before
				}
				switch state {
				case "deleted":
					if mode == "writer" {
						before += "\nОсн.Ссылка.Удалить();"
					} else {
						before += fmt.Sprintf("\nДокументы.FillSource.НайтиПоИдентификатору(\"%s\").Удалить();", sourceID)
					}
				case "row-access":
					// HTTP admitted an owned form. Change the persisted owner afterwards;
					// pending form fields still say u and must not authorize reading the row.
					if mode == "writer" {
						before += "\nОсн.Owner = \"other\"; Осн.Записать();"
					} else {
						before += fmt.Sprintf("\nДругой = Документы.FillSource.НайтиПоИдентификатору(\"%s\").ПолучитьОбъект(); Другой.Owner = \"other\"; Другой.Записать();", sourceID)
					}
				}
				module := fmt.Sprintf(`Процедура ЗаполнитьНажатие()
     %s
     Приемник = Документы.FillReceiver.Создать();
     %s.Текст = "unsaved header";
     %s.Строки.Очистить();
     Приемник.Заполнить(%s);
     Сообщить(Приемник.Текст);
     Для Каждого Копия Из Приемник.Строки Цикл
 Сообщить(Копия.Текст);
 КонецЦикла;
     %s
    КонецПроцедуры`, before, arg, arg, arg, map[bool]string{true: "ОтменитьТранзакцию();"}[state == "transaction"])
				if mode == "reference" { // references are immutable; only mutate the form
					module = strings.ReplaceAll(module, "Объект.Ссылка.Текст =", "Объект.Текст =")
					module = strings.ReplaceAll(module, "Объект.Ссылка.Строки.Очистить()", "Объект.Строки.Очистить()")
				}
				source.Forms = []*metadata.FormModule{{Name: "ФормаОбъекта", Kind: "object", EntityName: source.Name, LayoutKind: metadata.FormLayoutManaged,
					Elements: []*metadata.FormElement{{Kind: metadata.FormElementButton, Name: "Заполнить", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "ЗаполнитьНажатие"}}}, ProgramAST: mustParse(t, module)}}
				srv.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{source, receiver}, Programs: map[string]*ast.Program{receiver.Name: onFill}})
				srv.entitySvc = srv.newEntityService(nil)
				body := url.Values{"_element": {"Заполнить"}, "_event": {string(metadata.FormEventOnClick)}, "_kind": {"object"}, "Текст": {"pending form"}, "Owner": {"u"}}
				if state != "new" {
					body.Set("_id", sourceID.String())
				}
				req := httptest.NewRequest("POST", "/ui/document/FillSource/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("kind", "document")
				rctx.URLParams.Add("entity", source.Name)
				req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
				if state == "row-access" {
					user := &auth.User{Login: "u", Roles: []*auth.Role{{Permissions: auth.Permission{
						Documents: map[string][]string{source.Name: {"read", "write", "create", "delete"}, receiver.Name: {"read", "write", "create"}},
						RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{source.Name: {"read": {Field: "Owner", Op: "eq", Value: auth.RowValue{User: "login"}}}}}}}}}
					req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				}
				rec := httptest.NewRecorder()
				srv.handleManagedFormEvent(rec, req)
				if rec.Code != 200 {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				switch state {
				case "saved", "transaction":
					if !resp.OK {
						t.Fatalf("fill failed: %s", resp.Error)
					}
					if len(resp.Messages) != 2 || resp.Messages[0] != "saved header" || resp.Messages[1] != "saved row" {
						t.Fatalf("saved source not copied: %v", resp.Messages)
					}
				case "new", "deleted":
					if resp.OK || !strings.Contains(resp.Error, "основание не записано") || strings.Contains(resp.Error, "*ui.") {
						t.Fatalf("expected unsaved source diagnostic: %+v", resp)
					}
				case "based-on":
					if resp.OK || !strings.Contains(resp.Error, "не вводится на основании") {
						t.Fatalf("based_on bypassed: %+v", resp)
					}
				case "row-access":
					if resp.OK || !strings.Contains(resp.Error, "доступ") || len(resp.Messages) != 0 {
						t.Fatalf("source read access bypassed: %+v", resp)
					}
				}
			})
		}
	}
}
