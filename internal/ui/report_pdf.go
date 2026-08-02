package ui

// report_pdf.go — выгрузка отчёта в реальный бинарный PDF (issue #218).
// Данные те же, что у Excel-выгрузки (reportExportRows) и экранного рендера,
// поэтому PDF не расходится с тем, что видит пользователь. Рендер — общий движок
// sheet.PDF, тот же, что у печатных форм документов (/print/<form>/pdf).

import (
	"net/http"

	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/sheet"
)

func (s *Server) reportPDF(w http.ResponseWriter, r *http.Request) {
	rep := s.getReport(w, r)
	if rep == nil {
		return
	}
	if !s.requirePerm(w, r, "report", rep.Name, "run") {
		return
	}
	headers, rows, err := s.reportExportRows(r, rep)
	if err != nil {
		s.writeReportExportError(w, r, err)
		return
	}
	doc := buildReportSheet(reportDisplayTitle(rep, s.resolveLang(r)), headers, rows)
	pdfBytes, err := doc.PDF(sheet.PDFOptions{Title: rep.Name})
	if err != nil {
		http.Error(w, "PDF error: "+s.errText(r, err), 500)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	name := rep.Name + ".pdf"
	w.Header().Set("Content-Disposition", contentDisposition(name))
	writeDownload(w, name, pdfBytes)
}

// buildReportSheet строит табличный документ из заголовков и строк отчёта:
// необязательный заголовок сверху, затем жирная шапка по центру и строки данных.
// Числовые ячейки выравниваются вправо, прочие — влево. Ширины колонок проставляет
// PDF-движок автоматически (sheet.resolveColumnWidthsMM), специально задавать не
// нужно.
func buildReportSheet(title string, headers []string, rows [][]any) *sheet.Document {
	doc := sheet.NewDocument()
	headerRow := 0

	// Заголовок отчёта над таблицей — на всю ширину, без рамки.
	if title != "" && len(headers) > 0 {
		c := doc.GetOrCreateCell(0, 0)
		c.Text = title
		c.Bold = true
		c.FontSize = 13
		c.Align = "left"
		c.Border = "none"
		if len(headers) > 1 {
			doc.Merge(0, 0, 0, len(headers)-1)
		}
		headerRow = 2 // строка 1 — отступ между заголовком и шапкой таблицы
	}

	for j, h := range headers {
		c := doc.GetOrCreateCell(headerRow, j)
		c.Text = h
		c.Bold = true
		c.Align = "center"
		c.BackColor = "#e2e8f0"
	}
	for i, dataRow := range rows {
		for j, v := range dataRow {
			c := doc.GetOrCreateCell(headerRow+1+i, j)
			c.Text = fmtVal(v)
			if isNumericCell(v) {
				c.Align = "right"
			} else {
				c.Align = "left"
			}
		}
	}
	return doc
}

// isNumericCell сообщает, что значение ячейки числовое (для выравнивания вправо).
// composedRows отдаёт неформатированные показатели как float64, отформатированные
// — строкой; первые выравниваем по правому краю, вторые остаются как есть.
func isNumericCell(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	}
	return false
}

// reportDisplayTitle возвращает локализованный заголовок отчёта с откатом на
// Title и затем на Name.
func reportDisplayTitle(rep *reportpkg.Report, lang string) string {
	if lang != "" && rep.Titles != nil {
		if t, ok := rep.Titles[lang]; ok && t != "" {
			return t
		}
	}
	if rep.Title != "" {
		return rep.Title
	}
	return rep.Name
}
