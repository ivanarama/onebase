package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// #1951: полное восстановление в PostgreSQL падало на внешнем ключе.
// Ключи снимались ДО миграции схемы внутри импорта: в пустой базе снимать было
// нечего, и миграция создавала таблицы уже с ключами; в базе со схемой
// миграция возвращала снятые ключи круга ссылок (ensureCycleFKs). Таблицы
// грузятся по алфавиту имён таблиц («циклб» раньше «цикля»), а конфигурация
// обходится в другом порядке («ЦиклЯ» раньше «Циклб» — прописная Я раньше
// строчной б), поэтому ключ круга оказывается на таблице, загружаемой первой,
// и импорт падал: «violates foreign key constraint».
func TestUniversalImportReferenceCycle_Matrix(t *testing.T) {
	appeal := &metadata.Entity{
		Name: "ЦиклЯ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "ДополнениеКЗаявке", Type: metadata.FieldType("reference:Циклб"), RefEntity: "Циклб"},
		},
	}
	request := &metadata.Entity{
		Name: "Циклб", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Обращение", Type: metadata.FieldType("reference:ЦиклЯ"), RefEntity: "ЦиклЯ"},
		},
	}
	entities := []*metadata.Entity{appeal, request}

	cfg := t.TempDir()
	files := map[string]string{
		"onebase.yaml": "name: test\n",
		filepath.Join("documents", "ЦиклЯ.yaml"): "name: ЦиклЯ\nfields:\n  - name: Номер\n    type: string\n" +
			"  - name: ДополнениеКЗаявке\n    type: reference:Циклб\n",
		filepath.Join("documents", "Циклб.yaml"): "name: Циклб\nfields:\n  - name: Номер\n    type: string\n" +
			"  - name: Обращение\n    type: reference:ЦиклЯ\n",
	}
	for name, body := range files {
		path := filepath.Join(cfg, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	source := newSQLite(t, "fk-cycle")
	if err := source.EnsureServiceSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := source.Migrate(ctx, entities); err != nil {
		t.Fatal(err)
	}
	appealID, requestID := uuid.New(), uuid.New()
	if err := source.Upsert(ctx, appeal.Name, appealID, map[string]any{"Номер": "О-1"}, appeal); err != nil {
		t.Fatal(err)
	}
	if err := source.Upsert(ctx, request.Name, requestID, map[string]any{"Номер": "З-1", "Обращение": appealID}, request); err != nil {
		t.Fatal(err)
	}
	if err := source.Upsert(ctx, appeal.Name, appealID, map[string]any{"Номер": "О-1", "ДополнениеКЗаявке": requestID}, appeal); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportUniversal(ctx, source, "file", cfg, "", "test", &archive); err != nil {
		t.Fatal("export:", err)
	}

	for _, withSchema := range []bool{false, true} {
		name := map[bool]string{false: "пустая база", true: "схема уже есть"}[withSchema]
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, target *storage.DB) {
				target.SetFilesDir(t.TempDir())
				if withSchema {
					if err := target.Migrate(ctx, entities); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := ImportUniversalWithOptions(ctx, target, "file", t.TempDir(), "", bytes.NewReader(archive.Bytes()), int64(archive.Len()),
					ImportOptions{ExchangeMode: ExchangeRestoreClone}); err != nil {
					t.Fatal("import:", err)
				}
				row, err := target.GetByID(ctx, request.Name, requestID, request)
				if err != nil || row["Номер"] != "З-1" {
					t.Fatalf("заявка не перенесена: %v %v", row, err)
				}
				// Ключи вернулись настоящими: висячая ссылка отвергается.
				err = target.Upsert(ctx, request.Name, uuid.New(), map[string]any{"Номер": "З-2", "Обращение": uuid.New()}, request)
				if err == nil {
					t.Fatal("после импорта внешний ключ не восстановлен: висячая ссылка записалась")
				}
			})
		})
	}
}
