package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/realtime"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Проведение документа реализовано ТРИЖДЫ почти дословно: список
// (handlers_entity.postDocument), DSL (docWriter.post) и entityservice.Save с
// Action=post, которым пользуются форма и REST. #812 честно отложил
// консолидацию (CORE-01), но не оставил и теста-паритета: трём копиям нечем
// доказывать эквивалентность, и следующая правка одной из них разъедет
// поведение молча (#880).
//
// Этот тест — та самая недостающая опора. Он не консолидирует копии; он
// фиксирует, что наблюдаемые эффекты у них одинаковы, чтобы консолидацию можно
// было делать не вслепую.
//
// Сравниваются эффекты, которые видит пользователь и смежные подсистемы:
// признак проведения, движения регистра, записанные хуком расчётные реквизиты
// шапки, версия строки и уведомление живому списку.

// postingEffects — снимок последствий проведения.
type postingEffects struct {
	Posted     bool
	Version    int64
	CalcField  string   // реквизит, который проставил ОбработкаПроведения
	Movements  []string // «номенклатура=количество», отсортировано
	ChangeSeen string   // action, доехавший до живого списка
}

func newParityServer(t *testing.T) (context.Context, *storage.DB, *Server, *metadata.Entity, *realtime.Hub) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	doc := &metadata.Entity{
		Name:    "ПоступлениеТоваров",
		Kind:    metadata.KindDocument,
		Posting: true,
		// NotifyChanges включён намеренно: уведомление живому списку — один из
		// эффектов, которые копии обязаны выполнять одинаково.
		NotifyChanges: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Итог", Type: metadata.FieldTypeString},
		},
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

	// Хук делает обе вещи, которые пути обязаны сохранить: пишет движения и
	// правит расчётный реквизит шапки. Второе особенно важно — именно его
	// проведение из списка когда-то теряло (#775).
	onPostSrc := `Процедура ОбработкаПроведения()
  Всего = 0;
  Для Каждого Стр Из ЭтотОбъект.Товары Цикл
    Дв = Движения.ОстаткиТоваров.Добавить();
    Дв.ВидДвижения = "Приход";
    Дв.Номенклатура = Стр.Номенклатура;
    Дв.Количество = Стр.Количество;
    Всего = Всего + Стр.Количество;
  КонецЦикла;
  ЭтотОбъект.Итог = "итого:" + Строка(Всего);
КонецПроцедуры`

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities:  []*metadata.Entity{doc},
		Programs:  map[string]*ast.Program{doc.Name: mustParse(t, onPostSrc)},
		Registers: []*metadata.Register{reg},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc

	// Шина поднята намеренно: без неё newChangePublisher отдаёт nil, публикация
	// у DSL- и списочного пути становится no-op, и «паритет» свёлся бы к
	// сравнению тишины с тишиной.
	hub := realtime.NewHub()
	t.Cleanup(hub.Close)
	s := &Server{store: db, reg: registry, interp: interp, hub: hub,
		lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
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
		// Ровно как в проде (dsl_catalogs.go): тот же публикатор поверх Server.
		ChangePublisher: s.newChangePublisher(),
	}
	return ctx, db, s, doc, hub
}

// createDoc записывает непроведённый документ с одной строкой ТЧ.
func createDoc(t *testing.T, ctx context.Context, s *Server, doc *metadata.Entity, num string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     id,
		IsNew:  true,
		Fields: map[string]any{"Номер": num},
		TablePartRows: map[string][]map[string]any{
			"Товары": {{"Номенклатура": "Тумбочка", "Количество": float64(100)}},
		},
	}); err != nil {
		t.Fatalf("подготовка документа %s: %v", num, err)
	}
	return id
}

