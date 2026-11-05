package ui

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Шапка таблиц выделена фоном, тёмным полужирным текстом и заметной линией —
// одинаково в списках, табличных частях, подборе, виджетах и отчётах. Раньше
// светло-серый текст на белом фоне сливался с данными.
func TestListPage_TableHeaderStandsOut(t *testing.T) {
	ent := &metadata.Entity{Name: "ШапкаКлиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
	}}
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
	req := httptest.NewRequest("GET", "/ui/catalog/шапкаклиент", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "шапкаклиент")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.list(rec, req)
	if rec.Code != 200 {
		t.Fatalf("список: code = %d, body: %s", rec.Code, rec.Body.String())
	}
	checkHeaderRules(t, "список", rec.Body.String(), `\.tbl th`, `\.tp-table th`, `\.report-composed thead th`, "\nth")

	// Виджеты-таблицы рабочего стола («Список задач») — стили главной.
	home := httptest.NewRecorder()
	s.index(home, httptest.NewRequest("GET", "/ui/", nil).WithContext(ctx))
	if home.Code != 200 {
		t.Fatalf("главная: code = %d", home.Code)
	}
	checkHeaderRules(t, "главная", home.Body.String(), `\.w-list th`)
}

func checkHeaderRules(t *testing.T, page, body string, selectors ...string) {
	t.Helper()
	for _, sel := range selectors {
		rule := regexp.MustCompile(sel + `\{[^}]*\}`).FindString(body)
		if rule == "" {
			t.Errorf("%s: в стилях нет правила %s", page, sel)
			continue
		}
		for _, want := range []string{"background:#eef2f7", "color:#334155", "font-weight:600", "border-bottom:2px solid #cbd5e1"} {
			if !strings.Contains(rule, want) {
				t.Errorf("%s, шапка %s: нет %s в %q", page, sel, want, strings.TrimSpace(rule))
			}
		}
	}
}
