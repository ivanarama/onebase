package parser1c

import (
	"os"
	"path/filepath"
	"testing"
)

// Свойства кода справочника и номера документа парсер не читал вовсе: в
// CatalogMeta/DocumentMeta не было ни одного соответствующего поля, а
// xml-структуры их не объявляли. Автонумерация, длина, периодичность,
// контроль уникальности и признак иерархии терялись молча — в отчёте
// конвертации об этом не было ни строки (план 117, Д8).

const catalogWithCodeXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Catalog>
    <Properties>
      <Name>Контрагенты</Name>
      <Hierarchical>true</Hierarchical>
      <CodeLength>9</CodeLength>
      <CodeType>String</CodeType>
      <CheckUnique>true</CheckUnique>
      <Autonumbering>true</Autonumbering>
    </Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>ИНН</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:string</Type></Type>
      </Properties></Attribute>
    </ChildObjects>
  </Catalog>
</MetaDataObject>`

const documentWithNumberXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Document>
    <Properties>
      <Name>РеализацияТоваров</Name>
      <Posting>Allow</Posting>
      <NumberType>String</NumberType>
      <NumberLength>11</NumberLength>
      <NumberPeriodicity>Year</NumberPeriodicity>
      <Autonumbering>true</Autonumbering>
    </Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>Контрагент</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">cfg:CatalogRef.Контрагенты</Type></Type>
      </Properties></Attribute>
    </ChildObjects>
  </Document>
</MetaDataObject>`

func writeObj(t *testing.T, dir, name, xml string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".xml"), []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParse_ЧитаетСвойстваКодаИНомера(t *testing.T) {
	src := t.TempDir()
	writeObj(t, filepath.Join(src, "Catalogs"), "Контрагенты", catalogWithCodeXML)
	writeObj(t, filepath.Join(src, "Documents"), "РеализацияТоваров", documentWithNumberXML)

	cats, err := parseCatalogs(filepath.Join(src, "Catalogs"))
	if err != nil {
		t.Fatalf("parseCatalogs: %v", err)
	}
	if len(cats) != 1 {
		t.Fatalf("справочников %d, ожидался 1", len(cats))
	}
	c := cats[0]
	if !c.Hierarchical {
		t.Error("иерархия справочника не прочитана")
	}
	if !c.Code.Auto || c.Code.Length != 9 || c.Code.Type != "String" || !c.Code.CheckUnique {
		t.Errorf("свойства кода прочитаны неверно: %+v", c.Code)
	}

	docs, err := parseDocuments(filepath.Join(src, "Documents"))
	if err != nil {
		t.Fatalf("parseDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("документов %d, ожидался 1", len(docs))
	}
	d := docs[0]
	if !d.Posting {
		t.Error("признак проведения не прочитан")
	}
	if !d.Number.Auto || d.Number.Length != 11 || d.Number.Periodicity != "Year" {
		t.Errorf("свойства номера прочитаны неверно: %+v", d.Number)
	}
}
