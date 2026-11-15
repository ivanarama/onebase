package ui

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/report"
)

type reportDrilldownRenderer struct {
	rep      *report.Report
	rowTitle string
}

func (d *reportDrilldownRenderer) HasRows() bool {
	if d == nil {
		return false
	}
	for _, a := range d.rep.Drilldowns {
		if a.Field == "" {
			return true
		}
	}
	return false
}
func (d *reportDrilldownRenderer) RowTitle() string {
	if d == nil {
		return ""
	}
	return d.rowTitle
}

func reportDrilldownValue(row map[string]any, field string) string {
	for k, v := range row {
		if strings.EqualFold(k, field) {
			if b, ok := v.([]byte); ok {
				return string(b)
			}
			if v != nil {
				return fmt.Sprint(v)
			}
			return ""
		}
	}
	return ""
}

func (d *reportDrilldownRenderer) link(index int, a report.Drilldown, row map[string]any, label string) string {
	id, err := uuid.Parse(reportDrilldownValue(row, a.IDField))
	if err != nil || id == uuid.Nil {
		return html.EscapeString(label)
	}
	typ := reportDrilldownValue(row, a.TypeField)
	if _, ok := a.SelectTarget(typ); !ok {
		return html.EscapeString(label)
	}
	q := url.Values{"d": {strconv.Itoa(index)}, "id": {id.String()}}
	if a.TypeField != "" {
		q.Set("type", typ)
	}
	href := "/ui/report/" + url.PathEscape(d.rep.Name) + "/drilldown?" + q.Encode()
	return `<a class="report-drilldown" href="` + html.EscapeString(href) + `">` + html.EscapeString(label) + `</a>`
}

func (d *reportDrilldownRenderer) Cell(field string, row map[string]any, label string) template.HTML {
	if d != nil {
		for i, a := range d.rep.Drilldowns {
			if a.Field != "" && strings.EqualFold(a.Field, field) {
				return template.HTML(d.link(i, a, row, label))
			}
		}
	} //nolint:gosec // Generated internal route and escaped label.
	return template.HTML(html.EscapeString(label)) //nolint:gosec // Escaped text.
}
func (d *reportDrilldownRenderer) Row(row map[string]any) template.HTML {
	var links []string
	if d != nil {
		for i, a := range d.rep.Drilldowns {
			if a.Field == "" {
				links = append(links, d.link(i, a, row, a.Title))
			}
		}
	}
	return template.HTML(strings.Join(links, " · ")) //nolint:gosec // Each link/label escaped by link.
}

func reportDrilldownSourceFields(rep *report.Report) []string {
	var fields []string
	for _, a := range rep.Drilldowns {
		fields = append(fields, a.IDField)
		if a.TypeField != "" {
			fields = append(fields, a.TypeField)
		}
	}
	return fields
}

func reportColumnSkipped(col string, fields []string) bool {
	for _, field := range fields {
		if field != "" && strings.EqualFold(col, field) {
			return true
		}
	}
	return false
}

// reportDrilldown revalidates metadata and live target rights. IDs/selectors
// are not capabilities. No arbitrary URL, write or callback is executed.
func (s *Server) reportDrilldown(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	if err := report.ValidateDrilldowns(rep, s.reg); err != nil {
		http.NotFound(w, r)
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("d"))
	if err != nil || index < 0 || index >= len(rep.Drilldowns) {
		http.NotFound(w, r)
		return
	}
	target, ok := rep.Drilldowns[index].SelectTarget(r.URL.Query().Get("type"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id, err := uuid.Parse(r.URL.Query().Get("id"))
	if err != nil || id == uuid.Nil {
		http.NotFound(w, r)
		return
	}
	href := ""
	switch target.Kind {
	case "entity":
		e := s.reg.GetEntity(target.Entity)
		if e == nil || !s.can(r, string(e.Kind), e.Name, "read") || !s.rowAllowsID(r.Context(), e, "read", id) {
			http.NotFound(w, r)
			return
		}
		href = "/ui/" + string(e.Kind) + "/" + url.PathEscape(e.Name) + "/" + id.String()
	case "image":
		blob, rc, err := s.store.OpenBlob(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		closeRead("картинку расшифровки", rc)
		if blob.OwnerEntity == "" || !s.blobAllowed(r, blob) {
			http.NotFound(w, r)
			return
		}
		href = "/ui/_image/" + id.String()
	case "attachment":
		att, err := s.store.GetAttachment(r.Context(), id)
		if err != nil || (!s.rowAllowsOwnerID(r, att.OwnerKind, att.OwnerName, "read", att.OwnerID) && !s.uploaderPreviewAllowed(r, att)) {
			http.NotFound(w, r)
			return
		}
		href = "/ui/attachments/" + id.String() + "/download"
	default:
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, href, http.StatusSeeOther)
}
