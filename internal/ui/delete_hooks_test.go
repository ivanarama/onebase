package ui

// Хуки удаления в модуле объекта. События ПередУдалением/ПриУдалении/
// ПослеУдаления были объявлены в метаданных форм и не вызывались НИОТКУДА:
// `ПередУдалением: ПроверитьСсылки` молчал, а объект удалялся. Это защита,
// которая не защищает, — и в отличие от прочих находок этого класса,
// последствие необратимо: данных уже нет, когда замечают.
//
// Хук живёт в модуле объекта, а не формы: удаляют из списка, пачкой, из DSL и
// по REST — форменный обработчик закрывал бы один путь из пяти.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"path/filepath"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// deleteHookServer поднимает сервер со справочником и модулем объекта,
// содержащим переданный код хуков.
func deleteHookServer(t *testing.T, moduleSrc string) (*Server, *metadata.Entity, *storage.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "delete-hooks.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	ent := &metadata.Entity{
		Name: "Контрагент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Заблокирован", Type: metadata.FieldTypeBool},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	progs := map[string]*ast.Program{}
	if moduleSrc != "" {
		progs["Контрагент"] = mustParse(t, moduleSrc)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{ent}, Programs: progs})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}
	s.entitySvc = s.newEntityService(nil)
	return s, ent, db
}

func seedContragent(t *testing.T, db *storage.DB, ent *metadata.Entity, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.Upsert(context.Background(), ent.Name, id, map[string]any{"Наименование": name}, ent); err != nil {
		t.Fatalf("вставка: %v", err)
	}
	return id
}

func exists(t *testing.T, db *storage.DB, ent *metadata.Entity, id uuid.UUID) bool {
	t.Helper()
	row, err := db.GetByID(context.Background(), ent.Name, id, ent)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	return row != nil
}

const blockingHook = `Процедура ПередУдалением()
	Если ЭтотОбъект.Наименование = "Нельзя" Тогда
		ВызватьИсключение("Удаление запрещено: " + ЭтотОбъект.Наименование);
	КонецЕсли;
КонецПроцедуры`

// ПередУдалением отменяет удаление — объект остаётся в базе.
func TestDeleteHook_BeforeDeleteCancels(t *testing.T) {
	s, ent, db := deleteHookServer(t, blockingHook)
	id := seedContragent(t, db, ent, "Нельзя")

	res, err := s.entityService().Delete(context.Background(), ent, id)
	if err != nil {
		t.Fatalf("технический сбой: %v", err)
	}
	if res.DSLError == "" {
		t.Fatal("хук не отменил удаление")
	}
	if !strings.Contains(res.DSLError, "Удаление запрещено") {
		t.Errorf("текст отказа потерян: %q", res.DSLError)
	}
	if !exists(t, db, ent, id) {
		t.Error("объект удалён вопреки отказу хука")
	}
}

// Хук пропускает — объект удаляется.
func TestDeleteHook_BeforeDeleteAllows(t *testing.T) {
	s, ent, db := deleteHookServer(t, blockingHook)
	id := seedContragent(t, db, ent, "Можно")

	res, err := s.entityService().Delete(context.Background(), ent, id)
	if err != nil || res.DSLError != "" {
		t.Fatalf("удаление не прошло: err=%v dsl=%q", err, res.DSLError)
	}
	if exists(t, db, ent, id) {
		t.Error("объект остался в базе")
	}
}

// ПослеУдаления выполняется и видит данные удалённого объекта: иначе хук не
// смог бы ни записать в журнал, ни убрать связанные данные.
func TestDeleteHook_AfterDeleteSeesObject(t *testing.T) {
	s, ent, db := deleteHookServer(t, `Процедура ПослеУдаления()
	Сообщить("удалён: " + ЭтотОбъект.Наименование);
КонецПроцедуры`)
	id := seedContragent(t, db, ent, "Ромашка")

	res, err := s.entityService().Delete(context.Background(), ent, id)
	if err != nil || res.DSLError != "" {
		t.Fatalf("удаление не прошло: err=%v dsl=%q", err, res.DSLError)
	}
	joined := strings.Join(res.DSLMessages, "|")
	if !strings.Contains(joined, "удалён: Ромашка") {
		t.Errorf("ПослеУдаления не увидел объект, сообщения=%v", res.DSLMessages)
	}
	if exists(t, db, ent, id) {
		t.Error("объект остался в базе")
	}
}

