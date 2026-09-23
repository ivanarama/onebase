package configcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// Линт ведёт СВОЙ список допустимых YAML-ключей, отдельный от загрузчика.
// Пока ключ туда не внесён, он печатает «загрузчик его игнорирует» — то есть
// сообщает конфигуратору неправду и советует удалить работающее объявление.
// Ровно так линт когда-то советовал убрать `id` реквизита, снимая страховку от
// потери данных (#873); с `default` (план 153) это повторилось и покраснило
// джоб build на examples/trade.
//
// Граница проходит там же, где у `required`: ключ известен линту у реквизитов
// сущности (шапка и табличная часть) и неизвестен у измерений/ресурсов
// регистров и в табличных частях обработок — там значение действительно
// никуда не идёт. Про `default` в табличной части сущности линт молчит
// намеренно: его отвергает `ValidateDefaults` отдельным сообщением, которое
// объясняет причину, и второе предупреждение о том же только шумит.
func TestLintYAML_DefaultKnownWhereItIsExecuted(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "documents", "defaults.yaml"), `name: DefaultsDocument
fields:
  - name: Header
    type: string
    default: значение
tableparts:
  - name: Lines
    fields:
      - name: RowValue
        type: string
        default: значение
`)
	mkFile(t, filepath.Join(dir, "registers", "unsupported.yaml"), `name: UnsupportedDefault
dimensions:
  - name: Key
    type: string
    default: значение
resources:
  - name: Value
    type: number
`)
	mkFile(t, filepath.Join(dir, "processors", "unsupported.yaml"), `name: UnsupportedProcessorDefault
table_parts:
  - name: Rows
    fields:
      - name: Value
        type: string
        default: значение
`)

	var registerWarning, processorWarning bool
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code != "metadata.unvalidated-key" || !strings.Contains(issue.Message, "default") {
			continue
		}
		if strings.Contains(issue.File, "documents") {
			t.Fatalf("default реквизита сущности объявлен неизвестным ключом: %+v", issue)
		}
		if strings.Contains(issue.File, "registers") {
			registerWarning = true
		}
		if strings.Contains(issue.File, "processors") {
			processorWarning = true
		}
	}
	if !registerWarning {
		t.Fatal("default у измерения регистра принят молча, хотя запись регистра его не применяет")
	}
	if !processorWarning {
		t.Fatal("default в табличной части обработки принят молча, хотя он не применяется")
	}
}
