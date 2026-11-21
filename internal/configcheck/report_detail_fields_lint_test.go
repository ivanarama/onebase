package configcheck

import (
	"path/filepath"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
	"gopkg.in/yaml.v3"
)

// detail_fields (поля детальной записи) модель читает, поэтому линт не должен
// называть ключ неизвестным — ни в композиции отчёта, ни в его варианте.
func TestLintYAML_ReportDetailFieldsKnown(t *testing.T) {
	dir := t.TempDir()
	src := `name: КонтрольОТК
query: ВЫБРАТЬ Мастер, Номер, Штраф ИЗ Документ.Жалоба
composition:
  groupings: [Мастер]
  detail: true
  detail_fields:
    - field: Номер
      title: Номер заявки
    - field: Наименование
  measures:
    - field: Штраф
variants:
  - name: Кратко
    composition:
      groupings: [Мастер]
      detail_fields:
        - field: Номер
`
	mkFile(t, filepath.Join(dir, "reports", "контроль.yaml"), src)
	for _, issue := range CheckLintYAML(dir) {
		if issue.Code == "metadata.unvalidated-key" {
			t.Fatalf("detail_fields объявлен неизвестным, хотя модель его читает: %+v", issue)
		}
	}
	var rep report.Report
	if err := yaml.Unmarshal([]byte(src), &rep); err != nil {
		t.Fatal(err)
	}
	got := rep.Composition.DetailFields
	if len(got) != 2 || got[0] != (report.DetailField{Field: "Номер", Title: "Номер заявки"}) || got[1].Field != "Наименование" {
		t.Fatalf("detail_fields = %+v", got)
	}
}
