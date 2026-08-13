package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

func newRegistryForEnumTest(t *testing.T) *runtime.Registry {
	t.Helper()
	reg := runtime.NewRegistry()
	reg.Load(runtime.LoadOptions{
		Enums: []*metadata.Enum{
			{
				Name:   "Приоритет",
				Values: []string{"Высокий", "Низкий"},
				ValueTitles: map[string]map[string]string{
					"Высокий": {"en": "High"},
				},
			},
		},
	})
	return reg
}

func TestBuildEnumLabels(t *testing.T) {
	reg := newRegistryForEnumTest(t)
	s := &Server{reg: reg}
	ent := &metadata.Entity{
		Name: "Задача",
		Fields: []metadata.Field{
			{Name: "Приоритет", Type: "enum:Приоритет", EnumName: "Приоритет"},
			{Name: "Имя", Type: "string"},
		},
	}
	labels := s.buildEnumLabels(ent, "en")
	if labels["Приоритет"]["Высокий"] != "High" {
		t.Errorf("labels = %v", labels)
	}
	if _, ok := labels["Имя"]; ok {
		t.Error("не-enum поле не должно попадать в EnumLabels")
	}
}

func TestBuildTPEnumLabels(t *testing.T) {
	reg := newRegistryForEnumTest(t)
	s := &Server{reg: reg}
	ent := &metadata.Entity{
		Name: "Заказ",
		TableParts: []metadata.TablePart{
			{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Приоритет", Type: "enum:Приоритет", EnumName: "Приоритет"},
					{Name: "Количество", Type: "number"},
				},
			},
		},
	}
	tpLabels := s.buildTPEnumLabels(ent, "en")
	tp := tpLabels["Товары"]
	if tp == nil {
		t.Fatal("tpLabels[Товары] == nil")
	}
	if tp["Приоритет"]["Высокий"] != "High" {
		t.Errorf("tpLabels[Товары][Приоритет][Высокий] = %q, ждали High", tp["Приоритет"]["Высокий"])
	}
	if _, ok := tp["Количество"]; ok {
		t.Error("не-enum поле не должно попадать в TPEnumLabels")
	}
}

// TestManagedFormGridEnumAttr проверяет, что при рендере managed-формы
// с ТабличнойЧастью, содержащей enum-поле, в HTML присутствует data-sg-enum
// и флаг "enum":true в data-sg-cols — признаки того, что карта переводов
// прокинута в SlickGrid.
func TestManagedFormGridEnumAttr(t *testing.T) {
	form := &metadata.FormModule{
		Name:       "ФормаОбъекта",
		Kind:       "object",
		EntityName: "Заказ",
		LayoutKind: metadata.FormLayoutManaged,
		Title:      map[string]string{"ru": "Заказ"},
		Elements: []*metadata.FormElement{
			{
				Kind:     metadata.FormElementTablePart,
				Name:     "ЭлементТовары",
				TitleMap: map[string]string{"ru": "Товары"},
				DataPath: "Объект.Товары",
			},
		},
	}
	ent := &metadata.Entity{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		TableParts: []metadata.TablePart{
			{
				Name: "Товары",
				Fields: []metadata.Field{
					{Name: "Приоритет", Type: "enum:Приоритет", EnumName: "Приоритет"},
					{Name: "Количество", Type: "number"},
				},
			},
		},
		Forms: []*metadata.FormModule{form},
	}

	tpEnumLabels := map[string]map[string]map[string]string{
		"Товары": {
			"Приоритет": {"Высокий": "High", "Низкий": "Low"},
		},
	}

	data := map[string]any{
		"Entity":        ent,
		"Form":          form,
		"IsNew":         true,
		"Values":        map[string]string{},
		"RefOptions":    map[string]any{},
		"EnumOptions":   map[string]any{},
		"TPRefOptions":  map[string]any{},
		"TPEnumLabels":  tpEnumLabels,
		"TPEnumOrder":   map[string]map[string][]string{},
		"TPRefMeta":     map[string]any{},
		"TablePartRows": map[string][]map[string]any{"Товары": {}},
		"User":          nil,
		"Lang":          "en",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "page-managed-form", data); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, "data-sg-enum=") {
		t.Error("HTML не содержит data-sg-enum — карта переводов enum не прокинута в грид")
	}
	cols := parseManagedTPColumns(t, html)
	enumFound := false
	for _, col := range cols {
		enumFound = enumFound || col.ID == "Приоритет" && col.Enum
	}
	if !enumFound {
		t.Error("enum-поле не помечено в data-sg-cols")
	}
	if !strings.Contains(html, "Высокий") {
		t.Error("HTML не содержит значений enum-карты в data-sg-enum")
	}
}

func TestLoadEnumOptions_TranslatesLabels(t *testing.T) {
	reg := newRegistryForEnumTest(t)
	s := &Server{reg: reg}
	ent := &metadata.Entity{
		Name: "Задача",
		Fields: []metadata.Field{
			{Name: "Приоритет", Type: "enum:Приоритет", EnumName: "Приоритет"},
		},
	}
	opts := s.loadEnumOptions(ent, "en")
	got := opts["Приоритет"]
	if len(got) != 2 {
		t.Fatalf("ожидалось 2 опции, %d", len(got))
	}
	if got[0].Value != "Высокий" || got[0].Label != "High" {
		t.Errorf("opt0 = %+v (ждали Value=Высокий Label=High)", got[0])
	}
	if got[1].Value != "Низкий" || got[1].Label != "Низкий" {
		t.Errorf("opt1 = %+v", got[1])
	}
}
