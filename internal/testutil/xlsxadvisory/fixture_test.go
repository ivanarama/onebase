package xlsxadvisory

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRewriteWorksheetByteLimits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entrySizes []int
		sheetSize  int
		wantError  bool
	}{
		{"entry_exact", []int{maxFixtureEntryBytes}, 1, false},
		{"entry_exceeded", []int{maxFixtureEntryBytes + 1}, 1, true},
		{"archive_exact", []int{maxFixtureEntryBytes, maxFixtureEntryBytes, maxFixtureEntryBytes}, maxFixtureEntryBytes, false},
		{"archive_exceeded", []int{maxFixtureEntryBytes, maxFixtureEntryBytes, maxFixtureEntryBytes, 1}, maxFixtureEntryBytes, true},
		{"replacement_exceeded", []int{1}, maxFixtureEntryBytes + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source bytes.Buffer
			zw := zip.NewWriter(&source)
			for i, size := range append(tc.entrySizes, 1) {
				name := fmt.Sprintf("entry-%d", i)
				if i == len(tc.entrySizes) {
					name = "xl/worksheets/sheet1.xml"
				}
				w, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(w, strings.Repeat("x", size)); err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			sheet := strings.Repeat("s", tc.sheetSize)
			out, err := rewriteWorksheet(source.Bytes(), sheet)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "byte limit") || out != nil {
					t.Fatalf("oversized fixture was not rejected: len=%d, err=%v", len(out), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
			if err != nil {
				t.Fatal(err)
			}
			if len(zr.File) != len(tc.entrySizes)+1 {
				t.Fatalf("entries lost: %d", len(zr.File))
			}
			for i, entry := range zr.File {
				r, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(io.LimitReader(r, maxFixtureEntryBytes+1))
				closeErr := r.Close()
				if readErr != nil || closeErr != nil {
					t.Fatalf("read=%v, close=%v", readErr, closeErr)
				}
				want := sheet
				if i < len(tc.entrySizes) {
					want = strings.Repeat("x", tc.entrySizes[i])
				}
				if string(data) != want {
					t.Fatalf("entry %q was truncated or changed", entry.Name)
				}
			}
		})
	}
}
