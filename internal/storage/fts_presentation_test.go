package storage

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

func TestBuildFTSDoc_PresentationFallbackPrecedesOtherFields(t *testing.T) {
	e := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Presentation: []string{"Артикул", "Наименование"},
	}
	doc := BuildFTSDoc(e, uuid.New(), map[string]any{
		"Код": "К-1", "Артикул": "", "Наименование": "Стул",
	})
	if doc.Title != "Стул" {
		t.Fatalf("Title=%q, ожидалось fallback-представление Стул", doc.Title)
	}
}

func TestBuildFTSDoc_LegacyEmptyLabelStillFallsThroughFulltextOrder(t *testing.T) {
	e := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
		},
	}
	doc := BuildFTSDoc(e, uuid.New(), map[string]any{
		"Код": "Код прежний", "Наименование": "", "Артикул": "Артикул другой",
	})
	if doc.Title != "Код прежний" {
		t.Fatalf("legacy Title=%q, ожидался прежний fallback по fulltext-порядку Код", doc.Title)
	}
}
