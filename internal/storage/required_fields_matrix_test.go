package storage_test

// Обязательность реквизита (#1033).
//
// Ключ `required: true` у реквизита сущности молча игнорировался: конфигурация
// писала правило, которого нет, а линт вдобавок объявлял ключ неизвестным и
// валил на нём CI. Теперь обязательность читается из метаданных и проверяется
// при записи — там же, где значения перечислений.
//
// Матричный: правило живёт в общем пути записи.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func requiredCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Контрагенты", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString, Required: true},
			{Name: "ИНН", Type: metadata.FieldTypeString},
			{Name: "Скидка", Type: metadata.FieldTypeNumber, Required: true},
		},
	}
}

func TestRequiredFields_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		ent := requiredCatalog()
		if err := db.Migrate(ctx, []*metadata.Entity{ent}); err != nil {
			t.Fatal(err)
		}

		t.Run("пустое значение обязательного реквизита отклоняется", func(t *testing.T) {
			err := db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"наименование": "", "скидка": 0}, ent)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("пустое обязательное значение записано: %v", err)
			}
		})

		t.Run("ноль — заполненное значение", func(t *testing.T) {
			// Объявить ноль пустым значило бы запретить «Скидка = 0» у
			// обязательного реквизита — а это осмысленное значение.
			if err := db.Upsert(ctx, ent.Name, uuid.New(),
				map[string]any{"наименование": "ООО Ромашка", "скидка": 0}, ent); err != nil {
				t.Fatalf("ноль отвергнут как незаполненный: %v", err)
			}
		})

		t.Run("частичная запись без ключа проходит", func(t *testing.T) {
			// Отсутствие ключа означает «не меняем»: требовать полный набор в
			// каждой правке значило бы сломать частичную запись — обновление
			// одного поля, служебную правку, миграцию.
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "ООО Ромашка", "скидка": 5}, ent); err != nil {
				t.Fatal(err)
			}
			if err := db.Upsert(ctx, ent.Name, id, map[string]any{"инн": "7701234567"}, ent); err != nil {
				t.Fatalf("частичная запись отклонена: %v", err)
			}
		})

		t.Run("явная очистка обязательного реквизита отклоняется", func(t *testing.T) {
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "ООО Ромашка", "скидка": 5}, ent); err != nil {
				t.Fatal(err)
			}
			err := db.Upsert(ctx, ent.Name, id, map[string]any{"наименование": "   "}, ent)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("обязательный реквизит очищен пробелами: %v", err)
			}
		})

		t.Run("путь с оптимистичной блокировкой тоже прикрыт", func(t *testing.T) {
			id := uuid.New()
			if err := db.Upsert(ctx, ent.Name, id,
				map[string]any{"наименование": "ООО Ромашка", "скидка": 5}, ent); err != nil {
				t.Fatal(err)
			}
			var version int64 = 1
			err := db.UpsertVersioned(ctx, ent.Name, id, map[string]any{"наименование": ""}, ent, &version)
			if !errors.Is(err, storage.ErrRequiredFieldEmpty) {
				t.Fatalf("UpsertVersioned пропустил пустое обязательное значение: %v", err)
			}
		})

		// Обмен исключён по той же причине, что и у перечислений: узел-приёмник
		// может работать на версии конфигурации, где обязательности ещё нет
		// (#1037).
		t.Run("обмен проходит мимо проверки", func(t *testing.T) {
			if err := db.ApplyReplicatedEntity(ctx, ent.Name, uuid.New(),
				map[string]any{"наименование": "", "скидка": 0}, ent,
				`["exchange","План","Узел",1]`); err != nil {
				t.Fatalf("обмен отклонён проверкой обязательности: %v", err)
			}
		})
	})
}

// Полнота набора требуется только при создании — там объект известен целиком.
func TestValidateRequiredValues_CreateNeedsEveryField(t *testing.T) {
	ent := requiredCatalog()
	if msg := storage.ValidateRequiredValues(ent, map[string]any{"наименование": "ООО"}, true); msg == "" {
		t.Error("создание без обязательного реквизита «Скидка» прошло")
	}
	if msg := storage.ValidateRequiredValues(ent, map[string]any{"наименование": "ООО"}, false); msg != "" {
		t.Errorf("правка без ключа отклонена: %s", msg)
	}
	full := map[string]any{"наименование": "ООО", "скидка": 0}
	if msg := storage.ValidateRequiredValues(ent, full, true); msg != "" {
		t.Errorf("полный набор отклонён: %s", msg)
	}
}
