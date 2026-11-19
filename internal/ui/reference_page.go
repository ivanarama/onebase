package ui

import (
	"context"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// Processor forms keep their existing text fallback unless a form attribute
// explicitly requests reference_page navigation. Load only those attributes,
// using the same permission, selected-value and collision checks as entity forms.
func (s *Server) mergeProcessorReferencePageOptions(ctx context.Context, form *metadata.FormModule, data map[string]any) {
	requested := map[string]bool{}
	form.Walk(func(el *metadata.FormElement) bool {
		if el.Kind == metadata.FormElementField && el.ReferencePage != "" {
			name := el.DataPath
			if i := strings.LastIndex(name, "."); i >= 0 {
				name = name[i+1:]
			}
			requested[name] = true
		}
		return true
	})
	scoped := &metadata.FormModule{}
	for _, attr := range form.Attributes {
		if attr != nil && requested[attr.Name] {
			scoped.Attributes = append(scoped.Attributes, attr)
		}
	}
	s.mergeFormLocalRefOptions(ctx, scoped, data)
}
