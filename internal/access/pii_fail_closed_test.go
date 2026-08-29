package access

import (
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Признак ПДн у поля (pii: true) закрывает реквизит по умолчанию: роль, которая
// читает объект и НЕ объявила правило, получает маску, а не полное значение.
// Иначе новый реквизит с телефоном открыт всем, пока про него не вспомнили в
// каждой роли по отдельности.

func клиентСПДн() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Телефон", Type: metadata.FieldTypeString, PII: true},
			{Name: "Комментарий", Type: metadata.FieldTypeString},
		},
	}
}

func рольЧитатель(name string, fa auth.FieldAccess) *auth.Role {
	return &auth.Role{
		Name: name,
		Permissions: auth.Permission{
			Catalogs:    map[string][]string{"Клиент": {"read"}},
			FieldAccess: fa,
		},
	}
}

func пользователь(roles ...*auth.Role) *auth.User {
	return &auth.User{Login: "u", Roles: roles}
}

func TestПДн_РольБезПравилаПолучаетМаску(t *testing.T) {
	u := пользователь(рольЧитатель("Супервизор", auth.FieldAccess{}))
	dec := FieldDecisions(u, "catalog", "Клиент", клиентСПДн())

	d, есть := dec["Телефон"]
	if !есть {
		t.Fatalf("ПДн-поле не закрыто: решения = %#v", dec)
	}
	if d.Strategy != FieldMaskAll {
		t.Errorf("стратегия = %q, ожидалась %q", d.Strategy, FieldMaskAll)
	}
	if _, есть := dec["Комментарий"]; есть {
		t.Error("обычное поле не должно закрываться без правила в роли")
	}
}

func TestПДн_ЯвноеПравилоРолиВажнее(t *testing.T) {
	// Роль, объявившая частичную маску, получает именно её — умолчание не
	// перекрывает осознанное решение конфигуратора.
	u := пользователь(рольЧитатель("Оператор", auth.FieldAccess{
		Catalogs: map[string]auth.FieldPolicies{
			"Клиент": {"Телефон": {Read: "mask_tail", Keep: 4}},
		},
	}))
	dec := FieldDecisions(u, "catalog", "Клиент", клиентСПДн())
	if dec["Телефон"].Strategy != FieldMaskTail || dec["Телефон"].Keep != 4 {
		t.Errorf("решение = %#v, ожидалась объявленная ролью mask_tail keep=4", dec["Телефон"])
	}
}

func TestПДн_ЯвныйFullОткрываетЗначение(t *testing.T) {
	// Выход из fail-closed есть, но он ЯВНЫЙ: роль пишет read: full и тем самым
	// принимает решение о раскрытии на себя.
	u := пользователь(рольЧитатель("Комплаенс", auth.FieldAccess{
		Catalogs: map[string]auth.FieldPolicies{
			"Клиент": {"Телефон": {Read: "full"}},
		},
	}))
	if dec := FieldDecisions(u, "catalog", "Клиент", клиентСПДн()); len(dec) != 0 {
		t.Errorf("решения = %#v, ожидалось полное значение", dec)
	}
}

func TestПДн_НаименееОграничительнаяРольПобеждает(t *testing.T) {
	// Семантика та же, что у остальных полей: если хотя бы одна читающая роль
	// разрешает полное значение, поле открыто.
	u := пользователь(
		рольЧитатель("Супервизор", auth.FieldAccess{}),
		рольЧитатель("Комплаенс", auth.FieldAccess{
			Catalogs: map[string]auth.FieldPolicies{"Клиент": {"Телефон": {Read: "full"}}},
		}),
	)
	if dec := FieldDecisions(u, "catalog", "Клиент", клиентСПДн()); len(dec) != 0 {
		t.Errorf("решения = %#v: роль с явным full должна открыть значение", dec)
	}
}

func TestПДн_БезПризнакаПоведениеПрежнее(t *testing.T) {
	// Конфигурации без pii: true не меняются вовсе — роль без field_access
	// продолжает видеть значения целиком.
	без := клиентСПДн()
	для := &без.Fields[1]
	для.PII = false

	u := пользователь(рольЧитатель("Супервизор", auth.FieldAccess{}))
	if dec := FieldDecisions(u, "catalog", "Клиент", без); len(dec) != 0 {
		t.Errorf("решения = %#v, ожидалось прежнее поведение (без маскирования)", dec)
	}
}
