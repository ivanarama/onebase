package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// choice_search_proc — поиск подбора функцией конфигурации, как
// ОбработкаПолученияДанныхВыбора 1С. Всё через публичный GET
// /ui/_ref-options/{entity}?q=…&flt=… — тот же путь, что поле, ячейка ТЧ и
// окно выбора.

type choiceSearchFixture struct {
	s      *Server
	street uuid.UUID
	houses map[string]uuid.UUID // наименование → id (улица А)
	other  uuid.UUID            // «д.15, корп.1» другой улицы
}

func newChoiceSearchFixture(t *testing.T, moduleSrc string) choiceSearchFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "choice-search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	street := &metadata.Entity{Name: "Улица", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
	house := &metadata.Entity{Name: "Дом", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Улица", Type: metadata.FieldType("reference:Улица"), RefEntity: "Улица"},
		},
		ChoiceSearchProc: "СервисАдресов.ПоискДомов",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{street, house}); err != nil {
		t.Fatal(err)
	}
	f := choiceSearchFixture{street: uuid.New(), houses: map[string]uuid.UUID{}, other: uuid.New()}
	otherStreet := uuid.New()
	for id, name := range map[uuid.UUID]string{f.street: "Дмитровское ш", otherStreet: "Тверская"} {
		if err := db.Upsert(ctx, street.Name, id, map[string]any{"наименование": name}, street); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"д.15", "д.150", "д.15, корп.1", "д.15, корп.2"} {
		id := uuid.New()
		f.houses[name] = id
		if err := db.Upsert(ctx, house.Name, id, map[string]any{"наименование": name, "улица": f.street.String()}, house); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Upsert(ctx, house.Name, f.other, map[string]any{"наименование": "д.15, корп.1", "улица": otherStreet.String()}, house); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{street, house}})
	prog, err := parser.New(lexer.New(moduleSrc, "сервисадресов.module.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse module: %v", err)
	}
	reg.LoadModules(map[string]*ast.Program{"СервисАдресов": prog})
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	f.s = &Server{reg: reg, store: db, interp: interp, ops: newOperationLimiter()}
	return f
}

// Функция знает правило «15к1 → д.15, корп.1» и ставит точное совпадение
// первым. Улицу она берёт из Параметры.Отбор, но нарочно возвращает и дом
// чужой улицы — его обязана отсечь платформа.
const choiceSearchModule = `
Функция ПоискДомов(Текст, Параметры) Экспорт
	Если Параметры.Отбор.Получить("Улица") = Неопределено Тогда
		ВызватьИсключение "нет улицы в отборе";
	КонецЕсли;
	Если Параметры.Справочник <> "Дом" Или Параметры.Предел <> 1000 Тогда
		ВызватьИсключение "неверные параметры";
	КонецЕсли;
	З = Новый Запрос;
	З.Текст = "ВЫБРАТЬ Ссылка, Наименование ИЗ Справочник.Дом УПОРЯДОЧИТЬ ПО Наименование";
	Р = З.Выполнить();
	Точные = Новый Массив;
	Прочие = Новый Массив;
	Для Каждого Стр Из Р Цикл
		Если Текст = "15к1" И Стр.Наименование = "д.15, корп.1" Тогда
			Точные.Добавить(Стр.Ссылка);
		Иначе
			Прочие.Добавить(Стр.Ссылка);
		КонецЕсли;
	КонецЦикла;
	Для Каждого С Из Прочие Цикл
		Точные.Добавить(С);
	КонецЦикла;
	Возврат Точные;
КонецФункции

Функция Сломанная(Текст, Параметры) Экспорт
	ВызватьИсключение "сбой поиска";
КонецФункции

Функция Пишущая(Текст, Параметры) Экспорт
	Д = Справочники.Дом.СоздатьЭлемент();
	Д.Наименование = "вписан поиском";
	Д.Записать();
	Возврат Новый Массив;
КонецФункции
`

