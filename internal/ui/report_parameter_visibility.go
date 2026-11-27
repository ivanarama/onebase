package ui

import (
	"encoding/json"

	reportpkg "github.com/ivantit66/onebase/internal/report"
)

func (p reportParamUI) VisibilityJSON() string {
	if p.VisibleWhen == nil {
		return ""
	}
	encoded, _ := json.Marshal(p.VisibleWhen)
	return string(encoded)
}

func (p reportParamUI) Checked(values map[string]any) bool {
	return reportpkg.ParamBoolValue(values[p.Name])
}
