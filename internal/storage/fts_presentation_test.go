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

func TestBuildFTSDoc_EmptyExplicitPresentationDoesNotUseUnlistedTitle(t *testing.T) {
	e := &metadata.Entity{
		Name: "Номенклатура", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Артикул", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
		Presentation: []string{"Артикул", "Наименование"},
	}
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	doc := BuildFTSDoc(e, id, map[string]any{
		"Код": "Код не является подписью", "Артикул": "", "Наименование": "\u00a0",
	})
	if want := ftsNormalize(id.String()); doc.Title != want {
		t.Fatalf("Title=%q, ожидался id fallback %q без неуказанного Кода", doc.Title, want)
	}
	if doc.Body != "Код не является подписью" {
		t.Fatalf("Body=%q, непредставляющее fulltext-поле всё равно должно индексироваться", doc.Body)
	}
}
