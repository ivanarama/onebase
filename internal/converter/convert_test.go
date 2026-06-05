package converter_test

// Сквозной регрессионный тест конвертера: выгрузка 1С (v8.3 XML) → onebase-проект.
// Проверяет, что Convert проходит целиком и раскладывает YAML-объекты по папкам
// с ожидаемым содержимым. Без этого теста любая правка парсера/писателя ломает
// конвертацию незаметно (пакет converter ранее имел 0% покрытия).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/converter"
)

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
    </ChildObjects>
  </Document>
</MetaDataObject>`

func writeV83(t *testing.T, kindDir, objName, xml string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(kindDir, objName), 0o755); err != nil {
		t.Fatalf("mkdir object: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kindDir, objName+".xml"), []byte(xml), 0o644); err != nil {
		t.Fatalf("write xml: %v", err)
	}
}

func TestConvertEndToEnd(t *testing.T) {
	src := t.TempDir()
	out := filepath.Join(t.TempDir(), "result")
	writeV83(t, filepath.Join(src, "Catalogs"), "Контрагенты", catalogXML)
	writeV83(t, filepath.Join(src, "Documents"), "РеализацияТоваров", documentXML)

	report, err := converter.Convert(converter.Options{SourceDir: src, OutDir: out})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.Catalogs != 1 || report.Documents != 1 {
		t.Fatalf("отчёт: ожидалось 1 справочник и 1 документ, получено %+v", report)
	}

	// Файл справочника создан и содержит имя.
	catYAML := readFile(t, filepath.Join(out, "catalogs", "контрагенты.yaml"))
	if !strings.Contains(catYAML, "Контрагенты") {
		t.Errorf("в catalogs/контрагенты.yaml нет имени справочника:\n%s", catYAML)
	}
	if !strings.Contains(catYAML, "ИНН") {
		t.Errorf("в catalogs/контрагенты.yaml нет реквизита ИНН:\n%s", catYAML)
	}
	// Стандартные реквизиты справочника 1С (issue #26 п.2).
	if !strings.Contains(catYAML, "Код") || !strings.Contains(catYAML, "Наименование") {
		t.Errorf("в справочнике нет стандартных Код/Наименование:\n%s", catYAML)
	}

	// Отчёт не должен показывать фантомную константу (issue #26 п.5).
	rep := readFile(t, filepath.Join(out, "conversion_report.txt"))
	if !strings.Contains(rep, "Констант:              0 → 0 YAML") {
		t.Errorf("отчёт показывает неверное число констант:\n%s", rep)
	}

	// Файл документа создан.
	docYAML := readFile(t, filepath.Join(out, "documents", "реализациятоваров.yaml"))
	if !strings.Contains(docYAML, "Контрагент") {
		t.Errorf("в документе нет реквизита Контрагент:\n%s", docYAML)
	}

	// Служебные артефакты.
	if _, err := os.Stat(filepath.Join(out, "config", "app.yaml")); err != nil {
		t.Errorf("не создан config/app.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "conversion_report.txt")); err != nil {
		t.Errorf("не создан conversion_report.txt: %v", err)
	}
}

// Макеты 1С импортируются как заготовки печатных форм (issue #26 п.3).
func TestConvertTemplatesScaffold(t *testing.T) {
	src := t.TempDir()
	out := filepath.Join(t.TempDir(), "result")
	writeV83(t, filepath.Join(src, "Catalogs"), "Контрагенты", catalogXML)
	// Макет объекта: Catalogs/Контрагенты/Templates/Карточка/Ext/Template.xml
	tmplExt := filepath.Join(src, "Catalogs", "Контрагенты", "Templates", "Карточка", "Ext")
	if err := os.MkdirAll(tmplExt, 0o755); err != nil {
		t.Fatalf("mkdir template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplExt, "Template.xml"), []byte("<Spreadsheet/>"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	report, err := converter.Convert(converter.Options{SourceDir: src, OutDir: out})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.Templates != 1 {
		t.Fatalf("ожидался 1 импортированный макет, получено %d", report.Templates)
	}
	pf := readFile(t, filepath.Join(out, "printforms", "контрагенты_карточка.yaml"))
	if !strings.Contains(pf, "name: Карточка") || !strings.Contains(pf, "document: Контрагенты") {
		t.Errorf("заготовка печатной формы некорректна:\n%s", pf)
	}
	// Исходник макета скопирован рядом.
	if _, err := os.Stat(filepath.Join(out, "printforms", "контрагенты_карточка.src.xml")); err != nil {
		t.Errorf("не скопирован исходник макета: %v", err)
	}
}

const minimalFormXML = `<?xml version="1.0" encoding="UTF-8"?>
<Form xmlns="http://v8.1c.ru/8.3/xcf/logform" xmlns:v8="http://v8.1c.ru/8.1/data/core" version="2.20">
  <Attributes>
    <Attribute name="Объект" id="1">
      <Type><v8:Type>cfg:DocumentRef.РеализацияТоваров</v8:Type></Type>
      <MainAttribute>true</MainAttribute>
    </Attribute>
  </Attributes>
</Form>`

// Управляемые формы объектов импортируются bulk-конвертером через onec_forms
// (issue #26 п.4).
func TestConvertImportsForms(t *testing.T) {
	src := t.TempDir()
	out := filepath.Join(t.TempDir(), "result")
	writeV83(t, filepath.Join(src, "Documents"), "РеализацияТоваров", documentXML)
	extDir := filepath.Join(src, "Documents", "РеализацияТоваров", "Forms", "ФормаДокумента", "Ext")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("mkdir form: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "Form.xml"), []byte(minimalFormXML), 0o644); err != nil {
		t.Fatalf("write form xml: %v", err)
	}

	report, err := converter.Convert(converter.Options{SourceDir: src, OutDir: out})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.Forms != 1 {
		t.Fatalf("ожидалась 1 импортированная форма, получено %d (warnings=%v)", report.Forms, report.FormWarnings)
	}
	if _, err := os.Stat(filepath.Join(out, "forms", "реализациятоваров", "формадокумента.form.yaml")); err != nil {
		t.Errorf("не создан .form.yaml формы: %v", err)
	}
}

func TestConvertRequiresDirs(t *testing.T) {
	if _, err := converter.Convert(converter.Options{OutDir: "x"}); err == nil {
		t.Error("Convert без SourceDir должен вернуть ошибку")
	}
	if _, err := converter.Convert(converter.Options{SourceDir: "x"}); err == nil {
		t.Error("Convert без OutDir должен вернуть ошибку")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	return string(data)
}
