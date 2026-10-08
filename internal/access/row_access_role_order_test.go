package access_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
)

// brokenPolicyRole — роль с политикой, которая не компилируется ни для одного
// пользователя: собственных атрибутов пользователя платформа не хранит.
func brokenPolicyRole() *auth.Role {
	return docRole("Сделка", []string{"read"}, auth.RowPolicies{"read": {
		Field: "Подразделение",
		Op:    "eq",
		Value: auth.RowValue{UserAttr: "подразделение"},
	}})
}

// Роль без политики на операцию снимает ограничения целиком, и ответ не должен
// зависеть от порядка ролей. Роли пользователя загружаются по имени, и раньше
// роль со сломанной политикой обрывала решение ошибкой, только если шла первой:
// переименование роли переключало доступ между «отказ» и «без ограничений».
func TestDecide_BrokenPolicyDoesNotDependOnRoleOrder(t *testing.T) {
	for _, order := range []string{"сломанная первой", "сломанная второй"} {
		roles := []*auth.Role{brokenPolicyRole(), docRole("Сделка", []string{"read"}, nil)}
		if order == "сломанная второй" {
			roles[0], roles[1] = roles[1], roles[0]
		}
		u := &auth.User{Login: "ivan", Roles: roles}
		dec, err := access.Decide(u, "document", "Сделка", "read", dealEntity())
		if err != nil {
			t.Fatalf("%s: Decide: %v", order, err)
		}
		if !dec.Allowed || !dec.Unrestricted {
			t.Fatalf("%s: роль без политики должна снять ограничения, получено %+v", order, dec)
		}
	}
}

// Без роли, снимающей ограничения, сломанная политика по-прежнему закрывает
// доступ — в любом порядке.
func TestDecide_BrokenPolicyFailsClosedInAnyOrder(t *testing.T) {
	for _, order := range []string{"сломанная первой", "сломанная второй"} {
		roles := []*auth.Role{brokenPolicyRole(), docRole("Сделка", []string{"read"}, respPolicy())}
		if order == "сломанная второй" {
			roles[0], roles[1] = roles[1], roles[0]
		}
		u := &auth.User{ID: "u1", Login: "ivan", Roles: roles}
		dec, err := access.Decide(u, "document", "Сделка", "read", dealEntity())
		if err == nil || dec.Allowed {
			t.Fatalf("%s: сломанная политика должна закрыть доступ, получено dec=%+v err=%v", order, dec, err)
		}
	}
}
