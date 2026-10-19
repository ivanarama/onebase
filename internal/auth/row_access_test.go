package auth

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPermissionRowAccessYAMLandJSONRoundTrip(t *testing.T) {
	src := []byte(`
name: sales
permissions:
  catalogs:
    Контрагенты: [read, write]
  row_access:
    catalogs:
      Контрагенты:
        read:
          field: Ответственный
          op: eq
          value: { user: login }
        write:
          same_as: read
        delete:
          field: Подразделение
          op: eq
          value: { user_attr: Department }
`)
	var role Role
	if err := yaml.Unmarshal(src, &role); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	policy, ok := role.Permissions.RowAccess.Policy("catalog", "Контрагенты", "write")
	if !ok {
		t.Fatal("write row policy not resolved")
	}
	if policy.Field != "Ответственный" || policy.Value.User != "login" {
		t.Fatalf("policy = %+v", policy)
	}
	policy, ok = role.Permissions.RowAccess.Policy("catalog", "Контрагенты", "delete")
	if !ok {
		t.Fatal("delete row policy not resolved")
	}
	if policy.Value.UserAttr != "department" {
		t.Fatalf("user_attr was not normalized: %+v", policy)
	}
	raw, err := marshalPermissions(role.Permissions)
	if err != nil {
		t.Fatalf("marshalPermissions: %v", err)
	}
	got := unmarshalPermissions([]byte(raw))
	policy, ok = got.RowAccess.Policy("catalog", "Контрагенты", "read")
	if !ok {
		t.Fatal("json row policy not restored")
	}
	if policy.Field != "Ответственный" || policy.Value.User != "login" {
		t.Fatalf("json policy = %+v", policy)
	}
}

func TestRowAccessInvalidSameAsStillCountsAsPolicy(t *testing.T) {
	ra := RowAccess{Catalogs: map[string]RowPolicies{
		"Контрагенты": {
			"read":  {SameAs: "write"},
			"write": {SameAs: "read"},
		},
	}}
	policy, ok := ra.Policy("catalog", "Контрагенты", "read")
	if !ok {
		t.Fatal("объявленная циклическая policy должна считаться policy для fail-closed")
	}
	if policy.Field != "" || policy.SameAs != "" {
		t.Fatalf("invalid policy marker = %+v", policy)
	}

	ra = RowAccess{Catalogs: map[string]RowPolicies{
		"Контрагенты": {"write": {SameAs: "missing"}},
	}}
	if _, ok := ra.Policy("catalog", "Контрагенты", "write"); !ok {
		t.Fatal("same_as на отсутствующую операцию должен считаться policy для fail-closed")
	}
}

func TestUserCardRoleStoragePreservesShorthand(t *testing.T) {
	role, err := ParseRole([]byte(`name: Employee
permissions:
  documents: {Order: [read]}
  row_access:
    documents:
      Order:
        read: {field: Branch, op: eq, value: {user_card: Employees.Branch}}
`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := marshalPermissions(role.Permissions)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := unmarshalPermissions([]byte(raw)).RowAccess.Policy("document", "Order", "read")
	if !ok || p.Value.UserCard == nil || !p.Value.UserCard.Shorthand {
		t.Fatalf("shorthand lost in role storage: %s", raw)
	}
}
