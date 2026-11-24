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
	"github.com/ivantit66/onebase/internal/metadata"
)

// ui.list_density: compact — класс ob-compact-lists на <body> страницы списка и
// плотные правила в CSS (12px, видимые границы столбцов #cbd5e1, таблицы формы
// объекта не трогаются). Без настройки — прежний вид. Тест — production-маршрут
// GET списка справочника.
func TestCompactListsThroughHTTP(t *testing.T) {
	for _, compact := range []bool{false, true} {
		ent := &metadata.Entity{Name: "Клиент", Kind: metadata.KindCatalog, Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		s, ctx := newSubmitTestServer(t, []*metadata.Entity{ent})
		s.cfg.CompactLists = compact
		if err := s.store.Upsert(ctx, ent.Name, uuid.New(), map[string]any{"Наименование": "Иванов"}, ent); err != nil {
			t.Fatal(err)
		}
		router := chi.NewRouter()
		s.Mount(router)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/catalog/"+url.PathEscape("Клиент"), nil).WithContext(context.Background()))
		if rec.Code != http.StatusOK {
			t.Fatalf("compact=%t: GET list %d", compact, rec.Code)
		}
		page := rec.Body.String()
		if !strings.Contains(page, "Иванов") {
			t.Fatalf("compact=%t: list row missing", compact)
		}
		hasClass := strings.Contains(page, `class="ob-compact-lists"`)
		if hasClass != compact {
			t.Fatalf("compact=%t: body class present=%t", compact, hasClass)
		}
		// Правила всегда в CSS, действуют только под классом; проверяем их суть.
		for _, rule := range []string{
			`.ob-compact-lists main table:not(.tp-table):not(.tbl-plain):not(#main-form *) td{padding:2px 6px;font-size:12px;line-height:17px;border:1px solid #cbd5e1`,
		} {
			if !strings.Contains(page, rule) {
				t.Fatalf("compact CSS rule missing: %s", rule)
			}
		}
	}
}
