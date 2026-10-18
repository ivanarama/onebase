package configcheck

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func CheckFormEntityLists(p *project.Project) []Issue {
	var issues []Issue
	for _, e := range p.Entities {
		for _, f := range e.Forms {
			if err := metadata.ValidateFormEntityLists(e, f, p.Entities); err != nil {
				issues = append(issues, Issue{File: formFileLabel(e, f), Object: e.Name, Code: "form.entity-list", Message: err.Error()})
			}
		}
	}
	for _, p := range p.Processors {
		for _, f := range p.Forms {
			f.Walk(func(el *metadata.FormElement) bool {
				if el.Kind == metadata.FormElementEntityList {
					issues = append(issues, Issue{Code: "form.entity-list", Message: "entity list requires document/catalog object form"})
				}
				return true
			})
		}
	}
	return issues
}
