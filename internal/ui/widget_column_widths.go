package ui

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/widget"
)

func widgetWidthsUserID(r *http.Request) string {
	if u := auth.UserFromContext(r.Context()); u != nil {
		return u.ID
	}
	return "open-access"
}

func (s *Server) prepareWidgetColumnWidths(r *http.Request, res *widget.Result) {
	if res == nil || res.Type != "list" || s.store == nil {
		return
	}
	res.WidthsURL = "/ui/_widget/" + url.PathEscape(res.Name) + "/widths"
	if sub := r.URL.Query().Get("subsystem"); sub != "" {
		res.WidthsURL += "?subsystem=" + url.QueryEscape(sub)
	}
	widths := s.store.GetWidgetColumnWidths(r.Context(), res.Name, widgetWidthsUserID(r))
	res.ColumnWidths = make(map[string]int)
	res.TableWidth = 0
	for _, c := range res.Columns {
		if px := widths[c.Field]; px >= 40 && px <= 1200 {
			res.ColumnWidths[c.Field] = px
		}
	}
	if len(res.ColumnWidths) > 0 {
		for _, c := range res.Columns {
			px := res.ColumnWidths[c.Field]
			if px == 0 {
				px = 160
				res.ColumnWidths[c.Field] = px
			}
			res.TableWidth += px
		}
	}
}

// Same dashboard/side-panel identity and access boundary as widgetPartial.
// The setting is presentation only and cannot change a query or source.
func (s *Server) widgetColumnWidthsSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	if !s.requireSubsystemVisible(w, r) {
		return
	}
	meta := s.dashboardWidget(r, chi.URLParam(r, "name"))
	if meta == nil || meta.Type != metadata.WidgetTypeList {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid widths", 400)
		return
	}
	var widths map[string]int
	if err := json.Unmarshal([]byte(r.FormValue("widths")), &widths); err != nil || len(widths) > 200 {
		http.Error(w, "invalid widths", 400)
		return
	}
	runner := widget.New(s.reg, s.store)
	runner.User = auth.UserFromContext(r.Context())
	runner.Cache = s.widgetCache
	if runner.User != nil {
		runner.CurrentUser = runner.User.Login
	}
	res := runner.Run(r.Context(), meta)
	if res.AccessDenied {
		http.Error(w, "forbidden", 403)
		return
	}
	if res.Error != "" {
		http.Error(w, "widget unavailable", 400)
		return
	}
	allowed := make(map[string]bool, len(res.Columns))
	for _, c := range res.Columns {
		allowed[c.Field] = true
	}
	for field, px := range widths {
		if !allowed[field] || px < 40 || px > 1200 {
			http.Error(w, "invalid widths", 400)
			return
		}
	}
	if err := s.store.SaveWidgetColumnWidths(r.Context(), meta.Name, widgetWidthsUserID(r), widths); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
