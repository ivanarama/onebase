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
	if len(got) != 2 {
		t.Fatalf("typed projection warnings = %d, want 2; all warnings: %+v", len(got), result.Warnings)
	}
	if got[0].File != "reports/Типы.yaml" || !strings.Contains(got[0].Message, "Ссылка, Активен, Дата") {
		t.Errorf("unexpected report warning: %+v", got[0])
	}
	if got[1].File != "src/типы.os" || got[1].Line != 3 || !strings.Contains(got[1].Message, "Активен") {
		t.Errorf("unexpected module warning: %+v", got[1])
	}
}
