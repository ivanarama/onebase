package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func TestCheckWidgetRowStyle(t *testing.T) {
	const q = "ВЫБРАТЬ З.Наименование, Р.Цвет КАК ЦветФона, З.Срочно КАК Жирный ИЗ Справочник.Задача КАК З"
	issues := func(w *metadata.Widget) []string {
		var out []string
		for _, is := range CheckWidgetRowStyle(&project.Project{Widgets: []*metadata.Widget{w}}) {
			out = append(out, is.Message)
		}
		return out
	}
	list := func(rs *metadata.WidgetRowStyle) *metadata.Widget {
		return &metadata.Widget{Name: "Задачи", Type: metadata.WidgetTypeList, Query: q, RowStyle: rs}
	}
	if got := issues(list(&metadata.WidgetRowStyle{Background: "ЦветФона", Bold: "жирный"})); len(got) != 0 {
		t.Fatalf("верные колонки: ложные ошибки %v", got)
	}
	got := issues(list(&metadata.WidgetRowStyle{Background: "ЦветФон"}))
	if len(got) != 1 || !strings.Contains(got[0], "ЦветФон") {
		t.Fatalf("колонка-префикс чужого слова должна быть ошибкой: %v", got)
	}
	if got := issues(list(&metadata.WidgetRowStyle{})); len(got) != 1 {
		t.Fatalf("пустой row_style: %v", got)
	}
	kpi := list(&metadata.WidgetRowStyle{Background: "ЦветФона"})
	kpi.Type = metadata.WidgetTypeKPI
	if got := issues(kpi); len(got) != 1 || !strings.Contains(got[0], "только для виджетов типа list") {
		t.Fatalf("row_style у kpi: %v", got)
	}
}