// Ошибка в ПослеУдаления откатывает всю транзакцию — объект возвращается.
// Иначе получилась бы полузапись: объекта нет, а связанные действия не сделаны.
func TestDeleteHook_AfterDeleteErrorRollsBack(t *testing.T) {
	s, ent, db := deleteHookServer(t, `Процедура ПослеУдаления()
	ВызватьИсключение("связанные данные не убрались");
КонецПроцедуры`)
	id := seedContragent(t, db, ent, "Ромашка")

	res, err := s.entityService().Delete(context.Background(), ent, id)
	if err != nil {
		t.Fatalf("технический сбой: %v", err)
	}
	if res.DSLError == "" {
		t.Fatal("ошибка ПослеУдаления проглочена")
	}
	if !exists(t, db, ent, id) {
		t.Error("объект удалён, хотя ПослеУдаления упал — транзакция не откатилась")
	}
}

// Главное свойство: запрет действует на ВСЕХ путях удаления, а не только там,
// где его написали. Хук, который обходится сменой способа удаления, — не
// защита; ради этого он и вынесен в модуль объекта.
func TestDeleteHook_BlocksEveryPath(t *testing.T) {
	// Путь DSL: Документы.X.Удалить(Ссылка). Идёт через docProxy, а не через
	// сервис напрямую, — то есть проверяем именно проводку пути к хуку.
	src := `Процедура ПередУдалением()
	ВызватьИсключение("Удаление запрещено");
КонецПроцедуры`
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "paths.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	doc := &metadata.Entity{
		Name:   "Заявка",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{
		Entities: []*metadata.Entity{doc},
		Programs: map[string]*ast.Program{"Заявка": mustParse(t, src)},
	})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}

	id := uuid.New()
	if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Комментарий": "тест"}, doc); err != nil {
		t.Fatalf("вставка: %v", err)
	}

	root := newDocsRoot(s, interpreter.NewTxState(ctx))
	dp := root.Get("Заявка").(*docProxy)
	var raised any
	func() {
		defer func() { raised = recover() }()
		dp.CallMethod("удалить", []any{id.String()})
	}()
	if raised == nil {
		t.Error("DSL-путь удалил документ вопреки запрету хука")
	}
	if !exists(t, db, doc, id) {
		t.Error("документ удалён вопреки отказу хука")
	}
}

// Тот же запрет действует на DSL-пути справочника: Справочники.X.Удалить(Ссылка)
// идёт через CatalogProxy → dslCatalogDeleter → entityservice.Delete, а не через
// прямой db.Delete (issue #854 — раньше хук здесь молчал и объект удалялся).
// Прокси строится той же проводкой, что boevой buildDSLVarsTx (handlers_dsl.go).
func TestDeleteHook_BlocksDSLCatalogPath(t *testing.T) {
	s, ent, db := deleteHookServer(t, blockingHook)
	id := seedContragent(t, db, ent, "Нельзя")

	cp := dslCatalogRootForTest(s).Get(ent.Name).(*interpreter.CatalogProxy)
	var raised any
	func() {
		defer func() { raised = recover() }()
		cp.CallMethod("удалить", []any{&interpreter.Ref{UUID: id.String(), Type: ent.Name, Manager: cp}})
	}()
	if raised == nil {
		t.Error("DSL-путь удалил справочник вопреки запрету хука")
	}
	if !exists(t, db, ent, id) {
		t.Error("объект удалён вопреки отказу хука ПередУдалением")
	}
}

