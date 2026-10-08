package xlsximport

// Регрессионный тест GO-2026-6452 (CVE-2026-59162, GHSA-fx5j-qcqg-grpf):
// недоверенный .xlsx с отрицательным индексом shared-строк (t="s", <v>-1</v>)
// раньше паниковал внутри excelize при разборе (sharedStrings[-1]). Проверяем,
// что публичный API возвращает ошибку, а не роняет процесс. В go.mod запинен
// upstream-коммит с исправлением (см. tools/govulnpolicy/main.go) — при откате
// пина этот тест продолжает проходить благодаря recover в ImportBytes, но CI
// в job vuln снова станет красным: policy-фильтр требует версию с фиксом.

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildMaliciousXLSX собирает минимальную книгу, где ячейка A1 ссылается на
// shared-строку с индексом -1.
func buildMaliciousXLSX(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
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
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
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

// TestImportBytes_NegativeSharedStringNoPanic: на книге с атакой импорт не
// паникует. Исправленная excelize (пин в go.mod) возвращает для битой ячейки
// пустую строку — импорт проходит. На уязвимой версии (релизы до фикса,
// GO-2026-6452) разбор паниковал: recover в ImportBytes превратил бы панику
// в ErrParse, и этот тест упал — он реально ловит откат пина.
// См. tools/govulnpolicy/main.go.
func TestImportBytes_NegativeSharedStringNoPanic(t *testing.T) {
	data := buildMaliciousXLSX(t)

	res, err := ImportBytes(data, Options{})
	if err != nil {
		t.Fatalf("исправленная excelize не должна давать ошибку на отрицательном shared-индексе (ожидается пустая строка в ячейке), получено: %v", err)
	}
	if res == nil || res.Layout == nil {
		t.Fatal("импорт не вернул черновик макета")
	}
}
