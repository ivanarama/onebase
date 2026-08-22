package storage_test

import (
	"slices"
	"testing"

	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestListEntityColumns(t *testing.T) {
	tests := []struct {
		name   string
		entity *metadata.Entity
		want   []string
	}{
		{
			name: "catalog",
			entity: &metadata.Entity{
				Kind:   metadata.KindCatalog,
				Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
			},
			want: []string{"id", "наименование", "deletion_mark"},
		},
		{
			name: "document",
			entity: &metadata.Entity{
				Kind: metadata.KindDocument,
				Fields: []metadata.Field{
					{Name: metadata.StandardNumberField, Type: metadata.FieldTypeString},
					{Name: "Дата", Type: metadata.FieldTypeDate},
				},
			},
			want: []string{"id", "номер", "дата", "posted", "deletion_mark"},
		},
		{
			name: "hierarchical predefined catalog",
			entity: &metadata.Entity{
				Kind:         metadata.KindCatalog,
				Hierarchical: true,
				Fields:       []metadata.Field{{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString}},
				Predefined:   []*metadata.PredefinedItem{{Name: "Основной"}},
			},
			want: []string{"id", "код", "deletion_mark", "_is_predefined", "is_folder", "parent_id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := storage.ListEntityColumns(tc.entity); !slices.Equal(got, tc.want) {
				t.Fatalf("ListEntityColumns() = %v, want %v", got, tc.want)
			}
		})
	}
}
