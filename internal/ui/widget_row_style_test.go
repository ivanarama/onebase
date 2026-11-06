package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// row_style list-виджета через публичную страницу дашборда: цвет фона и
// жирный шрифт строки — из колонок выборки, как ПриВыводеСтроки стола КЦ 1С.
// Колонки оформления в таблице не показываются; цвет не по формату #RGB /
// #RRGGBB игнорируется — CSS из данных в страницу не попадает.
func TestWidgetRowStyle_Markup(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "row-style.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entity := &metadata.Entity{Name: "Задача", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{Name: "Наименование", Type: metadata.FieldTypeString},
		{Name: "Цвет", Type: metadata.FieldTypeString},
		{Name: "Срочно", Type: metadata.FieldTypeBool},
	}}
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []map[string]any{
		{"Наименование": "Красная", "Цвет": "#FF0055", "Срочно": true},
		{"Наименование": "Светлая", "Цвет": "#ffd", "Срочно": false},
		{"Наименование": "Атака", "Цвет": "red;background:url(https://evil/x)", "Срочно": false},
		{"Наименование": "Пустая", "Цвет": "", "Срочно": false},
	} {
		if err := db.Upsert(ctx, entity.Name, uuid.New(), r, entity); err != nil {
			t.Fatal(err)
		}
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	reg.LoadWidgets([]*metadata.Widget{{
		Name: "Задачи", Type: metadata.WidgetTypeList, Title: "Задачи",
		Query:    "ВЫБРАТЬ Наименование, Цвет КАК ЦветФона, Срочно КАК Жирный ИЗ Справочник.Задача УПОРЯДОЧИТЬ ПО Наименование",
		RowStyle: &metadata.WidgetRowStyle{Background: "ЦветФона", Bold: "Жирный"},
	}})
	reg.LoadHomePage(&metadata.HomePage{Layout: "rows", Rows: []metadata.HomePageRow{{Widgets: []string{"Задачи"}}}})
	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	html := rec.Body.String()
	for _, want := range []string{
		`<tr class="ob-row-styled" style="background-color:#ff0055;color:#ffffff;font-weight:600">`,
		`<tr class="ob-row-styled" style="background-color:#ffffdd;color:#111827">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("нет строки с оформлением %s", want)
		}
	}
	if strings.Contains(html, "evil") {
		t.Error("значение цвета не по формату попало в страницу")
	}
	if strings.Count(html, `class="ob-row-styled"`) != 2 {
		t.Errorf("оформлены %d строк, want 2 (атака и пустой цвет — без оформления)", strings.Count(html, `class="ob-row-styled"`))
	}
	for _, header := range []string{">ЦветФона<", ">Жирный<"} {
		if strings.Contains(html, header) {
			t.Errorf("колонка оформления %s показана в таблице", header)
		}
	}
}

func TestNormalizeRowColor(t *testing.T) {
	for in, want := range map[any]string{
		"#FF0055": "#ff0055", "#abc": "#aabbcc", " #A1B2C3 ": "#a1b2c3",
		"red": "", "#12345": "", "#ggg": "", "#fff;color:red": "", "url(x)": "", "": "", 42: "",
	} {
		if got := widget.NormalizeRowColor(in); got != want {
			t.Errorf("NormalizeRowColor(%q) = %q, want %q", in, got, want)
		}
	}
	if widget.RowTextColor("#ffffff") != "#111827" || widget.RowTextColor("#000000") != "#ffffff" {
		t.Error("RowTextColor: светлый фон — тёмный текст, тёмный — белый")
	}
}
