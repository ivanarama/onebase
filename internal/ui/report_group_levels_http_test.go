package ui

import (
	"bytes"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/xuri/excelize/v2"
)

func TestReportGroupLevelsHTTPAndExports(t *testing.T) {
	for _, mode := range []string{"grouped", "cross"} {
		t.Run(mode, func(t *testing.T) { testReportGroupLevelsHTTPAndExports(t, mode == "cross") })
	}
}

func testReportGroupLevelsHTTPAndExports(t *testing.T, cross bool) {
	rep := &reportpkg.Report{Name: "levels", Query: `ВЫБРАТЬ "Север" КАК Филиал, "Сервис" КАК Направление, "A" КАК Категория, "Янв" КАК Период, 12.5 КАК Сумма`,
		Params: []reportpkg.Param{{Name: "Филиал", Type: "string", Label: "Филиал <проверка>"}},
		Composition: &reportpkg.Composition{Groupings: []string{"Филиал", "Направление", "Категория"}, Detail: true,
			Measures: []reportpkg.Measure{{Field: "Сумма", Agg: "sum"}}, Totals: reportpkg.Totals{Grand: true, Subtotals: true}}}
	wantRows := 6
	if cross {
		rep.Composition.Columns = []string{"Период"}
		wantRows = 5
	}
	s := newReportExportTestServer(t, rep)
	router := chi.NewRouter()
	s.Mount(router)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %.200s", path, w.Code, w.Body.String())
		}
		return w
	}
	page := get("/ui/report/levels?__run=1").Body.String()
	for _, want := range []string{`aria-label="Уровни группировки"`, `data-level="2"`, `class="rc-fold"`, `--rc-group-fill:`, `<tr class="grand"><td>Итого</td>`, `<th>Филиал &lt;проверка&gt; / Направление / Категория</th>`} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, absent := range []string{`id="rc-expand"`, `id="rc-collapse"`, `class="subtotal"`, `Итого: Север`} {
		if strings.Contains(page, absent) {
			t.Fatalf("unexpected %q", absent)
		}
	}
	data := get("/ui/report/levels/excel").Body.Bytes()
	book, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := book.Close(); err != nil {
			t.Error(err)
		}
	})
	name := book.GetSheetName(0)
	rows, err := book.GetRows(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != wantRows || rows[len(rows)-1][0] != "Итого" {
		t.Fatalf("rows: %v", rows)
	}
	if rows[0][0] != "Филиал <проверка> / Направление / Категория" {
		t.Fatalf("header label lost: %v", rows[0])
	}
	if rep.Composition.Groupings[0] != "Филиал" {
		t.Fatal("display label mutated report metadata")
	}
	previous := ""
	for _, cell := range []string{"B2", "B3", "B4"} {
		id, err := book.GetCellStyle(name, cell)
		if err != nil {
			t.Fatal(err)
		}
		style, err := book.GetStyle(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(style.Fill.Color) != 1 || !style.Font.Bold || style.Alignment.Horizontal != "right" {
			t.Fatalf("lost row/number style %s: %+v", cell, style)
		}
		if previous == style.Fill.Color[0] {
			t.Fatalf("group levels have same fill %s", previous)
		}
		previous = style.Fill.Color[0]
		value, err := book.GetCellValue(name, cell, excelize.Options{RawCellValue: true})
		if err != nil || value != "12.5" {
			t.Fatalf("numeric %s: %q %v", cell, value, err)
		}
	}
	id, err := book.GetCellStyle(name, "B5")
	if err != nil {
		t.Fatal(err)
	}
	style, err := book.GetStyle(id)
	if err != nil {
		t.Fatal(err)
	}
	if !cross && len(style.Fill.Color) > 0 {
		t.Fatalf("detail has automatic fill: %+v", style.Fill)
	}
	pdf := get("/ui/report/levels/pdf").Body.Bytes()
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("not PDF")
	}
	var streams []byte
	for _, match := range regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(pdf, -1) {
		r, err := zlib.NewReader(bytes.NewReader(match[1]))
		if err != nil {
			continue
		}
		part, err := io.ReadAll(io.LimitReader(r, 16<<20))
		closeErr := r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		streams = append(streams, part...)
	}
	for _, color := range []string{"0.941 0.937 0.831 rg", "0.961 0.961 0.890 rg", "0.980 0.980 0.945 rg"} {
		if !bytes.Contains(streams, []byte(color)) {
			t.Fatalf("PDF lost group fill %q", color)
		}
	}
}
