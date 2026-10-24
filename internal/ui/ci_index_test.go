package ui

import (
	"strings"
	"testing"
)

// equalFoldKey обязан делить строки ровно на классы strings.EqualFold: индекс
// заменяет перебор с EqualFold, и любое расхождение молча потеряло бы поле
// (или нашло чужое).
func TestEqualFoldKeyMatchesEqualFold(t *testing.T) {
	words := []string{
		"", "Канал", "канал", "КАНАЛ", "КаНаЛ", "Ёлка", "ёлка", "Елка",
		"Kelvin", "kelvin", "Kelvin", "Sum", "ſum", "SUM",
		"ΣΊΣΥΦΟΣ", "σίσυφος", "σίσυφοσ", "straße", "STRASSE", "İstanbul", "istanbul",
		"ǅ", "ǆ", "Ǆ", "parent_id", "PARENT_ID", "Реквизит1", "реквизит1",
		"\xff", "�", "a\xffb", "A�B",
	}
	for _, a := range words {
		for _, b := range words {
			want := strings.EqualFold(a, b)
			if got := equalFoldKey(a) == equalFoldKey(b); got != want {
				t.Errorf("%q ~ %q: ключи равны = %v, EqualFold = %v", a, b, got, want)
			}
		}
	}
}

// ciGet находит ключ в любом регистре и не находит отсутствующий, а
// дописанный через add ключ виден сразу.
func TestCIIndexGetAndAdd(t *testing.T) {
	m := map[string]any{"Канал": 1, "склад": 2}
	ix := newCIIndex(m, equalFoldKey)
	if k, v, ok := ciGet(m, ix, "КАНАЛ"); !ok || k != "Канал" || v != 1 {
		t.Fatalf("КАНАЛ: %q %v %v", k, v, ok)
	}
	if _, _, ok := ciGet(m, ix, "Номер"); ok {
		t.Fatal("отсутствующий ключ найден")
	}
	m["Номер"] = 3
	ix.add("Номер")
	if _, v, ok := ciGet(m, ix, "номер"); !ok || v != 3 {
		t.Fatalf("дописанный ключ не найден: %v %v", v, ok)
	}
}
