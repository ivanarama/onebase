package navigation_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
)

func schoolObjects() []navigation.Object {
	return []navigation.Object{
		{Target: navigation.Target{Kind: "catalog", Name: "Years"}, Title: "Годы", Titles: map[string]string{"en": "Years"}},
		{Target: navigation.Target{Kind: "catalog", Name: "Periods"}, Title: "Периоды"},
		{Target: navigation.Target{Kind: "document", Name: "Order"}, Title: "Приказ"},
		{Target: navigation.Target{Kind: "inforeg", Name: "Bells"}, Title: "Звонки", Periodic: true},
		{Target: navigation.Target{Kind: "processor", Name: "ClassJournal"}, Title: "Журнал"},
		{Target: navigation.Target{Kind: "register", Name: "Attendance", View: "movements"}, Title: "Посещаемость"},
		{Target: navigation.Target{Kind: "register", Name: "Attendance", View: "balances"}, Title: "Посещаемость"},
		{Target: navigation.Target{Kind: "page", Name: "Dashboard"}, Title: "Панель"},
		{Target: navigation.Target{Kind: "system", Name: "constants"}, Title: "Константы"},
	}
}

func schoolContents() *metadata.SubsystemContents {
	return &metadata.SubsystemContents{Catalogs: []string{"Periods", "Years"}, Documents: []string{"Order"}, InfoRegs: []string{"Bells"},
		Processors: []string{"ClassJournal"}, Registers: []string{"Attendance"}, Pages: []string{"Dashboard"}}
}

func TestNormalizeMixedSchoolMenu(t *testing.T) {
	menu := &metadata.Menu{Sections: []metadata.MenuSection{{ID: "academic-years", Title: "Учебные годы", Titles: map[string]string{"en": "Academic years"},
		Items: []metadata.MenuItem{{ID: "years", Target: "catalog:Years"}},
		Groups: []metadata.MenuGroup{{ID: "work", Title: "Работа", Items: []metadata.MenuItem{
			{ID: "order", Target: "document:Order"}, {ID: "bells", Target: "inforeg:Bells"}, {ID: "journal", Target: "processor:ClassJournal"},
			{ID: "years-again", Target: "catalog:Years"},
		}}},
	}}}
	objects := schoolObjects()
	scope := navigation.NewScope(objects, schoolContents(), false)
	tree, diagnostics := navigation.Normalize("Education", menu, scope)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if len(tree.Sections) != 2 || tree.Sections[1].ID != "cfg:other" {
		t.Fatalf("unplaced targets missing: %+v", tree)
	}
	if got := tree.Sections[0].Groups[0].Items; len(got) != 4 || got[0].Target != "document:Order" || got[2].Target != "processor:ClassJournal" {
		t.Fatalf("mixed group/order lost: %+v", got)
	}
	var other []string
	for _, i := range tree.Sections[1].Items {
		other = append(other, i.Target)
	}
	want := "catalog:Periods,register:Attendance:movements,register:Attendance:balances,page:Dashboard"
	if strings.Join(other, ",") != want {
		t.Fatalf("other=%v; want %s", other, want)
	}
	hash, err := tree.Hash()
	if err != nil {
		t.Fatal(err)
	}
	treeAgain, _ := navigation.Normalize("Education", menu, scope)
	hashAgain, _ := treeAgain.Hash()
	if hash != hashAgain || !strings.HasPrefix(hash, "sha256:") || len(hash) != 71 {
		t.Fatalf("unstable hash: %s/%s", hash, hashAgain)
	}
	tree.Sections[0].Titles["en"] = "Changed"
	tree.Sections[0].Items[0].Object.Titles["en"] = "Changed"
	if menu.Sections[0].Titles["en"] != "Academic years" || objects[0].Titles["en"] != "Years" {
		t.Fatal("normalization mutated metadata")
	}
	hashUnchanged, _ := treeAgain.Hash()
	if hashUnchanged != hash {
		t.Fatal("normalizations share maps")
	}
}

func TestGlobalScopeCompatibilityMatrix(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, mode := range []string{"nil", "empty", "scoped"} {
			for _, semantic := range []bool{false, true} {
				t.Run(strings.Join([]string{mode, map[bool]string{true: "global", false: "subsystem"}[global], map[bool]string{true: "menu", false: "legacy"}[semantic]}, "/"), func(t *testing.T) {
					var contents *metadata.SubsystemContents
					if mode == "empty" {
						contents = &metadata.SubsystemContents{}
					}
					if mode == "scoped" {
						contents = &metadata.SubsystemContents{Catalogs: []string{"Years"}, Registers: []string{"Attendance"}, Pages: []string{"Dashboard"}}
					}
					var menu *metadata.Menu
					if semantic {
						menu = &metadata.Menu{}
					}
					tree, diags := navigation.Normalize("context", menu, navigation.NewScope(schoolObjects(), contents, global))
					if len(diags) != 0 {
						t.Fatal(diags)
					}
					targets := map[string]bool{}
					for _, s := range tree.Sections {
						for _, i := range s.Items {
							targets[i.Target] = true
						}
					}
					flat := global && mode != "scoped"
					if targets["system:constants"] != flat {
						t.Fatalf("constants membership: %v", targets)
					}
					if targets["page:Dashboard"] != (mode == "scoped") {
						t.Fatalf("pages membership: %v", targets)
					}
					if mode == "scoped" || flat {
						if !targets["register:Attendance:movements"] || !targets["register:Attendance:balances"] {
							t.Fatal("register view missing")
						}
					} else if len(targets) != 0 {
						t.Fatalf("empty subsystem exposed objects: %v", targets)
					}
					if semantic && len(targets) > 0 && (len(tree.Sections) != 1 || tree.Sections[0].ID != "cfg:other") {
						t.Fatal("semantic other missing")
					}
				})
			}
		}
	}
}

