package configcheck

import (
	"github.com/ivantit66/onebase/internal/csssafe"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func CheckWidgetAppearance(proj *project.Project) []Issue {
	var issues []Issue
	for _, w := range proj.Widgets {
		if w != nil && w.Background != "" && csssafe.Color(w.Background) == "" {
			issues = append(issues, Issue{File: "widgets/" + w.Name + ".yaml", Object: w.Name, Kind: "Виджет", Message: "background: недопустимый CSS-цвет"})
		}
	}
	check := func(file string, hp *metadata.HomePage) {
		if hp != nil && hp.SidePanel != nil && hp.SidePanel.Background != "" && csssafe.Color(hp.SidePanel.Background) == "" {
			issues = append(issues, Issue{File: file, Kind: "Главная", Message: "side_panel.background: недопустимый CSS-цвет"})
		}
	}
	check("config/home_page.yaml", proj.HomePage)
	for _, s := range proj.Subsystems {
		if s != nil {
			check("subsystems/"+s.Name+".yaml", s.HomePage)
		}
	}
	return issues
}
