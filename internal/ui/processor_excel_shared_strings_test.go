package ui

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/storage"
)

// Upload the same generated XLSX with precisely controlled sheet and string
// table parts. Spreadsheet writers cannot generate invalid shared-string indexes.
func importWorkbookWithSharedStrings(t *testing.T, index int, table string) []byte {
	t.Helper()
	sheet := fmt.Sprintf(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>`+
		`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="s"><v>3</v></c></row>`+
		`<row r="3"><c r="A3" t="s"><v>4</v></c><c r="B3" t="s"><v>%d</v></c></row>`+
		`</sheetData></worksheet>`, index)
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
		switch entry.Name {
		case "xl/worksheets/sheet1.xml":
			_, err = io.WriteString(w, sheet)
		case "xl/sharedStrings.xml":
			_, err = io.WriteString(w, table)
		default:
			r, openErr := entry.Open()
			if openErr != nil {
				t.Fatal(openErr)
			}
			var n int64
			n, err = io.Copy(w, io.LimitReader(r, 1<<20))
			if closeErr := r.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if n == 1<<20 {
				t.Fatal("fixture part exceeds helper limit")
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcessorBinaryParam_SharedStringTableRejectsWholeBook(t *testing.T) {
	const start = `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="999" uniqueCount="999">`
	const items = `<si><t>Code</t></si><si><t>Name</t></si><si><t>007</t></si><si><t>First</t></si><si><t>012</t></si><si><r><t>La</t></r><r><t>st</t></r></si>`
	const extension = `<extLst><ext uri="test"><si><t>Unused</t></si></ext></extLst>`
	for _, tc := range []struct {
		name   string
		index  int
		table  string
		reject bool
	}{
		{"healthy", 5, start + items + `</sst>`, false},
		{"healthy_with_extension", 5, start + items + extension + `</sst>`, false},
		{"invalid_index", 6, start + items + `</sst>`, true},
		{"nested_extension_does_not_extend_table", 6, start + items + extension + `</sst>`, true},
		{"nested_item_does_not_extend_table", 6, start + strings.Replace(items, `<t>First</t>`, `<t>First</t><si><t>Unused</t></si>`, 1) + `</sst>`, true},
		{"wrong_root", 5, strings.ReplaceAll(start+items+`</sst>`, "sst", "other"), true},
		{"second_root", 6, start + items + `</sst><si><t>Unused</t></si>`, true},
		{"malformed_extension", 5, start + items + `<extLst></sst>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
				s, ctx, ts, cat := binaryImportFixture(t, db)
				data := importWorkbookWithSharedStrings(t, tc.index, tc.table)
				status, page := postBinaryProcessor(t, ts, "ЗагрузкаУчеников", "Файл", "strings.xlsx", data)
				rows, err := s.store.List(ctx, cat.Name, cat, storage.ListParams{RowFilterEvaluated: true})
				if err != nil {
					t.Fatal(err)
				}
				if tc.reject {
					if len(rows) != 0 {
						t.Errorf("invalid workbook created %d catalog rows: %v", len(rows), rows)
					}
					if !strings.Contains(page, "повреждённая структура книги") {
						t.Error("missing workbook corruption error")
					}
					if strings.Contains(page, "путь: ") {
						t.Error("DSL continued after reading an invalid workbook")
					}
					return
				}
				if status != http.StatusOK || len(rows) != 2 {
					t.Fatalf("healthy workbook: HTTP %d, rows=%v, page=%s", status, rows, page)
				}
				byCode := map[string]string{}
				for _, row := range rows {
					byCode[fmt.Sprint(row["Код"])] = fmt.Sprint(row["Наименование"])
				}
				if byCode["007"] != "First" || byCode["012"] != "Last" {
					t.Fatalf("healthy workbook values changed: %v", byCode)
				}
			})
		})
	}
}
