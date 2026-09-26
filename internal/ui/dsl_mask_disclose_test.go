package ui

import (
	"context"
	"testing"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/metadata"
)

func maskDSLEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Клиент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Телефон", Type: metadata.FieldTypeString, PII: true}},
	}
}

// Без права disclose значение ПДн видно коду только под маской.
func TestDSLFieldMasked_WithoutDisclose(t *testing.T) {
	ent := maskDSLEntity()
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	ctx := auth.ContextWithUser(context.Background(), uiMaskUser([]string{"read", "write"}, nil))
	if !s.dslFieldMasked(ctx, ent, "Телефон") {
		t.Fatal("ПДн отдано коду без маски")
	}
}

// С правом disclose маска в КОДЕ снимается: обработка переносит значение из
// документа в документ (копия адреса, повторная заявка), и звёздочки вместо
// телефона — это испорченные данные, а не защита.
func TestDSLFieldMasked_DiscloseSeesValue(t *testing.T) {
	ent := maskDSLEntity()
	s, _ := newSubmitTestServer(t, []*metadata.Entity{ent})
	ctx := auth.ContextWithUser(context.Background(), uiMaskUser([]string{"read", "write", "disclose"}, nil))
	if s.dslFieldMasked(ctx, ent, "Телефон") {
		t.Fatal("право disclose не сняло маску в DSL")
	}
}