func TestNormalizeRejectsInvalidReferencesAndIDs(t *testing.T) {
	for _, tc := range []struct{ id, target, code string }{
		{"valid", "catalog:Missing", "navigation.membership"},
		{"valid", "system:constants", "navigation.membership"},
		{"valid", "https://example.com", "navigation.target"},
		{"valid", "register:Attendance:totals", "navigation.target"},
		{"bad_id", "catalog:Years", "navigation.id"},
		{"other", "catalog:Years", "navigation.reserved-id"},
		{"section", "catalog:Years", "navigation.duplicate-id"},
	} {
		t.Run(tc.target+"/"+tc.id, func(t *testing.T) {
			menu := &metadata.Menu{Sections: []metadata.MenuSection{{ID: "section", Title: "Title", Items: []metadata.MenuItem{{ID: tc.id, Target: tc.target}}}}}
			_, diags := navigation.Normalize("sub", menu, navigation.NewScope(schoolObjects(), schoolContents(), false))
			for _, d := range diags {
				if d.Code == tc.code && !d.Warning {
					return
				}
			}
			t.Fatalf("missing %s: %+v", tc.code, diags)
		})
	}
}

func TestDuplicateOccurrenceAndResolver(t *testing.T) {
	menu := &metadata.Menu{Sections: []metadata.MenuSection{{ID: "section", Title: "Title", Items: []metadata.MenuItem{
		{ID: "first", Target: "catalog:Years"}, {ID: "second", Target: "catalog:Years", Title: "Custom", Titles: map[string]string{"en": "Custom EN"}},
	}}}}
	tree, diags := navigation.Normalize("sub", menu, navigation.NewScope(schoolObjects(), schoolContents(), false))
	if len(diags) != 1 || !diags[0].Warning || diags[0].Code != "navigation.duplicate-target" {
		t.Fatal(diags)
	}
	translate := func(s string) string { return s }
	first := navigation.ResolveItem(tree.Sections[0].Items[0], "en", "Education & Science", translate, false)
	if first.Label != "Years" || first.URL != "/ui/catalog/Years?subsystem=Education+%26+Science" || first.Action != "read" {
		t.Fatal(first)
	}
	second := navigation.ResolveItem(tree.Sections[0].Items[1], "en", "", translate, false)
	if second.Label != "Custom EN" {
		t.Fatal(second)
	}
	for _, i := range tree.Sections[1].Items {
		r := navigation.ResolveItem(i, "ru", "", translate, false)
		if i.Target == "register:Attendance:balances" && r.URL != "/ui/register/attendance/balances" {
			t.Fatal(r)
		}
		if i.Target == "processor:ClassJournal" && r.Action != "run" {
			t.Fatal(r)
		}
	}
}

func TestTargetGrammar(t *testing.T) {
	for _, s := range []string{"catalog:X", "document:Приказ", "register:X:movements", "register:X:balances", "inforeg:X", "report:X", "processor:X", "journal:X", "page:X", "system:constants"} {
		target, err := navigation.ParseTarget(s)
		if err != nil || target.String() != s {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"", "catalog:", "catalog:..", "catalog:X/Y", "catalog:X?subsystem=Z", "catalog:X%2fY", "catalog:X\n", "catalog:X:view", "register:X", "register:X:other", "system:admin", "https://example.com", "CATALOG:X"} {
		if _, err := navigation.ParseTarget(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestNormalizeLimitsAndIconNames(t *testing.T) {
	menu := &metadata.Menu{Sections: []metadata.MenuSection{{ID: "section", Title: "Title", Icon: " Calendar_Days ", Items: []metadata.MenuItem{{ID: "item", Target: "catalog:Years"}}}}}
	tree, diags := navigation.Normalize("sub", menu, navigation.NewScope(schoolObjects(), schoolContents(), false))
	if len(diags) != 0 || tree.Sections[0].Icon != "calendar-days" {
		t.Fatalf("normalization: %+v %+v", tree, diags)
	}
	for i := 0; i < navigation.MaxSections; i++ {
		menu.Sections = append(menu.Sections, metadata.MenuSection{ID: fmt.Sprintf("section-%d", i), Title: "Title"})
	}
	_, diags = navigation.Normalize("sub", menu, navigation.NewScope(schoolObjects(), schoolContents(), false))
	for _, d := range diags {
		if d.Code == "navigation.limit" && !d.Warning {
			return
		}
	}
	t.Fatal("menu size limit missing")
}

func TestResolverTranslationOnlyOverrideKeepsDefaultRegisterView(t *testing.T) {
	menu := &metadata.Menu{Sections: []metadata.MenuSection{{ID: "section", Title: "Title", Items: []metadata.MenuItem{
		{ID: "balances", Target: "register:Attendance:balances", Titles: map[string]string{"en": "Available"}},
	}}}}
	tree, diags := navigation.Normalize("sub", menu, navigation.NewScope(schoolObjects(), schoolContents(), false))
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	item := tree.Sections[0].Items[0]
	translate := func(s string) string { return s }
	if got := navigation.ResolveItem(item, "ru", "", translate, false).Label; got != "Посещаемость (остатки)" {
		t.Fatalf("default register view lost: %s", got)
	}
	if got := navigation.ResolveItem(item, "en", "", translate, false).Label; got != "Available" {
		t.Fatalf("override lost: %s", got)
	}
}
