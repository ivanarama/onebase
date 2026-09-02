package storage_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// copiedReference repeats the relevant interpreter.Ref contract: GetRefUUID is
// declared on the pointer, while application code may copy the value itself.
type copiedReference struct{ id string }

func (r *copiedReference) GetRefUUID() string { return r.id }

func TestReferenceTypeMismatchWriteMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		target := &metadata.Entity{
			Name: "КонтрагентСсылки", Kind: metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		refType := metadata.FieldType("reference:" + target.Name)
		doc := &metadata.Entity{
			Name: "ЗаказСсылки", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Клиент", Type: refType, RefEntity: target.Name},
			},
			TableParts: []metadata.TablePart{{
				Name:   "Товары",
				Fields: []metadata.Field{{Name: "Товар", Type: refType, RefEntity: target.Name}},
			}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{target, doc}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		first, second := uuid.New(), uuid.New()
		for id, name := range map[uuid.UUID]string{first: "Первый", second: "Второй"} {
			if err := db.Upsert(ctx, target.Name, id, map[string]any{"Наименование": name}, target); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}
		}

		docID := uuid.New()
		if err := db.Upsert(ctx, doc.Name, docID, map[string]any{
			"Номер": "З-1", "Клиент": first.String(),
		}, doc); err != nil {
			t.Fatalf("seed document: %v", err)
		}

		t.Run("невалидная строка отклоняется и шапка сохраняется", func(t *testing.T) {
			err := db.Upsert(ctx, doc.Name, docID, map[string]any{"Клиент": "не UUID"}, doc)
			assertReferenceTypeMismatch(t, err, "Клиент", "reference:"+target.Name, "Строка")
			row, loadErr := db.GetByID(ctx, doc.Name, docID, doc)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			assertReferenceValue(t, row["Клиент"], first)
		})

		t.Run("UUID и копия ссылки принимаются", func(t *testing.T) {
			if err := db.Upsert(ctx, doc.Name, docID, map[string]any{"Клиент": second}, doc); err != nil {
				t.Fatalf("uuid.UUID rejected: %v", err)
			}
			if err := db.Upsert(ctx, doc.Name, docID, map[string]any{
				"Клиент": copiedReference{id: first.String()},
			}, doc); err != nil {
				t.Fatalf("copied reference rejected: %v", err)
			}
			row, err := db.GetByID(ctx, doc.Name, docID, doc)
			if err != nil {
				t.Fatal(err)
			}
			assertReferenceValue(t, row["Клиент"], first)
		})

		tp := doc.TableParts[0]
		if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, docID,
			[]map[string]any{{"Товар": first.String()}}, tp); err != nil {
			t.Fatalf("seed table part: %v", err)
		}

		t.Run("все строки проверяются до удаления старых", func(t *testing.T) {
			err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, docID, []map[string]any{
				{"Товар": second.String()},
				{"Товар": "не UUID"},
			}, tp)
			assertReferenceTypeMismatch(t, err, "Товар", "reference:"+target.Name, "Строка")
			if !strings.Contains(err.Error(), doc.Name+"."+tp.Name+"[2]") {
				t.Errorf("ошибка не называет строку ТЧ: %v", err)
			}
			rows, loadErr := db.GetTablePartRows(ctx, doc.Name, tp.Name, docID, tp)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if len(rows) != 1 {
				t.Fatalf("неуспешная замена удалила старые строки: %#v", rows)
			}
			assertReferenceValue(t, rows[0]["Товар"], first)
		})

		t.Run("пустое значение остаётся допустимым", func(t *testing.T) {
			if err := db.Upsert(ctx, doc.Name, docID, map[string]any{"Клиент": ""}, doc); err != nil {
				t.Fatalf("empty header reference rejected: %v", err)
			}
			if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, docID,
				[]map[string]any{{"Товар": ""}}, tp); err != nil {
				t.Fatalf("empty table-part reference rejected: %v", err)
			}
			row, err := db.GetByID(ctx, doc.Name, docID, doc)
			if err != nil {
				t.Fatal(err)
			}
			if row["Клиент"] != nil {
				t.Errorf("empty header reference = %#v, want nil", row["Клиент"])
			}
			rows, err := db.GetTablePartRows(ctx, doc.Name, tp.Name, docID, tp)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0]["Товар"] != nil {
				t.Errorf("empty table-part reference = %#v, want one nil value", rows)
			}
		})
	})
}

func assertReferenceTypeMismatch(t *testing.T, err error, parts ...string) {
	t.Helper()
	if !errors.Is(err, storage.ErrReferenceTypeMismatch) {
		t.Fatalf("error = %v, want ErrReferenceTypeMismatch", err)
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not contain %q", err, part)
		}
	}
}

func assertReferenceValue(t *testing.T, got any, want uuid.UUID) {
	t.Helper()
	if fmt.Sprint(got) != want.String() {
		t.Errorf("reference = %v (%T), want %s", got, got, want)
	}
}
