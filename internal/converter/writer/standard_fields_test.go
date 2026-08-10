package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
)

// Импортированный из 1С документ приезжал вообще без «Номера», «Даты», блока
// numerator: и признака posting — WriteDocuments звал convertFields напрямую,
// минуя стандартные реквизиты, которые справочник получал. Из-за этого жалоба
// «в документах нет НОМЕРА» (issue #658) относилась к конвертеру, а не к
// движку: автонумерацию документов платформа умеет и делает атомарно
// (план 117, Д6).

func readOut(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	return string(b)
}

func TestWriteDocuments_ДобавляетСтандартныеРеквизитыИНумератор(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	docs := []*parser1c.DocumentMeta{{
		Name:       "РеализацияТоваров",
		Attributes: []parser1c.Attribute{{Name: "Контрагент", Type: parser1c.FieldType{Primary: "xs:string"}}},
		Posting:    true,
		Number:     parser1c.Numbering{Auto: true, Length: 11, Type: "String", Periodicity: "Year"},
	}}
	if err := WriteDocuments(docs, out, notes); err != nil {
		t.Fatalf("WriteDocuments: %v", err)
	}
	got := readOut(t, filepath.Join(out, "documents", fileName("РеализацияТоваров")+".yaml"))
	for _, must := range []string{"name: Номер", "name: Дата", "posting: true", "numerator:", "length: 11", "period: year"} {
		if !strings.Contains(got, must) {
			t.Errorf("в YAML документа нет %q:\n%s", must, got)
		}
	}
	// Стандартные реквизиты идут первыми, как в 1С.
	if strings.Index(got, "name: Номер") > strings.Index(got, "name: Контрагент") {
		t.Errorf("«Номер» должен идти перед пользовательскими реквизитами:\n%s", got)
	}
}

// Документ без автонумерации получает «Номер» и «Дату», но без блока numerator:
// — иначе конвертер навязал бы нумерацию там, где её в 1С не было.
func TestWriteDocuments_БезАвтонумерацииНетБлока(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	docs := []*parser1c.DocumentMeta{{Name: "Заявка"}}
	if err := WriteDocuments(docs, out, notes); err != nil {
		t.Fatalf("WriteDocuments: %v", err)
	}
	got := readOut(t, filepath.Join(out, "documents", fileName("Заявка")+".yaml"))
	if strings.Contains(got, "numerator:") {
		t.Errorf("блок numerator: не должен появляться без автонумерации:\n%s", got)
	}
	if !strings.Contains(got, "name: Номер") || !strings.Contains(got, "name: Дата") {
		t.Errorf("стандартные реквизиты обязаны быть всегда:\n%s", got)
	}
}

// Уже объявленные пользователем «Номер»/«Дата» не дублируются.
func TestWriteDocuments_НеДублируетСуществующие(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	docs := []*parser1c.DocumentMeta{{
		Name:       "Заявка",
		Attributes: []parser1c.Attribute{{Name: "Номер", Type: parser1c.FieldType{Primary: "xs:string"}}},
	}}
	if err := WriteDocuments(docs, out, notes); err != nil {
		t.Fatalf("WriteDocuments: %v", err)
	}
	got := readOut(t, filepath.Join(out, "documents", fileName("Заявка")+".yaml"))
	if strings.Count(got, "name: Номер") != 1 {
		t.Errorf("«Номер» задвоился:\n%s", got)
	}
}

// Иерархия справочника переносится, а то, что платформа пока не умеет
// (автонумерация кода, контроль уникальности), попадает в отчёт, а не пропадает
// молча.
func TestWriteCatalogs_ИерархияИОтчётПоКоду(t *testing.T) {
	out := t.TempDir()
	notes := &ConversionReport{}
	cats := []*parser1c.CatalogMeta{{
		Name:         "Контрагенты",
		Hierarchical: true,
		Code:         parser1c.Numbering{Auto: true, Length: 9, Type: "String", CheckUnique: true},
	}}
	if err := WriteCatalogs(cats, out, notes); err != nil {
		t.Fatalf("WriteCatalogs: %v", err)
	}
	got := readOut(t, filepath.Join(out, "catalogs", fileName("Контрагенты")+".yaml"))
	if !strings.Contains(got, "hierarchical: true") {
		t.Errorf("иерархия не перенесена:\n%s", got)
	}
	if strings.Contains(got, "numerator:") {
		t.Errorf("блок numerator: у справочника пока no-op, писать его нельзя:\n%s", got)
	}
	joined := strings.Join(notes.TypeWarnings, "\n")
	if !strings.Contains(joined, "автонумерация кода не перенесена") {
		t.Errorf("в отчёте нет предупреждения об автонумерации кода: %v", notes.TypeWarnings)
	}
	if !strings.Contains(joined, "контроль уникальности кода не перенесён") {
		t.Errorf("в отчёте нет предупреждения о контроле уникальности: %v", notes.TypeWarnings)
	}
}
