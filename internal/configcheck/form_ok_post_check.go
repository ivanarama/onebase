package configcheck

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/project"
)

// CheckFormOKPost предупреждает об actions.ok.post у формы непроводимой
// сущности: «ОК» там остаётся записью, и ключ ничего не меняет.
func CheckFormOKPost(proj *project.Project) []Issue {
	if proj == nil {
		return nil
	}
	var warns []Issue
	for _, ent := range proj.Entities {
		if ent == nil || ent.Posting {
			continue
		}
		for _, form := range ent.Forms {
			if !form.OKPosts() {
				continue
			}
			warns = append(warns, Issue{
				File:         formFileLabel(ent, form),
				Object:       ent.Name,
				Kind:         "Управляемая форма",
				Code:         "form.ok-post",
				Message:      fmt.Sprintf("actions.ok.post игнорируется: %s не проводится, «ОК» остаётся записью", ent.Name),
				SuggestedFix: "Удалите actions.ok.post или включите проведение (posting: true).",
			})
		}
	}
	return warns
}
