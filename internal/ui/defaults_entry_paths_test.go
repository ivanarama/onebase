package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
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

// Четвёртый путь создания — управляемая форма. Она рисует только размещённые
// элементы, поэтому неразмещённый реквизит в POST не приходит вовсе и до #1189
// записывался пустым: дефолт и ПриСозданииНового считались на GET и терялись по
// дороге. Через DSL и REST тот же реквизит заполнялся — пути расходились, хотя
// entityservice/defaults.go обещает обратное.
//
// Ровно этот реквизит и есть самый естественный кандидат на дефолт: он
// заполняется сам, поэтому его и не выносят на форму.
func TestDefaults_УправляемаяФормаЗаполняетНеразмещённый(t *testing.T) {
	ents := defaultsEntities()
	doc := ents[1]
	doc.Forms = []*metadata.FormModule{managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"))}
	s, ctx := newSubmitTestServer(t, ents)
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	// Сначала GET — как в браузере. Он же доказывает, почему POST неполон:
	// значения посчитаны, но в разметку не попали.
	rec := httptest.NewRecorder()
	s.form(rec, reqWithChi("GET", "/ui/document/реализация/new", nil, map[string]string{"entity": "реализация"}))
	if rec.Code != 200 {
		t.Fatalf("форма не открылась: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "по умолчанию") {
		t.Fatal("управляемая форма отрисовала неразмещённый реквизит — тест проверяет не тот случай")
	}

	// Браузер шлёт ровно то, что было на форме.
	row := submitNewManagedDoc(t, s, ctx, doc, url.Values{"Номер": {"0001"}})

	if got := refString(row["Организация"]); got != orgID.String() {
		t.Errorf("Организация = %v, ожидался %s — дефолт «единственный» не доехал", row["Организация"], orgID)
	}
	if got := row["Комментарий"]; got != "по умолчанию" {
		t.Errorf("Комментарий = %v, ожидался литеральный дефолт", got)
	}
}

// Хук ПриСозданииНового на том же пути. Проверяется отдельно от декларативного
// дефолта: посчитанное хуком пропадало так же молча, а порядок «хук после
// дефолта» виден по тому, что литерал «по умолчанию» перекрыт.
func TestDefaults_УправляемаяФормаЗоветХукПриСозданииНового(t *testing.T) {
	ents := defaultsEntities()
	doc := ents[1]
	doc.Forms = []*metadata.FormModule{managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"))}
	src := `Процедура ПриСозданииНового(Объект)
  Объект.Комментарий = "из хука";
КонецПроцедуры`
	s, ctx := newSubmitTestServerWithPrograms(t, ents, map[string]string{"Реализация": src})
	seedSingleOrg(t, s, ctx, ents[0])

	row := submitNewManagedDoc(t, s, ctx, doc, url.Values{"Номер": {"0001"}})

	if got := row["Комментарий"]; got != "из хука" {
		t.Errorf("Комментарий = %v, ожидалось значение из ПриСозданииНового", got)
	}
}

// POST обязан самостоятельно проверить результат ПриСозданииНового, даже когда
// управляемая форма прислала все реквизиты. Баннер на предшествующем GET не
// является доказательством: прямой POST и ошибка, возникшая между GET и POST,
// иначе записывали частично инициализированный объект.
func TestDefaults_УправляемаяФормаНеПишетПослеОшибкиПриСозданииНового(t *testing.T) {
	ents := defaultsEntities()
	doc := ents[1]
	doc.Forms = []*metadata.FormModule{managedObjectForm(
		fieldEl("ПолеНомер", "Объект.Номер"),
		fieldEl("ПолеОрганизация", "Объект.Организация"),
		fieldEl("ПолеКомментарий", "Объект.Комментарий"),
	)}
	src := `Процедура ПриСозданииНового(Объект)
  ВызватьИсключение("инициализация не выполнена");
КонецПроцедуры`
	s, ctx := newSubmitTestServerWithPrograms(t, ents, map[string]string{"Реализация": src})
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	rec := httptest.NewRecorder()
	s.submit(rec, reqWithChi("POST", "/ui/document/реализация/new", url.Values{
		"Номер":       {"0001"},
		"Организация": {orgID.String()},
		"Комментарий": {"введено пользователем"},
	},
		map[string]string{"entity": "реализация"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("ошибка хука не перерисовала форму: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "инициализация не выполнена") {
		t.Fatalf("форма не показала ошибку ПриСозданииНового: %s", rec.Body.String())
	}
	assertNoManagedDocs(t, s, ctx, doc)
}

// Технический сбой вычисления дефолта — отдельный канал NewObject. Он так же
// должен остановить публичный POST до Save, а не превратиться в пустое значение.
func TestDefaults_УправляемаяФормаНеПишетПослеТехническойОшибкиДефолта(t *testing.T) {
	ents := defaultsEntities()
	doc := ents[1]
	doc.Fields[2].Default = "константа." // конфигурация загружена в тесте в обход check
	doc.Forms = []*metadata.FormModule{managedObjectForm(fieldEl("ПолеНомер", "Объект.Номер"))}
	s, ctx := newSubmitTestServer(t, ents)

	rec := httptest.NewRecorder()
	s.submit(rec, reqWithChi("POST", "/ui/document/реализация/new", url.Values{"Номер": {"0001"}},
		map[string]string{"entity": "реализация"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("техническая ошибка не перерисовала форму: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Реализация.Комментарий") {
		t.Fatalf("форма не показала источник технической ошибки: %s", rec.Body.String())
	}
	assertNoManagedDocs(t, s, ctx, doc)
}

// Парный случай: реквизит на форме есть, и пользователь его очистил — дефолт не
// имеет права зарасти обратно. Различает «поле не прислали» и «прислали пустым»
// то же правило присутствия ключа, что и при записи существующего объекта.
func TestDefaults_УправляемаяФормаНеЗатираетОчищенноеПоле(t *testing.T) {
	ents := defaultsEntities()
	doc := ents[1]
	doc.Forms = []*metadata.FormModule{managedObjectForm(
		fieldEl("ПолеНомер", "Объект.Номер"),
		fieldEl("ПолеКомм", "Объект.Комментарий"),
	)}
	s, ctx := newSubmitTestServer(t, ents)
	seedSingleOrg(t, s, ctx, ents[0])

	row := submitNewManagedDoc(t, s, ctx, doc, url.Values{"Номер": {"0001"}, "Комментарий": {""}})

	if row["Комментарий"] != nil {
		t.Errorf("Комментарий = %v, очищенное пользователем поле заросло дефолтом", row["Комментарий"])
	}
}

// Снятый флажок браузер не шлёт вовсе, и отличить его от неразмещённого поля по
// одному лишь отсутствию ключа нельзя. Дефолт `истина` не имеет права поставить
// галочку обратно — иначе снять её было бы невозможно.
func TestDefaults_УправляемаяФормаНеВозвращаетСнятыйФлажок(t *testing.T) {
	ents := defaultsEntities()
	doc := ents[1]
	doc.Fields = append(doc.Fields, metadata.Field{Name: "Согласован", Type: metadata.FieldTypeBool, Default: "истина"})
	doc.Forms = []*metadata.FormModule{managedObjectForm(
		fieldEl("ПолеНомер", "Объект.Номер"),
		&metadata.FormElement{Kind: metadata.FormElementCheckbox, Name: "ФлагСогл", DataPath: "Объект.Согласован"},
	)}
	s, ctx := newSubmitTestServer(t, ents)
	seedSingleOrg(t, s, ctx, ents[0])

	row := submitNewManagedDoc(t, s, ctx, doc, url.Values{"Номер": {"0001"}})

	if isTruthyStored(row["Согласован"]) {
		t.Errorf("Согласован = %v, снятый флажок вернулся дефолтом", row["Согласован"])
	}
}

// Действие ИИ-чата — ещё один путь создания: он строил объект напрямую
// (runtime.NewObject + Save) и не видел ни дефолтов, ни хука. Заявка #1189
// назвала это заодно, решение человека — чинить (вариант 2 разбора).
func TestDefaults_ДействиеИИЧата(t *testing.T) {
	ents := defaultsEntities()
	s, ctx := newSubmitTestServer(t, ents)
	orgID := seedSingleOrg(t, s, ctx, ents[0])

	rec := httptest.NewRecorder()
	s.aiActionRun(rec, httptest.NewRequest("POST", "/ui/ai/action", strings.NewReader(
		`{"тип":"создать","вид":"document","сущность":"Реализация","поля":{"Номер":"0001"}}`)))
	var out struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("разбор ответа: %v (%s)", err, rec.Body.String())
	}
	if !out.OK {
		t.Fatalf("действие не выполнено: %s", out.Error)
	}
	id, err := uuid.Parse(out.ID)
	if err != nil {
		t.Fatalf("нет корректного id в ответе: %q", out.ID)
	}
	row, err := s.store.GetByID(ctx, ents[1].Name, id, ents[1])
	if err != nil || row == nil {
		t.Fatalf("документ не найден: %v", err)
	}
	if got := refString(row["Организация"]); got != orgID.String() {
		t.Errorf("Организация = %v, ожидался %s", row["Организация"], orgID)
	}
	if got := row["Комментарий"]; got != "по умолчанию" {
		t.Errorf("Комментарий = %v, ожидался литеральный дефолт", got)
	}
}

// submitNewManagedDoc проводит запись через публичный вход submit и возвращает
// единственную сохранённую строку. Приватный parseSubmitForm не зовём: тест
// обязан идти тем же путём, что и пользователь (повод — #611).
func submitNewManagedDoc(t *testing.T, s *Server, ctx context.Context, doc *metadata.Entity, form url.Values) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.submit(rec, reqWithChi("POST", "/ui/document/реализация/new", form,
		map[string]string{"entity": "реализация"}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("запись не прошла: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась одна запись, получено %d", len(rows))
	}
	return rows[0]
}

func assertNoManagedDocs(t *testing.T, s *Server, ctx context.Context, doc *metadata.Entity) {
	t.Helper()
	rows, err := s.store.List(ctx, doc.Name, doc, storage.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("после отказа сохранено записей: %d", len(rows))
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
