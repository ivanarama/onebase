package ui

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	reportpkg "github.com/ivantit66/onebase/internal/report"
	"github.com/xuri/excelize/v2"
	"golang.org/x/net/html"
)

func periodReport(t *testing.T) *reportpkg.Report {
	t.Helper()
	yes, no := true, false
	return &reportpkg.Report{Name: "Periods", Params: []reportpkg.Param{
		{Name: "ByHours", Type: "bool"},
		{Name: "From", Type: "date", Default: "{{today}}", VisibleWhen: &reportpkg.ParamVisibility{Param: "ByHours", Equals: &no}},
		{Name: "To", Type: "date", Default: "{{today}}", PeriodEnd: true, VisibleWhen: &reportpkg.ParamVisibility{Param: "ByHours", Equals: &no}},
		{Name: "FromTime", Type: "datetime", Default: "{{today | start_of_day}}", VisibleWhen: &reportpkg.ParamVisibility{Param: "ByHours", Equals: &yes}},
		{Name: "ToTime", Type: "datetime", Default: "{{today | end_of_day}}", VisibleWhen: &reportpkg.ParamVisibility{Param: "ByHours", Equals: &yes}},
	}, Query: "ВЫБРАТЬ ВЫБОР КОГДА &ByHours = ИСТИНА ТОГДА &FromTime ИНАЧЕ &From КОНЕЦ КАК SelectedFrom, ВЫБОР КОГДА &ByHours = ИСТИНА ТОГДА &ToTime ИНАЧЕ &To КОНЕЦ КАК SelectedTo"}
}

func periodControl(t *testing.T, body, name string) (map[string]string, bool) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var found *html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			for _, attr := range n.Attr {
				if attr.Key == "name" && attr.Val == name {
					found = n
					return
				}
			}
		}
		for child := n.FirstChild; child != nil && found == nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if found == nil {
		t.Fatalf("missing control %s", name)
	}
	attrs := map[string]string{}
	for _, attr := range found.Attr {
		attrs[attr.Key] = attr.Val
	}
	for parent := found.Parent; parent != nil; parent = parent.Parent {
		for _, attr := range parent.Attr {
			if attr.Key == "hidden" {
				return attrs, true
			}
		}
		if parent.Data == "form" {
			break
		}
	}
	return attrs, false
}

func periodXLSXText(t *testing.T, data []byte) string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var result strings.Builder
	for _, file := range z.File {
		if !strings.HasPrefix(file.Name, "xl/") {
			continue
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		result.Write(b)
	}
	return result.String()
}