func getRefOptions(t *testing.T, s *Server, entity string, q url.Values) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ui/_ref-options/"+entity+"?"+q.Encode(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", entity)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.refOptionsJSON(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func searchOptionLabels(body map[string]any) []string {
	items, _ := body["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		row, _ := it.(map[string]any)
		out = append(out, refValueString(row["_label"]))
	}
	return out
}

func searchOptionIDs(body map[string]any) []string {
	items, _ := body["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		row, _ := it.(map[string]any)
		out = append(out, refValueString(row["id"]))
	}
	return out
}

func TestChoiceSearchProc_OrdersAndFiltersThroughRefOptions(t *testing.T) {
	f := newChoiceSearchFixture(t, choiceSearchModule)
	flt, _ := json.Marshal(map[string]string{"Улица": f.street.String()})
	q := url.Values{"q": {"15к1"}, "flt": {string(flt)}, "limit": {"50"}}
	rec, body := getRefOptions(t, f.s, "Дом", q)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	ids := searchOptionIDs(body)
	if len(ids) != 4 || ids[0] != f.houses["д.15, корп.1"].String() {
		t.Fatalf("точное совпадение обязано быть первым, а дома улицы — все четыре: %v %v", searchOptionLabels(body), ids)
	}
	for _, id := range ids {
		if id == f.other.String() {
			t.Fatal("дом чужой улицы прошёл мимо отбора подбора")
		}
	}
	// Остальные — в порядке функции (по наименованию).
	want := []string{"д.15, корп.1", "д.15", "д.15, корп.2", "д.150"}
	if got := searchOptionLabels(body); len(got) != len(want) || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("порядок функции не сохранён: %v, ждали %v", got, want)
	}
	if body["total"] != float64(4) || body["matched"] != true {
		t.Fatalf("total=%v matched=%v", body["total"], body["matched"])
	}

	// Страницы считает платформа по отфильтрованному списку.
	q.Set("limit", "2")
	q.Set("offset", "1")
	_, page := getRefOptions(t, f.s, "Дом", q)
	if got := searchOptionLabels(page); len(got) != 2 || got[0] != "д.15" || got[1] != "д.15, корп.2" || page["total"] != float64(4) {
		t.Fatalf("страница 2×1: %v total=%v", got, page["total"])
	}
}

func TestChoiceSearchProc_EmptyInputKeepsDefaultList(t *testing.T) {
	f := newChoiceSearchFixture(t, `
Функция ПоискДомов(Текст, Параметры) Экспорт
	ВызватьИсключение "при пустом вводе функция не зовётся";
КонецФункции
`)
	flt, _ := json.Marshal(map[string]string{"Улица": f.street.String()})
	rec, body := getRefOptions(t, f.s, "Дом", url.Values{"q": {""}, "flt": {string(flt)}})
	if rec.Code != http.StatusOK || len(searchOptionIDs(body)) != 4 || body["matched"] != nil {
		t.Fatalf("пустой ввод: status=%d labels=%v matched=%v", rec.Code, searchOptionLabels(body), body["matched"])
	}
}

func TestChoiceSearchProc_FailureIs422WithoutSubstringFallback(t *testing.T) {
	for _, proc := range []string{"СервисАдресов.Сломанная", "СервисАдресов.Пишущая", "СервисАдресов.НетТакой"} {
		t.Run(proc, func(t *testing.T) {
			f := newChoiceSearchFixture(t, choiceSearchModule)
			f.s.reg.GetEntity("Дом").ChoiceSearchProc = proc
			rec, body := getRefOptions(t, f.s, "Дом", url.Values{"q": {"д.15"}})
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, ждали 422: %s", rec.Code, rec.Body.String())
			}
			if len(searchOptionIDs(body)) != 0 || body["error"] == nil {
				t.Fatalf("отказ обязан быть пустым и с сообщением: %v", body)
			}
			rows, err := f.s.store.List(context.Background(), "Дом", f.s.reg.GetEntity("Дом"), storage.ListParams{})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 5 {
				t.Fatalf("функция поиска изменила данные: домов %d", len(rows))
			}
		})
	}
}

