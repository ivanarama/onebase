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
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
	"github.com/ivantit66/onebase/internal/widget"
)

func filtersFixtureReg(store *storage.DB) (*runtime.Registry, []*metadata.Entity) {
	задача := &metadata.Entity{
		Name: "Задача", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Тема", Type: metadata.FieldTypeString},
			{Name: "Срочно", Type: metadata.FieldTypeBool},
			{Name: "Дедлайн", Type: metadata.FieldTypeDate},
		},
	}
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача}})
	reg.LoadWidgets([]*metadata.Widget{
		{
			Name: "ЗадачиФильтр", Type: metadata.WidgetTypeList, Title: "Задачи",
			Query: "ВЫБРАТЬ Ссылка, Тема ИЗ Документ.Задача\nГДЕ (&Тема ЕСТЬ ПУСТО ИЛИ Тема = &Тема)\n  И (&Срочно ЕСТЬ ПУСТО ИЛИ Срочно = &Срочно)\n  И (&Дедлайн ЕСТЬ ПУСТО ИЛИ Дедлайн = &Дедлайн)",
			Filters: []metadata.WidgetFilter{
				{Name: "Тема", Label: "Тема", Type: "string", Param: "Тема"},
				{Name: "Срочно", Label: "Срочно", Type: "bool", Param: "Срочно"},
				{Name: "Дедлайн", Label: "Дедлайн", Type: "date", Param: "Дедлайн"},
			},
		},
	})
	_ = store
	return reg, []*metadata.Entity{задача}
}

func newFiltersFixture(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "filters.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	reg, entities := filtersFixtureReg(db)
	if err := db.Migrate(ctx, entities); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	deadline := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := db.Upsert(ctx, "Задача", mustUUID(t, "79f4d98a-3ce0-4da4-82ea-e9c8686e804f"), map[string]any{"Тема": "Гвозди", "Срочно": true, "Дедлайн": deadline}, entities[0]); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := db.Upsert(ctx, "Задача", mustUUID(t, "a8b64d2d-d422-490a-9e4e-092eefc47a40"), map[string]any{"Тема": "О'Брайен молоток", "Срочно": nil, "Дедлайн": nil}, entities[0]); err != nil {
		t.Fatalf("Upsert(2): %v", err)
	}
	s := &Server{reg: reg, store: db, widgetCache: widget.NewCache(time.Minute), messages: NewMessageStore()}
	router := chi.NewRouter()
	s.Mount(router)
	return router
}

func TestWidgetFiltersPartial_FiltersRows(t *testing.T) {
	router := newFiltersFixture(t)
	key := widgetFilterKey("ЗадачиФильтр", "Тема")
	req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape("ЗадачиФильтр")+"?"+key+"="+urlQueryEscape("Гвозди"), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Гвозди") || strings.Contains(rec.Body.String(), "молоток") {
		t.Fatalf("string filter not applied: %s", rec.Body.String())
	}
}

func TestWidgetFiltersPartial_BoolTriStateKeepsNullRows(t *testing.T) {
	router := newFiltersFixture(t)
	key := widgetFilterKey("ЗадачиФильтр", "Срочно")
	// Пустой отбор: NULL-строка на месте.
	req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape("ЗадачиФильтр"), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "молоток") {
		t.Fatalf("empty filter must keep NULL rows: %s", rec.Body.String())
	}
	// «Нет» — только непустые false; NULL-строка исчезает из отбора по значению.
	req2 := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape("ЗадачиФильтр")+"?"+key+"=false", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if strings.Contains(rec2.Body.String(), "Гвозди") || strings.Contains(rec2.Body.String(), "молоток") {
		t.Fatalf("bool=false must exclude NULL rows: %s", rec2.Body.String())
	}
}

