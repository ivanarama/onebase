package ui

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Запись ИЗ ОБРАБОТЧИКА ФОРМЫ («Объект.Записать()») обязана проходить ту же
// защиту от записи маски, что submit, REST и DSL-документы. Без неё оператор,
// которому телефон показан звёздочками, затирал ими настоящий номер — маска
// уезжала в базу при первом же нажатии кнопки формы.
func TestFormObjectWrite_MaskedFieldGuard(t *testing.T) {
	cat := uiClientEntity()
	s, ctx := newSubmitTestServer(t, []*metadata.Entity{cat})
	id := uuid.New()
	if err := s.store.Upsert(ctx, "Клиент", id, map[string]any{
		"Наименование": "Иванов", "Телефон": "+79161234455",
	}, cat); err != nil {
		t.Fatal(err)
	}
	user := uiMaskUser([]string{"read", "write"}, auth.FieldPolicies{"Телефон": {Read: "mask_all"}})
	ctx = auth.ContextWithUser(ctx, user)

	obj := &runtime.Object{ID: id, Fields: map[string]any{
		"Наименование": "Петров",
		"Телефон":      "••••••", // ровно то, что видел обработчик
	}}
	this := s.newFormObjectThisLive(ctx, nil, obj, cat, nil, false)
	if err := this.write(); err != nil {
		t.Fatalf("запись из обработчика: %v", err)
	}

	row, err := s.store.GetByID(ctx, "Клиент", id, cat)
	if err != nil {
		t.Fatal(err)
	}
	if row["Телефон"] != "+79161234455" {
		t.Fatalf("маска затёрла настоящий номер: %v", row["Телефон"])
	}
	if row["Наименование"] != "Петров" {
		t.Fatalf("видимое поле должно записаться: %v", row["Наименование"])
	}
	// Защита не должна стать каналом раскрытия: после записи обработчик видит
	// то же, что видел до неё.
	if obj.Fields["Телефон"] != "••••••" {
		t.Fatalf("после записи обработчику раскрылось значение: %v", obj.Fields["Телефон"])
	}
}
