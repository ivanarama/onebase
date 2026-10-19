package configcheck

import (
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/project"
)

func okPostProject(posting bool) *project.Project {
	yes := true
	return &project.Project{Entities: []*metadata.Entity{{
		Name:    "Заказ",
		Kind:    metadata.KindDocument,
		Posting: posting,
		Forms: []*metadata.FormModule{{
			Name:    "объекта",
			Actions: map[string]*metadata.FormAction{"ok": {Post: &yes}},
		}},
	}}}
}

func TestCheckFormOKPost(t *testing.T) {
	if warns := CheckFormOKPost(okPostProject(true)); len(warns) != 0 {
		t.Fatalf("у проводимого документа ok.post допустим: %+v", warns)
	}
	warns := CheckFormOKPost(okPostProject(false))
	if len(warns) != 1 || warns[0].Code != "form.ok-post" {
		t.Fatalf("у непроводимой сущности — form.ok-post: %+v", warns)
	}
}
