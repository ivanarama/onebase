package xlsxtemplate

// Регрессионный тест GO-2026-6452 (CVE-2026-59162): недоверенный .xlsx с
// отрицательным индексом shared-строк не должен паниковать при рендере —
// RenderBytes стоит на пути пользовательских печатных форм. См. также
// internal/xlsximport/security_test.go и tools/govulnpolicy/main.go.

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/ivantit66/onebase/internal/printform"
)

// TestRenderBytes_MaliciousTemplateNoPanic: на книге с атакой рендер не
// паникует. Исправленная excelize возвращает для битой ячейки пустую строку
// (GetRows: [["" "ok"]]) — рендер проходит успешно. На уязвимой версии
// (релизы до фикса, GO-2026-6452) GetRows паниковал: recover в RenderBytes
// превратил бы панику в ошибку, и этот тест упал — то есть он реально ловит
// откат пина из go.mod. См. tools/govulnpolicy/main.go.
func TestRenderBytes_MaliciousTemplateNoPanic(t *testing.T) {
	// Та же атакующая книга, что в xlsximport: ячейка A1 = shared-строка -1.
	// Дублируем сборку локально, чтобы тест не зависел от внутренностей соседа.
	data := buildNegativeSharedStringXLSX(t)

	out, err := RenderBytes(data, &printform.RenderContext{})
	if err != nil {
		t.Fatalf("исправленная excelize не должна давать ошибку на отрицательном shared-индексе (ожидается пустая строка в ячейке), получено: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("рендер вернул пустой результат")
	}
}

// buildXLSXFromParts пакует минимальную книгу из пар «путь → xml».
func buildXLSXFromParts(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func buildNegativeSharedStringXLSX(t *testing.T) []byte {
	t.Helper()
	return buildXLSXFromParts(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>
</Relationships>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="1" uniqueCount="1"><si><t>обычная строка</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<sheetData><row r="1"><c r="A1" t="s"><v>-1</v></c><c r="B1" t="s"><v>0</v></c></row></sheetData>
</worksheet>`,
	})
}
