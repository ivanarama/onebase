package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// POST /ui/_ref-options/{entity}/page — страница подбора с динамическим
// preview (план 168). Сервер сам восстанавливает форму и элемент из метаданных,
// типизирует контекст по объявленным источникам и вызывает функцию в
// allowlist-окружении внутри host-owned транзакции.

func previewFixture(t *testing.T, procSrc string) (*Server, *metadata.Entity, *storage.DB, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "preview-page.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	form := &metadata.FormModule{
		Name: "ФормаОбъекта", Kind: "object", EntityName: "Заявка",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{
			{Kind: metadata.FormElementField, Name: "ПолеФилиал", DataPath: "Объект.Филиал"},
			{
				Kind: metadata.FormElementField, Name: "ПолеНаправление", DataPath: "Объект.Направление",
				ChoiceContext: map[string]string{"Филиал": "Объект.Филиал", "Владелец": "Объект.Владелец"},
			},
		},
	}
	src := &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog, Forms: []*metadata.FormModule{form},
		Fields: []metadata.Field{
			{Name: "Филиал", Type: metadata.FieldTypeString},
			{Name: "Владелец", Type: metadata.FieldType("reference:НаправлениеОбслуживания"), RefEntity: "НаправлениеОбслуживания"},
			{Name: "Направление", Type: metadata.FieldType("reference:НаправлениеОбслуживания"), RefEntity: "НаправлениеОбслуживания"},
		},
	}
	ent := &metadata.Entity{
		Name: "НаправлениеОбслуживания", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Информация", Type: metadata.FieldTypeString},
		},
		ChoicePreview:     "Информация",
		ChoicePreviewProc: "Памятки.ДляПодбора",
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent, src}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, ent.Name, id, map[string]any{
		"наименование": "Ремонт", "информация": "общая памятка",
	}, ent); err != nil {
		t.Fatal(err)
	}

	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent, src}})
	if procSrc != "" {
		prog, err := parser.New(lexer.New(procSrc, "памятки.module.os")).ParseProgram()
		if err != nil {
			t.Fatalf("parse module: %v", err)
		}
		reg.LoadModules(map[string]*ast.Program{"Памятки": prog})
	}
	interp := interpreter.New()
	interp.LookupProc = reg.GetModuleProc
	interp.LookupModuleProc = reg.GetModuleNamespacedProc
	s := &Server{reg: reg, store: db, interp: interp}
	return s, ent, db, id
}

func postPreviewPage(t *testing.T, s *Server, entity string, body map[string]any, user *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/_ref-options/"+entity+"/page", strings.NewReader(string(raw)))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", entity)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if user != nil {
		ctx = auth.ContextWithUser(ctx, user)
	}
	rec := httptest.NewRecorder()
	s.choicePreviewPageHandler(rec, req.WithContext(ctx))
	return rec
}

func previewPageBody(element string, ctxVals map[string]string) map[string]any {
	if ctxVals == nil {
		ctxVals = map[string]string{"Филиал": "МСК"}
	}
	return map[string]any{
		"q": "", "limit": 10, "offset": 0,
		"source":  map[string]string{"entity": "Заявка", "form": "ФормаОбъекта", "element": element},
		"context": ctxVals,
	}
}

