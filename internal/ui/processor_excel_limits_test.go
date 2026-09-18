package ui

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/storage"
)

// Keep the real workbook metadata, but replace sheet XML directly: normal
// spreadsheet writers cannot produce the malformed input an upload can contain.
func importWorkbookWithSheet(t *testing.T, sheet string, extraBytes int64) []byte {
	t.Helper()
	source := xlsxWithStudents(t)
	zr, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range zr.File {
		w, err := zw.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "xl/worksheets/sheet1.xml" {
			if _, err := io.WriteString(w, sheet); err != nil {
				t.Fatal(err)
			}
			continue
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		// Every source part belongs to the small generated fixture. Bound the
		// decompression in the helper too, before adding the deliberate payload.
		copied, copyErr := io.Copy(w, io.LimitReader(r, 1<<20))
		closeErr := r.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatalf("copy: %v; close: %v", copyErr, closeErr)
		}
		if copied == 1<<20 {
			t.Fatal("source fixture part exceeds the helper limit")
		}
	}
	if extraBytes > 0 {
		w, err := zw.Create("xl/padding.bin")
		if err != nil {
			t.Fatal(err)
		}
		chunk := bytes.Repeat([]byte{' '}, 1024)
		for extraBytes > 0 {
			n := min(extraBytes, int64(len(chunk)))
			if _, err := w.Write(chunk[:n]); err != nil {
				t.Fatal(err)
			}
			extraBytes -= n
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcessorBinaryParam_RejectsUnsafeExcelBeforeWriting(t *testing.T) {
	const prefix = `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><dimension ref="A1:B2"/><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Code</t></is></c><c r="B1" t="inlineStr"><is><t>Name</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>007</t></is></c><c r="B2" t="inlineStr"><is><t>First</t></is></c></row>`
	const suffix = `</sheetData></worksheet>`
	for _, tc := range []struct {
		name       string
		tail       string
		extraBytes int64
		want       string
	}{
		{"sparse_wide_sheet", `<row r="128"><c r="XFD128"><v>1</v></c></row>`, 0, "предел импорта"},
		{"row_limit", `<row r="10001"><c r="A10001"><v>1</v></c></row>`, 0, "предел импорта"},
		{"column_limit", `<row r="3"><c r="IW3"><v>1</v></c></row>`, 0, "предел импорта"},
		{"rectangle_limit", `<row r="4000"><c r="IV4000"><v>1</v></c></row>`, 0, "предел импорта"},
		{"unpacked_limit", "", (32 << 20) + 1, "предел импорта"},
		{"bad_cell_reference", `<row r="3"><c r="INVALID!"><v>1</v></c></row><row r="4"><c r="A4"><v>012</v></c></row>`, 0, "повреждённая"},
		{"broken_xml_after_valid_rows", `<row r="3"><c r="A3"><v>`, 0, "повреждённая"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx, ts, cat := binaryImportFixture(t)
			data := importWorkbookWithSheet(t, prefix+tc.tail+suffix, tc.extraBytes)
			_, page := postBinaryProcessor(t, ts, "ЗагрузкаУчеников", "Файл", "book.xlsx", data)
			rows, err := s.store.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Errorf("invalid workbook created %d catalog rows", len(rows))
			}
			if !strings.Contains(page, tc.want) {
				t.Errorf("missing import error %q", tc.want)
			}
			if strings.Contains(page, "путь: ") {
				t.Error("DSL continued after reading an invalid workbook")
			}
		})
	}
}
