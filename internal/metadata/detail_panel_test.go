package metadata

// Валидация блока detail_panel (план 118C). Опечатка в имени реквизита иначе
// дала бы пустую строку в панели без объяснения — автор искал бы причину в
// данных, а не в YAML.

import (
	"os"
	"path/filepath"
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
		{"fields и явный пустой tabs вместе", &DetailPanel{FieldsSet: true, TabsSet: true}, "либо fields, либо tabs"},
		{"явный пустой tabs", &DetailPanel{TabsSet: true}, "tabs не может быть пустым"},
		{"пустая закладка", &DetailPanel{Tabs: []DetailPanelTab{{Name: "Т"}}}, ".fields не может быть пустым"},
		{"минимальная ширина", &DetailPanel{Width: DetailPanelMinWidth}, ""},
		{"максимальная ширина", &DetailPanel{Width: DetailPanelMaxWidth}, ""},
		{"слишком узко", &DetailPanel{Width: DetailPanelMinWidth - 1}, "detail_panel.width"},
		{"слишком широко", &DetailPanel{Width: DetailPanelMaxWidth + 1}, "detail_panel.width"},
		{"tableparts 118D", &DetailPanel{Tabs: []DetailPanelTab{{Name: "Lines", TablePartsSet: true}}}, "пока не поддерживаются"},
		{"attachments false 118D", &DetailPanel{Tabs: []DetailPanelTab{{Name: "Files", AttachmentsSet: true}}}, "пока не поддерживаются"},
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

func TestLoadDetailPanelPreservesExplicitEmptyTabs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entity.yaml")
	data := []byte("name: Товар\nfields:\n  - {name: Наименование, type: string}\ndetail_panel:\n  tabs: []\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	entity, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if entity.DetailPanel == nil || !entity.DetailPanel.TabsSet || len(entity.DetailPanel.Tabs) != 0 {
		t.Fatalf("explicit tabs: [] was lost: %+v", entity.DetailPanel)
	}
	if err := Validate([]*Entity{entity}, nil); err == nil || !strings.Contains(err.Error(), "tabs не может быть пустым") {
		t.Fatalf("explicit empty tabs must fail closed, got %v", err)
	}
}

func TestLoadDetailPanelPreservesUnsupportedDeclarationsForValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entity.yaml")
	data := []byte(`name: Товар
fields:
  - {name: Наименование, type: string}
detail_panel:
  tabs:
    - name: Файлы
      tableparts: []
      attachments: false
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	entity, err := LoadFile(path, KindCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if entity.DetailPanel == nil || len(entity.DetailPanel.Tabs) != 1 {
		t.Fatalf("detail panel not loaded: %+v", entity.DetailPanel)
	}
	tab := entity.DetailPanel.Tabs[0]
	if !tab.TablePartsSet || !tab.AttachmentsSet {
		t.Fatalf("explicit empty/false reserved keys were lost: %+v", tab)
	}
	if err := Validate([]*Entity{entity}, nil); err == nil || !strings.Contains(err.Error(), "пока не поддерживаются") {
		t.Fatalf("unsupported declarations were not rejected: %v", err)
	}
}
