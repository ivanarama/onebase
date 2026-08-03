package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/converter/parser1c"
)

func TestSynonymTitle(t *testing.T) {
	cases := []struct{ name, syn, want string }{
		{"Номенклатура", "Номенклатура", ""}, // совпадает с именем — отбрасываем
		{"Номенклатура", "", ""},             // пустой синоним
		{"X", "  ", ""},                      // только пробелы
		{"ПоступлениеНаРасчётныйСчёт", "Поступление на расчётный счёт", "Поступление на расчётный счёт"},
	}
	for _, c := range cases {
		if got := synonymTitle(c.name, c.syn); got != c.want {
			t.Errorf("synonymTitle(%q,%q)=%q, ожидалось %q", c.name, c.syn, got, c.want)
		}
	}
}

// Синоним 1С переносится в title YAML справочника; совпадающий с именем — нет.
func TestWriteCatalogs_Title(t *testing.T) {
	dir := t.TempDir()
	cats := []*parser1c.CatalogMeta{
		{Name: "ПоступлениеНаРасчётныйСчёт", Synonym: "Поступление на расчётный счёт"},
		{Name: "Номенклатура", Synonym: "Номенклатура"},
	}
	if err := WriteCatalogs(cats, dir, &ConversionReport{}); err != nil {
		t.Fatal(err)
	}

	withTitle, _ := os.ReadFile(filepath.Join(dir, "catalogs", fileName("ПоступлениеНаРасчётныйСчёт")+".yaml"))
	if !strings.Contains(string(withTitle), "title: Поступление на расчётный счёт") {
		t.Errorf("ожидался title в YAML, получено:\n%s", withTitle)
	}
	noTitle, _ := os.ReadFile(filepath.Join(dir, "catalogs", fileName("Номенклатура")+".yaml"))
	if strings.Contains(string(noTitle), "title:") {
		t.Errorf("title не должен выписываться, когда синоним = имя:\n%s", noTitle)
	}
}

// Синоним 1С переносится в title YAML документа.
func TestWriteDocuments_Title(t *testing.T) {
	dir := t.TempDir()
	docs := []*parser1c.DocumentMeta{
		{Name: "ПоступлениеНаРасчётныйСчёт", Synonym: "Поступление на расчётный счёт"},
	}
	if err := WriteDocuments(docs, dir, &ConversionReport{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "documents", fileName("ПоступлениеНаРасчётныйСчёт")+".yaml"))
	if !strings.Contains(string(data), "title: Поступление на расчётный счёт") {
		t.Errorf("ожидался title в YAML документа, получено:\n%s", data)
	}
}
