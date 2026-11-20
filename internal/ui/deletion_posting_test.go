package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// newPostingDoc поднимает SQLite-Server с проводимым документом ПоступлениеТоваров
// (ТЧ Товары), который в ОбработкаПроведения пишет приход в регистр ОстаткиТоваров.
func newPostingDoc(t *testing.T) (context.Context, *storage.DB, *Server, *docProxy, *metadata.Entity) {
	t.Helper()
	return newPostingDocWithModule(t, "")
}

// newPostingDocWithModule — тот же документ, в модуль которого дописаны
// процедуры extraSrc (например, ОбработкаУдаленияПроведения).
func newPostingDocWithModule(t *testing.T, extraSrc string) (context.Context, *storage.DB, *Server, *docProxy, *metadata.Entity) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "ПоступлениеТоваров",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields:  []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	reg := &metadata.Register{
		Name:       "ОстаткиТоваров",
		Dimensions: []metadata.Field{{Name: "Номенклатура", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	onPostSrc := `Процедура ОбработкаПроведения()
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Дв = Движения.ОстаткиТоваров.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Количество = Стр.Количество;
  КонецЦикла;
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{"ПоступлениеТоваров": mustParse(t, onPostSrc+"\n"+extraSrc)},
		Registers: []*metadata.Register{reg},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = &entityservice.Service{
		Store:        db,
		Reg:          registry,
		Interp:       interp,
		PrepareHook:  s.enrichHeaderRefs,
		EnrichTPRows: s.enrichTPRowsWithRefs,
		BuildVars:    s.buildDSLVarsWithMessagesTx,
		MakeThis: func(ctx context.Context, ctxSrc interpreter.CtxSource, obj *runtime.Object, e *metadata.Entity) interpreter.This {
			return s.newFormObjectThisLive(ctx, ctxSrc, obj, e, nil, false)
		},
	}
	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("ПоступлениеТоваров").(*docProxy)
	return ctx, db, s, dp, doc
}

// postOne создаёт+заполняет+проводит документ ПОС-001 (Тумбочка x100) и возвращает writer.
func postOne(t *testing.T, dp *docProxy) *docWriter {
	t.Helper()
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "ПОС-001")
	tp := w.Get("Товары").(*tpProxy)
	r := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r.Set("Номенклатура", "Тумбочка")
	r.Set("Количество", float64(100))
	w.CallMethod("провести", nil)
	return w
}

// DSL .Провести() помеченного документа должно отклоняться РАНО — до запуска
// ОбработкаПроведения и записи движений. Берём непроведённый помеченный документ:
// с ранним guard'ом движений не будет (0); без него хук отработал бы и saveMovements
// записал бы движения до того, как backstop SetPosted вернёт ошибку (утечка движений).
func TestDocWriterPost_BlockedWhenMarked(t *testing.T) {
	ctx, db, _, dp, _ := newPostingDoc(t)

	// Создать + Записать (без проведения) → строка документа есть, движений нет.
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "ПОС-002")
	tp := w.Get("Товары").(*tpProxy)
	r := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r.Set("Номенклатура", "Тумбочка")
	r.Set("Количество", float64(100))
	w.CallMethod("записать", nil)

	// Пометить непроведённый документ на удаление.
	if err := db.MarkForDeletion(ctx, "ПоступлениеТоваров", w.obj.ID, true); err != nil {
		t.Fatal(err)
	}

	// Провести помеченный → ранний guard, ErrPostingDeletionMarked.
	if err := w.post(); !errors.Is(err, storage.ErrPostingDeletionMarked) {
		t.Fatalf("ожидался ErrPostingDeletionMarked, получили %v", err)
	}

	// Ранний guard сработал ДО хука/saveMovements ⇒ движений нет и posted=false.
	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 0 {
		t.Fatalf("guard должен сработать до saveMovements: движений %d, ожидалось 0", mov)
	}
	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров LIMIT 1").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("помеченный документ не должен быть проведён")
	}
}

// Пометка проведённого документа на удаление авто-отменяет проведение
// (чистит движения, снимает posted). Снятие пометки проведение не возвращает.
func TestServerMarkForDeletion_AutoUnposts(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDoc(t)
	w := postOne(t, dp)

	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 1 {
		t.Fatalf("до пометки ожидалось 1 движение, получили %d", mov)
	}

	if err := s.markForDeletion(ctx, doc, w.obj.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 0 {
		t.Errorf("после пометки движений должно быть 0, получили %d", mov)
	}
	var posted, marked bool
	if err := db.QueryRow(ctx, "SELECT posted, deletion_mark FROM поступлениетоваров LIMIT 1").Scan(&posted, &marked); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("проведение должно быть снято при пометке")
	}
	if !marked {
		t.Error("документ должен быть помечен на удаление")
	}

	// Снятие пометки проведение НЕ возвращает.
	if err := s.markForDeletion(ctx, doc, w.obj.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "SELECT posted, deletion_mark FROM поступлениетоваров LIMIT 1").Scan(&posted, &marked); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("снятие пометки не должно проводить документ")
	}
	if marked {
		t.Error("пометка должна быть снята")
	}
}

// DSL-методы менеджера документов: ОтменитьПроведение / ПометитьНаУдаление / СнятьПометку.
func TestDocsRoot_UnpostAndMarkMethods(t *testing.T) {
	ctx, db, _, dp, _ := newPostingDoc(t)
	_ = postOne(t, dp)
	ref := dp.CallMethod("найтипономеру", []any{"ПОС-001"}).(*interpreter.Ref)

	// ОтменитьПроведение → движения 0, posted false.
	dp.CallMethod("отменитьпроведение", []any{ref})
	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 0 {
		t.Errorf("после ОтменитьПроведение движений 0 ожидалось, получили %d", mov)
	}
	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM поступлениетоваров LIMIT 1").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if posted {
		t.Error("posted=false ожидался после ОтменитьПроведение")
	}

	// ПометитьНаУдаление → deletion_mark true.
	dp.CallMethod("пометитьнаудаление", []any{ref})
	var marked bool
	if err := db.QueryRow(ctx, "SELECT deletion_mark FROM поступлениетоваров LIMIT 1").Scan(&marked); err != nil {
		t.Fatal(err)
	}
	if !marked {
		t.Error("документ должен быть помечен после ПометитьНаУдаление")
	}

	// СнятьПометку → deletion_mark false.
	dp.CallMethod("снятьпометку", []any{ref})
	if err := db.QueryRow(ctx, "SELECT deletion_mark FROM поступлениетоваров LIMIT 1").Scan(&marked); err != nil {
		t.Fatal(err)
	}
	if marked {
		t.Error("пометка должна быть снята после СнятьПометку")
	}
}

func TestUnpostDocument_HookErrorRollsBack(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDoc(t)
	w := postOne(t, dp)

	program := mustParse(t, `Процедура ОбработкаУдаленияПроведения()
  ВызватьИсключение("отмена из UI запрещена");
КонецПроцедуры`)
	s.reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: s.reg.Registers(),
		Programs:  map[string]*ast.Program{doc.Name: program},
	})

	r := reqWithChi("POST", "/ui/document/поступлениетоваров/"+w.obj.ID.String()+"/unpost", url.Values{},
		map[string]string{"kind": "document", "entity": "поступлениетоваров", "id": w.obj.ID.String()})
	rec := httptest.NewRecorder()
	s.unpostDocument(rec, r)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ожидался 303, получен %d: %s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "posting_error=") {
		t.Fatalf("редирект не содержит ошибку OnUnpost: %s", location)
	}
	row, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !asBool(row["posted"]) {
		t.Fatalf("ошибка OnUnpost не откатила posted: %#v", row)
	}
	var movements int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if movements != 1 {
		t.Fatalf("ошибка OnUnpost не откатила движения: осталось %d", movements)
	}
}

// Регрессия #36: карточка помеченного на удаление документа (рендер через
// HTTP-handler formEdit) НЕ должна показывать кнопки «Провести» и ДОЛЖНА
// показывать «Снять пометку». На SQLite GetByID отдаёт deletion_mark как
// int64(1); handler обязан нормализовать его к "true", иначе шаблонные
// сравнения с литералом "true" дают неверный UI (баг финального ревью #36).
func TestFormEdit_MarkedDoc_HidesPostShowsUnmark(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDoc(t)
	// Минимальный cfg, чтобы render() не падал на nil Bundle и т.п.
	s.cfg = Config{AppName: "test"}

	// Создать+провести документ, затем пометить на удаление (авто-снимает
	// проведение). После этого в БД deletion_mark = 1 (SQLite int64).
	w := postOne(t, dp)
	if err := s.markForDeletion(ctx, doc, w.obj.ID, true); err != nil {
		t.Fatal(err)
	}

	// Санити: GetByID на SQLite отдаёт deletion_mark как НЕ-bool (int64(1)) —
	// именно это значение handler обязан нормализовать.
	row, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, isBool := row["deletion_mark"].(bool); isBool {
		t.Fatalf("ожидался не-bool deletion_mark из GetByID на SQLite, получили %T(%v)",
			row["deletion_mark"], row["deletion_mark"])
	}

	// Рендер карточки через реальный HTTP-handler formEdit.
	r := reqWithChi("GET", "/ui/document/поступлениетоваров/"+w.obj.ID.String(), url.Values{},
		map[string]string{"kind": "document", "entity": "поступлениетоваров", "id": w.obj.ID.String()})
	rec := httptest.NewRecorder()
	s.formEdit(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// Должна быть кнопка снятия пометки (action .../delete?mark=0).
	if !strings.Contains(body, "mark=0") {
		t.Errorf("карточка помеченного документа должна содержать кнопку снятия пометки (mark=0); тело:\n%s", body)
	}
	// НЕ должно быть кнопок проведения (name=\"_action\" value=\"post\" / \"post_and_close\").
	if strings.Contains(body, `value="post"`) {
		t.Errorf("карточка помеченного документа НЕ должна содержать кнопку «Провести» (value=\"post\"); тело:\n%s", body)
	}
}

// Регрессия #366 (баг 1): DSL Документы.X.ОтменитьПроведение(Ссылка) должно
// запускать ОбработкаУдаленияПроведения (OnUnpost) — симметрично UI-кнопке и
// REST, которые идут через entityservice.Unpost. Ошибка хука откатывает и снятие
// проведения, и очистку движений. До фикса DSL-отмена чистила движения и снимала
// posted напрямую, молча пропуская хук отката (ошибки бы не было вовсе).
func TestDocProxyUnpost_DSL_RunsOnUnpostHook(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDoc(t)
	w := postOne(t, dp)

	program := mustParse(t, `Процедура ОбработкаУдаленияПроведения()
  ВызватьИсключение("отмена из DSL запрещена");
КонецПроцедуры`)
	s.reg.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Registers: s.reg.Registers(),
		Programs:  map[string]*ast.Program{doc.Name: program},
	})

	err := dp.unpostRef(w.obj.ID.String())
	if err == nil || !strings.Contains(err.Error(), "отмена из DSL запрещена") {
		t.Fatalf("ожидалась ошибка OnUnpost при DSL-отмене, получили %v", err)
	}

	// Ошибка хука откатила транзакцию: проведение и движения остались на месте.
	row, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !asBool(row["posted"]) {
		t.Fatalf("ошибка OnUnpost не откатила posted: %#v", row)
	}
	var movements int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if movements != 1 {
		t.Fatalf("ошибка OnUnpost не откатила движения: осталось %d", movements)
	}
}

// Регрессия #366 (баг 2): DSL Провести() должно персистить правки реквизитов
// шапки, сделанные в ОбработкаПроведения (OnPost) — как entityservice.Save
// (upsert после хука). До фикса post() не upsert'ил шапку после OnPost, поэтому
// расчётные поля жили при проведении из UI/REST, но пропадали при DSL-проведении.
func TestDocWriterPost_DSL_PersistsOnPostFieldEdits(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc := &metadata.Entity{
		Name:    "РасходТоваров",
		Kind:    metadata.KindDocument,
		Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "СуммаДокумента", Type: metadata.FieldTypeNumber},
		},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Сумма", Type: metadata.FieldTypeNumber},
		}}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatal(err)
	}

	// OnPost вычисляет СуммаДокумента из строк ТЧ и пишет её в шапку this.
	onPostSrc := `Процедура ОбработкаПроведения()
  Итого = 0;
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Итого = Итого + Стр.Сумма;
  КонецЦикла;
  ЭтотОбъект.СуммаДокумента = Итого;
КонецПроцедуры`
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{doc.Name: mustParse(t, onPostSrc)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	// Документы.X.Создать() идёт через entityservice (план 153: дефолты и
	// ПриСозданииНового), поэтому сервер собираем как в проде.
	s.entitySvc = s.newEntityService(nil)

	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get("РасходТоваров").(*docProxy)
	w := dp.CallMethod("создать", nil).(*docWriter)
	w.Set("Номер", "РАС-001")
	tp := w.Get("Товары").(*tpProxy)
	r1 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r1.Set("Номенклатура", "Тумбочка")
	r1.Set("Сумма", float64(300))
	r2 := tp.CallMethod("добавить", nil).(*interpreter.MapThis)
	r2.Set("Номенклатура", "Стол")
	r2.Set("Сумма", float64(700))

	w.CallMethod("провести", nil)

	// СуммаДокумента, вычисленная в OnPost, должна оказаться в БД (300+700=1000).
	var total float64
	if err := db.QueryRow(ctx, "SELECT суммадокумента FROM расходтоваров LIMIT 1").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1000 {
		t.Fatalf("СуммаДокумента из OnPost не персистилась: в БД %v, ожидалось 1000", total)
	}
	var posted bool
	if err := db.QueryRow(ctx, "SELECT posted FROM расходтоваров LIMIT 1").Scan(&posted); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Error("документ должен быть проведён после Провести()")
	}
}

// Пометка проведённого документа на удаление снимает проведение, как
// «Отменить проведение»: с ОбработкаУдаленияПроведения (как в 1С). Отказ
// обработчика — 409, документ остаётся проведённым, непомеченным, движения на
// месте. Путь — HTTP-обработчик кнопки «Пометить на удаление».
func TestMarkForDeletion_RunsUnpostHook(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDocWithModule(t, `Процедура ОбработкаУдаленияПроведения()
  ВызватьИсключение("снимать проведение нельзя");
КонецПроцедуры`)
	w := postOne(t, dp)
	id := w.obj.ID.String()

	rec := httptest.NewRecorder()
	s.deleteRecord(rec, reqWithChi("POST", "/ui/document/ПоступлениеТоваров/"+id+"/delete?mark=1", url.Values{},
		map[string]string{"kind": "document", "entity": doc.Name, "id": id}))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "снимать проведение нельзя") {
		t.Fatalf("отказ обработчика — 409 с его текстом, получено %d: %s", rec.Code, rec.Body.String())
	}
	row, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !asBool(row["posted"]) || asBool(row["deletion_mark"]) {
		t.Fatalf("документ должен остаться проведённым и непомеченным: posted=%v deletion_mark=%v", row["posted"], row["deletion_mark"])
	}
	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 1 {
		t.Fatalf("движения должны остаться: %d", mov)
	}
}

// Без отказа пометка снимает проведение через обработчик: он видит
// непроведённый документ (как после «Отменить проведение»).
func TestMarkForDeletion_UnpostHookSeesUnpostedDocument(t *testing.T) {
	ctx, db, s, dp, doc := newPostingDocWithModule(t, `Процедура ОбработкаУдаленияПроведения()
  Если ЭтотОбъект.Posted Тогда
    ВызватьИсключение("обработчик увидел проведённый документ");
  КонецЕсли;
КонецПроцедуры`)
	w := postOne(t, dp)
	id := w.obj.ID.String()

	rec := httptest.NewRecorder()
	s.deleteRecord(rec, reqWithChi("POST", "/ui/document/ПоступлениеТоваров/"+id+"/delete?mark=1", url.Values{},
		map[string]string{"kind": "document", "entity": doc.Name, "id": id}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("пометка должна пройти, получено %d: %s", rec.Code, rec.Body.String())
	}
	row, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if asBool(row["posted"]) || !asBool(row["deletion_mark"]) {
		t.Fatalf("документ должен стать непроведённым и помеченным: posted=%v deletion_mark=%v", row["posted"], row["deletion_mark"])
	}
	var mov int
	if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM рег_остаткитоваров").Scan(&mov); err != nil {
		t.Fatal(err)
	}
	if mov != 0 {
		t.Fatalf("движения должны быть сняты: %d", mov)
	}
}

// Пометка из кода конфигурации (Документы.X.УстановитьПометкуУдаления) идёт
// тем же путём: отказ обработчика — ошибка, документ проведён.
func TestDSLMarkForDeletion_RunsUnpostHook(t *testing.T) {
	ctx, db, _, dp, doc := newPostingDocWithModule(t, `Процедура ОбработкаУдаленияПроведения()
  ВызватьИсключение("снимать проведение нельзя");
КонецПроцедуры`)
	w := postOne(t, dp)
	var raised any
	func() {
		defer func() { raised = recover() }()
		dp.CallMethod("пометитьнаудаление", []any{&interpreter.Ref{UUID: w.obj.ID.String(), Type: doc.Name, Kind: doc.Kind}})
	}()
	if raised == nil || !strings.Contains(fmt.Sprint(raised), "снимать проведение нельзя") {
		t.Fatalf("ожидался отказ обработчика, получено: %v", raised)
	}
	row, err := db.GetByID(ctx, doc.Name, w.obj.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !asBool(row["posted"]) || asBool(row["deletion_mark"]) {
		t.Fatalf("документ должен остаться проведённым и непомеченным: posted=%v deletion_mark=%v", row["posted"], row["deletion_mark"])
	}
}
