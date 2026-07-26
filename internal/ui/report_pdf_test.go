package ui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/sheet"
	"github.com/ivantit66/onebase/internal/storage"
)

// buildReportSheet строит документ: заголовок, жирная шапка, строки данных с
// выравниванием чисел вправо. Проверяем структуру и что .PDF() даёт валидный PDF.
func TestBuildReportSheet_StructureAndPDF(t *testing.T) {
	headers := []string{"Товар", "Сумма"}
	rows := [][]any{
		{"Стол", float64(1200)},
		{"Стул", float64(750)},
	}
	doc := buildReportSheet("Отчёт по продажам", headers, rows)

	// Заголовок задан → шапка таблицы на строке 2 (строка 1 — отступ).
	hc := doc.GetCell(2, 0)
	if hc == nil || !hc.Bold || hc.Text != "Товар" {
		t.Fatalf("ячейка шапки должна быть жирной с текстом «Товар»: %+v", hc)
	}
	if nc := doc.GetCell(3, 1); nc == nil || nc.Align != "right" || nc.Text != "1200" {
		t.Errorf("числовая ячейка должна быть по правому краю с текстом 1200: %+v", nc)
	}
	if tc := doc.GetCell(3, 0); tc == nil || tc.Align != "left" {
		t.Errorf("текстовая ячейка должна быть по левому краю: %+v", tc)
	}

	pdf, err := doc.PDF(sheet.PDFOptions{Title: "T"})
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Errorf("результат не похож на PDF (len=%d)", len(pdf))
	}
}

func newReportExportTestServer(t *testing.T, rep *reportpkg.Report) *Server {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "rpdf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureSettingsSchema(ctx); err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Reports: []*reportpkg.Report{rep}})
	s := &Server{store: db, reg: registry}
	t.Cleanup(s.Close)
	return s
}

// reportPDF-маршрут отдаёт реальный бинарный PDF с корректными заголовками.
func TestReportPDF_EndpointReturnsPDF(t *testing.T) {
	rep := &reportpkg.Report{Name: "Тест", Query: "ВЫБРАТЬ 1 КАК Номер, 2 КАК Сумма"}
	s := newReportExportTestServer(t, rep)

	r := reqWithChi("GET", "/ui/report/Тест/pdf", url.Values{}, map[string]string{"name": "Тест"})
	w := httptest.NewRecorder()
	s.reportPDF(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, ожидался application/pdf", ct)
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF")) {
		t.Errorf("тело ответа не похоже на PDF")
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, ".pdf") {
		t.Errorf("Content-Disposition без .pdf: %q", cd)
	}
}

func TestReportExcel_InvalidQueryKeepsCompileErrorStatus(t *testing.T) {
	rep := &reportpkg.Report{Name: "Битый", Query: "ВЫБРАТЬ Ном ИЗ РегистрНакопления.Неизвестный.Остатки()"}
	s := newReportExportTestServer(t, rep)

	r := reqWithChi("GET", "/ui/report/Битый/excel", url.Values{}, map[string]string{"name": "Битый"})
	w := httptest.NewRecorder()
	s.reportExcel(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("ожидался 400 для ошибки компиляции запроса, получен %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "query compile error") {
		t.Errorf("ответ должен сохранить прежний префикс ошибки Excel: %q", w.Body.String())
	}
}

func renderExportButtons(t *testing.T, rep *reportpkg.Report) string {
	t.Helper()
	data := map[string]any{
		"Report":        rep,
		"ParamValues":   map[string]any{},
		"ActiveVariant": "",
		"Lang":          "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "report-export-buttons", data); err != nil {
		t.Fatalf("execute report-export-buttons: %v", err)
	}
	return buf.String()
}

func TestReportExportButtons_DefaultExcelFirst(t *testing.T) {
	out := renderExportButtons(t, &reportpkg.Report{Name: "Прод"})
	if !strings.Contains(out, "/export/excel") || !strings.Contains(out, "/export/pdf") {
		t.Fatalf("кнопки должны запускать фоновые выгрузки:\n%s", out)
	}
	pdfIdx, excelIdx := strings.Index(out, "/pdf"), strings.Index(out, "/excel")
	if pdfIdx < 0 || excelIdx < 0 {
		t.Fatalf("обе кнопки должны присутствовать:\n%s", out)
	}
	if excelIdx > pdfIdx {
		t.Errorf("по умолчанию Excel идёт первой:\n%s", out)
	}
}

func TestReportExportButtons_OutputFormatPDFFirst(t *testing.T) {
	out := renderExportButtons(t, &reportpkg.Report{Name: "Прод", OutputFormat: "pdf"})
	if !strings.Contains(out, "/export/excel") || !strings.Contains(out, "/export/pdf") {
		t.Fatalf("кнопки должны запускать фоновые выгрузки:\n%s", out)
	}
	pdfIdx, excelIdx := strings.Index(out, "/pdf"), strings.Index(out, "/excel")
	if pdfIdx < 0 || excelIdx < 0 {
		t.Fatalf("обе кнопки должны присутствовать:\n%s", out)
	}
	if pdfIdx > excelIdx {
		t.Errorf("при output_format=pdf кнопка PDF должна быть первой:\n%s", out)
	}
}
