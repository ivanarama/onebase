package launcher

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/report"
)

func renderConfiguratorReport(t *testing.T, comp *report.Composition) string {
	t.Helper()
	data := &configuratorData{
		Base: &Base{ID: "b", Name: "Т", ConfigSource: "file"}, Lang: "ru", Tab: "tree",
		Reports: []cfgReport{{Name: "Прод", Composition: comp}},
	}
	var buf bytes.Buffer
	if err := cfgTmpl.ExecuteTemplate(&buf, "tab-tree", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func TestReportBuilderRender(t *testing.T) {
	html := renderConfiguratorReport(t, &report.Composition{
		Groupings: []string{"Менеджер"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	})
	for _, want := range []string{"comp.present", "comp.grouping.0", "Структура", "obj-tab"} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет %q", want)
		}
	}
	// nil composition не должен паниковать
	_ = renderConfiguratorReport(t, nil)
}

func TestReportBuilderChartTab(t *testing.T) {
	html := renderConfiguratorReport(t, &report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Chart:     &report.ChartSpec{Type: "bar", Category: "М", Series: []string{"Сумма"}},
	})
	for _, want := range []string{"График", "comp.chart.type", "comp.chart.category", "ot-rep-cchart-", `value="Сумма"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет %q", want)
		}
	}
	_ = renderConfiguratorReport(t, nil) // nil не должен паниковать
}

func TestReportBuilderCondTab(t *testing.T) {
	html := renderConfiguratorReport(t, &report.Composition{
		Groupings:   []string{"М"},
		Conditional: []report.CondRule{{When: "Сумма < 0", Style: report.CellStyle{Color: "#c00"}}},
	})
	for _, want := range []string{"Оформление", "comp.cond.0.when", "ot-rep-cond-"} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет %q", want)
		}
	}
}

func TestReportBuilderAppearance(t *testing.T) {
	// Секция «Оформление вывода» рендерится и преднаполняется из Composition.Appearance.
	html := renderConfiguratorReport(t, &report.Composition{
		Groupings:  []string{"М"},
		Measures:   []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Appearance: report.Appearance{Lines: "both", Zebra: true},
	})
	for _, want := range []string{
		"Оформление вывода",
		`name="comp.appearance.lines"`,
		`value="both" selected`,                // линии both выбраны
		`name="comp.appearance.zebra" checked`, // зебра включена
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет %q", want)
		}
	}
	_ = renderConfiguratorReport(t, nil) // nil composition не должен паниковать
}

// Начальное сворачивание групп (issue #575): поле есть в конструкторе и
// преднаполняется из composition. Ноль — заданное значение, а не «пусто».
func TestReportBuilderCollapseTo(t *testing.T) {
	zero := 0
	html := renderConfiguratorReport(t, &report.Composition{
		Groupings:  []string{"М"},
		Measures:   []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Appearance: report.Appearance{CollapseTo: &zero},
	})
	for _, want := range []string{
		"Свернуто до уровня",
		`name="comp.appearance.collapse_to"`,
		`value="0"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("нет %q", want)
		}
	}

	// Без ключа поле пустое — отчёт открывается развёрнутым, как было.
	plain := renderConfiguratorReport(t, &report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	})
	if !strings.Contains(plain, `name="comp.appearance.collapse_to" min="0" step="1" placeholder="—" value=""`) {
		t.Fatalf("без ключа поле должно быть пустым:\n%s", plain)
	}
}
