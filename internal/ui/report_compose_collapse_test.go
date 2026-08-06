package ui

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report"
	"github.com/ivantit66/onebase/internal/report/compose"
)

// collapseFixture — две группировки, детали и подытоги: уровень 0 (Участник),
// уровень 1 (Категория), под ними детали и подытоги уровня 2.
func collapseFixture(t *testing.T, collapseTo *int) string {
	t.Helper()
	rows := []compose.Row{
		{"Участник": "Иванов", "Категория": "Услуги", "Сумма": "10"},
		{"Участник": "Иванов", "Категория": "Товары", "Сумма": "20"},
		{"Участник": "Петров", "Категория": "Услуги", "Сумма": "30"},
	}
	spec := report.Composition{
		Groupings:  []string{"Участник", "Категория"},
		Measures:   []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:     report.Totals{Subtotals: true, Grand: true},
		Detail:     true,
		Appearance: report.Appearance{CollapseTo: collapseTo},
	}
	res, err := compose.Compose(rows, spec, newInterpEvaluator(interpreter.New()))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return string(renderComposedTable(res, &spec))
}

func intPtr(v int) *int { return &v }

// rcRows разбирает строки таблицы: класс, атрибуты и признак скрытости.
type rcRow struct {
	class  string
	attrs  string
	hidden bool
	folded bool // маркер «▶» — группа отрисована свёрнутой
}

func rcRows(html string) []rcRow {
	var out []rcRow
	for _, chunk := range strings.Split(html, `<tr `)[1:] {
		if i := strings.Index(chunk, "</tr>"); i >= 0 {
			chunk = chunk[:i]
		}
		head := chunk
		if i := strings.Index(head, ">"); i >= 0 {
			head = head[:i]
		}
		class := ""
		if i := strings.Index(head, `class="`); i >= 0 {
			rest := head[i+len(`class="`):]
			class = rest[:strings.Index(rest, `"`)]
		}
		out = append(out, rcRow{
			class:  class,
			attrs:  head,
			hidden: strings.Contains(head, "display:none"),
			folded: strings.Contains(chunk, "▶"),
		})
	}
	return out
}

// rcRowByAttr находит строку по подстроке её атрибутов (data-group/data-parent).
func rcRowByAttr(t *testing.T, html, attr string) rcRow {
	t.Helper()
	for _, r := range rcRows(html) {
		if strings.Contains(r.attrs, attr) {
			return r
		}
	}
	t.Fatalf("строка с %q не найдена:\n%s", attr, html)
	return rcRow{}
}

// Без appearance.collapse_to отчёт открывается развёрнутым — ровно как раньше:
// ни одной скрытой строки и ни одного маркера «свёрнуто».
func TestRenderComposedNoCollapseByDefault(t *testing.T) {
	out := collapseFixture(t, nil)
	if strings.Contains(out, "display:none") {
		t.Fatalf("без collapse_to строки не должны прятаться:\n%s", out)
	}
	if strings.Contains(out, "▶") {
		t.Fatalf("без collapse_to все группы развёрнуты (▼):\n%s", out)
	}
	if strings.Contains(out, "data-collapse-to") {
		t.Fatalf("без ключа атрибут не нужен:\n%s", out)
	}
}

// collapse_to: 0 — видны только группы верхнего уровня со своими итогами;
// вложенные группы, детали и подытоги приходят скрытыми.
func TestRenderComposedCollapseToZero(t *testing.T) {
	out := collapseFixture(t, intPtr(0))
	if !strings.Contains(out, `data-collapse-to="0"`) {
		t.Fatalf("нет data-collapse-to:\n%s", out)
	}

	top := rcRowByAttr(t, out, `data-group="/Иванов"`)
	if top.hidden || !top.folded {
		t.Errorf("группа верхнего уровня должна быть видимой и свёрнутой: %+v", top)
	}
	nested := rcRowByAttr(t, out, `data-group="/Иванов/Услуги"`)
	if !nested.hidden || !nested.folded {
		t.Errorf("вложенная группа должна быть скрытой и свёрнутой: %+v", nested)
	}

	for _, r := range rcRows(out) {
		switch r.class {
		case "det", "subtotal":
			if !r.hidden {
				t.Errorf("строка %s должна быть скрыта: %s", r.class, r.attrs)
			}
		case "grand":
			if r.hidden {
				t.Errorf("общий итог не сворачивается: %s", r.attrs)
			}
		}
	}
}

