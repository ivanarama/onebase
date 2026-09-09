package query_test

import (
	"reflect"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
)

func TestComplexProjectionReportsUnconvertedTypedFields(t *testing.T) {
	entities := []*metadata.Entity{{
		Name: "Заказ",
		Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Код", Type: metadata.FieldTypeString},
			{Name: "Активен", Type: metadata.FieldTypeBool},
			{Name: "Дата", Type: metadata.FieldTypeDate},
		},
	}}
	cases := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "union",
			text: `ВЫБРАТЬ Ссылка, Активен, Дата ИЗ Документ.Заказ
ОБЪЕДИНИТЬ ВСЕ
ВЫБРАТЬ Ссылка, Активен, Дата ИЗ Документ.Заказ`,
			want: []string{"Ссылка", "Активен", "Дата"},
		},
		{
			name: "subquery",
			text: `ВЫБРАТЬ Т.Активен ИЗ
(ВЫБРАТЬ Активен ИЗ Документ.Заказ) КАК Т`,
			want: []string{"Активен"},
		},
		{
			name: "simple query keeps normal conversion",
			text: `ВЫБРАТЬ Ссылка, Активен, Дата ИЗ Документ.Заказ`,
		},
		{
			name: "complex string projection needs no warning",
			text: `ВЫБРАТЬ Код ИЗ Документ.Заказ
ОБЪЕДИНИТЬ ВСЕ
ВЫБРАТЬ Код ИЗ Документ.Заказ`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := query.Compile(tc.text, query.CompileOpts{Entities: entities})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if !reflect.DeepEqual(result.UnconvertedTypedFields, tc.want) {
				t.Fatalf("UnconvertedTypedFields = %v, want %v", result.UnconvertedTypedFields, tc.want)
			}
		})
	}
}

func TestComplexProjectionReportsRegisterReferenceField(t *testing.T) {
	registers := []*metadata.Register{{
		Name: "Партии",
		Dimensions: []metadata.Field{{
			Name:      "Номенклатура",
			Type:      "reference:Номенклатура",
			RefEntity: "Номенклатура",
		}},
	}}
	entities := []*metadata.Entity{{
		Name: "Номенклатура",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{
			Name: "Наименование",
			Type: metadata.FieldTypeString,
		}},
	}}
	text := `ВЫБРАТЬ Номенклатура.Ссылка ИЗ РегистрНакопления.Партии
ОБЪЕДИНИТЬ ВСЕ
ВЫБРАТЬ Номенклатура.Ссылка ИЗ РегистрНакопления.Партии`

	result, err := query.Compile(text, query.CompileOpts{
		Registers: registers,
		Entities:  entities,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if want := []string{"Ссылка"}; !reflect.DeepEqual(result.UnconvertedTypedFields, want) {
		t.Fatalf("UnconvertedTypedFields = %v, want %v", result.UnconvertedTypedFields, want)
	}
}
