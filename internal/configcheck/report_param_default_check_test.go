package configcheck

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
)

func TestLintProject_ReportSelectDefaultMustBeAnOption(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "заказы.yaml"), `name: ЗаказыПоСтатусу
params:
  - name: Статус
    type: select
    options: [Новый, Закрыт]
    default: Архив
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(proj.Close)

	var found []Issue
	for _, issue := range CheckLintProject(dir, proj, nil) {
		if issue.Code == "report.select-default-not-in-options" {
			found = append(found, issue)
		}
	}
	if len(found) != 1 {
		t.Fatalf("ожидалось одно предупреждение о default вне options, получено %+v", found)
	}
	if found[0].File != "reports/ЗаказыПоСтатусу.yaml" || found[0].Object != "ЗаказыПоСтатусу" {
		t.Errorf("предупреждение не указывает на отчёт: %+v", found[0])
	}
	if !strings.Contains(found[0].Message, "Статус") || !strings.Contains(found[0].Message, "Архив") {
		t.Errorf("предупреждение не называет параметр и default: %q", found[0].Message)
	}
}

func TestLintProject_ReportSelectDefaultAcceptsOptionAndTemplate(t *testing.T) {
	dir := t.TempDir()
	mkFile(t, filepath.Join(dir, "reports", "заказы.yaml"), `name: ЗаказыПоСтатусу
params:
  - name: Статус
    type: select
    options: [Новый, Закрыт]
    default: Закрыт
  - name: Период
    type: select
    options: [Сегодня, Вчера]
    default: "{{today}}"
  - name: НеЗадан
    type: select
    options: [Один]
  - name: ПроизвольныйТекст
    type: string
    default: ВнеСписка
`)

	proj, err := project.Load(dir)
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Cleanup(proj.Close)

	for _, issue := range CheckLintProject(dir, proj, nil) {
		if issue.Code == "report.select-default-not-in-options" {
			t.Fatalf("корректное или шаблонное умолчание помечено ошибочным: %+v", issue)
		}
	}
}
