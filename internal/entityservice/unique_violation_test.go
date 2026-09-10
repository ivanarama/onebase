package entityservice

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestSaveExplainsUniqueViolationOnCreateAndVersionedUpdate(t *testing.T) {
	t.Run("declared index", func(t *testing.T) {
		entity := &metadata.Entity{
			Name: "Контрагенты",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "ИНН", Type: metadata.FieldTypeString},
				{Name: "Наименование", Type: metadata.FieldTypeString},
			},
			Indexes: []metadata.IndexSpec{{Fields: []string{"ИНН"}, Unique: true}},
		}
		ctx, svc := uniqueViolationFixture(t, entity)

		saveEntity(t, ctx, svc, entity, uuid.New(), true, nil, map[string]any{
			"ИНН": "77", "Наименование": "Первый",
		})

		_, err := svc.Save(ctx, SaveRequest{
			Entity: entity,
			ID:     uuid.New(),
			IsNew:  true,
			Fields: map[string]any{"ИНН": "77", "Наименование": "Дубль при создании"},
		})
		assertHumanDuplicate(t, err, entity.Name, "ИНН", "77")

		secondID := uuid.New()
		saveEntity(t, ctx, svc, entity, secondID, true, nil, map[string]any{
			"ИНН": "88", "Наименование": "Второй",
		})
		version := int64(1)
		_, err = svc.Save(ctx, SaveRequest{
			Entity:          entity,
			ID:              secondID,
			IsNew:           false,
			ExpectedVersion: &version,
			Fields:          map[string]any{"ИНН": "77", "Наименование": "Дубль при правке"},
		})
		assertHumanDuplicate(t, err, entity.Name, "ИНН", "77")
	})

	t.Run("unique numerator", func(t *testing.T) {
		entity := &metadata.Entity{
			Name: "Склады",
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: metadata.StandardCodeField, Type: metadata.FieldTypeString},
				{Name: "Наименование", Type: metadata.FieldTypeString},
			},
			Numerator: &metadata.Numerator{Prefix: "С-", Length: 6, Period: "none", Unique: true},
		}
		ctx, svc := uniqueViolationFixture(t, entity)

		saveEntity(t, ctx, svc, entity, uuid.New(), true, nil, map[string]any{
			metadata.StandardCodeField: "С-000001", "Наименование": "Первый",
		})
		secondID := uuid.New()
		saveEntity(t, ctx, svc, entity, secondID, true, nil, map[string]any{
			metadata.StandardCodeField: "С-000002", "Наименование": "Второй",
		})
		version := int64(1)
		_, err := svc.Save(ctx, SaveRequest{
			Entity:          entity,
			ID:              secondID,
			IsNew:           false,
			ExpectedVersion: &version,
			Fields: map[string]any{
				metadata.StandardCodeField: "С-000001", "Наименование": "Дубль при правке",
			},
		})
		assertHumanDuplicate(t, err, entity.Name, metadata.StandardCodeField, "С-000001")
	})
}

func uniqueViolationFixture(t *testing.T, entity *metadata.Entity) (context.Context, *Service) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, filepath.Join(t.TempDir(), "unique.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx, []*metadata.Entity{entity}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	registry := runtime.NewRegistry()
	registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{entity}})
	return ctx, &Service{Store: db, Reg: registry, Interp: interpreter.New()}
}

func saveEntity(t *testing.T, ctx context.Context, svc *Service, entity *metadata.Entity, id uuid.UUID,
	isNew bool, expectedVersion *int64, fields map[string]any,
) {
	t.Helper()
	if _, err := svc.Save(ctx, SaveRequest{
		Entity: entity, ID: id, IsNew: isNew, ExpectedVersion: expectedVersion, Fields: fields,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func assertHumanDuplicate(t *testing.T, err error, entityName, field, value string) {
	t.Helper()
	if !errors.Is(err, storage.ErrCodeDuplicate) {
		t.Fatalf("error = %v, want ErrCodeDuplicate", err)
	}
	for _, want := range []string{entityName, field, value} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q: %s", want, err)
		}
	}
	for _, leaked := range []string{"upsert versioned", "UNIQUE constraint", "SQLSTATE"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("driver detail leaked as %q: %s", leaked, err)
		}
	}
}
