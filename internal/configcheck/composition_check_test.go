package configcheck

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
	"github.com/ivantit66/onebase/internal/report"
)

func projWith(c *report.Composition) *project.Project {
	return &project.Project{Reports: []*report.Report{{Name: "R", Query: "ВЫБРАТЬ 1", Composition: c}}}
}

func TestCompositionOK(t *testing.T) {
	c := &report.Composition{
		Groupings:   []string{"М"},
		Measures:    []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Sort:        []report.SortKey{{Field: "Сумма", Dir: "desc"}},
		Chart:       &report.ChartSpec{Type: "bar", Category: "М", Series: []string{"Сумма"}},
		Conditional: []report.CondRule{{When: "Сумма < 0"}},
	}
	if iss := CheckReportComposition(projWith(c)); len(iss) != 0 {
		t.Fatalf("ожидали 0 проблем: %+v", iss)
	}
}

func TestMeasureAlignValidation(t *testing.T) {
	// Недопустимое значение align — должна быть проблема.
	cBad := &report.Composition{
		Groupings: []string{"М"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Align: "diagonal"}},
	}
	iss := CheckReportComposition(projWith(cBad))
	found := false
	for _, i := range iss {
		if strings.Contains(i.Message, "выравнивание") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ожидали проблему про выравнивание, получили: %+v", iss)
	}

	// Допустимые значения — проблем нет.
	for _, align := range []string{"", "left", "right", "center"} {
		cOK := &report.Composition{
			Groupings: []string{"М"},
			Measures:  []report.Measure{{Field: "Сумма", Agg: "sum", Align: align}},
		}
		if iss2 := CheckReportComposition(projWith(cOK)); len(iss2) != 0 {
			t.Fatalf("align=%q: ожидали 0 проблем, получили: %+v", align, iss2)
		}
	}
}

func TestCompositionCrossValidation(t *testing.T) {
	// Кросс-режим (columns) несовместим с detail:true.
	cDetail := &report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
		Detail:    true,
	}
	if iss := CheckReportComposition(projWith(cDetail)); !issuesContain(iss, "детал") {
		t.Fatalf("ожидали проблему про detail в кросс-режиме: %+v", iss)
	}

	// Поле не может быть одновременно в строках и колонках.
	cDup := &report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Товар"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	if iss := CheckReportComposition(projWith(cDup)); !issuesContain(iss, "колонк") {
		t.Fatalf("ожидали проблему про поле и в группировках, и в колонках: %+v", iss)
	}

	// Корректный кросс — без проблем.
	cOK := &report.Composition{
		Groupings: []string{"Товар"},
		Columns:   []string{"Месяц"},
		Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
	}
	if iss := CheckReportComposition(projWith(cOK)); len(iss) != 0 {
		t.Fatalf("корректный кросс: ожидали 0 проблем, получили: %+v", iss)
	}
}

func TestVariantValidation(t *testing.T) {
	// Вариант с неизвестным агрегатом — должна быть проблема с упоминанием варианта.
	rep := &report.Report{
		Name:        "R",
		Query:       "ВЫБРАТЬ 1",
		Composition: &report.Composition{Groupings: []string{"М"}, Measures: []report.Measure{{Field: "Сумма", Agg: "sum"}}},
		Variants: []report.ReportVariant{
			{Name: "Плохой", Composition: &report.Composition{
				Groupings: []string{"М"},
				Measures:  []report.Measure{{Field: "Сумма", Agg: "wat"}},
			}},
		},
	}
	proj := &project.Project{Reports: []*report.Report{rep}}
	iss := CheckReportComposition(proj)
	if !issuesContain(iss, "агрегат") {
		t.Fatalf("ожидали проблему про агрегат в варианте: %+v", iss)
	}
	if !issuesContain(iss, "Плохой") {
		t.Fatalf("ожидали упоминание имени варианта в сообщении: %+v", iss)
	}

	// Корректный вариант — без проблем.
	repOK := &report.Report{
		Name:        "R",
		Query:       "ВЫБРАТЬ 1",
		Composition: &report.Composition{Groupings: []string{"М"}, Measures: []report.Measure{{Field: "Сумма", Agg: "sum"}}},
		Variants: []report.ReportVariant{
			{Name: "По складам", Composition: &report.Composition{
				Groupings: []string{"Склад"},
				Measures:  []report.Measure{{Field: "Сумма", Agg: "sum"}},
			}},
		},
	}
	if iss := CheckReportComposition(&project.Project{Reports: []*report.Report{repOK}}); len(iss) != 0 {
		t.Fatalf("корректный вариант: ожидали 0 проблем, получили: %+v", iss)
	}
}

func issuesContain(iss []Issue, sub string) bool {
	for _, i := range iss {
		if strings.Contains(i.Message, sub) {
			return true
		}
	}
	return false
}

func TestCompositionBad(t *testing.T) {
	c := &report.Composition{
		Groupings:   []string{"М"},
		Measures:    []report.Measure{{Field: "Сумма", Agg: "wat"}},
		Chart:       &report.ChartSpec{Type: "donut", Category: "Нет", Series: []string{"Икс"}},
		Conditional: []report.CondRule{{When: "Сумма < "}}, // битое выражение
	}
	iss := CheckReportComposition(projWith(c))
	if len(iss) < 3 {
		t.Fatalf("ожидали несколько проблем, получили %d: %+v", len(iss), iss)
	}
}

func TestJournalConditionalValidation(t *testing.T) {
	proj := &project.Project{Journals: []*metadata.Journal{{
		Name: "Ж",
		Conditional: []metadata.JournalCondRule{
			{When: "Сумма < 0"},
			{When: "Сумма < "},
		},
	}}}
	iss := CheckJournalConditional(proj)
	if len(iss) != 1 || !strings.Contains(iss[0].Message, "Сумма < ") {
		t.Fatalf("ожидали одну ошибку условия журнала, got %+v", iss)
	}
}

func TestFormConditionalValidation(t *testing.T) {
	proj := &project.Project{Entities: []*metadata.Entity{{
		Name: "Заказ",
		Forms: []*metadata.FormModule{{
			Name:       "ФормаОбъекта",
			LayoutKind: metadata.FormLayoutManaged,
			Conditional: []metadata.FormCondRule{
				{When: "Сумма < 0"},
				{When: "Сумма < "},
			},
		}},
	}}}
	iss := CheckFormConditional(proj)
	if len(iss) != 1 || !strings.Contains(iss[0].Message, "Сумма < ") {
		t.Fatalf("ожидали одну ошибку условия формы, got %+v", iss)
	}
}
