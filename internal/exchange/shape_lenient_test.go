package exchange

// Приём пакета обмена терпим к НЕДОСТАЮЩИМ полям и строг к неизвестным
// (план 117D).
//
// Раньше требовалось точное совпадение набора: появление нового реквизита —
// например «Кода» при включении нумератора — роняло приём целиком, вместе с
// пакетами, уже лежащими в очереди и снятыми до изменения. Обновить все узлы
// одномоментно на живом обмене нереально.

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
)

func shapeEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Наименование", Type: metadata.FieldTypeString},
		},
	}
}

func TestValidateObjectShape_MissingFieldAccepted(t *testing.T) {
	obj := PackageObject{ID: "1", Fields: map[string]any{"Наименование": "Альфа"}}
	if err := validateObjectShape(shapeEntity(), obj); err != nil {
		t.Fatalf("пакет без нового реквизита отклонён: %v", err)
	}
}

func TestValidateObjectShape_UnknownFieldRejected(t *testing.T) {
	obj := PackageObject{ID: "1", Fields: map[string]any{
		"Код": "К-1", "Наименование": "Альфа", "Чужое": "x",
	}}
	err := validateObjectShape(shapeEntity(), obj)
	if err == nil || !strings.Contains(err.Error(), "неизвестное поле") {
		t.Fatalf("неизвестное поле принято: %v", err)
	}
}

func TestValidateObjectShape_FullShapeStillValid(t *testing.T) {
	obj := PackageObject{ID: "1", Fields: map[string]any{"Код": "К-1", "Наименование": "Альфа"}}
	if err := validateObjectShape(shapeEntity(), obj); err != nil {
		t.Fatalf("полный набор отклонён: %v", err)
	}
}