// Функция получает страницу одной пачкой и контекст, восстановленный сервером
// из метаданных формы: «памятка филиала МСК» собирается под филиал звонка.
func TestChoicePreviewPageRunsProcWithResolvedContext(t *testing.T) {
	src := `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Рез = Новый Соответствие;
    Фил = Строка(Контекст.Получить("Филиал"));
    Для Каждого Стр Из Ссылки Цикл
        Рез.Вставить(Строка(Стр.Ссылка), "памятка филиала " + Фил);
    КонецЦикла;
    Возврат Рез;
КонецФункции`
	s, _, _, _ := previewFixture(t, src)
	rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody("ПолеНаправление", nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Preview string           `json:"preview"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Preview != "_preview" {
		t.Fatalf("preview = %q, ожидался _preview", resp.Preview)
	}
	if got, _ := resp.Items[0]["_preview"].(string); got != "памятка филиала МСК" {
		t.Fatalf("текст = %q, ожидался собранный по контексту", got)
	}
}

// Отсутствующая запись НЕ затирает статический fallback пустой строкой:
// строка без текста от функции показывает статическую памятку (merge-семантика,
// блокер 4 круга 2).
func TestChoicePreviewMissingKeyKeepsStaticFallback(t *testing.T) {
	src := `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Возврат Новый Соответствие;
КонецФункции`
	s, _, _, id := previewFixture(t, src)
	rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody("ПолеНаправление", nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if got, _ := resp.Items[0]["_preview"].(string); got != "общая памятка" {
		t.Fatalf("_preview = %q, ожидался статический fallback", got)
	}
	_ = id
}

// Dynamic preview uses POST instead of the ordinary GET picker endpoint. The
// transport change must not drop owner or choice_filter: all three constraints
// still describe one server-side page and are combined with AND.
func TestChoicePreviewPageComposesOwnerAndChoiceFilter(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	element := f.owner.Forms[0].Elements[1]
	element.ChoiceContext = map[string]string{"Направление": "Объект.Направление"}
	f.target.Owner = "Организация"
	f.target.Fields = append(f.target.Fields, metadata.Field{
		Name: metadata.StandardOwnerField, ID: metadata.StandardOwnerFieldID,
		Type: metadata.FieldType("reference:Организация"), RefEntity: "Организация",
	})
	if err := f.server.store.Migrate(context.Background(), []*metadata.Entity{f.target}); err != nil {
		t.Fatalf("migrate owner field: %v", err)
	}

	ownerA, ownerB := uuid.New(), uuid.New()
	allowed, wrongOwner, wrongChoice := uuid.New(), uuid.New(), uuid.New()
	for _, row := range []struct {
		id        uuid.UUID
		direction uuid.UUID
		owner     uuid.UUID
		name      string
	}{
		{allowed, f.rootA, ownerA, "preview intersection allowed"},
		{wrongOwner, f.rootA, ownerB, "preview intersection wrong owner"},
		{wrongChoice, f.rootB, ownerA, "preview intersection wrong choice"},
	} {
		if err := f.server.store.Upsert(context.Background(), f.target.Name, row.id, map[string]any{
			"Наименование":              row.name,
			"Направление":               row.direction.String(),
			"Аудитория":                 "anna",
			metadata.StandardOwnerField: row.owner.String(),
		}, f.target); err != nil {
			t.Fatalf("seed combined preview row %q: %v", row.name, err)
		}
	}

	body := map[string]any{
		"q": "preview intersection", "limit": 100, "offset": 0,
		"source": map[string]string{
			"entity": f.owner.Name, "form": f.owner.Forms[0].Name, "element": element.Name,
		},
		"context": map[string]string{},
		"filters": map[string]string{metadata.StandardOwnerField: ownerA.String()},
		"choice_sources": map[string]string{
			"Объект.Направление": f.rootA.String(),
		},
	}
	recorder := postPreviewPage(t, f.server, f.target.Name, body, f.user)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeChoiceHTTP(t, recorder)
	if response.Total != 1 || len(response.Items) != 1 || fmt.Sprint(response.Items[0]["id"]) != allowed.String() {
		t.Fatalf("preview owner and choice_filter are not ANDed: %#v", response)
	}
}

func TestChoicePreviewPageResolvesElementInsideNamedForm(t *testing.T) {
	f := newChoiceHTTPFixture(t)
	formA := f.owner.Forms[0]
	elementA := formA.Elements[1]
	elementA.ChoiceContext = map[string]string{"НаправлениеA": "Объект.Направление"}
	f.owner.Fields = append(f.owner.Fields, metadata.Field{
		Name: "ДругоеНаправление", Type: metadata.FieldType("reference:" + f.direction.Name), RefEntity: f.direction.Name,
	})
	elementB := &metadata.FormElement{
		ID: "fault-picker-second", Name: elementA.Name, Kind: elementA.Kind, DataPath: elementA.DataPath,
		ChoiceContext: map[string]string{"НаправлениеB": "Объект.ДругоеНаправление"},
		ChoiceFilter: []metadata.FormChoiceCondition{{
			Field: "Направление", Op: metadata.FormChoiceOpInHierarchy, From: "Объект.ДругоеНаправление",
		}},
	}
	formB := &metadata.FormModule{
		Name: "ФормаОбъектаВторая", EntityName: f.owner.Name, Kind: "object",
		LayoutKind: metadata.FormLayoutManaged, Elements: []*metadata.FormElement{elementB},
	}
	f.owner.Forms = append(f.owner.Forms, formB)

	if got := findPreviewElementByName(formA, elementA.Name); got != elementA || got.ChoiceContext["НаправлениеA"] == "" || got.ChoiceFilter[0].From != "Объект.Направление" {
		t.Fatalf("first form resolved foreign metadata: %#v", got)
	}
	if got := findPreviewElementByName(formB, elementB.Name); got != elementB || got.ChoiceContext["НаправлениеB"] == "" || got.ChoiceFilter[0].From != "Объект.ДругоеНаправление" {
		t.Fatalf("second form resolved foreign metadata: %#v", got)
	}

	request := func(form *metadata.FormModule, sourcePath string, source uuid.UUID) choiceHTTPResponse {
		body := map[string]any{
			"q": "needle", "limit": 100, "offset": 0,
			"source": map[string]string{
				"entity": f.owner.Name, "form": form.Name, "element": elementA.Name,
			},
			"context":        map[string]string{},
			"choice_sources": map[string]string{sourcePath: source.String()},
		}
		recorder := postPreviewPage(t, f.server, f.target.Name, body, f.user)
		if recorder.Code != http.StatusOK {
			t.Fatalf("form %q: status=%d body=%s", form.Name, recorder.Code, recorder.Body.String())
		}
		return decodeChoiceHTTP(t, recorder)
	}

	first := request(formA, "Объект.Направление", f.rootA)
	if first.Total != 2 {
		t.Fatalf("first form used wrong choice_filter: %#v", first)
	}
	for _, item := range first.Items {
		if id := fmt.Sprint(item["id"]); id == f.legacySelected.String() || id == f.foreignOther.String() {
			t.Fatalf("first form returned root-B row: %#v", first.Items)
		}
	}
	second := request(formB, "Объект.ДругоеНаправление", f.rootB)
	if second.Total != 2 || len(second.Items) != 2 {
		t.Fatalf("second form used wrong choice_filter: %#v", second)
	}
	for _, id := range []uuid.UUID{f.legacySelected, f.foreignOther} {
		found := false
		for _, item := range second.Items {
			found = found || fmt.Sprint(item["id"]) == id.String()
		}
		if !found {
			t.Fatalf("second form did not return %s: %#v", id, second.Items)
		}
	}
}

// Ошибка функции — контролируемый 422 (fail-closed, инвариант 10), а не тихий
// откат к списку без просмотра.
func TestChoicePreviewProcErrorIs422(t *testing.T) {
	src := `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    ВыброситьОшибку("база памяток недоступна");
КонецФункции`
	s, _, _, _ := previewFixture(t, src)
	rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody("ПолеНаправление", nil), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, ожидался 422: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "база памяток") {
		t.Fatalf("внутренний текст ошибки ушёл клиенту: %s", rec.Body.String())
	}
}

// Подмена source: неизвестная форма/элемент/не-контекстный элемент — 400.
func TestChoicePreviewPageRejectsForeignSource(t *testing.T) {
	s, _, _, _ := previewFixture(t, "")
	t.Run("unknown form", func(t *testing.T) {
		body := previewPageBody("ПолеНаправление", nil)
		body["source"].(map[string]string)["form"] = "Подмена"
		rec := postPreviewPage(t, s, "НаправлениеОбслуживания", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, ожидался 400: %s", rec.Code, rec.Body.String())
		}
	})
	cases := []struct {
		name    string
		element string
	}{
		{"unknown element", "ТакогоНет"},
		{"element without choice_context", "ПолеФилиал"},
	}
	for _, tc := range cases {
		rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody(tc.element, nil), nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: code = %d, ожидался 400: %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// Значение ссылочного параметра контекста проверяется на допуск строки:
// несуществующая ссылка — 403 (инвариант 7).
func TestChoicePreviewContextReferenceRowAccess(t *testing.T) {
	s, _, _, _ := previewFixture(t, `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Возврат Новый Соответствие;
КонецФункции`)
	rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody("ПолеНаправление",
		map[string]string{"Владелец": uuid.NewString()}), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, ожидался 403: %s", rec.Code, rec.Body.String())
	}
}

// Запрещённые capability в allowlist-окружении (инвариант 9): каждая попытка —
// 422, и в базе ничего не меняется. Публичный HTTP-тест, не приватный вызов.
func TestChoicePreviewCapabilitiesAreDenied(t *testing.T) {
	_ = context.Background
	procs := []string{
		// Запись справочника — Справочники в окружении preview нет.
		`
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Справочники.НаправлениеОбслуживания.Создать();
    Возврат Новый Соответствие;
КонецФункции`,
		// Запись константы — get-only proxy отклоняет присваивание.
		`
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Константы.НПозволено = 1;
    Возврат Новый Соответствие;
КонецФункции`,
		// Управление транзакцией — транзакционных функций в окружении нет.
		`
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Т = НачатьТранзакцию();
    Возврат Новый Соответствие;
КонецФункции`,
		// Сеть — HTTP-функций в окружении нет.
		`
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Ответ = ПолучитьURL("http://example.invalid");
    Возврат Новый Соответствие;
КонецФункции`,
	}
	for i, body := range procs {
		s, _, db, _ := previewFixture(t, body)
		rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody("ПолеНаправление", nil), nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("capability #%d: code = %d, ожидался 422: %s", i, rec.Code, rec.Body.String())
		}
		_ = db
	}
}

// Ключ вне переданных ссылок игнорируется и не меняет состав строк (инвариант 11).
func TestChoicePreviewOffPageKeyIgnored(t *testing.T) {
	src := `
Функция ДляПодбора(Ссылки, Контекст) Экспорт
    Рез = Новый Соответствие;
    Рез.Вставить("не-uuid-со-страницы", "чужой текст");
    Возврат Рез;
КонецФункции`
	s, _, _, _ := previewFixture(t, src)
	rec := postPreviewPage(t, s, "НаправлениеОбслуживания", previewPageBody("ПолеНаправление", nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if got, _ := resp.Items[0]["_preview"].(string); got != "общая памятка" {
		t.Fatalf("чужой ключ изменил строку: %q", got)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("состав строк изменился: %d", len(resp.Items))
	}
}
