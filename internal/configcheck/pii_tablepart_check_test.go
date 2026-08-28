package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Признак ПДн работает там, куда дотягивается маскирование: шапка объекта и
// поля регистров. Поля табличных частей не адресует ни одна политика
// field_access, поэтому `pii` там нечему исполнять — и принимать его молча
// нельзя: конфигуратор прочтёт признак как «поле защищено», а защиты не будет.
//
// Проверка идёт через RunFull — ту же точку входа, что и `onebase check`:
// пользователь узнаёт о запрете именно оттуда, и запрет обязан работать без
// `--lint`, потому что советы линта необязательны, а этот — нет.
func TestRunFull_RejectsPIIOnTablePartField(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "обращение.yaml"), `name: Обращение
fields:
  - name: Дата
    type: date
tableparts:
  - name: Попытки
    fields:
      - name: Телефон
        type: string
        pii: true
`)

	res := RunFull(dir)

	if res.OK {
		t.Fatalf("RunFull вернул OK: pii на поле табличной части принят молча, %+v", res.Issues)
	}
	for _, is := range res.Issues {
		if strings.Contains(is.Message, "признак pii не поддерживается в табличных частях") &&
			strings.Contains(is.Message, "Попытки.Телефон") {
			return
		}
	}
	t.Fatalf("не найдено сообщение про pii в табличной части: %+v", res.Issues)
}

// Обратная сторона запрета: там, где маскирование признак исполняет, он обязан
// проходить проверку без единого слова — иначе гейт превратится в запрет на сам
// признак. Шапка объекта и измерение регистра накопления в одной конфигурации.
func TestRunFull_AcceptsPIIWhereMaskingApplies(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "catalogs", "клиент.yaml"), `name: Клиент
fields:
  - name: Наименование
    type: string
  - name: Телефон
    type: string
    pii: true
`)
	mkFile(t, filepath.Join(dir, "registers", "звонки.yaml"), `name: Звонки
dimensions:
  - name: Номер
    type: string
    pii: true
resources:
  - name: Длительность
    type: number
`)

	res := RunFull(dir)

	for _, is := range append(append([]Issue{}, res.Issues...), res.Warnings...) {
		if strings.Contains(strings.ToLower(is.Message), "pii") {
			t.Fatalf("pii на поле шапки/измерении регистра вызвал претензию: %+v", is)
		}
	}
}
