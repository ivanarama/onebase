package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Контекст события табличной части (ТекущаяСтрока и соседи) инжектируется
// только в её обработчики. Проверка обязана знать эти имена там — иначе
// документированный обработчик даёт предупреждение, а предупреждение на
// поставляемом примере это упавший CI (так и вышло на первом же примере). И
// обязана НЕ знать их в обычной процедуре, иначе перестанет ловить опечатку.
func tpContextLintProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "Заказ.yaml"), `name: Заказ
fields:
  - name: Дата
    type: date
tableparts:
  - name: Товары
    fields:
      - name: Количество
        type: number
      - name: Цена
        type: number
      - name: Сумма
        type: number
`)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.yaml"), `schema: onebase.form/v1
form:
  name: ФормаОбъекта
  kind: object
  entity: Заказ
elements:
  - kind: ТабличнаяЧасть
    name: ТабТовары
    data_path: Объект.Товары
    events:
      ПриИзменении: ТоварыПриИзменении
    children:
      - kind: Колонка
        name: КолЦена
        data_path: Объект.Товары.Цена
        events:
          ПриИзменении: ЦенаПриИзменении
`)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.os"), `Процедура ТоварыПриИзменении()
  Сообщить(ИмяТабличнойЧасти + Строка(НомерСтроки) + Строка(ТекущаяСтрока.Цена));
КонецПроцедуры

Процедура ЦенаПриИзменении()
  ТекущаяСтрока.Сумма = ТекущаяСтрока.Количество * ТекущаяСтрока.Цена;
КонецПроцедуры
`)
	return dir
}

func TestLintTPContext_ВОбработчикеТабличнойЧастиИмяИзвестно(t *testing.T) {
	res := RunFullWithOptions(tpContextLintProject(t), Options{Lint: true})
	for _, w := range res.Warnings {
		if w.Code == "dsl.unknown-global-member" {
			t.Fatalf("контекст табличной части принят за опечатку: %+v", w)
		}
	}
	if !res.OK {
		t.Fatalf("проверка не прошла: %+v", res.Issues)
	}
}

// Та же процедура, но не привязанная ни к одному событию табличной части:
// имени там взяться неоткуда, и молчать об этом нельзя.
func TestLintTPContext_ВнеОбработчикаИмяОстаётсяНеизвестным(t *testing.T) {
	dir := tpContextLintProject(t)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.os"), `Процедура ТоварыПриИзменении()
  Сообщить(Строка(ТекущаяСтрока.Цена));
КонецПроцедуры

Процедура ПересчитатьВручную() Экспорт
  Сообщить(Строка(ТекущаяСтрока.Цена));
КонецПроцедуры
`)
	res := RunFullWithOptions(dir, Options{Lint: true})

	var found []Issue
	for _, w := range res.Warnings {
		if w.Code == "dsl.unknown-global-member" {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("ожидалось ровно одно предупреждение — на процедуре вне обработчика, получено %+v", found)
	}
	if !strings.Contains(found[0].Message, "ТекущаяСтрока") {
		t.Errorf("в сообщении должно быть имя: %q", found[0].Message)
	}
	if found[0].Line != 6 {
		t.Errorf("предупреждение на строке %d, ожидалась 6 (тело ПересчитатьВручную)", found[0].Line)
	}
}
