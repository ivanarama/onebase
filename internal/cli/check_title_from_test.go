package cli

import (
	"fmt"
	"strings"
	"testing"
)

// Команда check видит те же параметры обработки, что runtime формы.
func TestCheckProcessorColumnTitleFromParameter(t *testing.T) {
	for _, source := range []string{"Заголовок", "Объект.Заголовок", "объект.заголовок", "НетПараметра"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			writeProcrunFixture(t, dir, "config/app.yaml", "name: title-from-test\nversion: '1.0'\n")
			writeProcrunFixture(t, dir, "processors/Подписи.yaml", `name: Подписи
params:
  - name: Заголовок
    type: string
table_parts:
  - name: Строки
    fields:
      - name: Значение
        type: string
`)
			writeProcrunFixture(t, dir, "forms/подписи/Форма.form.yaml", fmt.Sprintf(`schema: onebase.form/v1
form:
  name: Форма
  kind: object
  entity: Подписи
elements:
  - kind: ТабличнаяЧасть
    name: Строки
    data_path: Объект.Строки
    children:
      - kind: Колонка
        name: Значение
        data_path: Объект.Строки.Значение
        title_from: %s
`, source))
			out, err := runCheckCmd(t, runCheck, dir, nil)
			if source == "НетПараметра" {
				if err == nil || !strings.Contains(out, "title_from") {
					t.Fatalf("неизвестный параметр принят: %v\n%s", err, out)
				}
			} else if err != nil {
				t.Fatalf("параметр обработки отклонён: %v\n%s", err, out)
			}
		})
	}
}
