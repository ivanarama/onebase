package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

func TestRenderGroupConditionalForeground(t *testing.T) {
	for _, background := range []struct{ name, value, want string }{
		{"white", "#fff", "#000"}, {"dark", "#111", "#fff"},
		{"named", "white", "#000"}, {"rgb", "rgb(0,0,0)", "#fff"},
	} {
		for _, field := range []string{"", "Сумма"} {
			for _, explicit := range []bool{false, true} {
				mode := "auto"
				style := report.CellStyle{Background: background.value}
				want := background.want
				if explicit {
					mode, style.Color, want = "explicit", "#c00", "#c00"
				}
				t.Run(background.name+"/"+field+"/"+mode, func(t *testing.T) {
					spec := report.Composition{Groupings: []string{"Группа"}, Measures: []report.Measure{{Field: "Сумма", Agg: "sum"}},
						Conditional: []report.CondRule{{When: "Сумма < 0", Field: field, Style: style}}}
					result, err := compose.Compose([]compose.Row{{"Группа": "Север", "Сумма": -100}}, spec, newInterpEvaluator(interpreter.New()))
					if err != nil {
						t.Fatal(err)
					}
					page := string(renderComposedTable(result, &spec))
					row := regexp.MustCompile(`(?s)<tr class="grp"[^>]*>.*?</tr>`).FindString(page)
					if !strings.Contains(row, "Север") || !strings.Contains(row, "-100") {
						t.Fatalf("missing real group cells: %s", row)
					}
					pattern := `<tr[^>]*>`
					if field != "" {
						pattern = `<td class="num"[^>]*>`
					}
					tag := regexp.MustCompile(pattern).FindString(row)
					if !strings.Contains(tag, "color:"+want) || !strings.Contains(tag, "background:"+background.value) {
						t.Fatalf("explicit background needs inline foreground in either theme: %s", tag)
					}
				})
			}
		}
	}
}
