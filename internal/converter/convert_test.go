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

// Поле с EnumRef.X: если перечисление импортировано — в YAML уходит enum:X;
// если его нет в выгрузке — деградация в string с предупреждением, иначе
// конфигурация не пройдёт metadata.Validate (ревью задачи 5, issue #48 п.5).
func TestConvertEnumFieldResolution(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()

	catXML := `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Catalog>
    <Properties><Name>Контрагенты</Name></Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>Вид</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">cfg:EnumRef.ВидКонтрагента</Type></Type>
      </Properties></Attribute>
      <Attribute><Properties>
        <Name>Статус</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">cfg:EnumRef.НесуществующееПеречисление</Type></Type>
      </Properties></Attribute>
    </ChildObjects>
  </Catalog>
</MetaDataObject>`
	enumXML := `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Enumeration>
    <Properties><Name>ВидКонтрагента</Name></Properties>
    <ChildObjects>
      <EnumValue><Properties><Name>ЮрЛицо</Name></Properties></EnumValue>
    </ChildObjects>
  </Enumeration>
</MetaDataObject>`

	if err := os.MkdirAll(filepath.Join(src, "Catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Catalogs", "Контрагенты.xml"), []byte(catXML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "Enumerations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Enumerations", "ВидКонтрагента.xml"), []byte(enumXML), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := converter.Convert(converter.Options{SourceDir: src, OutDir: out})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "catalogs", "контрагенты.yaml"))
	if err != nil {
		t.Fatalf("каталог не записан: %v", err)
	}
	y := string(data)
	if !strings.Contains(y, "type: enum:ВидКонтрагента") {
		t.Errorf("импортированное перечисление должно дать enum:-тип, yaml:\n%s", y)
	}
	if strings.Contains(y, "НесуществующееПеречисление") {
		t.Errorf("висячая ссылка не должна остаться enum:-типом, yaml:\n%s", y)
	}
	var found bool
	for _, w := range report.TypeWarnings {
		if strings.Contains(w, "НесуществующееПеречисление") {
			found = true
		}
	}
	if !found {
		t.Errorf("нет предупреждения о висячем перечислении: %v", report.TypeWarnings)
	}
}

func TestConvertEnumAliasSectionResolution(t *testing.T) {
	src := t.TempDir()
	out := t.TempDir()

	catXML := `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Catalog>
    <Properties><Name>Currency</Name></Properties>
    <ChildObjects>
      <Attribute><Properties>
        <Name>RateType</Name>
        <Type><Type xmlns="http://v8.1c.ru/8.1/data/core">cfg:EnumRef.RateType</Type></Type>
      </Properties></Attribute>
    </ChildObjects>
  </Catalog>
</MetaDataObject>`
	enumXML := `<?xml version="1.0" encoding="UTF-8"?>
<MetaDataObject>
  <Enum>
    <Properties><Name>RateType</Name></Properties>
    <ChildObjects>
      <EnumValue><Properties><Name>Fixed</Name></Properties></EnumValue>
      <EnumValue><Properties><Name>Floating</Name></Properties></EnumValue>
    </ChildObjects>
  </Enum>
</MetaDataObject>`

	if err := os.MkdirAll(filepath.Join(src, "Catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Catalogs", "Currency.xml"), []byte(catXML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "Enums"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Enums", "RateType.xml"), []byte(enumXML), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := converter.Convert(converter.Options{SourceDir: src, OutDir: out})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if report.Enums != 1 {
		t.Fatalf("converted enums = %d, want 1", report.Enums)
	}

	catData, err := os.ReadFile(filepath.Join(out, "catalogs", "currency.yaml"))
	if err != nil {
		t.Fatalf("каталог не записан: %v", err)
	}
	if y := string(catData); !strings.Contains(y, "type: enum:RateType") {
		t.Fatalf("enum reference degraded, yaml:\n%s", y)
	}
	enumData, err := os.ReadFile(filepath.Join(out, "enums", "ratetype.yaml"))
	if err != nil {
		t.Fatalf("перечисление не записано: %v", err)
	}
	enumYAML := string(enumData)
	for _, want := range []string{"name: RateType", "- Fixed", "- Floating"} {
		if !strings.Contains(enumYAML, want) {
			t.Fatalf("enum yaml does not contain %q:\n%s", want, enumYAML)
		}
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
