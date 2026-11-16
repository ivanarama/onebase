package api

import (
	"errors"
	"net/http"

	"github.com/ivantit66/onebase/internal/access"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/query"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/resultfilter"
)

func (h *handler) reportResultFilterSettings(r *http.Request, rep *reportpkg.Report, res query.Result) (*reportpkg.UserReportSettings, *resultfilter.Plan, error) {
	user := auth.UserFromContext(r.Context())
	login := ""
	if user != nil {
		login = user.Login
	}
	qs := r.URL.Query()
	_, hasPreset := qs["__preset"]
	loaded, err := resultfilter.ResolveSettings(r.Context(), h.store, rep.Name, login, qs.Get("__settings"), qs.Get("__preset"), hasPreset)
	if err != nil {
		return nil, nil, err
	}
	fields := resultfilter.Schema(res, resultfilter.RegistryLookup(h.reg), resultfilter.ReportAllowed(user))
	var filters []reportpkg.Filter
	if loaded.Settings != nil {
		filters = loaded.Settings.Filters
	}
	plan, err := resultfilter.New(fields, filters, resultfilter.RegistryEnums(h.reg))
	return loaded.Settings, plan, err
}
func (h *handler) reportResultOutputMask(r *http.Request, res query.Result) access.QueryMaskPlan {
	return access.ReportOutputQueryMaskPlan(auth.UserFromContext(r.Context()), res, resultfilter.RegistryLookup(h.reg))
}
func (h *handler) runReportResultFilteredV2(w http.ResponseWriter, r *http.Request, rep *reportpkg.Report, res *query.Result, mask access.QueryMaskPlan, settings *reportpkg.UserReportSettings, plan *resultfilter.Plan, page, limit int) {
	composed := wantsReportComposition(r.URL.Query())
	offset, ok := reportPageOffset(page, limit)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid page", "", 0)
		return
	}
	opts := resultfilter.Options{MaxRows: limit, Offset: offset, CountAll: true}
	if composed {
		opts.MaxRows = 0
		opts.Offset = 0
	}
	result, err := resultfilter.Run(r.Context(), h.store, res, plan, opts)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, resultfilter.ErrScanLimit) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, err.Error(), "", 0)
		return
	}
	if err := mask.Apply(result.Rows); err != nil {
		writeError(w, http.StatusForbidden, "masked field: "+err.Error(), "", 0)
		return
	}
	if composed {
		variant := r.URL.Query().Get("variant")
		if variant == "" {
			variant = r.URL.Query().Get("__variant")
		}
		if variant == "" && settings != nil {
			variant = settings.Variant
		}
		comp := rep.ActiveComposition(variant)
		if comp == nil {
			writeError(w, http.StatusBadRequest, "report has no composition", "", 0)
			return
		}
		kind, data, err := h.composeReportV2(result.Rows, comp)
		if err != nil {
			writeError(w, http.StatusBadRequest, "report composition error: "+err.Error(), "", 0)
			return
		}
		writeJSONV2(w, http.StatusOK, restV2Envelope{Data: data, Meta: &restV2Meta{Total: result.Total, Page: 1, Limit: result.Total, TotalPages: totalPages(result.Total, result.Total), Columns: result.Columns, Composed: true, Variant: variant, Kind: kind}})
		return
	}
	writeJSONV2(w, http.StatusOK, restV2Envelope{Data: result.Rows, Meta: &restV2Meta{Total: result.Total, Page: page, Limit: limit, TotalPages: totalPages(result.Total, limit), Columns: result.Columns, Truncated: result.Truncated}})
}
