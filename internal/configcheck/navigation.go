package configcheck

import (
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/navigation"
	"github.com/ivantit66/onebase/internal/project"
)

// navigationObjects projects only targets supported by the current runtime nav.
// Account registers remain excluded: legacy buildFlatNav/buildNavFromContents
// do not expose them, even though older contents validation accepts their names.
func navigationObjects(p *project.Project) []navigation.Object {
	var objects []navigation.Object
	add := func(kind, name, title string, titles map[string]string) {
		objects = append(objects, navigation.Object{Target: navigation.Target{Kind: kind, Name: name}, Title: title, Titles: titles})
	}
	for _, e := range p.Entities {
		add(string(e.Kind), e.Name, e.DisplayName(""), e.Titles)
	}
	for _, r := range p.Registers {
		for _, view := range []string{"movements", "balances"} {
			objects = append(objects, navigation.Object{Target: navigation.Target{Kind: "register", Name: r.Name, View: view}, Title: r.DisplayName(""), Titles: r.Titles})
		}
	}
	for _, r := range p.InfoRegisters {
		add("inforeg", r.Name, r.DisplayName(""), r.Titles)
		objects[len(objects)-1].Periodic = r.Periodic
	}
	for _, r := range p.Reports {
		add("report", r.Name, r.DisplayName(""), r.Titles)
		objects[len(objects)-1].External = r.External
	}
	for _, r := range p.Processors {
		add("processor", r.Name, r.DisplayName(""), r.Titles)
		objects[len(objects)-1].External, objects[len(objects)-1].Trusted = r.External, r.Trusted
	}
	for _, r := range p.Journals {
		add("journal", r.Name, r.DisplayName(""), r.Titles)
	}
	for _, r := range p.Pages {
		add("page", r.Name, r.DisplayName(""), r.Titles)
	}
	if len(p.Constants) > 0 {
		add("system", "constants", "Константы", nil)
	}
	return objects
}

// checkNavigation uses the same normalization as the future renderer. Invalid
// targets are errors; duplicate occurrences in one parent are lint warnings.
func checkNavigation(p *project.Project, warnings bool) []Issue {
	objects := navigationObjects(p)
	var issues []Issue
	check := func(file, name string, menu *metadata.Menu, contents *metadata.SubsystemContents, global bool) {
		if menu == nil {
			return
		}
		context := "global"
		if !global {
			context = "subsystem:" + name
		}
		_, diagnostics := navigation.Normalize(context, menu, navigation.NewScope(objects, contents, global))
		for _, d := range diagnostics {
			if d.Warning == warnings {
				issues = append(issues, Issue{File: file, Object: name, Kind: "Навигация", Code: d.Code, Message: d.Node + ": " + d.Message,
					SuggestedFix: "Исправьте menu: стабильные уникальные id, typed target из contents/nav, не более трёх уровней."})
			}
		}
	}
	for _, sub := range p.Subsystems {
		check("subsystems/"+sub.Name+".yaml", sub.Name, sub.Menu, &sub.Contents, false)
		if !warnings && sub.HomePage != nil && sub.HomePage.Menu != nil {
			issues = append(issues, Issue{File: "subsystems/" + sub.Name + ".yaml", Object: sub.Name, Kind: "Навигация", Code: "navigation.placement",
				Message: "menu подсистемы должен находиться на верхнем уровне, вне home_page", SuggestedFix: "Перенесите home_page.menu в menu подсистемы."})
		}
	}
	if p.HomePage != nil {
		check("config/home_page.yaml", "global", p.HomePage.Menu, p.HomePage.Nav, true)
	}
	return issues
}
