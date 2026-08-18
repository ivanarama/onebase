package ui

// Экспорт списков в Excel.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ivantit66/onebase/internal/excel"
)

// listExcel exports an entity list (with current filters) as XLSX.
func (s *Server) listExcel(w http.ResponseWriter, r *http.Request) {
	entity := s.getEntity(w, r)
	if entity == nil {
		return
	}
	if !s.requirePerm(w, r, string(entity.Kind), entity.Name, "read") {
		return
	}
	params := parseListParams(r, entity, s.store.GetListPageSize(r.Context()))
	var ok bool
	params, ok = s.applyRowFilter(w, r, entity, "read", params)
	if !ok {
		return
	}
	rows, err := s.store.List(r.Context(), entity.Name, entity, params)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// План 88: маска ПДн применяется до выгрузки в Excel.
	s.maskRecords(r.Context(), entity, rows)
	s.resolveRefs(r.Context(), entity, rows)

	cols := make([]string, 0, len(entity.Fields))
	for _, f := range entity.Fields {
		cols = append(cols, f.Name)
	}

	xlsRows := make([][]any, len(rows))
	for i, row := range rows {
		cells := make([]any, len(cols))
		for j, col := range cols {
			cells[j] = row[col]
		}
		xlsRows[i] = cells
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", contentDisposition(entity.Name+".xlsx"))
	// Stream the workbook straight to the client instead of buffering the whole
	// file in memory (план 111, P2-3). StreamWriter errors surface before any
	// bytes reach w, so a failure here still yields a clean 500 rather than a
	// truncated download.
	if err := excel.WriteList(w, cols, xlsRows); err != nil {
		w.Header().Del("Content-Disposition")
		http.Error(w, "Excel error: "+s.errText(r, err), 500)
		return
	}
}

// contentDisposition собирает заголовок Content-Disposition по RFC 6266:
// ASCII-фолбэк в filename= (для старых клиентов) и полное имя в
// filename*=UTF-8” (issue #46 — сырой UTF-8 в quoted-string браузеры
// декодируют как latin-1, имя файла превращается в кракозябры).
func contentDisposition(filename string) string {
	return dispositionHeader("attachment", filename)
}

// dispositionHeader строит Content-Disposition указанного вида (attachment или
// inline) с ASCII-fallback и filename*=UTF-8” для не-ASCII имён.
func dispositionHeader(disposition, filename string) string {
	fallback := make([]rune, 0, len(filename))
	for _, r := range sanitizeFilename(filename) {
		if r < 0x80 {
			fallback = append(fallback, r)
		} else {
			fallback = append(fallback, '_')
		}
	}
	return disposition + "; filename=\"" + string(fallback) + "\"; filename*=UTF-8''" + encodeRFC5987(filename)
}

// encodeRFC5987 кодирует строку для filename*=UTF-8” — percent-кодируется
// всё, кроме attr-char по RFC 5987 (url.PathEscape оставляет «=», «@», «:»,
// которые ломают разбор заголовка).
func encodeRFC5987(s string) string {
	const attr = "!#$&+-.^_`|~"
	var b strings.Builder
	for _, c := range []byte(s) { // побайтово: октеты UTF-8 кодируются
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			strings.IndexByte(attr, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// sanitizeFilename replaces characters unsafe for Content-Disposition filename.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
