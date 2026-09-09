package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/project"
)

// CheckLintReports validates advisory report settings that require the loaded
// project model. A literal default for a select parameter must name one of its
// options; otherwise the report form opens without any selected value.
func CheckLintReports(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}

	var issues []Issue
	for _, rep := range proj.Reports {
		if rep == nil {
			continue
		}
		for _, param := range rep.Params {
			defaultValue := strings.TrimSpace(param.Default)
			if param.Type != "select" || defaultValue == "" || isReportParamDefaultTemplate(defaultValue) {
				continue
			}

			found := false
			for _, option := range param.Options {
				if option == defaultValue {
					found = true
					break
				}
			}
			if found {
				continue
			}

			issues = append(issues, Issue{
				File:         "reports/" + rep.Name + ".yaml",
				Object:       rep.Name,
				Kind:         "Отчёт",
				Code:         "report.select-default-not-in-options",
				Message:      fmt.Sprintf("параметр %q имеет default %q, которого нет в options", param.Name, defaultValue),
				SuggestedFix: "Укажите в default одно из значений options либо добавьте нужное значение в options.",
			})
		}
	}
	return issues
}

func isReportParamDefaultTemplate(value string) bool {
	return strings.HasPrefix(value, "{{") && strings.HasSuffix(value, "}}")
}
