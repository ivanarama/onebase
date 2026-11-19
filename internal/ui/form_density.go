package ui

import "github.com/ivantit66/onebase/internal/metadata"

// The config was already resolved for this request's roles. Defaults apply to
// object/processor forms only, and an explicit form density always wins.
func prepareDefaultFormDensity(page string, data map[string]any) {
	delete(data, "DefaultFormDensity")
	delete(data, "AutoFormDensity")
	switch page {
	case "page-form", "page-managed-form", "page-processor":
	default:
		return
	}
	cfg, ok := data["Cfg"].(Config)
	if !ok || (cfg.DefaultFormDensity != metadata.FormDensityCompact && cfg.DefaultFormDensity != metadata.FormDensityDense) {
		return
	}
	form, _ := data["Form"].(*metadata.FormModule)
	if form != nil && form.Density != "" {
		return
	}
	data["DefaultFormDensity"] = cfg.DefaultFormDensity
	if form == nil {
		data["AutoFormDensity"] = true
	}
}