func TestChoiceSearchIDs_Contract(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	arr := &interpreter.Array{}
	arr.CallMethod("добавить", []any{&interpreter.Ref{UUID: a.String(), Type: "Дом"}})
	arr.CallMethod("добавить", []any{b.String()})
	arr.CallMethod("добавить", []any{a.String()})
	ids, err := choiceSearchIDs(arr)
	if err != nil || len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("ссылки и строки, повторы схлопнуты: %v %v", ids, err)
	}
	if ids, err := choiceSearchIDs(nil); err != nil || len(ids) != 0 {
		t.Fatalf("Неопределено — ничего не найдено: %v %v", ids, err)
	}
	bad := &interpreter.Array{}
	bad.CallMethod("добавить", []any{"не ссылка"})
	if _, err := choiceSearchIDs(bad); err == nil {
		t.Fatal("строка не-id принята")
	}
	if _, err := choiceSearchIDs("строка"); err == nil {
		t.Fatal("не Массив принят")
	}
}

func TestChoiceSearchProc_EightFormConditions(t *testing.T) {
	module := strings.Replace(choiceSearchModule, "Функция ПоискДомов(Текст, Параметры) Экспорт", `Функция ПоискДомов(Текст, Параметры) Экспорт
	Если Текст = "empty" Тогда
		Возврат Новый Массив;
	КонецЕсли;`, 1)
	f := newChoiceSearchFixture(t, module)
	ent := f.s.reg.GetEntity("Дом")
	yes := true
	element := &metadata.FormElement{ID: "house-picker", Name: "Дом", Kind: metadata.FormElementField, DataPath: "Объект.Дом"}
	for i := range 8 {
		name := fmt.Sprintf("Флаг%d", i)
		ent.Fields = append(ent.Fields, metadata.Field{Name: name, Type: metadata.FieldTypeBool})
		element.ChoiceFilter = append(element.ChoiceFilter, metadata.FormChoiceCondition{Field: name, Op: metadata.FormChoiceOpEqual, Value: &yes})
	}
	if err := f.s.store.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	for name, id := range f.houses {
		values := map[string]any{"Наименование": name, "Улица": f.street.String()}
		for i := range 8 {
			values[fmt.Sprintf("Флаг%d", i)] = name != "д.150" || i != 7
		}
		if err := f.s.store.Upsert(t.Context(), ent.Name, id, values, ent); err != nil {
			t.Fatal(err)
		}
	}
	owner := &metadata.Entity{Name: "Адрес", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Дом", Type: metadata.FieldType("reference:Дом"), RefEntity: "Дом"}},
		Forms:  []*metadata.FormModule{{Name: "ФормаОбъекта", EntityName: "Адрес", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{element}}}}
	f.s.reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{f.s.reg.GetEntity("Улица"), ent, owner}})
	// Load replaces modules too: restore the same public configuration function.
	prog, err := parser.New(lexer.New(module, "сервисадресов.module.os")).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	f.s.reg.LoadModules(map[string]*ast.Program{"СервисАдресов": prog})
	flt, _ := json.Marshal(map[string]string{"Улица": f.street.String()})
	q := url.Values{"form_entity": {"Адрес"}, "form": {"ФормаОбъекта"}, "element": {"house-picker"}, "sources": {"{}"}, "flt": {string(flt)}}
	for _, text := range []string{"", "15к1"} {
		q.Set("q", text)
		rec, body := getRefOptions(t, f.s, "Дом", q)
		if rec.Code != http.StatusOK || body["total"] != float64(3) || len(searchOptionIDs(body)) != 3 {
			t.Fatalf("q=%q: status=%d body=%s", text, rec.Code, rec.Body.String())
		}
		for _, id := range searchOptionIDs(body) {
			if id == f.houses["д.150"].String() {
				t.Fatal("eighth condition was not applied")
			}
		}
	}
	q.Set("q", "empty")
	rec, body := getRefOptions(t, f.s, "Дом", q)
	if rec.Code != http.StatusOK || body["total"] != float64(0) || len(searchOptionIDs(body)) != 0 || body["matched"] != true {
		t.Fatalf("empty search with eight conditions: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestChoiceSearchProc_PostPageRejectsNegativeOffset(t *testing.T) {
	f := newChoiceSearchFixture(t, choiceSearchModule)
	ent := f.s.reg.GetEntity("Дом")
	ent.Fields = append(ent.Fields, metadata.Field{Name: "ВыборДома", Type: metadata.FieldType("reference:Дом"), RefEntity: "Дом"})
	ent.Forms = []*metadata.FormModule{{Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{Name: "Дом", Kind: metadata.FormElementField, DataPath: "Объект.ВыборДома", ChoiceContext: map[string]string{"Улица": "Объект.Улица"}}}}}
	if err := f.s.store.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"q": "15к1", "offset": 0, "source": map[string]string{"entity": "Дом", "element": "Дом"}, "filters": map[string]string{"Улица": f.street.String()}}
	rec := postPreviewPage(t, f.s, "Дом", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid baseline status=%d: %s", rec.Code, rec.Body.String())
	}
	body["offset"] = -1
	defer func() {
		if v := recover(); v != nil {
			t.Errorf("negative offset panicked: %v", v)
		}
	}()
	rec = postPreviewPage(t, f.s, "Дом", body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestChoiceSearchProc_SQLTimeoutThroughHTTP(t *testing.T) {
	// Five rows across thirteen aliases keep SQL busy beyond the configured
	// second; interpreter-only deadlines cannot interrupt Query.Выполнить().
	query := "ВЫБРАТЬ КОЛИЧЕСТВО(*) КАК Всего ИЗ Справочник.Дом КАК Д0"
	for i := 1; i < 13; i++ {
		query += fmt.Sprintf(" ВНУТРЕННЕЕ СОЕДИНЕНИЕ Справочник.Дом КАК Д%d ПО ИСТИНА", i)
	}
	module := fmt.Sprintf(`
Функция ПоискДомов(Текст, Параметры) Экспорт
	Если Текст = "быстро" Тогда
		Возврат Новый Массив;
	КонецЕсли;
	З = Новый Запрос;
	З.Текст = "%s";
	З.Выполнить();
	Возврат Новый Массив;
КонецФункции
`, query)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			f := newChoiceSearchFixture(t, module)
			f.s.cfg.Limits.ProcessorTimeoutSec = 1
			ent := f.s.reg.GetEntity("Дом")
			ent.Fields = append(ent.Fields, metadata.Field{Name: "ВыборДома", Type: metadata.FieldType("reference:Дом"), RefEntity: "Дом"})
			ent.Forms = []*metadata.FormModule{{Name: "ФормаОбъекта", Kind: "object", LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{{Name: "Дом", Kind: metadata.FormElementField, DataPath: "Объект.ВыборДома", ChoiceContext: map[string]string{"Улица": "Объект.Улица"}}}}}
			if err := f.s.store.Migrate(t.Context(), []*metadata.Entity{ent}); err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			router.Get("/ui/_ref-options/{entity}", f.s.refOptionsJSON)
			router.Post("/ui/_ref-options/{entity}/page", f.s.choicePreviewPageHandler)
			request := func(ctx context.Context, text string) *httptest.ResponseRecorder {
				path := "/ui/_ref-options/Дом?q=" + url.QueryEscape(text)
				raw := ""
				if method == http.MethodPost {
					path = "/ui/_ref-options/Дом/page"
					body, err := json.Marshal(map[string]any{"q": text, "source": map[string]string{"entity": "Дом", "element": "Дом"}})
					if err != nil {
						t.Fatal(err)
					}
					raw = string(body)
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(raw)).WithContext(ctx))
				return rec
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			start := time.Now()
			rec := request(ctx, "15")
			elapsed := time.Since(start)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "error") {
				t.Fatalf("SQL timeout: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if ctx.Err() != nil || elapsed > 2500*time.Millisecond {
				t.Errorf("configured 1s timeout did not stop SQL before outer deadline: elapsed=%v outer=%v", elapsed, ctx.Err())
			}
			if elapsed < 500*time.Millisecond {
				t.Errorf("SQL failed before exercising the deadline: elapsed=%v", elapsed)
			}
			t.Logf("configured 1s SQL timeout: %v", elapsed)
			// A fresh request must acquire SQLite's single connection after the
			// failed search: timeout must retain the unconditional rollback.
			nextCtx, nextCancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer nextCancel()
			if next := request(nextCtx, "быстро"); next.Code != http.StatusOK {
				t.Fatalf("search after rollback: status=%d body=%s", next.Code, next.Body.String())
			}
		})
	}
}
