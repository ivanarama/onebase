package ui

// HTTP-обработчики журналов документов.
// Выделено из handlers.go (план 55, этап 1) — перенос as-is.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/excel"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func (s *Server) journalList(w http.ResponseWriter, r *http.Request) {
	j := s.getJournal(w, r)
	if j == nil {
		return
	}
	settings := loadJournalSettings(s.store, r, j)
	visibleColumns := effectiveJournalColumns(j, settings)

	// Build docs map
	docs := make(map[string]*metadata.Entity, len(j.Documents))
	for _, docName := range j.Documents {
		if e := s.reg.GetEntity(docName); e != nil {
			docs[docName] = e
		}
	}

	// Parse filter params from request
	params := storage.ListParams{Filters: make(map[string]storage.FilterValue)}
	for _, jf := range j.Filters {
		fv := storage.FilterValue{}
		switch {
		case jf.Type == "date_range":
			fv.From = r.URL.Query().Get("f." + jf.Field + ".from")
			fv.To = r.URL.Query().Get("f." + jf.Field + ".to")
		default:
			fv.Value = r.URL.Query().Get("f." + jf.Field)
		}
		params.Filters[jf.Field] = fv
	}

	const pageSize = 50
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	rows, total, colRefMap, err := s.store.JournalQuery(r.Context(), j, docs, params, pageSize, offset)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}

	// Resolve ref columns
	s.resolveJournalRefs(r.Context(), j, colRefMap, rows)
	var journalWarnings []string
	if s.interp != nil {
		journalWarnings = applyJournalConditionalStyles(rows, j.Conditional, newInterpEvaluator(s.interp))
	}

	// Load filter options for reference filters
	filterOpts := make(map[string][]map[string]any)
	for _, jf := range j.Filters {
		if !strings.HasPrefix(jf.Type, "reference:") {
			continue
		}
		refName := strings.TrimPrefix(jf.Type, "reference:")
		refEntity := s.reg.GetEntity(refName)
		if refEntity == nil {
			continue
		}
		refRows, err := s.store.List(r.Context(), refEntity.Name, refEntity, storage.ListParams{})
		if err != nil {
			continue
		}
		for _, row := range refRows {
			row["_label"] = firstStringField(row, refEntity)
		}
		filterOpts[jf.Field] = refRows
	}

	// Compute column formats from entity metadata
	colFormats := journalColumnFormats(j, docs)

	hasNext := offset+pageSize < total
	hasPrev := offset > 0
	prevOffset := offset - pageSize
	if prevOffset < 0 {
		prevOffset = 0
	}

	s.render(w, r, "page-journal", map[string]any{
		"Journal":                j,
		"JournalColumns":         visibleColumns,
		"JournalSettingsColumns": journalSettingsColumns(j, settings),
		"JournalSettingsJSON":    journalSettingsJSON(j, settings),
		"JournalSettingsActive":  settings != nil,
		"Rows":                   rows,
		"JournalWarnings":        journalWarnings,
		"Total":                  total,
		"Params":                 params,
		"FilterOptions":          filterOpts,
		"ColFormats":             colFormats,
		"Offset":                 offset,
		"Limit":                  pageSize,
		"HasPrev":                hasPrev,
		"HasNext":                hasNext,
		"PrevOffset":             prevOffset,
		"NextOffset":             offset + pageSize,
		"RequestURI":             r.URL.RequestURI(),
	})
}

func (s *Server) getJournal(w http.ResponseWriter, r *http.Request) *metadata.Journal {
	name := chi.URLParam(r, "name")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	j := s.reg.GetJournal(name)
	if j == nil {
		http.Error(w, "unknown journal: "+name, 404)
		return nil
	}
	return j
}