// Успешное DSL-удаление проходит и вызывает ПослеУдаления (полный путь через
// entityservice, не только отказ).
func TestDeleteHook_DSLCatalogDeletes(t *testing.T) {
	s, ent, db := deleteHookServer(t, blockingHook)
	id := seedContragent(t, db, ent, "Можно")

	cp := dslCatalogRootForTest(s).Get(ent.Name).(*interpreter.CatalogProxy)
	cp.CallMethod("удалить", []any{&interpreter.Ref{UUID: id.String(), Type: ent.Name, Manager: cp}})
	if exists(t, db, ent, id) {
		t.Error("объект остался в базе")
	}
}

// CheckRefs на DSL-пути: справочник, на который ссылается строка ТЧ документа,
// не удаляется (FK шапки такие ссылки не покрывает — раньше DSL тихо оставлял
// осиротевшую ссылку, класс DATA-01/#774).
func TestDSLCatalogDelete_BlockedByTablePartRef(t *testing.T) {
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "refs.db"))
	if err != nil {
		t.Fatalf("ConnectSQLite: %v", err)
	}
	t.Cleanup(db.Close)
	cat := &metadata.Entity{
		Name:   "Товар",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	doc := &metadata.Entity{
		Name:   "Продажа",
		Kind:   metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Комментарий", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{
			Name:   "Товары",
			Fields: []metadata.Field{{Name: "Товар", Type: metadata.FieldType("reference:Товар"), RefEntity: "Товар"}},
		}},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{cat, doc}); err != nil {
		t.Fatalf("миграция: %v", err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{cat, doc}})
	interp := interpreter.New()
	interp.LookupProc = registry.GetModuleProc
	s := &Server{store: db, reg: registry, interp: interp, lockMgr: runtime.NewLockManager(), messages: NewMessageStore()}

	goodsID := uuid.New()
	if err := db.Upsert(ctx, cat.Name, goodsID, map[string]any{"Наименование": "Гвозди"}, cat); err != nil {
		t.Fatalf("вставка товара: %v", err)
	}
	docID := uuid.New()
	if err := db.Upsert(ctx, doc.Name, docID, map[string]any{"Комментарий": "продажа"}, doc); err != nil {
		t.Fatalf("вставка документа: %v", err)
	}
	if err := db.UpsertTablePartRows(ctx, doc.Name, doc.TableParts[0].Name, docID,
		[]map[string]any{{"Товар": goodsID.String()}}, doc.TableParts[0]); err != nil {
		t.Fatalf("вставка строки ТЧ: %v", err)
	}

	cp := dslCatalogRootForTest(s).Get(cat.Name).(*interpreter.CatalogProxy)
	var raised any
	func() {
		defer func() { raised = recover() }()
		cp.CallMethod("удалить", []any{&interpreter.Ref{UUID: goodsID.String(), Type: cat.Name, Manager: cp}})
	}()
	if raised == nil {
		t.Error("DSL удалил справочник, на который ссылается ТЧ документа")
	}
	if !exists(t, db, cat, goodsID) {
		t.Error("товар удалён — осиротевшая ссылка в ТЧ")
	}
}

// Делетер проносится и в ссылку, которую возвращает Записать(): цепочка
// Создать() → Записать() → Ссылка.Удалить() тоже идёт через entityservice.
func TestDSLCatalogDelete_WriterRefCarriesDeleter(t *testing.T) {
	s, ent, db := deleteHookServer(t, blockingHook)

	root := dslCatalogRootForTest(s)
	cp := root.Get(ent.Name).(*interpreter.CatalogProxy)
	w := cp.CallMethod("создать", nil)
	writer, ok := w.(interface {
		Set(string, any)
		CallMethod(string, []any) any
	})
	if !ok {
		t.Fatalf("Создать() вернул %T без Set/CallMethod", w)
	}
	writer.Set("Наименование", "Нельзя")
	ref, ok := writer.CallMethod("записать", nil).(*interpreter.Ref)
	if !ok {
		t.Fatal("Записать() не вернул ссылку")
	}
	var raised any
	func() {
		defer func() { raised = recover() }()
		ref.CallMethod("удалить", nil)
	}()
	if raised == nil {
		t.Error("Ссылка.Удалить() после Записать() обошла хук ПередУдалением")
	}
	id, err := uuid.Parse(ref.UUID)
	if err != nil {
		t.Fatalf("uuid записанной ссылки: %v", err)
	}
	if !exists(t, db, ent, id) {
		t.Error("объект удалён вопреки отказу хука")
	}
}

