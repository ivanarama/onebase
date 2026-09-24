package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

func newRowNavFixture(t *testing.T) (http.Handler, *metadata.Entity, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "row-nav.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	entity := &metadata.Entity{
		Name: "Товар", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, entity.Name, id, map[string]any{"Наименование": "Гвозди"}, entity); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	reg.LoadWidgets([]*metadata.Widget{
		{
			Name: "СНавигацией", Type: metadata.WidgetTypeList, Title: "С навигацией",
			Query:  "ВЫБРАТЬ Ссылка, Наименование ИЗ Справочник.Товар",
			Source: &metadata.WidgetSource{Entity: "Товар", IDField: "Ссылка"},
		},
		{
			Name: "ЧужаяСущность", Type: metadata.WidgetTypeList, Title: "Чужая сущность",
			Query:  "ВЫБРАТЬ Ссылка, Наименование ИЗ Справочник.Товар",
			Source: &metadata.WidgetSource{Entity: "НеСуществоет", IDField: "Ссылка"},
		},
		{
			Name: "НеСсылочнаяКолонка", Type: metadata.WidgetTypeList, Title: "Не ссылочная колонка",
			Query:  "ВЫБРАТЬ Наименование ИЗ Справочник.Товар",
			Source: &metadata.WidgetSource{Entity: "Товар", IDField: "Наименование"},
		},
		{Name: "БезSource", Type: metadata.WidgetTypeList, Title: "Без source", Query: "ВЫБРАТЬ Наименование ИЗ Справочник.Товар"},
	})
	reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{"СНавигацией", "ЧужаяСущность", "НеСсылочнаяКолонка", "БезSource"}}}})
	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)
	return router, entity, id
}

func TestWidgetRowNavigation_Markup(t *testing.T) {
	router, entity, id := newRowNavFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	html := rec.Body.String()

	// html/template процент-энкодит кириллицу в URL-атрибутах нижним регистром hex.
	wantURL := strings.ToLower("/ui/" + string(entity.Kind) + "/" + url.PathEscape(entity.Name) + "/" + id.String())
	if !strings.Contains(html, `data-ob-row-url="`+wantURL+`"`) {
		t.Fatalf("no row link %q on dashboard:\n%s", wantURL, html)
	}
	if !strings.Contains(html, `class="ob-row-link"`) {
		t.Fatal("navigable row misses ob-row-link class")
	}
	// Сырой идентификатор не показывается в авто-колонках виджета с source:
	// заголовок «ссылка» в таблице навигационного виджета отсутствует.
	if strings.Contains(html, "<th>ссылка</th>") {
		t.Fatal("raw id column leaked into auto columns")
	}
	// Чужая сущность, не ссылочная колонка и отсутствие source — строки обычные.
	if got := strings.Count(html, "data-ob-row-url"); got != 1 {
		t.Fatalf("data-ob-row-url count=%d, want exactly the single valid row", got)
	}
}
