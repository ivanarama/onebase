package excel

import (
	"archive/zip"
	"errors"
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

// Крафтовая книга с точным контролем XML: excelize молча глотает ошибки
// отдельных ячеек (rowXMLHandler игнорирует cellXMLHandler и getValueFrom),
// поэтому повреждения нельзя собрать через сам excelize.
func writeRawBook(t *testing.T, sheetXML, sharedStrings string) string {
	t.Helper()
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":     `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Лист1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       sharedStrings,
		"xl/worksheets/sheet1.xml":   sheetXML,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := []string{
		"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml",
		"xl/_rels/workbook.xml.rels", "xl/sharedStrings.xml", "xl/worksheets/sheet1.xml",
	}
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "book.xlsx")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const rawBookStrings = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Код</t></si><si><t>Имя</t></si><si><t>007</t></si><si><t>First</t></si></sst>`

func rawBookSheet(row3 string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
		`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="s"><v>3</v></c></row>` +
		row3 + `</sheetData></worksheet>`
}

// Ошибка отдельной ячейки обязана отбраковывать всю книгу (fail-fast): excelize
// в rowXMLHandler игнорирует ошибки cellXMLHandler и getValueFrom, и битая
// ячейка приезжала в DSL пустым значением — тихая порча данных (круг 3 #1470).
func TestImportRows_CellLevelCorruptionRejectsWholeBook(t *testing.T) {
	cases := []struct {
		name string
		row3 string
	}{
		{
			// Нечисловой индекс стиля: cellXMLAttrHandler в excelize возвращает
			// ошибку, rowXMLHandler её проглатывает — ячейка терялась.
			name: "broken style attribute",
			row3: `<row r="3"><c r="A3" s="bad" t="inlineStr"><is><t>012</t></is></c><c r="B3" t="s"><v>999999</v></c></row>`,
		},
		{
			// Ссылка на shared string за пределами таблицы: getValueFrom в
			// excelize ошибается, ошибка проглатывается — пустое название.
			name: "shared string index out of range",
			row3: `<row r="3"><c r="A3" t="s"><v>999999</v></c><c r="B3" t="s"><v>3</v></c></row>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeRawBook(t, rawBookSheet(tc.row3), rawBookStrings)
			rows, err := ImportRows(path)
			if !errors.Is(err, errImportXML) {
				t.Fatalf("ожидали отказ структуры книги, получили rows=%v err=%v", rows, err)
			}
		})
	}

	// Контроль: здоровая книга из тех же деталей импортируется — строгость
	// не отсекает корректные файлы.
	path := writeRawBook(t,
		rawBookSheet(`<row r="3"><c r="A3" t="s"><v>2</v></c><c r="B3" t="s"><v>3</v></c></row>`),
		rawBookStrings)
	rows, err := ImportRows(path)
	if err != nil {
		t.Fatalf("здоровая книга отклонена: %v", err)
	}
	if len(rows) != 3 || rows[2][0] != "007" || rows[2][1] != "First" {
		t.Fatalf("неверные данные здоровой книги: %v", rows)
	}
}
