package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEntityListFullCheck(t *testing.T) {
	const valid = `form: {name: ФормаОбъекта, kind: object, entity: Звонок}
elements:
 - kind: ПолеВвода
   id: source
   name: Source
   data_path: Объект.Клиент
 - kind: Список
   id: history
   name: History
   entity: Заявка
   filter:
    - {field: Клиент, op: eq, source: Объект.Клиент}
    - {field: Флаг, op: eq, value: false}
    - {field: Сумма, op: eq, value: 0}
   columns: [Номер, Сумма]
   order_by: [Сумма desc, Номер asc]
   limit: 5
   on_row_click: открыть
`
	for _, tc := range []struct {
		name, old, new string
		ok             bool
	}{
		{name: "valid", ok: true},
		{"unknown key", "limit: 5", "limti: 5", false},
		{"zero limit", "limit: 5", "limit: 0", false},
		{"duplicate columns", "[Номер, Сумма]", "[Номер, Номер]", false},
		{"unknown source", "Объект.Клиент}", "Объект.Нет}", false},
		{"incompatible source", "source: Объект.Клиент", "source: Объект.Флаг", false},
		{"unknown target", "entity: Заявка", "entity: Нет", false},
		{"both operands", "value: false", "value: false, source: Объект.Флаг", false},
		{"ne operator", "op: eq", "op: ne", false},
		{"unknown condition key", "value: 0", "value: 0, sql: x", false},
		{"null literal", "value: false", "value: null", false},
		{"wrong boolean", "value: false", "value: 'false'", false},
		{"bad order", "Сумма desc", "Сумма invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "config/app.yaml"), "name: Lists\nversion: '1.0'\n")
			mkFile(t, filepath.Join(dir, "catalogs/клиент.yaml"), "name: Клиент\nfields: [{name: Наименование, type: string}]\n")
			fields := "fields: [{name: Клиент, type: 'reference:Клиент'}, {name: Флаг, type: bool}, {name: Сумма, type: number}, {name: Номер, type: string}]\n"
			mkFile(t, filepath.Join(dir, "documents/звонок.yaml"), "name: Звонок\n"+fields)
			mkFile(t, filepath.Join(dir, "documents/заявка.yaml"), "name: Заявка\n"+fields)
			yaml := valid
			if tc.old != "" {
				yaml = strings.Replace(yaml, tc.old, tc.new, 1)
			}
			mkFile(t, filepath.Join(dir, "forms/звонок/object.form.yaml"), yaml)
			result := RunFull(dir)
			if result.OK != tc.ok {
				t.Fatalf("OK=%v expected=%v issues=%+v", result.OK, tc.ok, result.Issues)
			}
		})
	}
}
