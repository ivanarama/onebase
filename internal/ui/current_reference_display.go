package ui

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

// retainCurrentObjectRefOptions is ONLY called with the masked persisted values
// of an already authorized formEdit. A current reference is part of that object,
// even when its target is outside the selectable catalog/RLS/choice page.
// This display-only projection does not accept request IDs, grant catalog read,
// or add candidates to picker/search endpoints. Never use it for processor or
// arbitrary submitted values. Field masking still applies to the target label.
func (s *Server) retainCurrentObjectRefOptions(ctx context.Context, entity *metadata.Entity, values map[string]string, options map[string][]map[string]any) (map[string][]map[string]any, error) {
	if options == nil {
		options = make(map[string][]map[string]any)
	}
	var placed map[string]bool
	if form := pickManagedForm(entity, "object"); form != nil {
		placed = map[string]bool{}
		form.Walk(func(el *metadata.FormElement) bool {
			if el != nil {
				for _, part := range strings.Split(el.DataPath, ".") {
					if part = strings.TrimSpace(part); part != "" {
						placed[strings.ToLower(part)] = true
					}
				}
			}
			return true
		})
	}
	for _, field := range entity.Fields {
		if field.RefEntity == "" || metadata.IsSystemRefTarget(field.RefEntity) || (placed != nil && !placed[strings.ToLower(field.Name)]) {
			continue
		}
		selected := strings.TrimSpace(values[field.Name])
		id, err := uuid.Parse(selected)
		if err != nil || id == uuid.Nil || len(selectedRefOptionsOnly(options[field.Name], selected)) != 0 {
			continue
		}
		target := s.reg.GetEntity(field.RefEntity)
		if target == nil {
			continue
		}
		// Read only presentation candidates; no whole target row enters the form.
		fields := displayField(target)
		rows, err := s.store.GetFieldsByIDs(ctx, target, []uuid.UUID{id}, fields)
		if err != nil {
			return nil, err
		}
		label := selected // retain even a dangling stored ID; never silently clear it.
		if row := rows[id.String()]; row != nil {
			label = s.maskedCurrentReferenceLabel(ctx, target, row)
		}
		options[field.Name] = append(options[field.Name], map[string]any{"id": selected, "_label": label, "_choice_outside_filter": true})
	}
	return options, nil
}

func (s *Server) maskedCurrentReferenceLabel(ctx context.Context, target *metadata.Entity, row map[string]any) string {
	access.MaskRecord(s.currentReferenceDisplayDecisions(ctx, target), row)
	return firstStringField(row, target)
}

// FieldDecisions normally assumes that object read was already authorized and
// ignores non-reading roles. For the bound label projection, consult explicit
// label policies even when none of the user's roles grants catalog read. The
// temporary roles are inputs ONLY to the pure masking calculation; they are
// never installed in a request, interpreter or storage context.
func (s *Server) currentReferenceDisplayDecisions(ctx context.Context, target *metadata.Entity) map[string]access.FieldDecision {
	user := auth.UserFromContext(ctx)
	if user == nil || (user.IsAdmin && !access.MaskAdmin()) {
		return s.fieldDecisions(ctx, target)
	}
	kind := string(target.Kind)
	for _, role := range user.Roles {
		if role != nil && auth.PermissionHas(role.Permissions, kind, target.Name, "read") {
			return s.fieldDecisions(ctx, target)
		}
	}
	viewer := *user
	viewer.Roles = nil
	for _, role := range user.Roles {
		if role == nil || len(role.Permissions.FieldAccess.Policies(kind, target.Name)) == 0 {
			continue
		}
		viewer.Roles = append(viewer.Roles, &auth.Role{Permissions: auth.Permission{FieldAccess: role.Permissions.FieldAccess}})
	}
	if len(viewer.Roles) == 0 {
		viewer.Roles = []*auth.Role{{}}
	}
	for _, role := range viewer.Roles {
		if target.Kind == metadata.KindDocument {
			role.Permissions.Documents = map[string][]string{target.Name: {"read"}}
		} else {
			role.Permissions.Catalogs = map[string][]string{target.Name: {"read"}}
		}
	}
	return access.FieldDecisions(&viewer, kind, target.Name, target)
}
