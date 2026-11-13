package cli

import (
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/ui"
)

// Both native launch modes must resolve the same app.yaml presentation keys.
// Per-user scopes are still evaluated by UI for each request.
func applyAppUIPresentation(dst *ui.Config, app *project.AppConfig) {
	if app == nil {
		app = &project.AppConfig{}
	}
	dst.CompactLists = app.CompactLists()
	dst.HideListDetailPanel = !app.ListDetailPanelEnabled()
	dst.HideListRowActions = !app.ListRowActionsEnabled()
	dst.HideAttachments = !app.AttachmentsEnabled()
	dst.UIEnhancements = app.UIEnhancementsConfigured()
	settings := app.UI
	if settings == nil {
		settings = &project.UIConfig{}
	}
	dst.UIRoles = settings.Roles
	dst.UIExceptRoles = settings.ExceptRoles
	dst.UIFormRoles = settings.FormRoles
	dst.UIFormExceptRoles = settings.FormExceptRoles
	dst.DefaultFormDensity = settings.FormDensity
	dst.UIReportRoles = settings.ReportRoles
	dst.UIReportExceptRoles = settings.ReportExceptRoles
	dst.DefaultReportDensity = settings.ReportDensity
	dst.ShortDateInput = settings.ShortDateInput
}
