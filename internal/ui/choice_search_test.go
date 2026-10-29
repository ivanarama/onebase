package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

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
