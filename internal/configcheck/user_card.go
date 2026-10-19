package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/project"
)

// A new database-backed authorization source must be statically valid in the
// normal check, independently of advisory lint for historical row policies.
func CheckUserCardPolicies(dir string, proj *project.Project, roles []*auth.Role) []Issue {
	var issues []Issue
	targets := rowAccessLintTargets(proj)
	files := roleFileLabels(dir)
	for _, role := range roles {
		if role == nil {
			continue
		}
		sections := map[string]map[string]auth.RowPolicies{"catalog": role.Permissions.RowAccess.Catalogs, "document": role.Permissions.RowAccess.Documents, "register": role.Permissions.RowAccess.Registers, "inforeg": role.Permissions.RowAccess.InfoRegs}
		for kind, objects := range sections {
			for object, policies := range objects {
				for op, raw := range policies {
					resolved, _ := policies.Resolve(op)
					if !auth.RowPolicyHasUserCard(resolved) {
						continue
					}
					target, ok := targets[rowAccessTargetKey(kind, object)]
					var err error
					if !ok {
						err = fmt.Errorf("unknown target %s.%s", kind, object)
					} else {
						err = validateRoleRowPolicy(policies, op, raw, target.meta, projEntityLookup(proj))
					}
					if err != nil {
						issues = append(issues, Issue{File: files[strings.ToLower(role.Name)], Object: role.Name, Kind: "Роль", Code: "rls.invalid-user-card", Message: fmt.Sprintf("user_card policy %s.%s.%s: %v", kind, object, op, err), SuggestedFix: "Исправьте источник user_card, типы полей и оператор; разрешён скалярный eq/ne."})
					}
				}
			}
		}
	}
	return issues
}