// collapse_to: 1 — верхний уровень развёрнут (▼), вложенные группы видны, но
// свёрнуты (▶); глубже — скрыто.
func TestRenderComposedCollapseToOne(t *testing.T) {
	out := collapseFixture(t, intPtr(1))

	top := rcRowByAttr(t, out, `data-group="/Иванов"`)
	if top.hidden || top.folded {
		t.Errorf("группа уровня 0 должна быть развёрнута: %+v", top)
	}
	nested := rcRowByAttr(t, out, `data-group="/Иванов/Услуги"`)
	if nested.hidden || !nested.folded {
		t.Errorf("группа уровня 1 должна быть видимой и свёрнутой: %+v", nested)
	}
	// Подытог группы уровня 0 идёт следом за её детьми и остаётся видимым,
	// а всё, что лежит под свёрнутой группой уровня 1, — скрыто.
	if r := rcRowByAttr(t, out, `class="subtotal" data-parent="/Иванов"`); r.hidden {
		t.Errorf("подытог развёрнутой группы виден: %+v", r)
	}
	if r := rcRowByAttr(t, out, `class="det" data-parent="/Иванов/Услуги"`); !r.hidden {
		t.Errorf("детали под свёрнутой группой должны быть скрыты: %+v", r)
	}
	for _, r := range rcRows(out) {
		if r.class == "grp" && r.hidden {
			t.Errorf("при collapse_to=1 группы не прячутся: %s", r.attrs)
		}
	}
}

// Отрицательный уровень равнозначен нулю, а условное оформление строки не
// теряется от добавленного display:none.
func TestRenderComposedCollapseNegativeAndConditional(t *testing.T) {
	if out := collapseFixture(t, intPtr(-3)); !strings.Contains(out, `data-collapse-to="0"`) {
		t.Fatalf("отрицательный уровень должен сводиться к 0:\n%s", out)
	}

	rows := []compose.Row{{"М": "Убыток", "Сумма": "-100"}}
	spec := report.Composition{
		Groupings:   []string{"М"},
		Measures:    []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Totals:      report.Totals{Subtotals: true},
		Conditional: []report.CondRule{{When: "Сумма < 0", Style: report.CellStyle{Color: "#c00"}}},
		Appearance:  report.Appearance{CollapseTo: intPtr(0)},
	}
	res, err := compose.Compose(rows, spec, newInterpEvaluator(interpreter.New()))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	out := string(renderComposedTable(res, &spec))
	if !strings.Contains(out, `style="color:#c00;display:none"`) {
		t.Fatalf("скрытый подытог должен сохранить условное оформление:\n%s", out)
	}
}

// collapseLevel: ключа нет — (0, false); значение — (N, true).
func TestCollapseLevel(t *testing.T) {
	if _, ok := collapseLevel(nil); ok {
		t.Error("nil-компоновка не задаёт сворачивание")
	}
	if _, ok := collapseLevel(&report.Composition{}); ok {
		t.Error("без ключа сворачивания нет")
	}
	if n, ok := collapseLevel(&report.Composition{Appearance: report.Appearance{CollapseTo: intPtr(2)}}); !ok || n != 2 {
		t.Errorf("ожидали (2,true), получили (%d,%v)", n, ok)
	}
	if n, ok := collapseLevel(&report.Composition{Appearance: report.Appearance{CollapseTo: intPtr(0)}}); !ok || n != 0 {
		t.Errorf("ноль — значимое значение: получили (%d,%v)", n, ok)
	}
}
