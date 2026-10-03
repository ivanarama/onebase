package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Проверка идёт через RunFull — публичную точку входа onebase check. rawField
// читает multiline во всех секциях регистра, поэтому нестроковое поле должно
// падать одинаково, а не становиться молчаливой декларацией без поведения.
func TestRunFull_RejectsMultilineOnNonStringRegisterFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dir    string
		body   string
		needle string
	}{
		{
			name: "измерение регистра накопления",
			dir:  "registers",
			body: `name: Продажи
dimensions:
  - name: ПериодДоставки
    type: date
    multiline: true
resources:
  - name: Сумма
    type: number
`,
			needle: "измерение ПериодДоставки",
		},
		{
			name: "ресурс регистра сведений",
			dir:  "inforegs",
			body: `name: Настройки
dimensions:
  - name: Код
    type: string
resources:
  - name: Лимит
    type: number
    multiline: true
`,
			needle: "ресурс Лимит",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, tc.dir, "объект.yaml"), tc.body)

			res := RunFull(dir)
			if res.OK {
				t.Fatalf("RunFull вернул OK: multiline на нестроковом поле принят молча, %+v", res.Issues)
			}
			for _, is := range res.Issues {
				if strings.Contains(is.Message, tc.needle) &&
					(strings.Contains(is.Message, "multiline допустим только для строкового реквизита") ||
						strings.Contains(is.Message, "multiline не поддерживается в этом контексте")) {
					return
				}
			}
			t.Fatalf("не найдено сообщение о неверном multiline: %+v", res.Issues)
		})
	}
}

func TestRunFull_RejectsMultilineInUnsupportedContexts(t *testing.T) {
	for _, tc := range []struct {
		name, dir, body, needle string
	}{
		{
			name: "строковое измерение регистра накопления", dir: "registers", needle: "измерение Условия",
			body: "name: Правила\ndimensions:\n  - name: Условия\n    type: string\n    multiline: true\nresources:\n  - name: Сумма\n    type: number\n",
		},
		{
			name: "атрибут регистра накопления", dir: "registers", needle: "реквизит Комментарий",
			body: "name: Правила\ndimensions:\n  - name: Код\n    type: string\nresources:\n  - name: Сумма\n    type: number\nattributes:\n  - name: Комментарий\n    type: string\n    multiline: true\n",
		},
		{
			name: "поле табличной части", dir: "catalogs", needle: "табличная часть Строки: реквизит Комментарий",
			body: "name: Правила\ntableparts:\n  - name: Строки\n    fields:\n      - name: Комментарий\n        type: string\n        multiline: true\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, tc.dir, "объект.yaml"), tc.body)
			res := RunFull(dir)
			if res.OK {
				t.Fatalf("RunFull принял multiline в неподдерживаемом контексте: %+v", res.Issues)
			}
			for _, issue := range res.Issues {
				if strings.Contains(issue.Message, tc.needle) && strings.Contains(issue.Message, "multiline не поддерживается") {
					return
				}
			}
			t.Fatalf("не найден контекстный отказ multiline: %+v", res.Issues)
		})
	}
}

func TestRunFull_AcceptsMultilineOnEntityAndInfoRegisterStrings(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "правила.yaml"), `name: Правила
fields:
  - name: Описание
    type: string
    multiline: true
`)
	mkFile(t, filepath.Join(dir, "inforegs", "примечания.yaml"), `name: Примечания
dimensions:
  - name: Контекст
    type: string
    multiline: true
resources:
  - name: Текст
    type: string
    multiline: true
`)

	res := RunFull(dir)
	if !res.OK {
		t.Fatalf("строковые поля регистра с multiline должны проходить check: %+v", res.Issues)
	}
	for _, is := range res.Issues {
		if strings.Contains(is.Message, "multiline") {
			t.Fatalf("строковое поле регистра отклонено: %+v", is)
		}
	}
}

func TestRunFull_RejectsNegativeManagedMultilineHeight(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "обращение.yaml"), "name: Обращение\nfields:\n  - name: Описание\n    type: string\n    multiline: true\n")
	mkFile(t, filepath.Join(dir, "forms", "обращение", "объект.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Обращение
elements:
  - kind: ПолеВвода
    name: Описание
    data_path: Объект.Описание
    height: -2
`)
	res := RunFull(dir)
	if res.OK {
		t.Fatalf("RunFull принял отрицательный height: %+v", res.Issues)
	}
	for _, issue := range res.Issues {
		if strings.Contains(issue.Message, "height не может быть отрицательным") {
			return
		}
	}
	t.Fatalf("не найден отказ отрицательного height: %+v", res.Issues)
}
