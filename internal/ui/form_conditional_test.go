package ui

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func TestApplyManagedFormConditionalStyles(t *testing.T) {
	form := testConditionalForm(false)
	rows := map[string][]map[string]any{
		"Товары": {
			{"Номенклатура": "A", "Количество": "-1", "Сумма": "-10"},
			{"Номенклатура": "B", "Количество": "2", "Сумма": "20"},
		},
	}
	warnings := applyManagedFormConditionalStyles(form, rows, map[string]any{"Организация": "Основная"}, newInterpEvaluator(interpreter.New()))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	rowClass := formRowClass(rows["Товары"][0])
	if !strings.HasPrefix(rowClass, "ob-cfr-") {
		t.Fatalf("row class not applied: %q", rowClass)
	}
	cellClass := formCellClass(rows["Товары"][0], "Сумма")
	if !strings.HasPrefix(cellClass, "ob-cfc-") {
		t.Fatalf("cell class not applied: %q", cellClass)
	}
	if got := formRowClass(rows["Товары"][1]); got != "" {
		t.Fatalf("second row should not be styled: %q", got)
	}
	css := formConditionalCSS(form)
	for _, want := range []string{rowClass, cellClass, "background:#fee2e2!important", "font-style:italic!important"} {
		if !strings.Contains(css, want) {
			t.Fatalf("CSS missing %q:\n%s", want, css)
		}
	}
	if strings.Contains(css, "javascript") {
		t.Fatalf("unsafe CSS leaked:\n%s", css)
	}
}

func TestManagedFormConditionalRenderNoGrid(t *testing.T) {
	form := testConditionalForm(true)
	ent := testConditionalEntity(form)
	rows := map[string][]map[string]any{
		"Товары": {{"Номенклатура": "A", "Количество": "-1", "Сумма": "-10"}},
	}
	applyManagedFormConditionalStyles(form, rows, nil, newInterpEvaluator(interpreter.New()))

	html := renderConditionalManagedForm(t, ent, form, rows)
	for _, want := range []string{`<tr class="ob-cfr-`, `<td class="ob-cfc-`, "background:#fee2e2!important"} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered no_grid form missing %q:\n%s", want, html)
		}
	}
}

func TestManagedFormConditionalRenderSlickGrid(t *testing.T) {
	form := testConditionalForm(false)
	ent := testConditionalEntity(form)
	rows := map[string][]map[string]any{
		"Товары": {{"Номенклатура": "A", "Количество": "-1", "Сумма": "-10"}},
	}
	applyManagedFormConditionalStyles(form, rows, nil, newInterpEvaluator(interpreter.New()))

	html := renderConditionalManagedForm(t, ent, form, rows)
	for _, want := range []string{"_form_row_class", "_form_cell_classes", "getItemMetadata = formGridItemMetadata", "cssClass: String(cc[field])"} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered grid form missing %q:\n%s", want, html)
		}
	}
}

func TestManagedFormEventStateAddsConditionalClasses(t *testing.T) {
	form := testConditionalForm(false)
	ent := testConditionalEntity(form)
	obj := &runtime.Object{
		Fields: map[string]any{},
		TablePartRows: map[string][]map[string]any{
			"Товары": {{"Номенклатура": "A", "Количество": "-1", "Сумма": "-10"}},
		},
	}
	s := &Server{interp: interpreter.New()}
	_, tableParts, _, msgs := s.serializeManagedFormEventState(form, ent, obj, nil)
	if len(msgs) != 0 {
		t.Fatalf("unexpected messages: %v", msgs)
	}
	row := tableParts["Товары"][0]
	if formRowClass(row) == "" || formCellClass(row, "Сумма") == "" {
		t.Fatalf("event state missing conditional classes: %+v", row)
	}
}

func testConditionalForm(noGrid bool) *metadata.FormModule {
	return &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: "Заказ",
		LayoutKind: metadata.FormLayoutManaged,
		Elements: []*metadata.FormElement{{
			Kind:     metadata.FormElementTablePart,
			Name:     "ТаблицаТовары",
			TitleMap: map[string]string{"ru": "Товары"},
			DataPath: "Объект.Товары",
			NoGrid:   noGrid,
		}},
		Conditional: []metadata.FormCondRule{
			{Target: "ТаблицаТовары", When: "Количество < 0", Style: metadata.FormCellStyle{Background: "#fee2e2", Bold: true}},
			{Target: "Товары", When: "Сумма < 0", Field: "Сумма", Style: metadata.FormCellStyle{Color: "red;background:url(javascript:alert(1))", Italic: true}},
		},
	}
}

func testConditionalEntity(form *metadata.FormModule) *metadata.Entity {
	return &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{{
			Name: "Товары",
			Fields: []metadata.Field{
				{Name: "Номенклатура", Type: metadata.FieldTypeString},
				{Name: "Количество", Type: metadata.FieldTypeNumber},
				{Name: "Сумма", Type: metadata.FieldTypeNumber},
			},
		}},
		Forms: []*metadata.FormModule{form},
	}
}

func renderConditionalManagedForm(t *testing.T, ent *metadata.Entity, form *metadata.FormModule, rows map[string][]map[string]any) string {
	t.Helper()
	data := map[string]any{
		"Entity":             ent,
		"Form":               form,
		"IsNew":              true,
		"Values":             map[string]string{},
		"RefOptions":         map[string]any{},
		"EnumOptions":        map[string]any{},
		"TPRefOptions":       map[string]map[string][]map[string]any{},
		"TPEnumLabels":       map[string]map[string]map[string]string{},
		"TPEnumOrder":        map[string]map[string][]string{},
		"TPRefMeta":          map[string]map[string]any{},
		"TablePartRows":      rows,
		"FormConditionalCSS": template.CSS(formConditionalCSS(form)),
		"Lang":               "ru",
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}
