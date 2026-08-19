package storage_test

// Страховка значений перечислений на уровне записи (#962, Н3).
//
// Проверка у входов (entityservice.Save, DSL-документы, кнопка «Провести»)
// остаётся, но перестаёт быть единственной: любой путь, который пишет через
// storage, теперь наследует гарантию, не вспоминая о ней. Тест как раз про
// «четвёртую дверь» — прямую запись мимо всех проверок входа.
//
// Матричный: затрагивается общий путь записи, а расхождение диалектов здесь
// увидеть иначе нечем.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// enumStub — минимальный источник перечислений (в проде его роль играет
// runtime.Registry).
type enumStub struct{ enums []*metadata.Enum }

func (e enumStub) Enums() []*metadata.Enum { return e.enums }
func (e enumStub) GetEnum(name string) *metadata.Enum {
	for _, en := range e.enums {
		if en.Name == name {
			return en
		}
	}
	return nil
}

func backstopEntity() *metadata.Entity {
	return &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument, Posting: true,
		Fields: []metadata.Field{
			{Name: "Номер", Type: metadata.FieldTypeString},
			{Name: "Статус", Type: metadata.FieldTypeString, EnumName: "СтатусЗаказа"},
		},
		TableParts: []metadata.TablePart{{Name: "Строки", Fields: []metadata.Field{
			{Name: "Товар", Type: metadata.FieldTypeString},
			{Name: "СостояниеСтроки", Type: metadata.FieldTypeString, EnumName: "СтатусЗаказа"},
		}}},
	}
}

func backstopSource() enumStub {
	return enumStub{enums: []*metadata.Enum{{Name: "СтатусЗаказа", Values: []string{"Новый", "Закрыт"}}}}
}

func TestEnumBackstop_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		doc := backstopEntity()
		if err := db.Migrate(ctx, []*metadata.Entity{doc}); err != nil {
			t.Fatal(err)
		}

		t.Run("без источника перечислений поведение прежнее", func(t *testing.T) {
			// Совместимость: procrun, бенчи и служебные прогоны поднимают базу
			// без реестра, и превращать это в отказ записи нельзя.
			if err := db.Upsert(ctx, doc.Name, uuid.New(),
				map[string]any{"номер": "З-0", "статус": "ЧТО_УГОДНО"}, doc); err != nil {
				t.Fatalf("запись без источника перечислений отклонена: %v", err)
			}
		})

		db.SetEnumSource(backstopSource())

		t.Run("прямая запись мимо входных проверок отклоняется", func(t *testing.T) {
			err := db.Upsert(ctx, doc.Name, uuid.New(),
				map[string]any{"номер": "З-1", "статус": "ТАКОГО_НЕТ"}, doc)
			if err == nil {
				t.Fatal("страховка пропустила несуществующее значение перечисления")
			}
			if !errors.Is(err, storage.ErrEnumValueUnknown) {
				t.Fatalf("ошибка не типизирована как отказ страховки: %v", err)
			}
		})

		t.Run("путь с оптимистичной блокировкой тоже прикрыт", func(t *testing.T) {
			// UpsertVersioned не заходит в общий upsert(): своя ветка записи —
			// отдельный вызов страховки, иначе «одна точка» осталась бы словами.
			id := uuid.New()
			if err := db.Upsert(ctx, doc.Name, id,
				map[string]any{"номер": "З-2", "статус": "Новый"}, doc); err != nil {
				t.Fatal(err)
			}
			var version int64 = 1
			err := db.UpsertVersioned(ctx, doc.Name, id,
				map[string]any{"номер": "З-2", "статус": "ТАКОГО_НЕТ"}, doc, &version)
			if !errors.Is(err, storage.ErrEnumValueUnknown) {
				t.Fatalf("UpsertVersioned пропустил мусор: %v", err)
			}
		})

		t.Run("допустимое значение проходит", func(t *testing.T) {
			if err := db.Upsert(ctx, doc.Name, uuid.New(),
				map[string]any{"номер": "З-3", "статус": "Закрыт"}, doc); err != nil {
				t.Fatalf("страховка отвергла допустимое значение: %v", err)
			}
		})

		t.Run("пустое значение допустимо", func(t *testing.T) {
			// «Не выбрано» — законное состояние; обязательность поля проверяется
			// другим механизмом.
			if err := db.Upsert(ctx, doc.Name, uuid.New(),
				map[string]any{"номер": "З-4", "статус": ""}, doc); err != nil {
				t.Fatalf("страховка отвергла пустое значение: %v", err)
			}
		})

		t.Run("строки табличной части тоже прикрыты", func(t *testing.T) {
			// Строки пишутся отдельным вызовом, и шапочная проверка их не видит:
			// без собственной страховки мусор в реквизите строки доезжал бы до
			// базы, даже когда шапка проверена.
			id := uuid.New()
			if err := db.Upsert(ctx, doc.Name, id,
				map[string]any{"номер": "З-6", "статус": "Новый"}, doc); err != nil {
				t.Fatal(err)
			}
			tp := doc.TableParts[0]
			err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id,
				[]map[string]any{{"товар": "Стол", "состояниестроки": "ТАКОГО_НЕТ"}}, tp)
			if !errors.Is(err, storage.ErrEnumValueUnknown) {
				t.Fatalf("страховка пропустила мусор в строке ТЧ: %v", err)
			}
			// Допустимое значение в строке проходит.
			if err := db.UpsertTablePartRows(ctx, doc.Name, tp.Name, id,
				[]map[string]any{{"товар": "Стол", "состояниестроки": "Закрыт"}}, tp); err != nil {
				t.Fatalf("страховка отвергла допустимое значение в строке ТЧ: %v", err)
			}
		})

		// Обмен проходит мимо страховки намеренно: узел-приёмник может работать
		// на другой версии конфигурации, и обрывать репликацию из-за одного
		// реквизита дороже, чем принять запись. Что делать с такими значениями —
		// отдельное решение (#1037); здесь оно НЕ принимается, но исключение
		// закреплено тестом, чтобы не исчезло молча.
		t.Run("обмен проходит мимо страховки (решение по #1037 отдельно)", func(t *testing.T) {
			id := uuid.New()
			source := `["exchange","План","Узел",1]`
			if err := db.ApplyReplicatedEntity(ctx, doc.Name, id,
				map[string]any{"номер": "З-5", "статус": "ЗНАЧЕНИЕ_ЧУЖОГО_УЗЛА"}, doc, source); err != nil {
				t.Fatalf("обмен отклонён страховкой: %v", err)
			}
			// Строки ТЧ обмена — тем же writer-ом: голый UpsertTablePartRows их
			// не пометил бы, и репликация рвалась бы на расхождении версий
			// конфигурации там, где решение ещё не принято.
			tp := doc.TableParts[0]
			if err := db.ApplyReplicatedTablePartRows(ctx, doc.Name, tp.Name, id,
				[]map[string]any{{"товар": "Стол", "состояниестроки": "ЗНАЧЕНИЕ_ЧУЖОГО_УЗЛА"}}, tp, source); err != nil {
				t.Fatalf("строки обмена отклонены страховкой: %v", err)
			}
		})
	})
}
