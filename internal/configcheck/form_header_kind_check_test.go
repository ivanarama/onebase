package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Форма документа с kind: document молча уводилась в автоформу. onebase check
// (RunFull — тот же путь, что команда) обязан назвать это ошибкой; известные
// виды, форма без kind (custom) и форма обработки ошибок не дают.
func TestCheckFormHeaderKindUnknownIsError(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "звонок.yaml"), "name: Звонок\nfields:\n  - name: Оценка\n    type: string\n")
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), "name: Клиент\nfields:\n  - name: Наименование\n    type: string\n")
	mkFile(t, filepath.Join(dir, "processors", "оценка.yaml"), "name: Оценка\n")
	form := func(name, kind, entity string) string {
		k := ""
		if kind != "" {
			k = "  kind: " + kind + "\n"
		}
		return "schema: onebase.form/v1\nform:\n  name: " + name + "\n" + k + "  entity: " + entity + "\nelements:\n  - kind: ПолеВвода\n    name: Поле\n    data_path: Объект.Оценка\n"
	}
	mkFile(t, filepath.Join(dir, "forms", "звонок", "оценки.form.yaml"), form("ФормаОценки", "document", "Звонок"))
	mkFile(t, filepath.Join(dir, "forms", "звонок", "объекта.form.yaml"), form("ФормаОбъекта", "Object", "Звонок"))
	mkFile(t, filepath.Join(dir, "forms", "клиент", "списка.form.yaml"), strings.Replace(form("ФормаСписка", "list", "Клиент"), "Объект.Оценка", "Объект.Наименование", 1))
	mkFile(t, filepath.Join(dir, "forms", "клиент", "своя.form.yaml"), strings.Replace(form("Своя", "", "Клиент"), "Объект.Оценка", "Объект.Наименование", 1))
	mkFile(t, filepath.Join(dir, "forms", "оценка", "обработки.form.yaml"), form("ФормаОбработки", "processor", "Оценка"))

	res := RunFull(dir)
	var got []Issue
	for _, issue := range res.Issues {
		if issue.Code == "form.header-kind" {
			got = append(got, issue)
		}
	}
	if len(got) != 1 || got[0].Object != "Звонок" || !strings.Contains(got[0].Message, `"document"`) {
		t.Fatalf("form.kind issues = %+v, want exactly one for kind document", got)
	}
	if res.OK {
		t.Fatal("check passed with an unknown form kind")
	}
}
