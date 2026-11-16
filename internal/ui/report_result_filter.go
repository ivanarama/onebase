package ui

import (
	"context"
	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
	"net/http"
)

func (s *Server) reportResultFilterFields(ctx context.Context, res query.Result) []resultfilter.Field {
	fields := resultfilter.Schema(res, resultfilter.RegistryLookup(s.reg), resultfilter.ReportAllowed(auth.UserFromContext(ctx)))
	for i := range fields {
		if fields[i].EnumName != "" {
			fields[i].Values = resultfilter.RegistryEnums(s.reg)(fields[i].EnumName)
		}
	}
	return fields
}
func (s *Server) reportResultFilterPlan(ctx context.Context, res query.Result, settings *reportpkg.UserReportSettings) (*resultfilter.Plan, error) {
	var filters []reportpkg.Filter
	if settings != nil {
		filters = settings.Filters
	}
	return resultfilter.New(s.reportResultFilterFields(ctx, res), filters, resultfilter.RegistryEnums(s.reg))
}
func (s *Server) reportResultOutputMask(ctx context.Context, res query.Result) access.QueryMaskPlan {
	return access.ReportOutputQueryMaskPlan(auth.UserFromContext(ctx), res, resultfilter.RegistryLookup(s.reg))
}
func (s *Server) runReportResultRows(ctx context.Context, res *query.Result, settings *reportpkg.UserReportSettings, maxRows int) ([]map[string]any, []string, bool, error) {
	plan, err := s.reportResultFilterPlan(ctx, *res, settings)
	if err != nil {
		return nil, nil, false, err
	}
	if plan.Empty() {
		return query.RunLimit(ctx, s.store, res, maxRows)
	}
	result, err := resultfilter.Run(ctx, s.store, res, plan, resultfilter.Options{MaxRows: maxRows})
	return result.Rows, result.Columns, result.Truncated, err
}

// addReportResultFilterData only publishes proven and currently permitted
// descriptors. Error responses must not echo obsolete protected preset values.
func (s *Server) addReportResultFilterData(r *http.Request, data map[string]any) {
	rep, ok := data["Report"].(*reportpkg.Report)
	if !ok || rep == nil || rep.Query == "" {
		return
	}
	values, _ := data["ParamValues"].(map[string]any)
	res, err := s.compileQueryWithRowAccess(r.Context(), rep.Query, values)
	if err != nil {
		return
	}
	if s.deniedQuerySource(r.Context(), res.Sources) != "" {
		return
	}
	data["ReportFilterFields"] = s.reportResultFilterFields(r.Context(), res)
	data["ReportHasComposition"] = effectiveComposition(rep, nil) != nil
	if data["ReportSettingsJSON"] == nil {
		data["ReportSettingsJSON"] = reportSettingsPanelJSON(rep, nil)
	}
}
func (s *Server) validateReportResultSettings(r *http.Request, rep *reportpkg.Report, settings *reportpkg.UserReportSettings) error {
	if settings == nil || len(settings.Filters) == 0 {
		return nil
	}
	values, err := reportParamValuesFromRequest(r, rep, s.constantResolver(r.Context()))
	if err != nil {
		return err
	}
	res, err := s.compileQueryWithRowAccess(r.Context(), rep.Query, values)
	if err != nil {
		return err
	}
	_, err = s.reportResultFilterPlan(r.Context(), res, settings)
	return err
}
