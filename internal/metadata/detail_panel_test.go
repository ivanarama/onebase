package metadata

// Валидация блока detail_panel (план 118C). Опечатка в имени реквизита иначе
// дала бы пустую строку в панели без объяснения — автор искал бы причину в
// данных, а не в YAML.

import (
	"strings"
	"testing"
)

func panelValidateEntity(dp *DetailPanel) []*Entity {
	return []*Entity{{
		Name: "Номенклатура",
		Kind: KindCatalog,
		Fields: []Field{
			{Name: "Наименование", Type: FieldTypeString},
			{Name: "Артикул", Type: FieldTypeString},
		},
		DetailPanel: dp,
	}}
}

func TestValidateDetailPanel(t *testing.T) {
	cases := []struct {
		name string
		dp   *DetailPanel
		want string // подстрока ошибки; "" — валидно
	}{
		{"без блока", nil, ""},
		{"корректные закладки", &DetailPanel{Tabs: []DetailPanelTab{{Name: "Основное", Fields: []string{"Артикул"}}}}, ""},
		{"короткая форма", &DetailPanel{Fields: []string{"Артикул"}, FieldsSet: true}, ""},
		{"опечатка в fields", &DetailPanel{Fields: []string{"Артикл"}, FieldsSet: true}, "неизвестный реквизит"},
		{"опечатка в закладке", &DetailPanel{Tabs: []DetailPanelTab{{Name: "Т", Fields: []string{"Нет"}}}}, "неизвестный реквизит"},
		{"опечатка в title", &DetailPanel{Title: "Нет"}, "detail_panel.title"},
		{"закладка без имени", &DetailPanel{Tabs: []DetailPanelTab{{Fields: []string{"Артикул"}}}}, "нет имени"},
		{"дубль закладки", &DetailPanel{Tabs: []DetailPanelTab{{Name: "Т", Fields: []string{"Артикул"}}, {Name: "т", Fields: []string{"Наименование"}}}}, "дважды"},
		{"fields и tabs вместе", &DetailPanel{Fields: []string{"Артикул"}, FieldsSet: true, Tabs: []DetailPanelTab{{Name: "Т", Fields: []string{"Наименование"}}}}, "либо fields, либо tabs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(panelValidateEntity(c.dp), nil)
			if c.want == "" {
				if err != nil {
					t.Fatalf("ожидалось без ошибки, получено: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ожидалась ошибка про %q, получено: %v", c.want, err)
			}
		})
	}
}
