package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// RunFull — публичный путь команды `onebase check`: предупреждение не должно
// жить только в приватном помощнике компилятора.
func TestRunFullWarnsAboutTypedColumnsInComplexQueries(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Demo\n")
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Активен
    type: bool
  - name: Дата
    type: date
`)
	mkFile(t, filepath.Join(dir, "reports", "типы.yaml"), `name: Типы
query: |
  ВЫБРАТЬ Ссылка, Активен, Дата ИЗ Документ.Заказ
  ОБЪЕДИНИТЬ ВСЕ
  ВЫБРАТЬ Ссылка, Активен, Дата ИЗ Документ.Заказ
`)
	mkFile(t, filepath.Join(dir, "src", "типы.os"), `Процедура Проверить()
  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Т.Активен ИЗ (ВЫБРАТЬ Активен ИЗ Документ.Заказ) КАК Т";
КонецПроцедуры
`)
	mkFile(t, filepath.Join(dir, "forms", "заказ", "объекта.form.os"), `Процедура ПриОткрытииФормы()
  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Т.Дата ИЗ (ВЫБРАТЬ Дата ИЗ Документ.Заказ) КАК Т";
КонецПроцедуры
`)

	result := RunFull(dir)
	if !result.OK {
		t.Fatalf("RunFull failed: %+v", result.Issues)
	}
	var got []Issue
	for _, warning := range result.Warnings {
		if warning.Code == "query.complex-projection-types" {
			got = append(got, warning)
		}
	}
	if len(got) != 3 {
		t.Fatalf("typed projection warnings = %d, want 3; all warnings: %+v", len(got), result.Warnings)
	}
	if got[0].File != "reports/Типы.yaml" || !strings.Contains(got[0].Message, "Ссылка, Активен, Дата") {
		t.Errorf("unexpected report warning: %+v", got[0])
	}
	if got[1].File != "src/типы.os" || got[1].Line != 3 || !strings.Contains(got[1].Message, "Активен") {
		t.Errorf("unexpected module warning: %+v", got[1])
	}
	if got[2].File != "forms/заказ/объекта.form.os" || got[2].Line != 3 || !strings.Contains(got[2].Message, "Дата") {
		t.Errorf("unexpected form module warning: %+v", got[2])
	}
}

// Диагностика типов не должна вытеснять проверки виджетов из публичного check.
func TestRunFullKeepsWidgetValidationWithQueryTypeWarnings(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: Demo\n")
	mkFile(t, filepath.Join(dir, "documents", "заказ.yaml"), `name: Заказ
fields:
  - name: Активен
    type: bool
`)
	mkFile(t, filepath.Join(dir, "reports", "типы.yaml"), `name: Типы
query: |
  ВЫБРАТЬ Активен ИЗ Документ.Заказ
  ОБЪЕДИНИТЬ ВСЕ
  ВЫБРАТЬ Активен ИЗ Документ.Заказ
`)
	mkFile(t, filepath.Join(dir, "widgets", "кнопки.yaml"), `name: Кнопки
type: actions
refresh_on: [данные.заказ]
`)
	mkFile(t, filepath.Join(dir, "widgets", "источник.yaml"), `name: Источник
type: kpi
query: ВЫБРАТЬ Ссылка ИЗ Документ.Заказ
source:
  entity: Заказ
  id_field: Ссылка
`)
	mkFile(t, filepath.Join(dir, "widgets", "отбор.yaml"), `name: Отбор
type: list
query: ВЫБРАТЬ Активен ИЗ Документ.Заказ
filters:
  - name: Чужой
    type: string
    param: Чужой
`)
	result := RunFull(dir)
	if result.OK {
		t.Fatal("invalid widgets passed check")
	}
	for _, object := range []string{"Кнопки", "Источник", "Отбор"} {
		found := false
		for _, issue := range result.Issues {
			if strings.HasPrefix(issue.Kind, "Виджет") && issue.Object == object {
				found = true
			}
		}
		if !found {
			t.Errorf("missing widget validation for %s: %+v", object, result.Issues)
		}
	}
	found := false
	for _, warning := range result.Warnings {
		if warning.Code == "query.complex-projection-types" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing query type warning: %+v", result.Warnings)
	}
}

func TestRunFullWarnsAboutComplexRegisterPeriod(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "config", "app.yaml"), "name: PeriodWarning\n")
	mkFile(t, filepath.Join(dir, "registers", "движения.yaml"), `name: Движения
resources:
  - name: Количество
    type: number
`)
	mkFile(t, filepath.Join(dir, "reports", "периоды.yaml"), `name: Периоды
query: |
  ВЫБРАТЬ Д.Период КАК Момент ИЗ РегистрНакопления.Движения КАК Д
  ОБЪЕДИНИТЬ ВСЕ
  ВЫБРАТЬ period ИЗ РегистрНакопления.Движения
`)
	mkFile(t, filepath.Join(dir, "reports", "простой.yaml"), `name: Простой
query: ВЫБРАТЬ Период ИЗ РегистрНакопления.Движения
`)
	mkFile(t, filepath.Join(dir, "src", "периоды.os"), `Процедура Проверить()
  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Т.Момент ИЗ (ВЫБРАТЬ Период КАК Момент ИЗ РегистрНакопления.Движения) КАК Т";
КонецПроцедуры
`)
	result := RunFull(dir)
	if !result.OK || len(result.Issues) != 0 {
		t.Fatalf("date diagnostic must remain advisory: %+v", result)
	}
	got := map[string]bool{}
	for _, warning := range result.Warnings {
		if warning.Code == "query.complex-projection-types" {
			if !strings.Contains(warning.Message, "Период") {
				t.Errorf("missing date in warning: %+v", warning)
			}
			got[warning.File] = true
		}
	}
	if len(got) != 2 || !got["reports/Периоды.yaml"] || !got["src/периоды.os"] {
		t.Fatalf("warnings=%+v", result.Warnings)
	}
}
