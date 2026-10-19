package widget

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Константы из миграции и формы хранятся JSON-строками. Проверяем результат
// настоящего запроса виджета, чтобы строка "true" не скрылась за mock-резолвером.
func TestRun_StoredConstantTypes(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		entity := &metadata.Entity{Name: "Товар", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}}}
		if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, entity.Name, uuid.New(), map[string]any{"Наименование": "Молоко"}, entity); err != nil {
			t.Fatal(err)
		}
		for i, tc := range []struct {
			name      string
			typ       metadata.FieldType
			def       string
			stored    any
			where     string
			wantRows  int
			wantError bool
		}{
			{name: "default true", typ: metadata.FieldTypeBool, def: "true", where: "&Значение", wantRows: 1},
			{name: "default false", typ: metadata.FieldTypeBool, def: "false", where: "&Значение"},
			{name: "form true", typ: "boolean", stored: "true", where: "&Значение", wantRows: 1},
			{name: "form false", typ: metadata.FieldTypeBool, stored: "false", where: "&Значение"},
			{name: "typed true", typ: metadata.FieldTypeBool, stored: true, where: "&Значение", wantRows: 1},
			{name: "typed false", typ: metadata.FieldTypeBool, stored: false, where: "&Значение"},
			{name: "default number", typ: metadata.FieldTypeNumber, def: "0.5", where: "&Значение > 10"},
			{name: "form number", typ: metadata.FieldTypeNumber, stored: "12,5", where: "&Значение > 10", wantRows: 1},
			{name: "typed number", typ: metadata.FieldTypeNumber, stored: 12.5, where: "&Значение > 10", wantRows: 1},
			{name: "invalid boolean", typ: metadata.FieldTypeBool, stored: "не булево", where: "&Значение", wantError: true},
			{name: "invalid number", typ: metadata.FieldTypeNumber, stored: "не число", where: "&Значение > 10", wantError: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				constant := &metadata.Constant{Name: fmt.Sprintf("Параметр%d", i), Type: tc.typ, Default: tc.def}
				if err := db.MigrateConstants(ctx, []*metadata.Constant{constant}); err != nil {
					t.Fatal(err)
				}
				if tc.def == "" {
					if err := db.SetConstant(ctx, constant.Name, tc.stored); err != nil {
						t.Fatal(err)
					}
				}
				reg := runtime.NewRegistry()
				reg.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}, Constants: []*metadata.Constant{constant}})
				result := New(reg, db).Run(ctx, &metadata.Widget{
					Name: "Проверка", Type: metadata.WidgetTypeList,
					Query:  "ВЫБРАТЬ Наименование ИЗ Справочник.Товар ГДЕ " + tc.where,
					Params: map[string]string{"Значение": "{{constant:" + constant.Name + "}}"},
				})
				if tc.wantError {
					if !strings.Contains(result.Error, constant.Name) {
						t.Fatalf("ожидалась ошибка константы %s, получено %#v", constant.Name, result)
					}
					return
				}
				if result.Error != "" || len(result.Rows) != tc.wantRows {
					t.Fatalf("ожидалось строк: %d, получено %#v", tc.wantRows, result)
				}
			})
		}
	})
}
