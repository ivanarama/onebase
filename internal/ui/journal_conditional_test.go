package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestApplyJournalConditionalStyles(t *testing.T) {
	rows := []map[string]any{
		{"_doc_kind": "Реализация", "id": "1", "Сумма": "-10"},
		{"_doc_kind": "Поступление", "id": "2", "Сумма": "20"},
	}
	rules := []metadata.JournalCondRule{
		{When: "Сумма < 0", Style: metadata.JournalCellStyle{Background: "#fee", Bold: true}},
		{When: `Документ = "Реализация"`, Field: "Документ", Style: metadata.JournalCellStyle{Color: "#c00"}},
		{When: "Сумма < 0", Field: "Сумма", Style: metadata.JournalCellStyle{Color: "red;background:url(javascript:alert(1))", Italic: true}},
	}

	warnings := applyJournalConditionalStyles(rows, rules, newInterpEvaluator(interpreter.New()))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if got := journalRowStyle(rows[0]); !strings.Contains(got, "background:#fee") || !strings.Contains(got, "font-weight:bold") {
		t.Fatalf("row style not applied: %q", got)
	}
	if got := journalCellStyle(rows[0], "_doc_kind"); got != "color:#c00" {
		t.Fatalf("document cell style: %q", got)
	}
	if got := journalCellStyle(rows[0], "Сумма"); got != "font-style:italic" {
		t.Fatalf("invalid color must be stripped but italic kept, got %q", got)
	}
	if got := journalRowStyle(rows[1]); got != "" {
		t.Fatalf("second row should not be styled: %q", got)
	}
}

func TestJournalConditionalRender(t *testing.T) {
	j := testJournal()
	rows := []map[string]any{{"_doc_kind": "Док", "id": "1", "Сумма": "-10", "Номер": "N1", "Контрагент": "К"}}
	applyJournalConditionalStyles(rows, []metadata.JournalCondRule{
		{When: "Сумма < 0", Style: metadata.JournalCellStyle{Background: "#fee"}},
		{When: "Сумма < 0", Field: "Сумма", Style: metadata.JournalCellStyle{Color: "#c00"}},
	}, newInterpEvaluator(interpreter.New()))

	var buf bytes.Buffer
	data := map[string]any{
		"Journal":                j,
		"JournalColumns":         j.Columns,
		"JournalSettingsColumns": journalSettingsColumns(j, nil),
		"JournalSettingsJSON":    journalSettingsJSON(j, nil),
		"Rows":                   rows,
		"Total":                  1,
		"Params":                 storage.ListParams{Filters: map[string]storage.FilterValue{}},
		"FilterOptions":          map[string][]map[string]any{},
		"ColFormats":             map[string]string{},
		"RequestURI":             "/ui/journal/журнал",
		"Cfg":                    Config{},
		"Lang":                   "ru",
	}
	if err := tmpl.ExecuteTemplate(&buf, "page-journal", data); err != nil {
		t.Fatalf("execute page-journal: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"background:#fee", "color:#c00"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered journal missing %q:\n%s", want, out)
		}
	}
}
