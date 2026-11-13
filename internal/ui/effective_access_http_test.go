package ui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/processor"
	"github.com/ivantit66/onebase/internal/runtime"
)

func TestEffectiveAccessFormEventHTTP(t *testing.T) {
	form := processorExecutionForm(&metadata.FormElement{Kind: metadata.FormElementButton, Name: "Check", Handlers: map[metadata.FormEventType]string{metadata.FormEventOnClick: "Check"}})
	form.ProgramAST = mustParse(t, `Процедура Check()
Если ЕстьПравоДоступа("catalog", "А_Мастера", "write") И HasAccessRight("catalog", "Склады", "write") Тогда
Сообщить("effective-allowed");
Иначе
Сообщить("effective-denied");
КонецЕсли;
КонецПроцедуры`)
	proc := &processor.Processor{Name: "EffectiveAccess", Forms: []*metadata.FormModule{form}}
	s, _ := newProcessorFormEventExecutionServer(t, proc, nil)
	s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{{Name: "А_Мастера", Kind: metadata.KindCatalog}, {Name: "Склады", Kind: metadata.KindCatalog}}})
	base := &auth.Role{Name: "OTK", Permissions: auth.Permission{Catalogs: map[string][]string{"А_Мастера": {"write"}}, Processors: map[string][]string{proc.Name: {"run"}}}}
	extra := &auth.Role{Name: "WarehouseExtra", Permissions: auth.Permission{Catalogs: map[string][]string{"Склады": {"write"}}, Processors: map[string][]string{}}}
	for _, tc := range []struct {
		name string
		user *auth.User
		want string
	}{
		{"without-extra", &auth.User{ID: "own", Roles: []*auth.Role{base}}, "effective-denied"},
		{"union", &auth.User{ID: "own", Roles: []*auth.Role{base, extra}}, "effective-allowed"},
		{"revoked-next-event", &auth.User{ID: "own", Roles: []*auth.Role{base}}, "effective-denied"},
		{"admin", &auth.User{ID: "own-admin", IsAdmin: true}, "effective-allowed"},
		{"missing", nil, "effective-denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/ui/processor/EffectiveAccess/form-event", strings.NewReader(processorClickBody("Check").Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			route := chi.NewRouteContext()
			route.URLParams.Add("name", proc.Name)
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
			if tc.user != nil {
				r = r.WithContext(auth.ContextWithUser(r.Context(), tc.user))
			}
			w := httptest.NewRecorder()
			s.handleProcessorFormEvent(w, r)
			resp := decodeFormEventResponse(t, w.Body.Bytes())
			if !resp.OK || len(resp.Messages) != 1 || resp.Messages[0] != tc.want {
				t.Fatalf("response: %+v", resp)
			}
		})
	}
}