func periodPDFPages(t *testing.T, data []byte) string {
	t.Helper()
	var pages strings.Builder
	for _, stream := range regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`).FindAllSubmatch(data, -1) {
		r, err := zlib.NewReader(bytes.NewReader(stream[1]))
		if err != nil {
			continue
		}
		plain, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(plain, []byte("BT ")) {
			pages.Write(plain)
		}
	}
	if pages.Len() == 0 {
		t.Fatal("missing PDF page contents")
	}
	return pages.String()
}

func TestReportPeriodVisibilityAndInitialDefaultsHTTP(t *testing.T) {
	rep := periodReport(t)
	s := newReportExportTestServer(t, rep)
	router := chi.NewRouter()
	s.Mount(router)
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	w := request("/ui/report/Periods")
	if w.Code != 200 {
		t.Fatalf("form: %d", w.Code)
	}
	for name, wantHidden := range map[string]bool{"From": false, "To": false, "FromTime": true, "ToTime": true} {
		attrs, hidden := periodControl(t, w.Body.String(), name)
		if hidden != wantHidden {
			t.Fatalf("%s hidden=%t", name, hidden)
		}
		if _, disabled := attrs["disabled"]; disabled {
			t.Fatalf("hidden period was disabled: %s", name)
		}
		if name == "ToTime" && !strings.HasSuffix(attrs["value"], "T23:59:59") {
			t.Fatalf("end default: %#v", attrs)
		}
		if name == "FromTime" && !strings.HasSuffix(attrs["value"], "T00:00:00") {
			t.Fatalf("start default: %#v", attrs)
		}
	}
	values := url.Values{"__run": {"1"}, "ByHours": {"true"}, "From": {"2099-02-03"}, "To": {"2099-02-04"}, "FromTime": {"2026-09-02T11:22:33"}, "ToTime": {"2026-09-02T12:34:56"}}
	w = request("/ui/report/Periods?" + values.Encode())
	attrs, hidden := periodControl(t, w.Body.String(), "ToTime")
	if hidden || attrs["value"] != values.Get("ToTime") {
		t.Fatalf("explicit datetime changed: %#v", attrs)
	}
	_, dateHidden := periodControl(t, w.Body.String(), "To")
	if !dateHidden {
		t.Fatal("inactive date pair visible")
	}
	falseValues := url.Values{}
	for key, items := range values {
		falseValues[key] = append([]string(nil), items...)
	}
	falseValues.Set("ByHours", "false")
	falseResponse := request("/ui/report/Periods?" + falseValues.Encode())
	flag, _ := periodControl(t, falseResponse.Body.String(), "ByHours")
	_, dateHidden = periodControl(t, falseResponse.Body.String(), "To")
	_, timeHidden := periodControl(t, falseResponse.Body.String(), "ToTime")
	if _, checked := flag["checked"]; checked || dateHidden || !timeHidden {
		t.Fatal("explicit false was treated as a truthy string")
	}
	for _, background := range []bool{false, true} {
		for _, clear := range []bool{false, true} {
			q := url.Values{}
			for key, items := range values {
				q[key] = append([]string(nil), items...)
			}
			if clear {
				q.Set("FromTime", "")
				q.Set("ToTime", "")
			}
			path := "/ui/report/Periods/excel"
			if background {
				path = "/ui/report/Periods/export/excel"
			}
			response := request(path + "?" + q.Encode())
			if background {
				if response.Code != http.StatusSeeOther {
					t.Fatalf("enqueue: %d", response.Code)
				}
				location := response.Header().Get("Location") + "/download"
				deadline := time.Now().Add(5 * time.Second)
				for {
					response = request(location)
					if response.Code == 200 || time.Now().After(deadline) {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if response.Code != 200 {
				t.Fatalf("export: %d %s", response.Code, response.Body.String())
			}
			xml := periodXLSXText(t, response.Body.Bytes())
			if strings.Contains(xml, "2099-02") || strings.Contains(xml, "23:59:59") {
				t.Fatalf("inactive/default period replaced explicit value: %s", xml)
			}
			book, err := excelize.OpenReader(bytes.NewReader(response.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			sheet := book.GetSheetList()[0]
			for cell, want := range map[string]string{"A2": q.Get("FromTime"), "B2": q.Get("ToTime")} {
				raw, err := book.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
				if err != nil {
					t.Fatal(err)
				}
				if want == "" {
					if raw != "" {
						t.Fatalf("cleared cell %s reapplied default: %q", cell, raw)
					}
					continue
				}
				expected, err := time.ParseInLocation("2006-01-02T15:04:05", want, time.Local)
				if err != nil {
					t.Fatal(err)
				}
				var got time.Time
				if serial, err := strconv.ParseFloat(raw, 64); err == nil {
					got, err = excelize.ExcelDateToTime(serial, false)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					// SQLite may return the same instant normalised to UTC; this
					// must be compared as a timestamp, not by wall-clock substring.
					got, err = time.Parse(time.RFC3339Nano, strings.Replace(raw, " ", "T", 1))
					if err != nil {
						got, err = time.ParseInLocation("2006-01-02T15:04:05", raw, time.Local)
					}
					if err != nil {
						t.Fatalf("%s invalid datetime %q: %v", cell, raw, err)
					}
				}
				delta := got.Sub(expected)
				if delta < -time.Millisecond || delta > time.Millisecond {
					t.Fatalf("%s rounded: %s want %s", cell, got, expected)
				}
			}
			_ = book.Close()
		}
	}
	syncPDF := request("/ui/report/Periods/pdf?" + values.Encode())
	if syncPDF.Code != 200 {
		t.Fatalf("PDF: %d", syncPDF.Code)
	}
	expectedPages := periodPDFPages(t, syncPDF.Body.Bytes())
	queued := request("/ui/report/Periods/export/pdf?" + values.Encode())
	if queued.Code != http.StatusSeeOther {
		t.Fatalf("enqueue PDF: %d", queued.Code)
	}
	location := queued.Header().Get("Location") + "/download"
	deadline := time.Now().Add(5 * time.Second)
	var downloaded *httptest.ResponseRecorder
	for {
		downloaded = request(location)
		if downloaded.Code == 200 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if downloaded.Code != 200 || periodPDFPages(t, downloaded.Body.Bytes()) != expectedPages {
		t.Fatal("background PDF changed explicit period")
	}
	changed := url.Values{}
	for key, items := range values {
		changed[key] = append([]string(nil), items...)
	}
	changed.Set("ToTime", "2026-09-02T13:14:15")
	otherPDF := request("/ui/report/Periods/pdf?" + changed.Encode())
	if otherPDF.Code != 200 || periodPDFPages(t, otherPDF.Body.Bytes()) == expectedPages {
		t.Fatal("PDF ignored/rounded an explicitly changed time")
	}
}