// dslCatalogRootForTest строит Справочники так же, как buildDSLVarsTx —
// включая WithDeleter: тест проверяет именно проводку боевого пути.
func dslCatalogRootForTest(s *Server) *interpreter.CatalogsRoot {
	txState := interpreter.NewTxState(context.Background())
	return interpreter.NewCatalogsRoot(txState, s.store, s.reg).
		WithRowAccessChecker(s.dslRowAccessChecker()).
		WithExchangeRegistrar(s.exchangeRegistrar()).
		WithObjectFactory(s.catObjectFactory(txState)).
		WithDeleter(dslCatalogDeleter{s: s})
}

func TestDSLCatalogDelete_CollectsHookMessages(t *testing.T) {
	tests := []struct {
		name      string
		module    string
		wantMsg   string
		wantError bool
	}{
		{
			name: "after delete success",
			module: `Процедура ПослеУдаления()
	Сообщить("из ПослеУдаления");
КонецПроцедуры`,
			wantMsg: "из ПослеУдаления",
		},
		{
			name: "before delete refusal",
			module: `Процедура ПередУдалением()
	Сообщить("до отказа");
	ВызватьИсключение("удаление запрещено");
КонецПроцедуры`,
			wantMsg:   "до отказа",
			wantError: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ent, db := deleteHookServer(t, tc.module)
			id := seedContragent(t, db, ent, "Ромашка")
			var messages []string
			ctx := withDSLMessageCollector(context.Background(), &messages)
			txState := interpreter.NewTxState(ctx)
			root := interpreter.NewCatalogsRoot(txState, s.store, s.reg).
				WithRowAccessChecker(s.dslRowAccessChecker()).
				WithExchangeRegistrar(s.exchangeRegistrar()).
				WithObjectFactory(s.catObjectFactory(txState)).
				WithDeleter(dslCatalogDeleter{s: s})
			cp := root.Get(ent.Name).(*interpreter.CatalogProxy)
			ref := &interpreter.Ref{UUID: id.String(), Name: "Ромашка", Type: ent.Name, Manager: cp}
			var raised any
			func() {
				defer func() { raised = recover() }()
				ref.CallMethod("удалить", nil)
			}()
			if tc.wantError != (raised != nil) {
				t.Fatalf("panic = %#v, wantError=%v", raised, tc.wantError)
			}
			if len(messages) != 1 || messages[0] != tc.wantMsg {
				t.Fatalf("сообщения хука = %v, ожидалось [%s]", messages, tc.wantMsg)
			}
			if tc.wantError && !exists(t, db, ent, id) {
				t.Fatal("объект удалён вопреки отказу хука")
			}
			if !tc.wantError && exists(t, db, ent, id) {
				t.Fatal("объект остался после успешного удаления")
			}
		})
	}
}

// Без объявленного хука удаление работает как раньше — цена за защиту не
// должна быть «теперь надо писать хук».
func TestDeleteHook_NoHookDeletesAsBefore(t *testing.T) {
	s, ent, db := deleteHookServer(t, "")
	id := seedContragent(t, db, ent, "Обычный")

	res, err := s.entityService().Delete(context.Background(), ent, id)
	if err != nil || res.DSLError != "" {
		t.Fatalf("удаление не прошло: err=%v dsl=%q", err, res.DSLError)
	}
	if exists(t, db, ent, id) {
		t.Error("объект остался в базе")
	}
}
