package configcheck

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormConceal rejects unsupported conceal contracts before runtime.
func CheckFormConceal(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var issues []Issue
	check := func(owner, file string, entity *metadata.Entity, form *metadata.FormModule) {
		if _, err := metadata.FormConcealRules(entity, form); err != nil {
			issues = append(issues, Issue{File: file, Object: owner, Kind: "Управляемая форма", Code: "form.conceal", Message: err.Error()})
		}
	}
	for _, entity := range proj.Entities {
		if entity == nil {
			continue
		}
		for _, form := range entity.Forms {
			check(entity.Name, formFileLabel(entity, form), entity, form)
		}
	}
	for _, proc := range proj.Processors {
		if proc == nil {
			continue
		}
		for _, form := range proc.Forms {
			check(proc.Name, procFormFileLabel(proc.Name, form), nil, form)
		}
	}
	return issues
}
