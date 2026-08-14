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

	docs, rowFilters, ok := s.journalDocsForRead(w, r, j)
	if !ok {
		return
	}

	// Parse filter params from request
	params := storage.ListParams{Filters: make(map[string]storage.FilterValue)}
	params.JournalRowFilters = rowFilters
	for _, jf := range j.Filters {
		fv := storage.FilterValue{}
		if jf.Type == "date_range" {
			fv.From = r.URL.Query().Get("f." + jf.Field + ".from")
			fv.To = r.URL.Query().Get("f." + jf.Field + ".to")
		} else {
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
		s.serverError(w, r, err)
		return
	}

	// Field masking belongs at the handler boundary, before reference UUIDs are
	// resolved and before rows (including the detail-panel payload) reach HTML.
	s.maskJournalRecords(r.Context(), j, docs, rows)
	// Resolve ref columns
	s.resolveJournalRefs(r.Context(), j, colRefMap, rows)
	var journalWarnings []string
	if s.interp != nil {
		journalWarnings = applyJournalConditionalStyles(rows, j.Conditional, newInterpEvaluator(s.interp))
	}

	// Load filter options for reference filters
	filterOpts := make(map[string][]map[string]any)
	filterRefEntities := make(map[string]string)
	for _, jf := range j.Filters {
		if !strings.HasPrefix(jf.Type, "reference:") {
			continue
		}
		refName := strings.TrimPrefix(jf.Type, "reference:")
		refEntity := s.reg.GetEntity(refName)
		if refEntity == nil {
			continue
		}
		refRows, err := s.initialReferenceOptions(r.Context(), refEntity, refOptionsFilter, []string{params.Filters[jf.Field].Value})
		if err != nil {
			continue
		}
		filterOpts[jf.Field] = refRows
		filterRefEntities[jf.Field] = refName
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
		"FilterRefEntities":      filterRefEntities,
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

func (s *Server) journalDocsForRead(w http.ResponseWriter, r *http.Request, j *metadata.Journal) (map[string]*metadata.Entity, map[string]*storage.Predicate, bool) {
	docs := make(map[string]*metadata.Entity, len(j.Documents))
	rowFilters := make(map[string]*storage.Predicate)
	for _, docName := range j.Documents {
		entity := s.reg.GetEntity(docName)
		if entity == nil {
			continue
		}
		if !s.can(r, string(entity.Kind), entity.Name, "read") {
			continue
		}
		dec, err := s.rowDecision(r.Context(), entity, "read")
		if err != nil || !dec.Allowed {
			s.renderForbidden(w, r)
			return nil, nil, false
		}
		docs[docName] = entity
		if !dec.Unrestricted {
			rowFilters[docName] = dec.Predicate
		}
	}
	return docs, rowFilters, true
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
			labels[idStr] = s.maskedRecordLabel(ctx, refEntity, refRow)
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

	docs, rowFilters, ok := s.journalDocsForRead(w, r, j)
	if !ok {
		return
	}

	params := storage.ListParams{Filters: make(map[string]storage.FilterValue)}
	params.JournalRowFilters = rowFilters
	for _, jf := range j.Filters {
		fv := storage.FilterValue{}
		if jf.Type == "date_range" {
			fv.From = r.URL.Query().Get("f." + jf.Field + ".from")
			fv.To = r.URL.Query().Get("f." + jf.Field + ".to")
		} else {
			fv.Value = r.URL.Query().Get("f." + jf.Field)
		}
		params.Filters[jf.Field] = fv
	}

	rows, _, colRefMap, err := s.store.JournalQuery(r.Context(), j, docs, params, 10000, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.maskJournalRecords(r.Context(), j, docs, rows)
	s.resolveJournalRefs(r.Context(), j, colRefMap, rows)

	// Выгрузка повторяет таблицу на экране: слева вид документа, дальше видимые
	// колонки журнала. Прежде колонок было две — «Дата» и «Вид», и обе брались
	// из ключей `date`/`doc_type`, которых в строках журнала нет: JournalQuery
	// отдаёт `_doc_kind`, `id` и поля колонок журнала в нижнем регистре. Обе
	// колонки приезжали в файл пустыми (#886). Отдельная «Дата» на экране не
	// показывается вовсе — дата документа объявляется обычной колонкой журнала
	// и уже попадает в выгрузку через visibleColumns.
	cols := make([]string, 0, len(visibleColumns)+1)
	cols = append(cols, "Документ")
	for _, jcol := range visibleColumns {
		cols = append(cols, jcol.Label)
	}

	xlsRows := make([][]any, len(rows))
	for i, row := range rows {
		cells := make([]any, len(cols))
		cells[0] = row["_doc_kind"]
		for ji, jcol := range visibleColumns {
			cells[1+ji] = row[jcol.Field]
		}
		xlsRows[i] = cells
	}

	data, err := excel.ExportList(cols, xlsRows)
	if err != nil {
		http.Error(w, "Excel error: "+s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	name := j.Name + ".xlsx"
	w.Header().Set("Content-Disposition", contentDisposition(name))
	writeDownload(w, name, data)
}
