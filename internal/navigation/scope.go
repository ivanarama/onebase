package navigation

import (
	"sort"

	"github.com/ivantit66/onebase/internal/metadata"
)

var kinds = []struct{ kind, title string }{
	{"catalog", "Справочники"}, {"document", "Документы"}, {"register", "Регистры"},
	{"inforeg", "Регистры сведений"}, {"report", "Отчёты"}, {"processor", "Обработки"},
	{"journal", "Журналы"}, {"page", "Страницы"}, {"system", "Настройки"},
}

// NewScope constructs legacy membership. For a subsystem, contents are always
// scoped, including empty contents. For global nil/empty nav, legacy flat scope
// includes constants and excludes pages. Both register projections are targets.
func NewScope(objects []Object, contents *metadata.SubsystemContents, global bool) Scope {
	flat := global && contents.IsEmpty()
	scope := Scope{objects: map[string]Object{}}
	for _, kind := range kinds {
		section := Section{ID: "cfg:legacy-" + kind.kind, Title: kind.title}
		var ordered []Object
		if flat {
			if kind.kind == "page" {
				continue
			}
			for _, o := range objects {
				if o.Target.Kind == kind.kind {
					ordered = append(ordered, o)
				}
			}
			sort.Slice(ordered, func(i, j int) bool {
				a, b := ordered[i], ordered[j]
				label := func(s string) string { return s }
				al, bl := a.Label("ru", label, true), b.Label("ru", label, true)
				if al != bl {
					return al < bl
				}
				return a.Target.URL("") < b.Target.URL("")
			})
		} else {
			var names []string
			if contents != nil {
				switch kind.kind {
				case "catalog":
					names = contents.Catalogs
				case "document":
					names = contents.Documents
				case "register":
					names = contents.Registers
				case "inforeg":
					names = contents.InfoRegs
				case "report":
					names = contents.Reports
				case "processor":
					names = contents.Processors
				case "journal":
					names = contents.Journals
				case "page":
					names = contents.Pages
				}
			}
			byName := make(map[string][]Object)
			for _, o := range objects {
				if o.Target.Kind == kind.kind {
					byName[o.Target.Name] = append(byName[o.Target.Name], o)
				}
			}
			for _, name := range names {
				ordered = append(ordered, byName[name]...)
				delete(byName, name)
			}
		}
		for _, o := range ordered {
			o = cloneObject(o)
			scope.objects[o.Target.key()] = o
			section.Items = append(section.Items, Item{ID: targetID(o.Target), Target: o.Target.String(), Object: o})
		}
		if len(section.Items) > 0 {
			scope.Sections = append(scope.Sections, section)
		}
	}
	return scope
}
