package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Нераспознанный тип реквизита молча становится текстовой колонкой: на SQLite
// незаметно, на PostgreSQL «Поле >= &Дата» падает (text >= timestamptz).
// Проверка идёт через RunFull — тот же путь, что `onebase check`, без --lint.
func TestCheckFieldTypes_UnknownTypeWarnsInDefaultCheck(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "мастер.yaml"), `name: Мастер
fields:
  - name: Наименование
    type: string`)
	mkFile(t, filepath.Join(dir, "documents", "задача.yaml"), `name: Задача
fields:
  - name: Срок
    type: datetime
  - name: Сумма
    type: nubmer
  - name: Цена
    type: number(15,2)
  - name: Объём
    type: decimal(10)
  - name: Когда
    type: date
  - name: Мастер
    type: reference:Мастер
  - name: Принята
    type: bool
tableparts:
  - name: Строки
    fields:
      - name: Отметка
        type: Дата
registers: []`)
	mkFile(t, filepath.Join(dir, "registers", "остатки.yaml"), `name: Остатки
dimensions:
  - name: Мастер
    type: reference:Мастер
resources:
  - name: Количество
    type: integer
attributes:
  - name: Момент
    type: timestamp`)

	res := RunFull(dir)
	var got []string
	for _, w := range res.Warnings {
		if w.Code == "field.type" {
			got = append(got, w.File+": "+w.Message)
		}
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		`documents/задача.yaml: тип реквизита "Срок" не распознан ("datetime")`,
		`documents/задача.yaml: тип реквизита "Сумма" не распознан ("nubmer")`,
		`documents/задача.yaml: тип реквизита табличной части "Строки" "Отметка" не распознан ("Дата")`,
		`registers/Остатки.yaml: тип ресурса "Количество" не распознан ("integer")`,
		`registers/Остатки.yaml: тип реквизита "Момент" не распознан ("timestamp")`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет предупреждения %q\nвсе field.type:\n%s", want, joined)
		}
	}
	if len(got) != 5 {
		t.Errorf("известные типы (number(L,P), decimal(L), date, reference, bool, string) не должны предупреждать; получено %d:\n%s", len(got), joined)
	}
}

// Примеры поставки обязаны проходить без этого предупреждения: пример —
// образец, который копируют в свои конфигурации.
func TestCheckFieldTypes_ExamplesClean(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "examples", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range matches {
		res := RunFull(dir)
		for _, w := range res.Warnings {
			if w.Code == "field.type" {
				t.Errorf("%s: %s: %s", dir, w.File, w.Message)
			}
		}
	}
}