func journalColumnFormats(j *metadata.Journal, docs map[string]*metadata.Entity) map[string]string {
	colFormats := make(map[string]string)
	for _, jcol := range j.Columns {
		if jcol.Format != "" {
			colFormats[jcol.Field] = jcol.Format
			continue
		}
		for _, entity := range docs {
			for _, f := range entity.Fields {
				if strings.EqualFold(f.Name, jcol.Field) {
					if f.Type == metadata.FieldTypeDate {
						colFormats[jcol.Field] = "date"
					}
					goto nextCol
				}
				for _, fb := range jcol.Fallback {
					if strings.EqualFold(f.Name, fb) && f.Type == metadata.FieldTypeDate {
						colFormats[jcol.Field] = "date"
					}
				}
			}
		}
	nextCol:
	}
	return colFormats
}

// resolveJournalRefs resolves UUID values in reference journal columns to display labels.
func (s *Server) resolveJournalRefs(ctx context.Context, j *metadata.Journal, colRefMap storage.ColRefMap, rows []map[string]any) {
	for colAlias, refEntityName := range colRefMap {
		refEntity := s.reg.GetEntity(refEntityName)
		if refEntity == nil {
			continue
		}
		// Find the JournalColumn with this field name
		var colField string
		for _, jcol := range j.Columns {
			if strings.ToLower(jcol.Field) == colAlias {
				colField = jcol.Field
				break
			}
		}
		if colField == "" {
			continue
		}
		// Collect unique UUIDs
		seen := map[string]bool{}
		for _, row := range rows {
			if v := row[colField]; v != nil {
				seen[fmt.Sprintf("%v", v)] = true
			}
		}
		// Resolve labels
		labels := make(map[string]string, len(seen))
		for idStr := range seen {
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			refRow, err := s.store.GetByID(ctx, refEntity.Name, id, refEntity)
			if err != nil {
				continue
			}
			labels[idStr] = firstStringField(refRow, refEntity)
		}
		// Replace in rows
		for _, row := range rows {
			if v := row[colField]; v != nil {
				if label, ok := labels[fmt.Sprintf("%v", v)]; ok {
					row[colField] = label
				}
			}
		}
	}
}

// journalExcel exports a journal as XLSX.
func (s *Server) journalExcel(w http.ResponseWriter, r *http.Request) {
	j := s.getJournal(w, r)
	if j == nil {
		return
	}
	visibleColumns := effectiveJournalColumns(j, loadJournalSettings(s.store, r, j))

	docs := make(map[string]*metadata.Entity, len(j.Documents))
	for _, docName := range j.Documents {
		if e := s.reg.GetEntity(docName); e != nil {
			docs[docName] = e
		}
	}

	params := storage.ListParams{Filters: make(map[string]storage.FilterValue)}
	for _, jf := range j.Filters {
		fv := storage.FilterValue{}
		switch {
		case jf.Type == "date_range":
			fv.From = r.URL.Query().Get("f." + jf.Field + ".from")
			fv.To = r.URL.Query().Get("f." + jf.Field + ".to")
		default:
			fv.Value = r.URL.Query().Get("f." + jf.Field)
		}
		params.Filters[jf.Field] = fv
	}

	rows, _, colRefMap, err := s.store.JournalQuery(r.Context(), j, docs, params, 10000, 0)
	if err != nil {
		http.Error(w, s.errText(r, err), 500)
		return
	}
	s.resolveJournalRefs(r.Context(), j, colRefMap, rows)

	cols := make([]string, 0, len(visibleColumns)+2)
	cols = append(cols, "Дата", "Вид")
	for _, jcol := range visibleColumns {
		cols = append(cols, jcol.Label)
	}

	xlsRows := make([][]any, len(rows))
	for i, row := range rows {
		cells := make([]any, len(cols))
		cells[0] = row["date"]
		cells[1] = row["doc_type"]
		for ji, jcol := range visibleColumns {
			cells[2+ji] = row[jcol.Field]
		}
		xlsRows[i] = cells
	}

	data, err := excel.ExportList(cols, xlsRows)
	if err != nil {
		http.Error(w, "Excel error: "+s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", contentDisposition(j.Name+".xlsx"))
	w.Write(data)
}
