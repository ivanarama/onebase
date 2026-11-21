package configcheck

import (
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
)

func CheckReportContext(proj *project.Project) []Issue {
	var issues []Issue
	for _, rep := range proj.Reports {
		if rep == nil {
			continue
		}
		hasContext := false
		for _, p := range rep.Params {
			hasContext = hasContext || p.Context != nil
		}
		if !hasContext {
			continue
		}
		if err := report.ValidateContextParams(rep, projEntityLookup(proj)); err != nil {
			issues = append(issues, Issue{File: "reports/" + rep.Name + ".yaml", Object: rep.Name, Kind: "Отчёт", Code: "report.invalid-context", Message: err.Error()})
		}
	}
	return issues
}
