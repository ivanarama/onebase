package ui

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Регрессия к issue #609: ответ события управляемой формы не должен отдавать
// клиенту реальное значение реквизита, закрытого полевой политикой роли.
//
// Утечка возникала не при чтении, а при СЕРИАЛИЗАЦИИ: restoreUnsubmittedFields
// и refreshFieldsWrittenByHandler дочитывают неприсланные реквизиты сырым
// store.GetByID — и это правильно, значения нужны настоящими для записи и для
// DSL-обработчика. Но тот же obj.Fields уходил в JSON ответа через
// serializeManagedFormEventState без маски.
//
// Поэтому маска накладывается на пути к клиенту, а obj.Fields остаётся
// нетронутым — оба свойства и проверяются ниже.
func TestSerializeManagedFormEventState_MasksProtectedField(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
	form := &metadata.FormModule{Name: "объекта"}

	obj := runtime.NewObject(entity.Name, entity.Kind)
	obj.Set("Номер", "СЧ-001")
	obj.Set("Телефон", "+7 999 123-45-67")

	user := &auth.User{ID: "op", Login: "operator", Roles: []*auth.Role{{
		Name: "Оператор",
		Permissions: auth.Permission{
			Documents: map[string][]string{"Заявка": {"read"}},
			FieldAccess: auth.FieldAccess{
				Documents: map[string]auth.FieldPolicies{"Заявка": {"Телефон": {Read: "mask_all"}}},
			},
		},
	}}}
	ctx := auth.ContextWithUser(context.Background(), user)

	s := &Server{}
	values, _, _, _, _ := s.serializeManagedFormEventState(ctx, form, entity, obj, nil, nil)

	got, ok := values["Телефон"]
	if !ok {
		t.Fatalf("реквизит «Телефон» пропал из ответа целиком: %#v", values)
	}
	if got == "+7 999 123-45-67" {
		t.Errorf("ответ события формы отдал НАСТОЯЩЕЕ значение защищённого реквизита: %#v", got)
	}
	if values["Номер"] != "СЧ-001" {
		t.Errorf("незащищённый реквизит искажён: %#v", values["Номер"])
	}

	// Ключевое: сам объект не тронут. Иначе маска уехала бы в Upsert и
	// затёрла реальное значение в базе — это хуже утечки, потому что
	// необратимо.
	if obj.Fields["телефон"] != "+7 999 123-45-67" && obj.Fields["Телефон"] != "+7 999 123-45-67" {
		t.Errorf("маска попала в obj.Fields — значение уедет в запись: %#v", obj.Fields)
	}
}

// Пользователь без ограничений видит реальное значение — маска не должна
// срабатывать на всех подряд.
func TestSerializeManagedFormEventState_UnrestrictedUserSeesRealValue(t *testing.T) {
	entity := &metadata.Entity{
		Name: "Заявка",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Телефон", Type: metadata.FieldTypeString},
		},
	}
	obj := runtime.NewObject(entity.Name, entity.Kind)
	obj.Set("Телефон", "+7 999 123-45-67")

	admin := &auth.User{ID: "a", Login: "admin", IsAdmin: true}
	ctx := auth.ContextWithUser(context.Background(), admin)

	s := &Server{}
	values, _, _, _, _ := s.serializeManagedFormEventState(ctx, &metadata.FormModule{}, entity, obj, nil, nil)
	if values["Телефон"] != "+7 999 123-45-67" {
		t.Errorf("администратору реквизит пришёл искажённым: %#v", values["Телефон"])
	}
}
