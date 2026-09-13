package metadata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOrderYAML кладёт YAML сущности во временный каталог и отдаёт путь.
func writeOrderYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// order_by принимает и строку, и список — «одно поле» частый случай.
func TestOrderByParsed(t *testing.T) {
	dir := t.TempDir()
	path := writeOrderYAML(t, dir, "Направление.yaml", `name: Направление
order_by: [ПорядокВОтчётах, Наименование]
fields:
  - name: Наименование
    type: string
  - name: ПорядокВОтчётах
    type: number
`)
	e, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.OrderBy) != 2 || e.OrderBy[0] != "ПорядокВОтчётах" {
		t.Fatalf("OrderBy = %v", e.OrderBy)
	}
	one := writeOrderYAML(t, dir, "Один.yaml", `name: Один
order_by: ПорядокВОтчётах
fields:
  - name: ПорядокВОтчётах
    type: number
`)
	e2, err := LoadFile(one, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(e2.OrderBy) != 1 || e2.OrderBy[0] != "ПорядокВОтчётах" {
		t.Fatalf("строковая форма order_by разобрана как %v", e2.OrderBy)
	}
}

// Направление сортировки задаётся суффиксом — и по-русски тоже: список порядка
// пишут руками, и «убыв» читается лучше, чем англоязычный хвост.
func TestSplitOrderSpec(t *testing.T) {
	cases := []struct {
		in    string
		field string
		desc  bool
	}{
		{"ПорядокВОтчётах", "ПорядокВОтчётах", false},
		{"  Наименование  ", "Наименование", false},
		{"Дата desc", "Дата", true},
		{"Дата DESC", "Дата", true},
		{"Дата убыв", "Дата", true},
		{"Дата asc", "Дата", false},
	}
	for _, c := range cases {
		f, d := SplitOrderSpec(c.in)
		if f != c.field || d != c.desc {
			t.Errorf("SplitOrderSpec(%q) = (%q,%v), ждали (%q,%v)", c.in, f, d, c.field, c.desc)
		}
	}
}

// Опечатка в имени реквизита падает при сборке: в рантайме она выглядит как
// «сортировка не применилась», и это не отличить от «значения не заполнены».
func TestOrderByValidation(t *testing.T) {
	e := &Entity{
		Name: "Направление", Kind: KindCatalog,
		OrderBy: []string{"ПорядокВОтчетах"}, // ё → е
		Fields:  []Field{{Name: "ПорядокВОтчётах", Type: FieldTypeNumber}},
	}
	err := Validate([]*Entity{e}, nil)
	if err == nil || !strings.Contains(err.Error(), "order_by") {
		t.Fatalf("ошибка = %v, ждали отказ на несуществующий реквизит", err)
	}
}
