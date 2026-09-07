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

// Второе место, где признак не исполняется, — ресурсы и субконто регистра
// БУХГАЛТЕРИИ. Предикатную сущность бухрегистра FieldDecisions видит, поэтому
// умолчание fail-closed само по себе туда дотянулось бы; но списки проводок и
// остатков (/ui/accountreg/*) не маскируют полей вовсе, и значение, закрытое в
// отчёте, осталось бы открытым на соседней странице. Признак, защищающий через
// раз, — не защита, а её видимость, поэтому отказ, а не тишина.
//
// Точка входа та же — RunFull: отказ обязан приходить из голого `onebase check`.
func TestRunFull_RejectsPIIOnAccountRegisterField(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "ресурс",
			body: `name: БухУчёт
accounts: Основной
resources:
  - name: Сумма
    type: number
    pii: true
subconto:
  - name: Контрагент
    type: string
`,
			want: "ресурс Сумма",
		},
		{
			name: "субконто",
			body: `name: БухУчёт
accounts: Основной
resources:
  - name: Сумма
    type: number
subconto:
  - name: Контрагент
    type: string
    pii: true
`,
			want: "субконто Контрагент",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mkFile(t, filepath.Join(dir, "accounts", "основной.yaml"), `name: Основной
accounts:
  - code: "51"
    name: Расчётные счета
    kind: active
`)
			mkFile(t, filepath.Join(dir, "accountregs", "бухучёт.yaml"), tc.body)

			res := RunFull(dir)

			if res.OK {
				t.Fatalf("RunFull вернул OK: pii на поле бухрегистра принят молча, %+v", res.Issues)
			}
			for _, is := range res.Issues {
				if strings.Contains(is.Message, "списки проводок и остатков бухрегистра поля не маскируют") &&
					strings.Contains(is.Message, tc.want) {
					return
				}
			}
			t.Fatalf("не найдено сообщение про pii у поля бухрегистра (%s): %+v", tc.want, res.Issues)
		})
	}
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
	// Бухрегистр без признака — рядом и в той же конфигурации: запрет обязан
	// целиться в ключ `pii`, а не в сам бухрегистр.
	mkFile(t, filepath.Join(dir, "accounts", "основной.yaml"), `name: Основной
accounts:
  - code: "51"
    name: Расчётные счета
    kind: active
`)
	mkFile(t, filepath.Join(dir, "accountregs", "бухучёт.yaml"), `name: БухУчёт
accounts: Основной
resources:
  - name: Сумма
    type: number
subconto:
  - name: Контрагент
    type: string
`)

	res := RunFull(dir)

	for _, is := range append(append([]Issue{}, res.Issues...), res.Warnings...) {
		if strings.Contains(strings.ToLower(is.Message), "pii") {
			t.Fatalf("pii на поле шапки/измерении регистра вызвал претензию: %+v", is)
		}
	}
}
