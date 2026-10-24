package storage_test

// Страховка «заглушка маски ПДн не становится данными» на уровне записи:
// любой путь, пишущий объект через storage (форма, REST, DSL, импорт),
// наследует её. Матричный — общий путь записи обоих диалектов.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func TestMaskBackstop_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := &metadata.Entity{
			Name: "Заявка", Kind: metadata.KindDocument,
			Fields: []metadata.Field{
				{Name: "Номер", Type: metadata.FieldTypeString},
				{Name: "Телефон", Type: metadata.FieldTypeString},
				{Name: "Адрес", Type: metadata.FieldTypeString},
			},
			TableParts: []metadata.TablePart{{Name: "Контакты", Fields: []metadata.Field{
				{Name: "Телефон", Type: metadata.FieldTypeString},
			}}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}
		tp := doc.TableParts[0]
		rejected := func(t *testing.T, err error) {
			t.Helper()
			if !errors.Is(err, storage.ErrMaskedValueWrite) {
				t.Fatalf("заглушка маски записана: err=%v", err)
			}
		}

		for _, placeholder := range []string{"••••••", "••••••••4455", "Москва, ••••••"} {
			rejected(t, db.Upsert(ctx, doc.Name, uuid.New(), map[string]any{"Номер": "1", "Телефон": placeholder}, doc))
		}

		id := uuid.New()
		if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": "2", "Телефон": "+79161234455", "Адрес": "Москва, Ленина 1"}, doc); err != nil {
			t.Fatalf("обычная запись отклонена: %v", err)
		}
		// Настоящее значение заменить заглушкой нельзя.
		rejected(t, db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": "2", "Телефон": "••••••"}, doc))
		// Точка внутри обычного текста — не заглушка.
		if err := db.Upsert(ctx, doc.Name, id, map[string]any{"Номер": "2", "Телефон": "+7 916 • доб. 12"}, doc); err != nil {
			t.Fatalf("текст с точкой отклонён: %v", err)
		}

		// Заглушка, уже лежащая в базе (запись до проверки), запись не
		// блокирует: испорченный документ остаётся рабочим.
		legacy := uuid.New()
		// Обмен страховку не проходит (как enumBackstop): им и засеваем.
		if err := db.ApplyReplicatedEntity(ctx, doc.Name, legacy, map[string]any{"Номер": "3", "Телефон": "••••••"}, doc, ""); err != nil {
			t.Fatal(err)
		}
		if err := db.Upsert(ctx, doc.Name, legacy, map[string]any{"Номер": "3", "Телефон": "••••••"}, doc); err != nil {
			t.Fatalf("документ с уже сохранённой заглушкой больше не записывается: %v", err)
		}
		// …но поменять заглушку на другую нельзя.
		rejected(t, db.Upsert(ctx, doc.Name, legacy, map[string]any{"Номер": "3", "Телефон": "•••••1122"}, doc))

		// Табличная часть: новая заглушка отклонена, уже сохранённая — нет.
		rejected(t, db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id, []map[string]any{{"Телефон": "••••••"}}, tp))
		if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id, []map[string]any{{"Телефон": "+79990001122"}}, tp); err != nil {
			t.Fatalf("обычные строки ТЧ отклонены: %v", err)
		}
	})
}
