package parser1c

// Регрессионные тесты ядра парсера выгрузки 1С. Фиксируют текущее поведение
// разбора v8.3 MDClasses XML и распознавания типов, чтобы изменения формата
// или рефакторинг не ломали конвертацию незаметно.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseType(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		primary   string
		refObject string
		composite bool
	}{
		{"пусто → string", nil, "string", "", false},
		{"примитив xs:string", []string{"xs:string"}, "xs:string", "", false},
		{"ссылка на справочник", []string{"cfg:CatalogRef.Контрагенты"}, "cfg:CatalogRef.Контрагенты", "Контрагенты", false},
		{"ссылка на документ", []string{"DocumentRef.Реализация"}, "DocumentRef.Реализация", "Реализация", false},
		{"составной тип", []string{"xs:string", "cfg:CatalogRef.Контрагенты"}, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseType(c.in)
			if c.composite {
				if !got.Composite {
					t.Fatalf("ожидался составной тип, получено %+v", got)
				}
				if len(got.AllTypes) != len(c.in) {
					t.Fatalf("AllTypes=%v, ожидалось %v", got.AllTypes, c.in)
				}
				return
			}
			if got.Primary != c.primary {
				t.Errorf("Primary=%q, ожидалось %q", got.Primary, c.primary)
			}
			if got.RefObject != c.refObject {
				t.Errorf("RefObject=%q, ожидалось %q", got.RefObject, c.refObject)
			}
		})
	}
}

// writeV83 создаёт пару «директория объекта + sibling .xml» в стиле выгрузки
// 1С v8.3, как её ожидает ParseDir.
func writeV83(t *testing.T, kindDir, objName, xml string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(kindDir, objName), 0o755); err != nil {
		t.Fatalf("mkdir object: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kindDir, objName+".xml"), []byte(xml), 0o644); err != nil {
		t.Fatalf("write xml: %v", err)
	}
}

const catalogXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Catalog>
    <Properties><Name>Контрагенты</Name></Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>ИНН</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:string</Type></Type>
      </Properties></Attribute>
    </ChildObjects>
  </Catalog>
</MetaDataObject>`

const documentXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Document>
    <Properties><Name>РеализацияТоваров</Name></Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>Контрагент</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">cfg:CatalogRef.Контрагенты</Type></Type>
      </Properties></Attribute>
      <TabularSection>
        <Properties><Name>Товары</Name></Properties>
        <ChildObjects>
          <Attribute><Properties>
            <Name>Количество</Name>
            <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:decimal</Type></Type>
          </Properties></Attribute>
        </ChildObjects>
      </TabularSection>
    </ChildObjects>
  </Document>
</MetaDataObject>`

func TestParseDirV83(t *testing.T) {
	src := t.TempDir()
	writeV83(t, filepath.Join(src, "Catalogs"), "Контрагенты", catalogXML)
	writeV83(t, filepath.Join(src, "Documents"), "РеализацияТоваров", documentXML)

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}

	// Справочник.
	if len(dump.Catalogs) != 1 {
		t.Fatalf("ожидался 1 справочник, получено %d", len(dump.Catalogs))
	}
	cat := dump.Catalogs[0]
	if cat.Name != "Контрагенты" {
		t.Errorf("имя справочника: %q", cat.Name)
	}
	if len(cat.Attributes) != 1 || cat.Attributes[0].Name != "ИНН" {
		t.Fatalf("реквизиты справочника: %+v", cat.Attributes)
	}
	if cat.Attributes[0].Type.Primary != "xs:string" {
		t.Errorf("тип ИНН: %+v", cat.Attributes[0].Type)
	}

	// Документ с табличной частью и ссылочным реквизитом.
	if len(dump.Documents) != 1 {
		t.Fatalf("ожидался 1 документ, получено %d", len(dump.Documents))
	}
	doc := dump.Documents[0]
	if doc.Name != "РеализацияТоваров" {
		t.Errorf("имя документа: %q", doc.Name)
	}
	if len(doc.Attributes) != 1 || doc.Attributes[0].Type.RefObject != "Контрагенты" {
		t.Fatalf("ссылочный реквизит документа разобран неверно: %+v", doc.Attributes)
	}
	if len(doc.TabularSections) != 1 {
		t.Fatalf("ожидалась 1 табличная часть, получено %d", len(doc.TabularSections))
	}
	ts := doc.TabularSections[0]
	if ts.Name != "Товары" || len(ts.Attributes) != 1 || ts.Attributes[0].Name != "Количество" {
		t.Fatalf("табличная часть разобрана неверно: %+v", ts)
	}
}

const enumXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Enumeration>
    <Properties><Name>ВидКонтрагента</Name></Properties>
    <ChildObjects>
      <EnumValue><Properties><Name>ЮрЛицо</Name></Properties></EnumValue>
      <EnumValue><Properties><Name>ФизЛицо</Name></Properties></EnumValue>
    </ChildObjects>
  </Enumeration>
</MetaDataObject>`

const enumAliasXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Enum>
    <Properties><Name>RateType</Name></Properties>
    <ChildObjects>
      <EnumValue><Properties><Name>Fixed</Name></Properties></EnumValue>
      <EnumValue><Properties><Name>Floating</Name></Properties></EnumValue>
    </ChildObjects>
  </Enum>
</MetaDataObject>`

// Перечисление в выгрузке 1С часто лежит ОДНИМ файлом «Имя.xml» без папки.
// Раньше parseEnumerations перебирал только подкаталоги и пропускал такой enum
// (issue #16: «Перечислений: 0 → 0»). Проверяем оба представления.
func TestParseDirEnumerations(t *testing.T) {
	src := t.TempDir()
	enumsDir := filepath.Join(src, "Enumerations")
	if err := os.MkdirAll(enumsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Одиночный .xml без папки-компаньона.
	if err := os.WriteFile(filepath.Join(enumsDir, "ВидКонтрагента.xml"), []byte(enumXML), 0o644); err != nil {
		t.Fatalf("write enum xml: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Enums) != 1 {
		t.Fatalf("ожидалось 1 перечисление, получено %d", len(dump.Enums))
	}
	em := dump.Enums[0]
	if em.Name != "ВидКонтрагента" {
		t.Errorf("имя перечисления: %q", em.Name)
	}
	if len(em.Values) != 2 || em.Values[0] != "ЮрЛицо" || em.Values[1] != "ФизЛицо" {
		t.Fatalf("значения перечисления разобраны неверно: %+v", em.Values)
	}
}

func TestParseDirEnumsAliasSectionAndTag(t *testing.T) {
	src := t.TempDir()
	enumsDir := filepath.Join(src, "Enums")
	if err := os.MkdirAll(enumsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(enumsDir, "RateType.xml"), []byte(enumAliasXML), 0o644); err != nil {
		t.Fatalf("write enum xml: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Enums) != 1 {
		t.Fatalf("ожидалось 1 перечисление из Enums/<Enum>, получено %d", len(dump.Enums))
	}
	em := dump.Enums[0]
	if em.Name != "RateType" {
		t.Errorf("имя перечисления: %q", em.Name)
	}
	if len(em.Values) != 2 || em.Values[0] != "Fixed" || em.Values[1] != "Floating" {
		t.Fatalf("значения перечисления разобраны неверно: %+v", em.Values)
	}
}

// Неизвестные/непарсящиеся разделы не должны валить разбор и НЕ должны
// превращаться в справочники (issue #26 п.1) — они уходят в SkippedDirs.
func TestParseDirUnknownSection(t *testing.T) {
	src := t.TempDir()
	junk := filepath.Join(src, "ОченьСтранныйРаздел", "Объект1")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir не должен падать на неизвестном разделе: %v", err)
	}
	if len(dump.Catalogs) != 0 {
		t.Fatalf("неизвестный раздел не должен давать справочников, получено %d", len(dump.Catalogs))
	}
	if len(dump.SkippedDirs) == 0 {
		t.Fatalf("неизвестный раздел должен попасть в SkippedDirs")
	}
}

// Подсистемы и общие картинки (логотип) — не прикладные данные и не должны
// становиться справочниками (issue #26 п.1, #16 п.4).
func TestParseDirSkipsNonApplied(t *testing.T) {
	src := t.TempDir()
	writeV83(t, filepath.Join(src, "Catalogs"), "Контрагенты", catalogXML)
	// Подсистема одним файлом + папкой.
	subDir := filepath.Join(src, "Subsystems", "MasterData")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir subsystem: %v", err)
	}
	// Логотип в CommonPictures.
	picDir := filepath.Join(src, "CommonPictures", "Логотип")
	if err := os.MkdirAll(picDir, 0o755); err != nil {
		t.Fatalf("mkdir picture: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Catalogs) != 1 || dump.Catalogs[0].Name != "Контрагенты" {
		t.Fatalf("ожидался ровно 1 справочник (Контрагенты), получено %+v", dump.Catalogs)
	}
	var skippedNames []string
	for _, s := range dump.SkippedDirs {
		skippedNames = append(skippedNames, s.Kind+"/"+s.Name)
	}
	wantSkipped := map[string]bool{"Subsystems/MasterData": false, "CommonPictures/Логотип": false}
	for _, n := range skippedNames {
		if _, ok := wantSkipped[n]; ok {
			wantSkipped[n] = true
		}
	}
	for n, seen := range wantSkipped {
		if !seen {
			t.Errorf("ожидался пропущенный объект %q, skipped=%v", n, skippedNames)
		}
	}
}

const catalogWithTSXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Catalog>
    <Properties><Name>Номенклатура</Name></Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>Артикул</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:string</Type></Type>
      </Properties></Attribute>
      <TabularSection>
        <Properties><Name>ЦеныПоставщиков</Name></Properties>
        <ChildObjects>
          <Attribute><Properties>
            <Name>Цена</Name>
            <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:decimal</Type></Type>
          </Properties></Attribute>
        </ChildObjects>
      </TabularSection>
    </ChildObjects>
  </Catalog>
</MetaDataObject>`

// Справочник в 1С может иметь табличную часть — она должна разбираться
// (issue #26 п.2).
func TestParseDirCatalogTabularSection(t *testing.T) {
	src := t.TempDir()
	writeV83(t, filepath.Join(src, "Catalogs"), "Номенклатура", catalogWithTSXML)

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Catalogs) != 1 {
		t.Fatalf("ожидался 1 справочник, получено %d", len(dump.Catalogs))
	}
	cat := dump.Catalogs[0]
	if len(cat.TabularSections) != 1 || cat.TabularSections[0].Name != "ЦеныПоставщиков" {
		t.Fatalf("табличная часть справочника разобрана неверно: %+v", cat.TabularSections)
	}
	ts := cat.TabularSections[0]
	if len(ts.Attributes) != 1 || ts.Attributes[0].Name != "Цена" {
		t.Fatalf("реквизиты ТЧ справочника разобраны неверно: %+v", ts.Attributes)
	}
}

// Объект без подчинённых элементов (форм, макетов) лежит в выгрузке ОДНИМ
// файлом «Имя.xml» без папки-компаньона. Раньше все парсеры, кроме
// parseEnumerations, перебирали только подкаталоги и молча теряли такие
// объекты (issue #48 п.1: «не импортированы 2 справочника и 4 константы»).
func TestParseDirFlatCatalog(t *testing.T) {
	src := t.TempDir()
	catsDir := filepath.Join(src, "Catalogs")
	if err := os.MkdirAll(catsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Одиночный .xml без папки-компаньона.
	if err := os.WriteFile(filepath.Join(catsDir, "Контрагенты.xml"), []byte(catalogXML), 0o644); err != nil {
		t.Fatalf("write xml: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Catalogs) != 1 {
		t.Fatalf("ожидался 1 справочник из плоского xml, получено %d", len(dump.Catalogs))
	}
	if dump.Catalogs[0].Name != "Контрагенты" {
		t.Errorf("имя справочника: %q", dump.Catalogs[0].Name)
	}
	if len(dump.Catalogs[0].Attributes) != 1 || dump.Catalogs[0].Attributes[0].Name != "ИНН" {
		t.Fatalf("реквизиты не разобраны: %+v", dump.Catalogs[0].Attributes)
	}
}

const constantXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Constant>
    <Properties>
      <Name>ВалютаУчета</Name>
      <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:string</Type></Type>
    </Properties>
  </Constant>
</MetaDataObject>`

const infoRegFlatXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <InformationRegister>
    <Properties><Name>КурсыВалют</Name></Properties>
    <ChildObjects>
      <Dimension><Properties>
        <Name>Валюта</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:string</Type></Type>
      </Properties></Dimension>
      <Resource><Properties>
        <Name>Курс</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">xs:decimal</Type></Type>
      </Properties></Resource>
    </ChildObjects>
  </InformationRegister>
</MetaDataObject>`

// Константы и регистры сведений тоже бывают «плоскими» (issue #48 п.1:
// «НЕ ИМПОРТИРОВАНЫ 4 КОНСТАНТЫ»).
func TestParseDirFlatConstantsAndInfoRegs(t *testing.T) {
	src := t.TempDir()
	for dir, files := range map[string]map[string]string{
		"Constants":            {"ВалютаУчета.xml": constantXML},
		"InformationRegisters": {"КурсыВалют.xml": infoRegFlatXML},
	} {
		if err := os.MkdirAll(filepath.Join(src, dir), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(src, dir, name), []byte(content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Constants) != 1 || dump.Constants[0].Name != "ВалютаУчета" {
		t.Fatalf("константа из плоского xml не разобрана: %+v", dump.Constants)
	}
	if len(dump.InfoRegisters) != 1 || dump.InfoRegisters[0].Name != "КурсыВалют" {
		t.Fatalf("регистр сведений из плоского xml не разобран: %+v", dump.InfoRegisters)
	}
	if len(dump.InfoRegisters[0].Dimensions) != 1 || dump.InfoRegisters[0].Dimensions[0].Name != "Валюта" {
		t.Fatalf("измерения регистра не разобраны: %+v", dump.InfoRegisters[0].Dimensions)
	}
}

// Платформа поддерживает тип enum:Имя (metadata/yaml.go), а конвертер
// деградировал ссылки на перечисления в string (issue #48 п.5).
func TestMapTypeEnumRef(t *testing.T) {
	for _, primary := range []string{"cfg:EnumRef.ВидКонтрагента", "ПеречислениеСсылка.ВидКонтрагента"} {
		got, note := MapType(FieldType{Primary: primary, RefObject: "ВидКонтрагента"})
		if got != "enum:ВидКонтрагента" {
			t.Errorf("%s → %q, ожидалось enum:ВидКонтрагента", primary, got)
		}
		if note != "" {
			t.Errorf("%s: неожиданное предупреждение %q", primary, note)
		}
	}
}

const processorXML = `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <DataProcessor>
    <Properties><Name>ЗагрузкаКурсов</Name></Properties>
    <ChildObjects/>
  </DataProcessor>
</MetaDataObject>`

// Формы обработок поддерживаются платформой (loadProcessorForms грузит их из
// forms/<обработка>/), но конвертер их не сканировал (issue #48 п.3).
func TestParseDirProcessorForms(t *testing.T) {
	src := t.TempDir()
	writeV83(t, filepath.Join(src, "DataProcessors"), "ЗагрузкаКурсов", processorXML)
	extDir := filepath.Join(src, "DataProcessors", "ЗагрузкаКурсов", "Forms", "Форма", "Ext")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("mkdir form: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "Form.xml"), []byte("<Form/>"), 0o644); err != nil {
		t.Fatalf("write form xml: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(dump.Processors) != 1 {
		t.Fatalf("ожидалась 1 обработка, получено %d", len(dump.Processors))
	}
	p := dump.Processors[0]
	if len(p.Forms) != 1 {
		t.Fatalf("ожидалась 1 форма обработки, получено %d", len(p.Forms))
	}
	if p.Forms[0].Entity != "ЗагрузкаКурсов" || p.Forms[0].FormName != "Форма" {
		t.Errorf("источник формы: %+v", p.Forms[0])
	}
}

// scanForms находит управляемые формы объекта в Forms/<X>/Ext/Form.xml
// (issue #26 п.4).
func TestParseDirFindsForms(t *testing.T) {
	src := t.TempDir()
	writeV83(t, filepath.Join(src, "Catalogs"), "Контрагенты", catalogXML)
	extDir := filepath.Join(src, "Catalogs", "Контрагенты", "Forms", "ФормаЭлемента", "Ext")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("mkdir form: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "Form.xml"), []byte("<Form/>"), 0o644); err != nil {
		t.Fatalf("write form xml: %v", err)
	}

	dump, err := ParseDir(src)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	cat := dump.Catalogs[0]
	if len(cat.Forms) != 1 {
		t.Fatalf("ожидалась 1 форма, получено %d", len(cat.Forms))
	}
	f := cat.Forms[0]
	if f.Entity != "Контрагенты" || f.FormName != "ФормаЭлемента" {
		t.Errorf("источник формы разобран неверно: %+v", f)
	}
}
