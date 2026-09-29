package parser1c

import (
	"path/filepath"
	"testing"
)

// Подчинённый справочник 1С приезжает подчинённым и в OneBase: без этого импорт
// молча превращал бы «договоры своего контрагента» в общий список из всех
// договоров базы.
func TestParseCatalogOwner(t *testing.T) {
	dir := t.TempDir()
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject xmlns="http://v8.1c.ru/8.3/MDClasses">
  <Catalog>
    <Properties>
      <Name>Договоры</Name>
      <Owners>
        <Owner>CatalogRef.Контрагенты</Owner>
      </Owners>
      <Hierarchical>false</Hierarchical>
    </Properties>
  </Catalog>
</MetaDataObject>`
	writeObj(t, filepath.Join(dir, "Catalogs"), "Договоры", xml)
	cats, err := parseCatalogs(filepath.Join(dir, "Catalogs"))
	if err != nil {
		t.Fatalf("parseCatalogs: %v", err)
	}
	if len(cats) != 1 {
		t.Fatalf("справочников = %d, ждали один: %+v", len(cats), cats)
	}
	if cats[0].Owner != "Контрагенты" {
		t.Fatalf("владелец = %q, ждали «Контрагенты»", cats[0].Owner)
	}
}

// Владельцем в 1С бывает не только справочник (план видов характеристик).
// Такого владельца пропускаем: у OneBase владельцем может быть только
// справочник, и молча подставить чужую ссылку нельзя.
func TestOwnerCatalogNameSkipsNonCatalog(t *testing.T) {
	if got := ownerCatalogName([]string{"ChartOfCharacteristicTypesRef.Виды"}); got != "" {
		t.Fatalf("владелец = %q, ждали пусто", got)
	}
	if got := ownerCatalogName([]string{"ChartOfCharacteristicTypesRef.Виды", "CatalogRef.Контрагенты"}); got != "Контрагенты" {
		t.Fatalf("владелец = %q, ждали «Контрагенты»", got)
	}
}