func TestWidgetFiltersPartial_RejectsBadValues(t *testing.T) {
	router := newFiltersFixture(t)
	тема := widgetFilterKey("ЗадачиФильтр", "Тема")
	срочно := widgetFilterKey("ЗадачиФильтр", "Срочно")
	дедлайн := widgetFilterKey("ЗадачиФильтр", "Дедлайн")
	cases := []struct {
		name  string
		query string
	}{
		{"лишний именаосванный ключ", "?" + widgetFilterKey("ЗадачиФильтр", "Чужое") + "=x"},
		{"неверная дата", "?" + дедлайн + "=01.02.2026"},
		{"select-значение не из списка", "?" + срочно + "=maybe"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape("ЗадачиФильтр")+tc.query, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d want 400", tc.name, rec.Code)
		}
	}
	_ = тема
}

func TestWidgetFiltersPartial_RejectsUnknownPlainParam(t *testing.T) {
	router := newFiltersFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/_widget/"+urlEscape("ЗадачиФильтр")+"?hack=1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestWidgetFiltersDashboard_ResilientToGarbage(t *testing.T) {
	router := newFiltersFixture(t)
	key := widgetFilterKey("ЗадачиФильтр", "Дедлайн")
	req := httptest.NewRequest(http.MethodGet, "/ui/?"+key+"=не-дата", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("garbage value must not break the page: %d", rec.Code)
	}
	// Мусорное значение — пустой отбор: обе строки на месте.
	if !strings.Contains(rec.Body.String(), "Гвозди") || !strings.Contains(rec.Body.String(), "молоток") {
		t.Fatalf("garbage value must degrade to empty filter: %s", rec.Body.String())
	}
}

// F5/закладка: значения фильтров из URL применяются к данным полной страницы,
// а не только к контролам — иначе после перезагрузки контролы заполнены,
// а строки неотфильтрованы (блокирующее замечание ревью #1671).
func TestWidgetFiltersDashboard_F5AppliesFilter(t *testing.T) {
	router := newFiltersFixture(t)
	key := widgetFilterKey("ЗадачиФильтр", "Тема")
	req := httptest.NewRequest(http.MethodGet, "/ui/?"+key+"="+urlQueryEscape("Гвозди"), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Гвозди") {
		t.Fatalf("F5 filter not applied to data: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "молоток") {
		t.Fatalf("F5 page renders unfiltered rows: %s", rec.Body.String())
	}
	// Контрол предзаполнен тем же значением.
	if !strings.Contains(rec.Body.String(), `value="Гвозди"`) {
		t.Fatalf("control not prefilled: %s", rec.Body.String())
	}
	// Без ключа — обе строки (контрольный заход).
	req2 := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), "Гвозди") || !strings.Contains(rec2.Body.String(), "молоток") {
		t.Fatalf("plain dashboard must be unfiltered")
	}
}

func TestWidgetFiltersMatrix_TypedParamsBothDialects(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		задача := &metadata.Entity{
			Name: "Задача", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Тема", Type: metadata.FieldTypeString},
				{Name: "Срочно", Type: metadata.FieldTypeBool},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{задача}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		rows := []struct {
			id     string
			тема   string
			срочно any
		}{
			{"79f4d98a-3ce0-4da4-82ea-e9c8686e804f", "О'Брайен кувалда", true},
			{"a8b64d2d-d422-490a-9e4e-092eefc47a40", "Гвозди", false},
			{"0f3f3371-c7cb-4fc0-b96a-a1a5a2b8ce99", "Саморезы", nil},
		}
		for _, r := range rows {
			if err := db.Upsert(ctx, "Задача", mustUUID(t, r.id), map[string]any{"Тема": r.тема, "Срочно": r.срочно}, задача); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
		}
		reg := runtime.NewRegistry()
		reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{задача}})
		w := &metadata.Widget{
			Name: "Матрица", Type: metadata.WidgetTypeList,
			Query: "ВЫБРАТЬ Ссылка, Тема ИЗ Документ.Задача\nГДЕ (&Тема ЕСТЬ ПУСТО ИЛИ Тема = &Тема)\n  И (&Срочно ЕСТЬ ПУСТО ИЛИ Срочно = &Срочно)",
			Filters: []metadata.WidgetFilter{
				{Name: "Тема", Type: "string", Param: "Тема"},
				{Name: "Срочно", Type: "bool", Param: "Срочно"},
			},
		}
		runner := widget.New(reg, db)
		// Пустые фильтры — типизированный nil, все строки (включая NULL-булево).
		res := runner.RunWithOptions(ctx, w, widget.RunOptions{Params: map[string]any{"Тема": nil, "Срочно": nil}, Fresh: true})
		if len(res.Rows) != 3 {
			t.Fatalf("empty filters: rows=%d want 3", len(res.Rows))
		}
		// Строка с кавычкой/апострофом остаётся одним аргументом.
		res = runner.RunWithOptions(ctx, w, widget.RunOptions{Params: map[string]any{"Тема": "О'Брайен кувалда", "Срочно": nil}, Fresh: true})
		if len(res.Rows) != 1 || !strings.Contains(rowText(res.Rows[0]), "кувалда") {
			t.Fatalf("quoted string filter: rows=%v", res.Rows)
		}
		// false не смешивается с NULL: строка с NULL-булевым не попадает в отбор.
		res = runner.RunWithOptions(ctx, w, widget.RunOptions{Params: map[string]any{"Тема": nil, "Срочно": false}, Fresh: true})
		if len(res.Rows) != 1 {
			t.Fatalf("bool=false: rows=%d want 1 (NULL excluded)", len(res.Rows))
		}
	})
}