func snapshot(t *testing.T, ctx context.Context, db *storage.DB, doc *metadata.Entity, id uuid.UUID, events <-chan realtime.Event) postingEffects {
	t.Helper()
	row, err := db.GetByID(ctx, doc.Name, id, doc)
	if err != nil {
		t.Fatalf("чтение документа: %v", err)
	}
	eff := postingEffects{
		Posted:    asBool(row["posted"]),
		CalcField: fmt.Sprintf("%v", row["Итог"]),
	}
	if v, ok := row["_version"]; ok {
		eff.Version = int64(toFloat(v))
	}
	// CAST намеренно: number на SQLite хранится TEXT, и его текстовое
	// представление зависит от того, пришло значение числом или строкой
	// («100.0» против «100»). Паритет — про ЗНАЧЕНИЯ движений, а не про байты;
	// разницу представлений вынес отдельной заявкой — #912.
	rows, err := db.Query(ctx,
		"SELECT номенклатура, CAST(количество AS NUMERIC) FROM рег_остаткитоваров WHERE recorder = ?", id.String())
	if err != nil {
		t.Fatalf("чтение движений: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var qty float64
		if err := rows.Scan(&name, &qty); err != nil {
			t.Fatal(err)
		}
		eff.Movements = append(eff.Movements, fmt.Sprintf("%s=%g", name, qty))
	}
	sort.Strings(eff.Movements)
	// Публикация асинхронна относительно возврата управления, поэтому ждём
	// событие с небольшим таймаутом, а не читаем неблокирующе.
	select {
	case ev := <-events:
		eff.ChangeSeen = ev.Name
	case <-time.After(2 * time.Second):
		eff.ChangeSeen = "(события не было)"
	}
	return eff
}

func TestПроведение_ПаритетТрёхПутей(t *testing.T) {
	ctx, db, s, doc, hub := newParityServer(t)
	_, events, cancel := hub.Subscribe("u1", "ivan", nil)
	defer cancel()
	// Подготовительные записи тоже публикуются — выгребаем их перед замером.
	drain := func() {
		for {
			select {
			case <-events:
			case <-time.After(300 * time.Millisecond):
				return
			}
		}
	}

	// 1. entityservice.Save(Action=post) — путь формы и REST.
	svcID := createDoc(t, ctx, s, doc, "СЕРВИС")
	drain()
	if _, err := s.entitySvc.Save(ctx, entityservice.SaveRequest{
		Entity: doc,
		ID:     svcID,
		Action: "post",
		Fields: map[string]any{"Номер": "СЕРВИС"},
		TablePartRows: map[string][]map[string]any{
			"Товары": {{"Номенклатура": "Тумбочка", "Количество": float64(100)}},
		},
	}); err != nil {
		t.Fatalf("entityservice.Save(post): %v", err)
	}
	viaService := snapshot(t, ctx, db, doc, svcID, events)

	// 2. DSL: Документы.X.ПолучитьОбъект().Провести().
	dslID := createDoc(t, ctx, s, doc, "DSL")
	drain()
	dp := newDocsRoot(s, interpreter.NewTxState(ctx)).Get(doc.Name).(*docProxy)
	loaded, err := dp.LoadObject(dslID.String())
	if err != nil {
		t.Fatalf("ПолучитьОбъект: %v", err)
	}
	loaded.(*docWriter).CallMethod("провести", nil)
	viaDSL := snapshot(t, ctx, db, doc, dslID, events)

	// 3. UI-список: POST /ui/document/{name}/{id}/post.
	listID := createDoc(t, ctx, s, doc, "СПИСОК")
	drain()
	req := reqWithChi(http.MethodPost, "/ui/document/"+doc.Name+"/"+listID.String()+"/post", nil,
		map[string]string{"entity": doc.Name, "id": listID.String()})
	rec := httptest.NewRecorder()
	s.postDocument(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("postDocument: код %d, тело %s", rec.Code, rec.Body.String())
	}
	viaList := snapshot(t, ctx, db, doc, listID, events)

	// Сравнение. Версия не сравнивается между путями: подготовка документов
	// одинакова, но у entityservice-пути проведение идёт вторым Save, поэтому
	// смысл имеет только «инкремент ровно один», что проверяется отдельно.
	paths := []struct {
		name string
		eff  postingEffects
	}{
		{"entityservice (форма/REST)", viaService},
		{"DSL Провести()", viaDSL},
		{"список postDocument", viaList},
	}
	base := paths[0]
	for _, p := range paths[1:] {
		if p.eff.Posted != base.eff.Posted {
			t.Errorf("признак проведения: %s=%v, %s=%v", base.name, base.eff.Posted, p.name, p.eff.Posted)
		}
		if p.eff.CalcField != base.eff.CalcField {
			t.Errorf("реквизит, записанный хуком: %s=%q, %s=%q — путь теряет правки ОбработкаПроведения",
				base.name, base.eff.CalcField, p.name, p.eff.CalcField)
		}
		if fmt.Sprint(p.eff.Movements) != fmt.Sprint(base.eff.Movements) {
			t.Errorf("движения: %s=%v, %s=%v", base.name, base.eff.Movements, p.name, p.eff.Movements)
		}
		if p.eff.ChangeSeen != base.eff.ChangeSeen {
			t.Errorf("уведомление живому списку: %s=%q, %s=%q", base.name, base.eff.ChangeSeen, p.name, p.eff.ChangeSeen)
		}
	}

	// Ни один путь не должен молча не сделать ничего: снимок обязан быть
	// содержательным, иначе «все три одинаковы» означало бы «все три пусты».
	if !base.eff.Posted || base.eff.CalcField != "итого:100" || len(base.eff.Movements) != 1 {
		t.Fatalf("эталонный путь не выполнил проведение: %+v", base.eff)
	}
	if base.eff.ChangeSeen == "(события не было)" {
		t.Errorf("живому списку не сообщено о проведении")
	}
}
