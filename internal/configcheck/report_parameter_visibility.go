package configcheck

import (
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
)

func CheckReportParamVisibility(proj *project.Project) []Issue {
	var issues []Issue
	for _, rep := range proj.Reports {
		if rep == nil {
			continue
		}
		if err := report.ValidateParamVisibility(rep); err != nil {
			issues = append(issues, Issue{File: "reports/" + rep.Name + ".yaml", Object: rep.Name, Kind: "Отчёт", Code: "report.param-visibility", Message: err.Error()})
		}
	}
	return issues
}
