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

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

// Фильтр даты list-виджета (план 182D) разбирал «ГГГГ-ММ-ДД» как полночь UTC,
// а дата из формы, отчёта и REST — местная полночь. В восточной зоне отбор
// «Срок >= &С» терял записи первых часов выбранного дня, а «Срок = &Д» не
// находил дату, введённую в форме. Тесты фиксируют местную зону, в которой
// расхождение видно: UTC+3, записи в 00:00 и 01:30 по Москве.

func withMoscowLocal(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skipf("нет базы часовых поясов: %v", err)
	}
	saved := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = saved })
	return loc
}

func dateFilterWidget() (*metadata.Entity, *metadata.Widget) {
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Тема", Type: metadata.FieldTypeString},
			{Name: "Срок", Type: metadata.FieldTypeDate},
		},
	}
	w := &metadata.Widget{
		Name: "ЗадачиПоСроку", Type: metadata.WidgetTypeList, Title: "Задачи",
		Query: "ВЫБРАТЬ Ссылка, Тема ИЗ Документ.Задача\n" +
			"ГДЕ (&С ЕСТЬ ПУСТО ИЛИ Срок >= &С)\n" +
			"  И (&Ровно ЕСТЬ ПУСТО ИЛИ Срок = &Ровно)",
		Filters: []metadata.WidgetFilter{
			{Name: "С", Label: "С", Type: "date", Param: "С"},
			{Name: "Ровно", Label: "Ровно", Type: "date", Param: "Ровно"},
		},
	}
	return задача, w
}

func seedDateFilterRows(t *testing.T, ctx context.Context, db *storage.DB, задача *metadata.Entity, loc *time.Location) {
	t.Helper()
	for тема, срок := range map[string]time.Time{
		"полночь":   time.Date(2026, 3, 1, 0, 0, 0, 0, loc),
		"ночь":      time.Date(2026, 3, 1, 1, 30, 0, 0, loc),
		"накануне":  time.Date(2026, 2, 28, 22, 0, 0, 0, loc),
		"следующий": time.Date(2026, 3, 2, 9, 0, 0, 0, loc),
	} {
		if err := db.Upsert(ctx, задача.Name, uuid.New(), map[string]any{"Тема": тема, "Срок": срок}, задача); err != nil {
			t.Fatalf("Upsert %s: %v", тема, err)
		}
	}
}

// Публичный вход — partial-endpoint карточки с фильтром в адресе, как его
// шлёт контроллер карточки.
func TestWidgetDateFilterUsesLocalMidnight(t *testing.T) {
	loc := withMoscowLocal(t)
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "widget-date.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	задача, w := dateFilterWidget()
	if err := db.Migrate(ctx, []*metadata.Entity{задача}); err != nil {
		t.Fatal(err)
	}
	seedDateFilterRows(t, ctx, db, задача, loc)
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача}})
	reg.LoadWidgets([]*metadata.Widget{w})
	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)

	get := func(filter, value string) string {
		t.Helper()
		key := widgetFilterKey(w.Name, filter)
		req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape(w.Name)+"?"+key+"="+value, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s=%s: status=%d body=%s", filter, value, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	from := get("С", "2026-03-01")
	for _, тема := range []string{"полночь", "ночь", "следующий"} {
		if !strings.Contains(from, тема) {
			t.Errorf("отбор «Срок >= 01.03» потерял запись «%s»: %s", тема, from)
		}
	}
	if strings.Contains(from, "накануне") {
		t.Errorf("отбор «Срок >= 01.03» захватил предыдущий день: %s", from)
	}

	exact := get("Ровно", "2026-03-01")
	if !strings.Contains(exact, "полночь") {
		t.Errorf("отбор «Срок = 01.03» не нашёл дату, введённую в форме (местная полночь): %s", exact)
	}
}

// Значение фильтра уходит в запрос параметром; оба диалекта обязаны отобрать
// одно и то же — местная полночь сравнивается с моментом записи одинаково.
func TestWidgetDateFilterLocalMidnightBothDialects(t *testing.T) {
	loc := withMoscowLocal(t)
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		задача, w := dateFilterWidget()
		if err := db.Migrate(ctx, []*metadata.Entity{задача}); err != nil {
			t.Fatal(err)
		}
		seedDateFilterRows(t, ctx, db, задача, loc)
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача}})
		от, err := widget.ParseFilterValue(w.Filters[0], "2026-03-01")
		if err != nil {
			t.Fatal(err)
		}
		res := widget.New(reg, db).RunWithOptions(ctx, w, widget.RunOptions{Params: map[string]any{"С": от, "Ровно": nil}, Fresh: true})
		if res.Error != "" {
			t.Fatalf("виджет: %s", res.Error)
		}
		if len(res.Rows) != 3 {
			var got []string
			for _, row := range res.Rows {
				got = append(got, rowText(row))
			}
			t.Fatalf("«Срок >= 01.03»: строк %d, ожидалось 3 (полночь, ночь, следующий): %v", len(res.Rows), got)
		}
	})
}
