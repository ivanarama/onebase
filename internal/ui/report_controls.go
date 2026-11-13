package ui

import "github.com/ivantit66/onebase/internal/widget"

func prepareReportControls(page string, data map[string]any) {
	delete(data, "UseShortDateInput")
	delete(data, "CompactReport")
	delete(data, "ReportDensity")
	cfg, ok := data["Cfg"].(Config)
	if ok && cfg.ShortDateInput {
		switch page {
		case "page-index", "page-app-shell":
			data["UseShortDateInput"] = presentedDateWidget(data)
		case "page-form", "page-managed-form", "page-processor", "page-report", "page-list", "page-inforeg-list", "page-inforeg-form", "page-register-movements", "page-register-balances", "page-accountreg-movements", "page-accountreg-balances", "page-journal", "page-constants", "page-custom":
			data["UseShortDateInput"] = true
		}
	}
	if page != "page-report" {
		return
	}
	if !ok || (cfg.DefaultReportDensity != "compact" && cfg.DefaultReportDensity != "dense") {
		return
	}
	data["CompactReport"] = true
	data["ReportDensity"] = cfg.DefaultReportDensity
}

// Reuse already presented controls. The shell with no date filters performs
// no date-module request and no metadata/database lookup for this feature.
func presentedDateWidget(data map[string]any) bool {
	hasDate := func(card widget.Result) bool {
		for _, filter := range card.Filters {
			if filter.Kind == "date" {
				return true
			}
		}
		return false
	}
	if rows, ok := data["WidgetRows"].([][]widget.Result); ok {
		for _, row := range rows {
			for _, card := range row {
				if hasDate(card) {
					return true
				}
			}
		}
	}
	if cards, ok := data["WidgetResults"].([]widget.Result); ok {
		for _, card := range cards {
			if hasDate(card) {
				return true
			}
		}
	}
	for _, name := range []string{"Home", "SidePanel"} {
		if nested, ok := data[name].(map[string]any); ok {
			if card, ok := nested["Card"].(widget.Result); ok && hasDate(card) {
				return true
			}
			if presentedDateWidget(nested) {
				return true
			}
		}
	}
	return false
}
