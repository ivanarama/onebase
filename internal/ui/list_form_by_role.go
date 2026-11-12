package ui

import (
	"context"
	"github.com/ivantit66/onebase/internal/metadata"
	"strings"
)

// A request-local metadata view lets existing column/tile/detail helpers use
// the same selected list form as filtering and row styling, without mutating
// the registry or changing any object form. This view is never persisted.
func entityWithUserListForm(ctx context.Context, e *metadata.Entity) *metadata.Entity {
	if e == nil {
		return nil
	}
	selected := pickManagedFormCtx(ctx, e, "list")
	if selected == nil || (len(selected.Roles) == 0 && len(selected.ExceptRoles) == 0) {
		return e
	}
	view := *e
	form := *selected
	form.Roles = nil
	form.ExceptRoles = nil
	view.Forms = make([]*metadata.FormModule, 0, len(e.Forms))
	added := false
	for _, f := range e.Forms {
		if f != nil && strings.EqualFold(f.Kind, "list") {
			if !added {
				view.Forms = append(view.Forms, &form)
				added = true
			}
			continue
		}
		view.Forms = append(view.Forms, f)
	}
	return &view
}
