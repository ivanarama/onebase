package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"golang.org/x/net/html"
)

// choice_style_value: выбранное значение в самом поле формы оформлено как его
// строка в списке выбора — направление по своему условию when, бренд по флагу
// направления звонка (choice_style_params), архивная реклама оранжево-красным.
// Поле без ключа вида не меняет, даже если строка оформлена.
func TestChoiceStyleValue_SavedValuesInForm(t *testing.T) {
	f := newStyleContextFixture(t)
	ctx := context.Background()
	dirs := f.server.reg.GetEntity("Направления")
	dirs.ChoiceRowStyle = &metadata.ChoiceRowStyle{Rules: []metadata.ChoiceStyleRule{
		{When: "ПодкрашиватьФонБренда", Style: metadata.FormCellStyle{Color: "rgb(250,0,0)"}},
	}}
	for _, el := range f.call.Forms[0].Elements {
		if el.ID == "direction" || el.ID == "brand-picker" || el.ID == "ad-picker" {
			el.ChoiceStyleValue = true
		}
	}
	saved := uuid.New()
	if err := f.server.store.Upsert(ctx, f.call.Name, saved, map[string]any{
		"Направление": f.paint.String(), "Бренд": f.red.String(), "Реклама": f.nested.String(),
	}, f.call); err != nil {
		t.Fatal(err)
	}
	sel := f.renderedSelects(t, saved)
	if s := sel["Направление"]; !strings.Contains(s, "flex:1;color:rgb(250,0,0)") {
		t.Errorf("направление с условием — поле красным: %q", s)
	}
	if s := strings.ToLower(sel["Бренд"]); !strings.Contains(s, "background:#ffc0c0") {
		t.Errorf("бренд при флаге направления — фон из карточки в самом поле: %q", s)
	}
	if s := strings.ToLower(sel["Реклама"]); !strings.Contains(s, "color:#ff4500") {
		t.Errorf("архивная реклама — поле оранжево-красным: %q", s)
	}

	// Обычное значение — без оформления; поле без ключа — без оформления.
	plain := uuid.New()
	if err := f.server.store.Upsert(ctx, f.call.Name, plain, map[string]any{
		"Направление": f.plainDir.String(), "Бренд": f.red.String(), "Реклама": f.current.String(),
	}, f.call); err != nil {
		t.Fatal(err)
	}
	sel = f.renderedSelects(t, plain)
	for _, name := range []string{"Направление", "Бренд", "Реклама"} {
		if s := sel[name]; s != "flex:1" {
			t.Errorf("%s: обычное значение оформлено: %q", name, s)
		}
	}
	for _, el := range f.call.Forms[0].Elements {
		el.ChoiceStyleValue = false
	}
	sel = f.renderedSelects(t, saved)
	if s := sel["Направление"]; s != "flex:1" {
		t.Errorf("без choice_style_value поле не меняет вид: %q", s)
	}
}

// renderedSelects — style полей-ссылок формы сохранённого звонка по имени.
func (f styleContextFixture) renderedSelects(t *testing.T, callID uuid.UUID) map[string]string {
	t.Helper()
	router := chi.NewRouter()
	f.server.Mount(router)
	req := httptest.NewRequest(http.MethodGet, "/ui/document/"+url.PathEscape(f.call.Name)+"/"+callID.String(), nil)
	req = req.WithContext(auth.ContextWithUser(req.Context(), styleContextUser()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form status=%d body=%s", rec.Code, rec.Body.String())
	}
	doc, err := html.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range []string{"Направление", "Бренд", "Реклама"} {
		sel := findSelectByName(doc, name)
		if sel == nil {
			t.Fatalf("select %q not found", name)
		}
		style, _ := htmlAttribute(sel, "style")
		out[name] = style
	}
	return out
}
