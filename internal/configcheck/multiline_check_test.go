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
					strings.Contains(is.Message, "multiline допустим только для строкового реквизита") {
					return
				}
			}
			t.Fatalf("не найдено сообщение о неверном multiline: %+v", res.Issues)
		})
	}
}

func TestRunFull_AcceptsMultilineOnStringRegisterFields(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "registers", "правила.yaml"), `name: Правила
dimensions:
  - name: Условия
    type: string
    multiline: true
resources:
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
