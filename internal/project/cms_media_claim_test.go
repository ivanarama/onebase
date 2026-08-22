package project_test

import (
	"slices"
	"testing"

	"github.com/ivantit66/onebase/internal/project"
)

// TestCMSMediaDeclaresCompositeImportClaim keeps the example configuration
// tied to the database guarantee exercised by storage's media transaction test.
// A read-before-create check alone cannot deduplicate concurrent YML imports.
func TestCMSMediaDeclaresCompositeImportClaim(t *testing.T) {
	proj, err := project.Load("../../examples/cms")
	if err != nil {
		t.Fatalf("load CMS example: %v", err)
	}
	defer proj.Close()

	var found bool
	for _, entity := range proj.Entities {
		if entity.Name != "Медиа" {
			continue
		}
		for _, index := range entity.Indexes {
			if index.Unique && slices.Equal(index.Fields, []string{"Сайт", "ВнешнийКлюч"}) {
				found = true
				break
			}
		}
		break
	}
	if !found {
		t.Fatal("CMS Медиа must declare UNIQUE (Сайт, ВнешнийКлюч)")
	}
}
