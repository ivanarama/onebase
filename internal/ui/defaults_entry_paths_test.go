package ui

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Значения по умолчанию (план 153) обязаны работать одинаково на ВСЕХ путях
// создания объекта. Здесь проверяются два из трёх — форма и DSL; REST-путь
// живёт в internal/api (defaults_rest_test.go), потому что там его публичная
// точка входа.
//
// Проверка идёт через публичный вход (HTTP-обработчик формы, вызов DSL), а не
// через ApplyDefaults напрямую: зелёный тест на функции, которую боевой путь
// не зовёт, — ровно история #611.

func defaultsEntities() []*metadata.Entity {
	org := &metadata.Entity{
		Name: "Организация",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
	doc := &metadata.Entity{
		Name: "Реализация",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Организация", Type: "reference:Организация", RefEntity: "Организация", Default: "единственный"},
			{Name: "Комментарий", Type: metadata.FieldTypeString, Default: "по умолчанию"},
		},
	}
	return []*metadata.Entity{org, doc}
}

// Единственная организация в справочнике — та, что должна подставиться.
func seedSingleOrg(t *testing.T, s *Server, ctx context.Context, org *metadata.Entity) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := s.store.Upsert(ctx, org.Name, id, map[string]any{"Наименование": "ООО Ромашка"}, org); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDefaults_ФормаСоздания(t *testing.T) {
	ents := defaultsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	req := httptest.NewRequest("GET", "/ui/document/реализация/new", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("entity", "реализация")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	s.form(rec, req)

	if rec.Code != 200 {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}
	body := rec.Body.String()
	// Именно `selected`, а не просто наличие UUID: организация есть в списке
	// выбора в любом случае, и проверка на подстроку была бы зелёной даже без
	// дефолта.
	if !strings.Contains(body, orgID.String()+`" selected`) {
		t.Errorf("форма не подставила единственную организацию %s", orgID)
	}
	if !strings.Contains(body, `value="по умолчанию"`) {
		t.Errorf("форма не подставила литеральный дефолт")
	}
}

func TestDefaults_DSLСоздать(t *testing.T) {
	ents := defaultsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("Реализация").(*docProxy)
	w, ok := dp.CallMethod("создать", nil).(*docWriter)
	if !ok {
		t.Fatal("Создать() не вернул объект документа")
	}
	if got := refString(w.Get("Организация")); got != orgID.String() {
		t.Errorf("Организация = %v, ожидался %s", w.Get("Организация"), orgID)
	}
	if got := w.Get("Комментарий"); got != "по умолчанию" {
		t.Errorf("Комментарий = %v", got)
	}
}

// Дефолт не должен переживать явное присваивание — ни на одном из путей.
func TestDefaults_DSLЯвноеЗначениеГлавнее(t *testing.T) {
	ents := defaultsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	seedSingleOrg(t, s, ctx, ents[0])

	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("Реализация").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Комментарий", "своё")
	if got := w.Get("Комментарий"); got != "своё" {
		t.Errorf("Комментарий = %v, ожидалось «своё»", got)
	}
}

// Второй элемент справочника отменяет подстановку: «единственный» перестаёт
// быть единственным, и поле остаётся пустым — на всех путях одинаково.
func TestDefaults_ВторойЭлементОтменяетПодстановку(t *testing.T) {
	ents := defaultsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	seedSingleOrg(t, s, ctx, ents[0])
	second := uuid.New()
	if err := s.store.Upsert(ctx, ents[0].Name, second, map[string]any{"Наименование": "ИП Иванов"}, ents[0]); err != nil {
		t.Fatal(err)
	}

	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("Реализация").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	if v := w.Get("Организация"); v != nil && refString(v) != "" {
		t.Errorf("Организация = %v, ожидалось пусто при двух элементах", v)
	}
}

// Хук ПриСозданииНового вызывается и из DSL-пути: иначе поведение
// программного создания разошлось бы с формой — как #366 развёл проведение и
// отмену проведения.
func TestDefaults_ХукВызываетсяНаDSLПути(t *testing.T) {
	ents := defaultsEntities()
	src := `Процедура ПриСозданииНового(Объект)
  Объект.Номер = "из хука";
КонецПроцедуры`
	s, ctx := newSubmitTestServerWithPrograms(t, ents, map[string]string{"Реализация": src})
	seedSingleOrg(t, s, ctx, ents[0])

	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("Реализация").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	if got := w.Get("Номер"); got != "из хука" {
		t.Errorf("Номер = %v, ожидалось значение из ПриСозданииНового", got)
	}
}

// newSubmitTestServerWithPrograms — тот же сервер, что newSubmitTestServer, но
// с модулями объектов: нужен, чтобы проверить хук ПриСозданииНового.
func newSubmitTestServerWithPrograms(t *testing.T, entities []*metadata.Entity, sources map[string]string) (*Server, context.Context) {
	t.Helper()
	s, ctx := newSubmitTestServer(t, entities)
	programs := map[string]*ast.Program{}
	for name, src := range sources {
		programs[name] = mustParse(t, src)
	}
	s.reg.Load(runtime.LoadOptions{Entities: entities, Programs: programs})
	return s, ctx
}

// refString достаёт UUID из значения ссылочного реквизита: DSL отдаёт его
// либо строкой, либо обогащённой ссылкой.
func refString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case *interpreter.Ref:
		return t.UUID
	}
	if r, ok := v.(interface{ GetRefUUID() string }); ok {
		return r.GetRefUUID()
	}
	return ""
}
