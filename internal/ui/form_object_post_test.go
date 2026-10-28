package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Объект.Провести() в обработчике кнопки формы проводит документ так же, как
// кнопка «Провести»: ОбработкаПроведения исполняется, документ отмечен
// проведённым. Раньше вызов уходил в runtime.Object и молча ничего не делал:
// документ даже не записывался.
func TestFormObject_ПровестиИзОбработчикаКнопки(t *testing.T) {
	doc := &metadata.Entity{
		Name: "Расчёт", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Отметка", Type: metadata.FieldTypeString},
		},
	}
	form := managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"), writeButton("КнПровести", "КнПровестиНажатие"))
	form.EntityName = doc.Name
	form.ProgramAST = mustParse(t, `
Процедура КнПровестиНажатие()
	Объект.Провести();
КонецПроцедуры
`)
	doc.Forms = []*metadata.FormModule{form}
	s, ctx := newSubmitTestServerWithPrograms(t, []*metadata.Entity{doc}, map[string]string{doc.Name: `
Процедура ОбработкаПроведения()
	this.Отметка = "проведён";
КонецПроцедуры
`})

	rec := postFormEvent(t, s, doc, "КнПровести")
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("обработчик не провёл документ: %d %s", rec.Code, rec.Body.String())
	}
	row := onlyDefaultsDoc(t, s, ctx, doc)
	if posted, _ := row["posted"].(bool); !posted {
		t.Fatalf("документ не проведён: %#v", row)
	}
	if got := row["Отметка"]; got != "проведён" {
		t.Fatalf("ОбработкаПроведения не исполнена: Отметка = %v", got)
	}
}

// Документ без проведения: Провести() сообщает об этом, а не молчит.
func TestFormObject_ПровестиНепроводимого(t *testing.T) {
	cat := &metadata.Entity{
		Name: "Справка", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
	}
	form := managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"), writeButton("КнПровести", "КнПровестиНажатие"))
	form.EntityName = cat.Name
	form.ProgramAST = mustParse(t, `
Процедура КнПровестиНажатие()
	Объект.Провести();
КонецПроцедуры
`)
	cat.Forms = []*metadata.FormModule{form}
	s, _ := newSubmitTestServerWithPrograms(t, []*metadata.Entity{cat}, nil)
	rec := postFormEvent(t, s, cat, "КнПровести")
	if !strings.Contains(rec.Body.String(), "объект не проводится") {
		t.Fatalf("ждали «объект не проводится»: %d %s", rec.Code, rec.Body.String())
	}
}

func postFormEvent(t *testing.T, s *Server, doc *metadata.Entity, element string) *httptest.ResponseRecorder {
	t.Helper()
	body := url.Values{"_element": {element}, "_event": {"Нажатие"}, "Номер": {"0001"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/document/"+doc.Name+"/form-event", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("kind", "document")
	rctx.URLParams.Add("entity", doc.Name)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.handleManagedFormEvent(rec, req)
	return rec
}

// Both policies must see the final fields after write/post autofill. In
// particular, post autofill must not turn an allowed write into a forbidden row.
func TestFormObject_PostChecksFinalRowAccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		write, post auth.RowPolicy
		allowed     bool
	}{
		{
			name:  "post_autofill_invalidates_write",
			write: auth.RowPolicy{Field: "Scope", Op: "empty"},
			post:  auth.RowPolicy{Field: "Scope", Op: "eq", Value: auth.RowValue{Literal: "blocked"}},
		},
		{
			name:    "post_autofill_satisfies_both_policies",
			write:   auth.RowPolicy{Not: &auth.RowPolicy{Field: "Scope", Op: "empty"}},
			post:    auth.RowPolicy{Field: "Scope", Op: "eq", Value: auth.RowValue{Literal: "allowed"}},
			allowed: true,
		},
		{
			name:  "write_autofill_invalidates_post",
			write: auth.RowPolicy{Field: "Scope", Op: "eq", Value: auth.RowValue{Literal: "allowed"}},
			post:  auth.RowPolicy{Field: "Scope", Op: "empty"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := &metadata.Entity{
				Name: "AccessDoc", Kind: metadata.KindDocument, Posting: true,
				Fields: []metadata.Field{
					{Name: "Номер", Type: metadata.FieldTypeString},
					{Name: "Scope", Type: metadata.FieldTypeString},
				},
			}
			form := managedObjectForm(fieldEl("Scope", "Объект.Scope"), writeButton("Post", "PostClick"))
			form.EntityName = doc.Name
			form.ProgramAST = mustParse(t, `Процедура PostClick()
	Объект.Провести();
КонецПроцедуры`)
			doc.Forms = []*metadata.FormModule{form}
			s, ctx := newSubmitTestServer(t, []*metadata.Entity{doc})
			user := &auth.User{Login: "writer", Roles: []*auth.Role{{
				Permissions: auth.Permission{
					Documents: map[string][]string{doc.Name: {"read", "write", "post"}},
					RowAccess: auth.RowAccess{Documents: map[string]auth.RowPolicies{
						doc.Name: {"write": tc.write, "post": tc.post},
					}},
				},
			}}}
			req := reqWithChi(http.MethodPost, "/ui/document/AccessDoc/form-event", url.Values{
				"_element": {"Post"}, "_event": {"Нажатие"}, "Номер": {"0001"}, "Scope": {""},
			}, map[string]string{"kind": "document", "entity": doc.Name})
			req = req.WithContext(auth.ContextWithUser(req.Context(), user))
			rec := httptest.NewRecorder()
			s.handleManagedFormEvent(rec, req)
			var response formEventResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode form response: %v: %s", err, rec.Body.String())
			}
			if response.OK != tc.allowed {
				t.Fatalf("ok=%v, want %v: %d %s", response.OK, tc.allowed, rec.Code, rec.Body.String())
			}
			rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.allowed {
				if !strings.Contains(response.Error, interpreter.ErrRowAccessDenied.Error()) {
					t.Fatalf("want row access denial, got %q", response.Error)
				}
				if len(rows) != 0 {
					t.Fatalf("denied operation persisted rows: %#v", rows)
				}
				return
			}
			if len(rows) != 1 || rows[0]["Scope"] != "allowed" || rows[0]["posted"] != true {
				t.Fatalf("want one posted document with autofilled Scope, got %#v", rows)
			}
		})
	}
}
