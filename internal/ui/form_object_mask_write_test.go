package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Exercise the browser's public command endpoint: checking only the wrapper's
// write method would miss form hydration, the live DSL transaction and response.
func TestFormObjectWriteProtectsMaskedFields(t *testing.T) {
	const real = "+79161234567"
	const typed = "+79001112233"
	for _, kind := range []metadata.Kind{metadata.KindCatalog, metadata.KindDocument} {
		for _, tc := range []struct {
			name, policy, stored, submitted, script, wantPhone, wantName string
			messages                                                     []string
			newObject                                                    bool
			omitPhone                                                    bool
			phoneDefault                                                 string
			writes                                                       int64
		}{
			{
				name: "pii_default_repeated_write", stored: real, submitted: "••••••",
				script: `Объект.Телефон = "••••••";
Объект.Наименование = "first";
Объект.Записать();
Сообщить(Объект.Телефон);
Объект.Наименование = "second";
Объект.Записать();
Сообщить(Объект.Телефон);`,
				wantPhone: real, wantName: "second", messages: []string{"••••••", "••••••"}, writes: 2,
			},
			{
				name: "partial_mask", policy: "mask_tail", stored: real, submitted: "•••••4567",
				script:    `Объект.Наименование = "saved"; Объект.Записать(); Сообщить(Объект.Телефон);`,
				wantPhone: real, wantName: "saved", messages: []string{"•••••4567"}, writes: 1,
			},
			{
				name: "hidden_crafted_value", policy: "hide", stored: real, submitted: typed,
				script:    `Объект.Наименование = "saved"; Объект.Записать(); Сообщить(Объект.Телефон);`,
				wantPhone: real, wantName: "saved", messages: []string{typed}, writes: 1,
			},
			{
				name: "empty_masked_can_be_filled", submitted: typed,
				script:    `Объект.Наименование = "saved"; Объект.Записать(); Сообщить(Объект.Телефон);`,
				wantPhone: typed, wantName: "saved", messages: []string{typed}, writes: 1,
			},
			{
				name: "empty_hidden_cannot_be_filled", policy: "hide", submitted: typed,
				script:    `Объект.Наименование = "saved"; Объект.Записать(); Сообщить(Объект.Телефон);`,
				wantPhone: "", wantName: "saved", messages: []string{typed}, writes: 1,
			},
			{
				name: "new_object", submitted: typed, newObject: true,
				script:    `Объект.Наименование = "saved"; Объект.Записать(); Сообщить(Объект.Телефон);`,
				wantPhone: typed, wantName: "saved", messages: []string{typed}, writes: 1,
			},
			{
				name: "new_explicit_empty_survives_rollback", policy: "hide", newObject: true, omitPhone: true, phoneDefault: "default phone",
				script: `Объект.Телефон = Неопределено;
НачатьТранзакцию();
Объект.Записать();
Объект.Записать();
ОтменитьТранзакцию();
Объект.Наименование = "retry";
Объект.Записать();
Сообщить(Объект.Телефон);`,
				wantName: "retry", messages: []string{""}, writes: 1,
			},
			{
				name: "rollback_and_retry", stored: real, submitted: "••••••",
				script: `НачатьТранзакцию();
Объект.Наименование = "rolled back";
Объект.Записать();
Сообщить(Объект.Телефон);
ОтменитьТранзакцию();
Сообщить(Объект.Телефон);
Объект.Наименование = "retry";
Объект.Записать();
Сообщить(Объект.Телефон);`,
				wantPhone: real, wantName: "retry", messages: []string{"••••••", "••••••", "••••••"}, writes: 1,
			},
			{
				name: "rejected_write_and_retry", stored: real, submitted: "••••••",
				script: `Объект.Наименование = "reject";
Попытка
 Объект.Записать();
Исключение
 Сообщить(Объект.Телефон);
КонецПопытки;
Объект.Наименование = "retry";
Объект.Записать();
Сообщить(Объект.Телефон);`,
				wantPhone: real, wantName: "retry", messages: []string{"••••••", "••••••"}, writes: 1,
			},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				entity := &metadata.Entity{Name: "Контакты", Kind: kind, Fields: []metadata.Field{
					{Name: "Наименование", Type: metadata.FieldTypeString},
					{Name: "Телефон", Type: metadata.FieldTypeString, PII: true, Default: tc.phoneDefault},
				}}
				form := managedObjectForm(fieldEl("Имя", "Объект.Наименование"),
					fieldEl("Телефон", "Объект.Телефон"), maskEventButton("Выполнить", "Выполнить"))
				form.EntityName = entity.Name
				form.ProgramAST = mustParse(t, "Процедура Выполнить()\n"+tc.script+"\nКонецПроцедуры")
				entity.Forms = []*metadata.FormModule{form}
				s, ctx := newSubmitTestServerWithPrograms(t, []*metadata.Entity{entity}, map[string]string{entity.Name: `
Процедура ПриЗаписи()
 Если ЭтотОбъект.Наименование = "reject" Тогда ВызватьИсключение "expected rejection"; КонецЕсли;
КонецПроцедуры`})
				id := uuid.New()
				var initialVersion int64
				if !tc.newObject {
					if err := s.store.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "initial", "Телефон": tc.stored}, entity); err != nil {
						t.Fatal(err)
					}
					var err error
					initialVersion, err = s.store.EntityVersion(ctx, entity.Name, id)
					if err != nil {
						t.Fatal(err)
					}
				}
				policies := auth.FieldPolicies{}
				if tc.policy != "" {
					policies["Телефон"] = auth.FieldPolicy{Read: tc.policy, Keep: 4}
				}
				permission := auth.Permission{}
				if kind == metadata.KindDocument {
					permission.Documents = map[string][]string{entity.Name: {"read", "write"}}
					permission.FieldAccess.Documents = map[string]auth.FieldPolicies{entity.Name: policies}
				} else {
					permission.Catalogs = map[string][]string{entity.Name: {"read", "write"}}
					permission.FieldAccess.Catalogs = map[string]auth.FieldPolicies{entity.Name: policies}
				}
				user := &auth.User{ID: "operator", Login: "operator", Roles: []*auth.Role{{Name: "Оператор", Permissions: permission}}}
				router := chi.NewRouter()
				s.Mount(router)
				body := url.Values{"_element": {"Выполнить"}, "_event": {"Нажатие"},
					"Наименование": {"initial"}, "Телефон": {tc.submitted}}
				if tc.omitPhone {
					body.Del("Телефон")
				}
				if !tc.newObject {
					body.Set("_id", id.String())
					body.Set("_version", strconv.FormatInt(initialVersion, 10))
				}
				req := httptest.NewRequest(http.MethodPost, "/ui/"+string(kind)+"/"+url.PathEscape(entity.Name)+"/form-event", strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req = req.WithContext(auth.ContextWithUser(req.Context(), user))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				resp := decodeFormEventResponse(t, rec.Body.Bytes())
				if rec.Code != http.StatusOK || !resp.OK || resp.Error != "" {
					t.Fatalf("form command: %d %+v", rec.Code, resp)
				}
				if !reflect.DeepEqual(resp.Messages, tc.messages) {
					t.Errorf("handler read after write = %q, want %q", resp.Messages, tc.messages)
				}
				if tc.newObject {
					id = uuid.MustParse(resp.SavedID)
				}
				row, err := s.store.GetByID(ctx, entity.Name, id, entity)
				if err != nil {
					t.Fatal(err)
				}
				if phone := toStringOrEmpty(row["Телефон"]); phone != tc.wantPhone || row["Наименование"] != tc.wantName {
					t.Fatalf("stored phone/name = %q/%v, want %q/%q", phone, row["Наименование"], tc.wantPhone, tc.wantName)
				}
				version, err := s.store.EntityVersion(ctx, entity.Name, id)
				if err != nil || version != initialVersion+tc.writes || resp.Version != version {
					t.Errorf("version: stored=%d response=%d err=%v, want %d", version, resp.Version, err, initialVersion+tc.writes)
				}
				if tc.stored != "" && strings.Contains(rec.Body.String(), real) {
					t.Errorf("form response disclosed the stored phone: %s", rec.Body.String())
				}
				if tc.policy == "hide" {
					if _, exists := resp.Values["Телефон"]; exists {
						t.Error("hidden phone present in response")
					}
				}
				if got := fmt.Sprint(resp.Values["Наименование"]); got != tc.wantName {
					t.Errorf("visible response field=%q, want %q", got, tc.wantName)
				}
			})
		}
	}
}
