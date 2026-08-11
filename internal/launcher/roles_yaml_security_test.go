package launcher

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"gopkg.in/yaml.v3"
)

func catalogRoleEdit(perms map[string][]string) []roleSectionEdit {
	return []roleSectionEdit{{
		kind: "catalog", key: "catalogs",
		managed: map[string]bool{"read": true, "write": true, "delete": true},
		perms:   perms,
	}}
}

func applyCatalogRoleEdit(t *testing.T, source string, perms map[string][]string) ([]byte, *auth.Role) {
	t.Helper()
	out, err := applyRoleMatrixToYAML([]byte(source), "Оператор", "", catalogRoleEdit(perms))
	if err != nil {
		t.Fatalf("applyRoleMatrixToYAML: %v", err)
	}
	role, err := auth.ParseRole(out)
	if err != nil {
		t.Fatalf("auth.ParseRole: %v\n%s", err, out)
	}
	return out, role
}

func TestRoleYAMLUpdatesNestedWrappersWithoutDroppingAliasSections(t *testing.T) {
	source := `name: Оператор
permissions:
  catalogs:
    Клиент: [read]
  policies:
    справочники:
      Клиент: [write, disclose] # право вне матрицы
      Удалённый: [read, disclose]
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{"клиент": {"read"}})
	if !auth.PermissionHas(role.Permissions, "catalog", "клиент", "read") ||
		auth.PermissionHas(role.Permissions, "catalog", "Клиент", "read") {
		t.Fatalf("отмеченное read потеряно: %v\n%s", role.Permissions.Catalogs, out)
	}
	if auth.PermissionHas(role.Permissions, "catalog", "Клиент", "write") ||
		auth.PermissionHas(role.Permissions, "catalog", "Удалённый", "read") {
		t.Fatalf("снятая операция осталась во вложенной/синонимичной секции: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !auth.PermissionHas(role.Permissions, "catalog", "Клиент", "disclose") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Удалённый", "disclose") {
		t.Fatalf("неуправляемая операция потеряна: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "право вне матрицы") || !strings.Contains(string(out), "policies:") {
		t.Fatalf("wrapper или комментарий потерян:\n%s", out)
	}
}

func TestRoleYAMLPreservesAliasedPermissionsAndDenyAllProcessors(t *testing.T) {
	source := `name: Оператор
shared_policy: &policy
  processors: {}
  row_access:
    documents:
      Задача:
        read:
          any:
            - {field: Исполнитель, op: eq, value: {user: login}}
  catalogs:
    Клиент: [read, disclose]
permissions: *policy
mirror: *policy
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{})
	if role.Permissions.Processors == nil {
		t.Fatalf("processors: {} превратился в nil/allow-all:\n%s", out)
	}
	if role.Permissions.RowAccess.IsZero() {
		t.Fatalf("row_access потерян через alias permissions:\n%s", out)
	}
	if auth.PermissionHas(role.Permissions, "catalog", "Клиент", "read") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Клиент", "disclose") {
		t.Fatalf("alias permissions обновлён неверно: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "&policy") || !strings.Contains(string(out), "mirror: *policy") ||
		!strings.Contains(string(out), "[read, disclose]") {
		t.Fatalf("общий anchor или независимый mirror были изменены:\n%s", out)
	}
}

func TestRoleYAMLPreservesProcessorNilVsEmptyAndComments(t *testing.T) {
	tests := []struct {
		name        string
		processors  string
		wantNonNil  bool
		wantComment bool
	}{
		{name: "omitted"},
		{name: "null", processors: "  processors: null # keep null processor policy\n", wantComment: true},
		{name: "empty", processors: "  processors: {} # keep deny-all processor policy\n", wantNonNil: true, wantComment: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "name: Operator\npermissions:\n" + tt.processors + "  catalogs:\n    Client: [read]\n"
			out, role := applyCatalogRoleEdit(t, source, map[string][]string{})
			if (role.Permissions.Processors != nil) != tt.wantNonNil {
				t.Fatalf("processor nil/empty semantics changed: %#v\n%s", role.Permissions.Processors, out)
			}
			if tt.wantComment && !strings.Contains(string(out), "keep ") {
				t.Fatalf("processor policy comment was lost:\n%s", out)
			}
		})
	}
}

