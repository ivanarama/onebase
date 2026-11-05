package configcheck

import (
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

// CheckReferencePages keeps custom reference navigation inside declared pages.
func CheckReferencePages(proj *project.Project) []Issue {
	pages := map[string]bool{}
	for _, page := range proj.Pages {
		pages[strings.ToLower(page.Name)] = true
	}
	var issues []Issue
	forEachProjectForm(proj, func(owner *metadata.Entity, form *metadata.FormModule) {
		walkFormElements(form.Elements, func(el *metadata.FormElement) {
			if el == nil || el.ReferencePage == "" {
				return
			}
			if el.Kind != metadata.FormElementField || !formRefCardFieldPath(owner, form, el.DataPath) {
				issues = append(issues, formKeyIssue(owner, form, el, "form.reference-page", "reference_page разрешён только у ссылочного поля ввода", "Укажите ссылочный data_path или уберите reference_page."))
			}
			if !pages[strings.ToLower(el.ReferencePage)] {
				issues = append(issues, formKeyIssue(owner, form, el, "form.reference-page", fmt.Sprintf("страница %q не найдена", el.ReferencePage), "Укажите имя страницы из pages/*.yaml."))
			}
		})
	})
	return issues
}
