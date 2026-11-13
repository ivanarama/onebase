package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/widget"
)

func widgetCellFilterKey(w *metadata.Widget) string {
	return "c." + base64.RawURLEncoding.EncodeToString([]byte(w.Name))
}

func (s *Server) parseWidgetCellFilter(r *http.Request, w *metadata.Widget) (*widget.CellFilter, error) {
	if !s.uiEnhancementsAllowed(r) {
		return nil, nil
	}
	values, present := r.URL.Query()[widgetCellFilterKey(w)]
	if !present {
		return nil, nil
	}
	if w.Type != metadata.WidgetTypeList || len(values) != 1 {
		return nil, widget.ErrCellFilter
	}
	if values[0] == "" {
		return nil, nil
	}
	if len(values[0]) > 16384 || !json.Valid([]byte(values[0])) {
		return nil, widget.ErrCellFilter
	}
	var filter widget.CellFilter
	decoder := json.NewDecoder(strings.NewReader(values[0]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&filter); err != nil || filter.Field == "" || len(filter.Field) > 512 {
		return nil, widget.ErrCellFilter
	}
	var value any
	if err := json.Unmarshal([]byte(filter.Value), &value); err != nil || len(filter.Value) > 8192 {
		return nil, widget.ErrCellFilter
	}
	switch value.(type) {
	case nil, bool, string, float64:
	default:
		return nil, widget.ErrCellFilter
	}
	return &filter, nil
}

func (s *Server) lenientWidgetCellFilter(r *http.Request, w *metadata.Widget) *widget.CellFilter {
	filter, _ := s.parseWidgetCellFilter(r, w)
	return filter
}

func (s *Server) prepareWidgetCellFilter(r *http.Request, w *metadata.Widget, res *widget.Result) {
	if w == nil || res.Type != "list" || !s.uiEnhancementsAllowed(r) {
		return
	}
	res.CellFilterKey = widgetCellFilterKey(w)
	if filter := s.lenientWidgetCellFilter(r, w); filter != nil {
		encoded, _ := json.Marshal(filter)
		res.CellFilterJSON = string(encoded)
	}
	lang := s.resolveLang(r)
	res.CellFilterLabel = s.tr(lang, "Отбор по значению текущей ячейки")
	res.CellFilterClearLabel = s.tr(lang, "Снять отбор")
}

func widgetCellAttrs(row map[string]any, field string) template.HTMLAttr {
	values, _ := row[widget.CellValuesKey].(map[string]string)
	value, exists := values[field]
	if !exists {
		return ""
	}
	return template.HTMLAttr(fmt.Sprintf(`data-ob-widget-cell="%s" data-ob-widget-cell-value="%s" tabindex="0"`, html.EscapeString(field), html.EscapeString(value))) //nolint:gosec // Fixed attribute names; metadata and scalar token are HTML-escaped.
}
