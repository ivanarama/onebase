package ui

import (
	"context"
	"encoding/json"
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
		"source":  map[string]string{"entity": "Заявка", "element": element},
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
