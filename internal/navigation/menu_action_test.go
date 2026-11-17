package navigation_test

import (
	"net/url"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"gopkg.in/yaml.v3"
)

// Команда «Создать» в дереве меню (1С «Новый документ»): action: new открывает
// форму нового объекта и требует права create; список и «создать» одного
// документа уживаются в одной папке.
func TestMenuActionNewResolvesCreateForm(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "recursive"}[recursive], func(t *testing.T) {
			section := metadata.MenuSection{ID: "otk", Title: "ОТК"}
			items := []metadata.MenuItem{{ID: "list", Target: "document:Жалоба"}, {ID: "add", Target: "document:Жалоба", Action: "new", Title: "Добавить жалобу"}, {ID: "item", Target: "catalog:Мастер", Action: "new"}}
			if recursive {
				section.Nodes = []metadata.MenuNode{{ID: "docs", Title: "Документы", Nodes: []metadata.MenuNode{
					{ID: "list", Target: items[0].Target}, {ID: "add", Target: items[1].Target, Action: "new", Title: items[1].Title}, {ID: "item", Target: items[2].Target, Action: "new"},
				}}}
			} else {
				section.Items = items
			}
			objects := []navigation.Object{{Target: navigation.Target{Kind: "document", Name: "Жалоба"}}, {Target: navigation.Target{Kind: "catalog", Name: "Мастер"}, Title: "Мастер"}}
			tree, ds := navigation.Normalize("subsystem:ОТК", &metadata.Menu{Sections: []metadata.MenuSection{section}}, navigation.NewScope(objects, nil, true))
			if len(ds) != 0 {
				t.Fatal(ds)
			}
			var got []navigation.ResolvedItem
			navigation.WalkItems(tree, func(i navigation.Item) {
				got = append(got, navigation.ResolveItem(i, "ru", "ОТК", func(s string) string { return s }, false))
			})
			if len(got) != 3 {
				t.Fatal(got)
			}
			path := func(raw string) string {
				u, err := url.Parse(raw)
				if err != nil {
					t.Fatal(err)
				}
				return u.Path
			}
			if path(got[0].URL) != "/ui/document/Жалоба" || got[0].Action != "read" {
				t.Fatalf("список: %+v", got[0])
			}
			if u, _ := url.Parse(got[1].URL); u.Query().Get("subsystem") != "ОТК" {
				t.Fatalf("контекст подсистемы потерян: %s", got[1].URL)
			}
			if path(got[1].URL) != "/ui/document/Жалоба/new" || got[1].Action != "create" || got[1].Label != "Добавить жалобу" {
				t.Fatalf("новый документ: %+v", got[1])
			}
			if path(got[2].URL) != "/ui/catalog/Мастер/new" || got[2].Action != "create" || got[2].Label != "Создать: Мастер" {
				t.Fatalf("новый элемент: %+v", got[2])
			}
		})
	}
}

func TestMenuActionInvalidBindings(t *testing.T) {
	scope := navigation.NewScope([]navigation.Object{
		{Target: navigation.Target{Kind: "report", Name: "Work"}},
		{Target: navigation.Target{Kind: "document", Name: "Жалоба"}},
	}, nil, true)
	for _, body := range []string{
		"nodes: [{id: a, target: 'report:Work', action: new}]",
		"items: [{id: a, target: 'report:Work', action: new}]",
		"nodes: [{id: a, target: 'document:Жалоба', action: open}]",
		"items: [{id: a, target: 'document:Жалоба', action: delete}]",
		"nodes: [{id: a, title: Folder, action: new, nodes: []}]",
	} {
		t.Run(body, func(t *testing.T) {
			var menu metadata.Menu
			if err := yaml.Unmarshal([]byte("sections:\n- id: s\n  title: S\n  "+body+"\n"), &menu); err != nil {
				return // папка с action отвергается строгим разбором метаданных
			}
			_, ds := navigation.Normalize("global", &menu, scope)
			for _, d := range ds {
				if !d.Warning && d.Code == "navigation.action" {
					return
				}
			}
			t.Fatal("invalid menu action accepted", ds)
		})
	}
}
