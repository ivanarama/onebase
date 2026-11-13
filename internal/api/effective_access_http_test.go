package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestEffectiveAccessObjectHookREST(t *testing.T) {
	for _, mode := range []string{"without-extra", "union", "admin", "missing"} {
		t.Run(mode, func(t *testing.T) {
			masters := &metadata.Entity{Name: "А_Мастера", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
			warehouse := &metadata.Entity{Name: "Склады", Kind: metadata.KindCatalog}
			prog := mustParseProgram(t, `Процедура OnWrite()
Если Не ЕстьПравоДоступа("catalog", "А_Мастера", "write") Или Не HasAccessRight("catalog", "Склады", "write") Тогда
ВызватьИсключение "effective-denied";
КонецЕсли;
КонецПроцедуры`)
			h, _ := newAPITestHandler(t, []*metadata.Entity{masters, warehouse}, map[string]*ast.Program{masters.Name: prog})
			r := reqWithEntity("POST", "/catalogs/А_Мастера", []byte(`{"Наименование":"own-synthetic"}`), map[string]string{"entity": masters.Name}, nil)
			u := apiUser("own", auth.Permission{Catalogs: map[string][]string{masters.Name: {"read", "write"}}})
			switch mode {
			case "union":
				u.Roles = append(u.Roles, &auth.Role{Name: "Extra", Permissions: auth.Permission{Catalogs: map[string][]string{warehouse.Name: {"write"}}}})
			case "admin":
				u.IsAdmin = true
			}
			if mode != "missing" {
				r = withUser(r, u)
			}
			w := httptest.NewRecorder()
			h.createObject(metadata.KindCatalog).ServeHTTP(w, r)
			if mode == "union" || mode == "admin" {
				if w.Code != http.StatusCreated {
					t.Fatalf("create: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "effective-denied") {
				t.Fatalf("expected hook refusal, got %d %s", w.Code, w.Body.String())
			}
		})
	}
}
