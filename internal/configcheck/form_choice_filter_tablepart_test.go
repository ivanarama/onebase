package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// choice_filter по колонке табличной части выбираемого справочника (#1822)
// через RunFull — тот же вызов, которым `onebase check` проверяет проект.

func tablePartChoiceProject(t *testing.T, condition string) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "направление.yaml"), "name: Направление\nfields:\n  - name: Наименование\n    type: string\n")
	mkFile(t, filepath.Join(dir, "catalogs", "филиал.yaml"), "name: Филиал\nfields:\n  - name: Наименование\n    type: string\n")
	mkFile(t, filepath.Join(dir, "catalogs", "бренд.yaml"), `name: Бренд
fields:
  - name: Наименование
    type: string
tableparts:
  - name: Направления
    fields:
      - name: Направление
        type: reference:Направление
      - name: Комментарий
        type: string
`)
	mkFile(t, filepath.Join(dir, "documents", "заявка.yaml"), `name: Заявка
fields:
  - name: Направление
    type: reference:Направление
  - name: Филиал
    type: reference:Филиал
  - name: Бренд
    type: reference:Бренд
`)
	mkFile(t, filepath.Join(dir, "forms", "заявка", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заявка
elements:
  - kind: ПолеВвода
    name: ПолеНаправление
    data_path: Объект.Направление
  - id: brand
    kind: ПолеВвода
    name: ПолеБренд
    data_path: Объект.Бренд
    choice_filter:
`+condition)
	return dir
}

func choiceFilterMessages(res Result) []string {
	var out []string
	for _, list := range [][]Issue{res.Issues, res.Warnings} {
		for _, is := range list {
			if is.Code == "form.choice-filter" {
				out = append(out, is.Message)
			}
		}
	}
	return out
}

func TestRunFullChoiceFilterTablePartAccepted(t *testing.T) {
	for name, condition := range map[string]string{
		"from":         "      - {field: Направления.Направление, op: eq, from: Объект.Направление}\n",
		"ref":          "      - {field: Направления.Направление, op: eq, ref: 3f2504e0-4f89-11d3-9a0c-0305e82c3301}\n",
		"регистр имён": "      - {field: направления.направление, op: eq, from: Объект.Направление}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if msgs := choiceFilterMessages(RunFull(tablePartChoiceProject(t, condition))); len(msgs) != 0 {
				t.Fatalf("корректное условие отклонено: %v", msgs)
			}
		})
	}
}

func TestRunFullChoiceFilterTablePartRejected(t *testing.T) {
	for _, tc := range []struct {
		name, condition, want string
	}{
		{"нет ТЧ", "      - {field: Цены.Направление, op: eq, from: Объект.Направление}\n", "нет табличной части"},
		{"нет колонки", "      - {field: Направления.Филиал, op: eq, from: Объект.Филиал}\n", "нет колонки"},
		{"строковая колонка", "      - {field: Направления.Комментарий, op: eq, from: Объект.Направление}\n", "только по ссылочной колонке"},
		{"in_hierarchy", "      - {field: Направления.Направление, op: in_hierarchy, from: Объект.Направление}\n", "допустим только eq"},
		{"eq_or_empty", "      - {field: Направления.Направление, op: eq_or_empty, from: Объект.Направление}\n", "допустим только eq"},
		{"value", "      - {field: Направления.Направление, op: eq, value: true}\n", "литерал value"},
		{"источник другой сущности", "      - {field: Направления.Направление, op: eq, from: Объект.Филиал}\n", "несовместимые ссылки"},
		{"ref не UUID", "      - {field: Направления.Направление, op: eq, ref: не-uuid}\n", "не является UUID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := choiceFilterMessages(RunFull(tablePartChoiceProject(t, tc.condition)))
			for _, msg := range msgs {
				if strings.Contains(msg, tc.want) {
					return
				}
			}
			t.Fatalf("нет замечания %q: %v", tc.want, msgs)
		})
	}
}
