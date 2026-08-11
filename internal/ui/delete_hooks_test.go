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
