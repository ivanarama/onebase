package ui

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/entityservice"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Запись объекта через entityservice.Save обязана регистрировать изменение в
// планах обмена (план 86): без строки в очереди узлы разъезжаются молча —
// приёмник просто никогда не узнает о новом объекте.
//
// Тест идёт публичным путём — s.entityService().Save, то есть через ту самую
// сборку сервиса, которой пользуется сервер (newEntityService). Это
// принципиально: соседние ui-тесты подменяют s.entitySvc собственным литералом
// и регистрацию обмена не задействуют вовсе, поэтому проводку «сервис →
// exchange» до сих пор не покрывал ни один тест — её можно было оборвать, не
// уронив ни одной проверки.
func TestEntityServiceSaveRegistersExchangeChange(t *testing.T) {
	db, reg, ctx, ent := newExchangeBaseDB(t)
	// База участвует в плане как узел center; в плоском плане обмена изменение
	// регистрируется всем остальным узлам, то есть fil01.
	if err := db.SaveExchangeThisNode(ctx, "Обмен", "center"); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		store:    db,
		reg:      reg,
		interp:   interpreter.New(),
		lockMgr:  runtime.NewLockManager(),
		messages: NewMessageStore(),
	}
	s.entitySvc = s.newEntityService(nil)

	id := uuid.New()
	if _, err := s.entityService().Save(ctx, entityservice.SaveRequest{
		Entity: ent,
		ID:     id,
		IsNew:  true,
		Fields: map[string]any{"Наименование": "Гвоздь"},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	changes, err := db.PendingExchangeChanges(ctx, "Обмен", "fil01")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("очередь обмена для fil01: получено %d строк, ожидалась 1: %+v", len(changes), changes)
	}
	if got := changes[0].ObjectID; got != id.String() {
		t.Errorf("ObjectID = %q, ожидался %q", got, id.String())
	}
	if got := changes[0].ObjectType; got != ent.Name {
		t.Errorf("ObjectType = %q, ожидался %q", got, ent.Name)
	}
	if changes[0].Deletion {
		t.Error("Deletion = true, ожидалось false: это обычная запись, а не пометка удаления")
	}
}
