package ui

import (
	"net/url"
	"sort"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Словарь имён контекста события табличной части объявлен в metadata, а значения
// инжектирует этот пакет. Разъедутся — получим одно из двух: проверка
// конфигураций начнёт ругаться на документированное имя как на опечатку
// (ровно это и случилось с `ТекущаяСтрока` на первом же поставляемом примере),
// либо, наоборот, перестанет ловить настоящую опечатку.
//
// Здесь вызывается внутренняя функция, а не HTTP-путь: проверяется не
// поведение (оно покрыто событийными тестами), а согласованность двух списков.
func TestFormTablePartContextVars_СходитсяСРантаймом(t *testing.T) {
	body := url.Values{}
	body.Set("_tp", "Товары")
	body.Set("_tp_selected", "0")
	body.Set("_tp_row", "0")
	body.Set("_tp_row_number", "1")
	body.Set("_tp_col", "Цена")
	body.Set("_tp_col_index", "1")
	read := func(key string) ([]string, bool) {
		values, ok := body[key]
		return values, ok
	}

	table := &metadata.FormElement{
		Kind: metadata.FormElementTablePart, Name: "ТабТовары", DataPath: "Объект.Товары",
	}
	allowed := map[string]metadata.FormTableDefinition{
		"Товары": {Name: "Товары", Columns: []string{"Количество", "Цена"}},
	}
	obj := &runtime.Object{TablePartRows: map[string][]map[string]any{
		"Товары": {{"Количество": 2.0, "Цена": 15.0}},
	}}

	vars := map[string]any{}
	if err := addValidatedTPEventContext(read, allowed, browserFormEventTarget{element: table}, obj, vars); err != nil {
		t.Fatalf("сборка контекста табличной части: %v", err)
	}

	got := make([]string, 0, len(vars))
	for name := range vars {
		got = append(got, name)
	}
	want := metadata.FormTablePartContextVars()
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("рантайм инжектирует %d имён, в metadata объявлено %d\nрантайм:  %v\nmetadata: %v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("списки имён разошлись на позиции %d: рантайм %q, metadata %q\nрантайм:  %v\nmetadata: %v",
				i, got[i], want[i], got, want)
		}
	}
}