func TestRoleYAMLExpandsOperationAliasLocally(t *testing.T) {
	source := `name: Оператор
operation_sets:
  standard: &ops [read, disclose] # общий набор
permissions:
  catalogs:
    Клиент: *ops
other_operations: *ops
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{})
	if auth.PermissionHas(role.Permissions, "catalog", "Клиент", "read") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Клиент", "disclose") {
		t.Fatalf("alias списка операций обновлён неверно: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "&ops") || !strings.Contains(string(out), "other_operations: *ops") ||
		!strings.Contains(string(out), "общий набор") || !strings.Contains(string(out), "[read, disclose]") {
		t.Fatalf("anchor или его комментарий потерян:\n%s", out)
	}
}

func TestRoleYAMLExpandsSectionAliasLocally(t *testing.T) {
	source := `name: Operator
shared_catalogs: &catalogs
  Client: [read, disclose]
permissions:
  catalogs: *catalogs
catalog_mirror: *catalogs
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{})
	if auth.PermissionHas(role.Permissions, "catalog", "Client", "read") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Client", "disclose") {
		t.Fatalf("section alias was updated incorrectly: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "catalog_mirror: *catalogs") ||
		!strings.Contains(string(out), "Client: [read, disclose]") {
		t.Fatalf("shared section target was changed:\n%s", out)
	}
}

func TestRoleYAMLRejectsAliasMaterializationThatActivatesHiddenRights(t *testing.T) {
	source := `name: Operator
shared_policy: &policy
  catalogs:
    Client: [read, disclose]
permissions:
  policies: *policy
`
	if _, err := applyRoleMatrixToYAML([]byte(source), "Operator", "", catalogRoleEdit(nil)); err == nil {
		t.Fatal("materializing a custom wrapper alias activated previously ignored unmanaged rights")
	}
}

func TestRoleYAMLRejectsChangingSharedAnchoredOperationList(t *testing.T) {
	source := `name: Оператор
permissions:
  catalogs:
    Клиент: &ops [read, disclose]
    Партнёр: *ops
`
	if _, err := applyRoleMatrixToYAML([]byte(source), "Оператор", "", catalogRoleEdit(map[string][]string{})); err == nil {
		t.Fatal("изменение общего anchored списка принято и могло затронуть второй объект")
	}
}

func TestRoleYAMLRejectsRemovingSharedAnchoredOperationList(t *testing.T) {
	source := `name: Operator
permissions:
  catalogs:
    Client: &ops [read]
operation_mirror: *ops
`
	if _, err := applyRoleMatrixToYAML([]byte(source), "Operator", "", catalogRoleEdit(nil)); err == nil {
		t.Fatal("removing an anchor definition with another consumer was accepted")
	}
}

func TestRoleYAMLPreservesOperationItemComment(t *testing.T) {
	source := `name: Оператор
permissions:
  catalogs:
    Клиент:
      - read # управляемое
      - disclose # объяснение disclose
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{})
	if auth.PermissionHas(role.Permissions, "catalog", "Клиент", "read") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Клиент", "disclose") {
		t.Fatalf("операции обновлены неверно: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "объяснение disclose") {
		t.Fatalf("комментарий элемента sequence потерян:\n%s", out)
	}
}

func TestRoleYAMLPreservesCommentedDuplicateSelectedOperation(t *testing.T) {
	source := `name: Operator
permissions:
  catalogs:
    Client:
      - read
      - read # keep duplicate explanation
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{"Client": {"read"}})
	if !auth.PermissionHas(role.Permissions, "catalog", "Client", "read") {
		t.Fatalf("selected operation was lost: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "keep duplicate explanation") {
		t.Fatalf("runtime-equivalent duplicate was rewritten and its comment lost:\n%s", out)
	}
}