func rowText(row map[string]any) string {
	var b strings.Builder
	for _, v := range row {
		if s, ok := v.(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("uuid %q: %v", s, err)
	}
	return id
}

func urlEscape(s string) string { return url.PathEscape(s) }

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// Классы отборов обязаны иметь правила в таблице стилей страницы.
//
// Разметка рисовала .w-filters/.w-filter с самого появления отборов, а правил
// для них не было вовсе: <label> инлайновый, поэтому подпись и контрол текли
// в строку и переносились как придётся, а ширина <select> равнялась самому
// длинному значению списка — один длинный логин растягивал карточку и ломал
// ряд. Тест держит связь «класс нарисован → класс оформлен»: краснеет и если
// правила убрать, и если в разметке появится новый класс отбора без оформления.
func TestWidgetFiltersDashboard_ClassesAreStyled(t *testing.T) {
	router := newFiltersFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("дашборд не отдан: %d", rec.Code)
	}
	body := rec.Body.String()
	// Блоков <style> на странице несколько, дашбордный — не первый: берём все.
	стили := styleBlocks(body)

	for _, класс := range []string{"w-filters", "w-filter", "w-filter-reset"} {
		if !strings.Contains(body, `class="`+класс+`"`) {
			t.Fatalf("класс %q не нарисован — тест потерял предмет проверки", класс)
		}
		if !strings.Contains(стили, "."+класс+"{") {
			t.Errorf("класс %q нарисован, но правил для него нет: ряд отборов не оформлен", класс)
		}
	}

	// Ряд: контейнер — флексбокс, каждый отбор занимает равную долю.
	if r := cssRule(t, стили, ".w-filters{"); !strings.Contains(r, "display:flex") {
		t.Errorf(".w-filters не флексбокс (%s) — отборы не выстроятся в ряд", r)
	}
	if r := cssRule(t, стили, ".w-filter{"); !strings.Contains(r, "flex:1 1 0") {
		t.Errorf(".w-filter без flex:1 1 0 (%s) — доли строки будут разными", r)
	}
	// Без min-width:0 у самого контрола flex-элемент не становится уже своего
	// содержимого, и длинное значение списка снова растянет карточку.
	if r := cssRule(t, стили, ".w-filter select"); !strings.Contains(r, "min-width:0") {
		t.Errorf("контрол отбора без min-width:0 (%s) — длинное значение растянет ряд", r)
	}
}

// styleBlocks склеивает содержимое всех <style> страницы: дашбордные правила
// лежат не в первом блоке, и поиск только по нему ничего бы не нашёл.
func styleBlocks(body string) string {
	var b strings.Builder
	rest := body
	for {
		i := strings.Index(rest, "<style")
		if i < 0 {
			break
		}
		rest = rest[i:]
		j := strings.Index(rest, ">")
		k := strings.Index(rest, "</style>")
		if j < 0 || k < 0 {
			break
		}
		b.WriteString(rest[j+1 : k])
		rest = rest[k+len("</style>"):]
	}
	return b.String()
}
