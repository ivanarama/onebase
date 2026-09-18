package excel

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// ImportRows выравнивает строки по самой длинной. Без этого обращение по
// индексу колонки зависело бы от того, где в конкретной строке файла
// закончились данные, и прикладной код падал бы на «коротких» строках.
func TestImportRows_PadsRaggedRowsAndKeepsText(t *testing.T) {
	data, err := ExportList(
		[]string{"Код", "ФИО", "Класс"},
		[][]any{
			{"007", "Иванов Иван", "5А"},
			{"012", "Петрова Анна"},
		},
	)
	if err != nil {
		t.Fatalf("сборка книги: %v", err)
	}
	path := filepath.Join(t.TempDir(), "book.xlsx")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := ImportRows(path)
	if err != nil {
		t.Fatalf("ImportRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("строк %d, ждали 3 (заголовок и две строки данных)", len(rows))
	}
	for i, row := range rows {
		if len(row) != 3 {
			t.Fatalf("строка %d имеет %d ячеек, ждали 3", i, len(row))
		}
	}
	// Ведущий ноль выживает только у текстовой ячейки: приведение к числу его
	// теряет, и код ученика «007» становится «7».
	if rows[1][0] != "007" {
		t.Fatalf("код с ведущим нулём потерян: %q", rows[1][0])
	}
	if rows[2][2] != "" {
		t.Fatalf("недостающая ячейка должна быть пустой, получено %q", rows[2][2])
	}
}

func TestImportRows_MissingFile(t *testing.T) {
	if _, err := ImportRows(filepath.Join(t.TempDir(), "нет.xlsx")); err == nil {
		t.Fatal("ожидалась ошибка на отсутствующем файле")
	}
}

func TestImportRows_AcceptsLimitBoundariesAndEmptySheets(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows int
		cols int
	}{
		{"empty", 0, 0},
		{"widest", 2, 256},
		{"million_cells", 10000, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := excelize.NewFile()
			defer closeBook(f)
			if tc.rows != 0 {
				cell, err := excelize.CoordinatesToCellName(tc.cols, tc.rows)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue("Sheet1", cell, "007"); err != nil {
					t.Fatal(err)
				}
			}
			filename := filepath.Join(t.TempDir(), "boundary.xlsx")
			if err := f.SaveAs(filename); err != nil {
				t.Fatal(err)
			}
			rows, err := ImportRows(filename)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.rows {
				t.Fatalf("got %d rows, want %d", len(rows), tc.rows)
			}
			for i, row := range rows {
				if len(row) != tc.cols {
					t.Fatalf("row %d: got %d columns, want %d", i, len(row), tc.cols)
				}
			}
			if tc.rows > 0 && rows[tc.rows-1][tc.cols-1] != "007" {
				t.Fatal("last value or leading zeroes lost")
			}
		})
	}
}

func TestImportRows_ResolvesWorksheetRelationship(t *testing.T) {
	data, err := ExportList([]string{"Code"}, [][]any{{"007"}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range zr.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read: %v; close: %v", readErr, closeErr)
		}
		name := strings.ReplaceAll(entry.Name, "sheet1.xml", "custom.xml")
		if entry.Name == "xl/_rels/workbook.xml.rels" || entry.Name == "[Content_Types].xml" {
			body = bytes.ReplaceAll(body, []byte("sheet1.xml"), []byte("custom.xml"))
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "custom.xlsx")
	if err := os.WriteFile(filename, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ImportRows(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(rows[1]) != 1 || rows[1][0] != "007" {
		t.Fatalf("wrong worksheet data: %v", rows)
	}
}