func TestRoleYAMLPreservesSelectedOperationCommentInLaterSection(t *testing.T) {
	source := `name: Operator
permissions:
  catalogs:
    Client: [disclose]
  policies:
    справочники:
      Client:
        - read # keep selected operation here
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{"Client": {"read"}})
	if !auth.PermissionHas(role.Permissions, "catalog", "Client", "read") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Client", "disclose") {
		t.Fatalf("selected or unmanaged operation was lost: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "keep selected operation here") {
		t.Fatalf("comment on a selected operation in a later section was lost:\n%s", out)
	}
}

func TestRoleYAMLPreservesMixedOperationItemComment(t *testing.T) {
	source := `name: Operator
permissions:
  catalogs:
    Client:
      - read, disclose # keep this explanation
`
	out, role := applyCatalogRoleEdit(t, source, map[string][]string{})
	if auth.PermissionHas(role.Permissions, "catalog", "Client", "read") ||
		!auth.PermissionHas(role.Permissions, "catalog", "Client", "disclose") {
		t.Fatalf("operations were updated incorrectly: %v\n%s", role.Permissions.Catalogs, out)
	}
	if !strings.Contains(string(out), "keep this explanation") {
		t.Fatalf("comment attached to a surviving comma-separated operation was lost:\n%s", out)
	}
}

func TestRoleYAMLRejectsCaseAmbiguousEntities(t *testing.T) {
	source := `name: Operator
permissions:
  catalogs:
    Client: [read]
    client: [write]
`
	if _, err := applyRoleMatrixToYAML([]byte(source), "Operator", "", catalogRoleEdit(nil)); err == nil {
		t.Fatal("case-distinct runtime entities were silently conflated")
	}
}

func TestRoleYAMLRejectsUnsafeDocumentShapes(t *testing.T) {
	tests := map[string]string{
		"duplicate key": `name: Operator
name: Other
permissions: {}
`,
		"nested duplicate key": `name: Operator
permissions:
  catalogs:
    Client: [read]
    Client: [write]
`,
		"multiple documents": `name: Operator
permissions: {}
---
name: Other
`,
		"merge key": `name: Operator
defaults: &defaults {catalogs: {Client: [read]}}
permissions:
  <<: *defaults
`,
		"self alias cycle": `name: Operator
permissions: &policy
  policies: *policy
`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := applyRoleMatrixToYAML([]byte(source), "Operator", "", catalogRoleEdit(nil)); err == nil {
				t.Fatal("unsafe YAML was accepted")
			}
		})
	}
}

func TestRoleYAMLAliasCloneBudgetIsGlobal(t *testing.T) {
	target := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i < 100; i++ {
		target.Content = append(target.Content,
			roleScalarNode(fmt.Sprintf("key_%d", i)), roleScalarNode("value"))
	}
	index := &roleYAMLIndex{aliasUsers: map[*yaml.Node]int{}}
	failed := false
	for i := 0; i < 100; i++ {
		if _, err := roleDetachedClone(target, index); err != nil {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("alias fan-out bypassed the cumulative clone-node budget")
	}
}

func TestRoleYAMLRejectsSharedNameAnchor(t *testing.T) {
	source := `name: &role_name Operator
display: *role_name
permissions: {}
`
	if _, err := applyRoleMatrixToYAML([]byte(source), "Renamed", "", catalogRoleEdit(nil)); err == nil {
		t.Fatal("replacing a shared name anchor would leave or alter an unrelated alias")
	}
}

func TestRoleYAMLRejectsMalformedExistingRole(t *testing.T) {
	source := "name: Оператор\npermissions: [\n"
	if _, err := applyRoleMatrixToYAML([]byte(source), "Оператор", "", catalogRoleEdit(nil)); err == nil {
		t.Fatal("повреждённая существующая роль была молча перезаписана")
	}
}
