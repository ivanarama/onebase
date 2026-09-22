package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// fkDiagnosisEntities: справочник-цель и запись с ДВУМЯ ссылочными полями,
// чтобы диагностика называла все битые поля, а не только первое найденное.
func fkDiagnosisEntities() (*metadata.Entity, *metadata.Entity) {
	brand := &metadata.Entity{
		Name:   "ДиагностикаБренд",
		Kind:   metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	client := &metadata.Entity{
		Name: "ДиагностикаКлиент",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Бренд", Type: metadata.FieldType("reference:ДиагностикаБренд"), RefEntity: "ДиагностикаБренд"},
			{Name: "Сегмент", Type: metadata.FieldType("reference:ДиагностикаБренд"), RefEntity: "ДиагностикаБренд"},
		},
	}
	return brand, client
}

// TestUpsertFKDiagnosis_Matrix: запись с битой ссылкой возвращает
// ErrForeignKeyViolation, а её текст называет виновное поле и значение —
// по тому же пути, что и пользователь: публичный db.Upsert (#1660).
func TestUpsertFKDiagnosis_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		brand, client := fkDiagnosisEntities()
		if err := db.Migrate(ctx, []*metadata.Entity{brand, client}); err != nil {
			t.Fatal(err)
		}

		known := uuid.New()
		if err := db.Upsert(ctx, brand.Name, known, map[string]any{
			"Наименование": "Известный",
		}, brand); err != nil {
			t.Fatalf("создать цель ссылки: %v", err)
		}

		missing := uuid.New()
		err := db.Upsert(ctx, client.Name, uuid.New(), map[string]any{
			"Наименование": "ООО Ромашка",
			"Бренд":        missing.String(),
			"Сегмент":      known.String(),
		}, client)
		if !errors.Is(err, storage.ErrForeignKeyViolation) {
			t.Fatalf("ошибка = %v, не оборачивает ErrForeignKeyViolation", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "поле Бренд → ДиагностикаБренд") {
			t.Fatalf("текст ошибки не называет виновное поле: %v", msg)
		}
		if !strings.Contains(msg, missing.String()) {
			t.Fatalf("текст ошибки не называет битое значение: %v", msg)
		}
		if strings.Contains(msg, "Сегмент") {
			t.Fatalf("валидное поле Сегмент ошибочно названо битым: %v", msg)
		}
	})
}

// TestUpsertFKDiagnosisAllBroken_Matrix: битые значения в нескольких полях —
// диагностика перечисляет каждое.
func TestUpsertFKDiagnosisAllBroken_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		brand, client := fkDiagnosisEntities()
		if err := db.Migrate(ctx, []*metadata.Entity{brand, client}); err != nil {
			t.Fatal(err)
		}

		first, second := uuid.New(), uuid.New()
		err := db.Upsert(ctx, client.Name, uuid.New(), map[string]any{
			"Наименование": "ООО Ромашка",
			"Бренд":        first.String(),
			"Сегмент":      second.String(),
		}, client)
		if !errors.Is(err, storage.ErrForeignKeyViolation) {
			t.Fatalf("ошибка = %v, не оборачивает ErrForeignKeyViolation", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, first.String()) || !strings.Contains(msg, second.String()) {
			t.Fatalf("текст ошибки называет не все битые значения: %v", msg)
		}
	})
}
