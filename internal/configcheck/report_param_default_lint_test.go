package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Линт обязан знать ровно те ключи параметра отчёта, которые читает модель.
// `default` модель теперь читает — предупреждать о нём нельзя: линт говорил бы
// «загрузчик его игнорирует» про работающую возможность. А `required` она
// по-прежнему не знает, и предупреждение о нём остаётся: разрешённый ключ молча
// ничего бы не делал.
func TestLintYAML_ReportParamDefaultKnown_RequiredStillWarns(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "просроченные.yaml"), `name: ПросроченныеЗадачи
query: ВЫБРАТЬ Наименование ИЗ Справочник.Задача ГДЕ Срок < &НаДату
params:
  - name: НаДату
    type: date
    default: "{{today}}"
  - name: Исполнитель
    type: string
    required: true
`)

	var requiredWarning bool
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code != "metadata.unvalidated-key" {
			continue
		}
		if strings.Contains(issue.Message, "default") {
			t.Fatalf("ключ default параметра отчёта объявлен неизвестным, хотя модель его читает: %+v", issue)
		}
		if strings.Contains(issue.Message, "required") {
			requiredWarning = true
		}
	}
	if !requiredWarning {
		t.Fatal("required у параметра отчёта принят молча, хотя report.Param такого поля не знает")
	}
}
