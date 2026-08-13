package entityservice

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// DATA-01 / issue #774: entityservice.Delete — единая точка удаления для REST
// v1/v2 и DSL — обязана работать как fail-closed предохранитель ссылочной
// целостности, когда на объект ссылается ТАБЛИЧНАЯ ЧАСТЬ другого документа.
// Такие ссылки не покрываются внешним ключом БД (FK создаётся только для полей
// шапки), поэтому раньше объект удалялся, а строка ТЧ оставалась указывать в
// никуда. CheckRefs звал лишь UI-путь — удаление тем же объектом через REST/DSL
// проверку обходило. Матрично: SQLite и PostgreSQL обязаны вести себя одинаково
// (COUNT-подзапросы CheckRefs диалектозависимы).
func TestDelete_BlockedByTablePartReference(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		client := &metadata.Entity{
			Name:   "Контрагент",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		order := &metadata.Entity{
			Name:   "Заказ",
			Kind:   metadata.KindDocument,
			Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
			TableParts: []metadata.TablePart{
				{Name: "Строки", Fields: []metadata.Field{
					{Name: "Контрагент", Type: metadata.FieldType("reference:Контрагент"), RefEntity: client.Name},
				}},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{client, order}); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{Entities: []*metadata.Entity{client, order}})
		svc := &Service{Store: db, Reg: registry, Interp: interpreter.New()}

		// Контрагент, на которого сошлётся строка ТЧ документа.
		clientID := uuid.New()
		if err := db.Upsert(ctx, "Контрагент", clientID, map[string]any{"Наименование": "ООО Ромашка"}, client); err != nil {
			t.Fatal(err)
		}
		// Заказ с одной строкой ТЧ, ссылающейся на контрагента (через общий путь
		// записи — так же, как это делают REST/DSL/форма).
		orderID := uuid.New()
		if _, err := svc.Save(ctx, SaveRequest{
			Entity: order,
			ID:     orderID,
			IsNew:  true,
			Fields: map[string]any{"Номер": "1"},
			TablePartRows: map[string][]map[string]any{
				"Строки": {{"Контрагент": clientID.String()}},
			},
		}); err != nil {
			t.Fatalf("Save заказа: %v", err)
		}

		// Удаление ссылаемого контрагента должно быть отклонено как soft-отказ
		// (объект на месте), а не техническим сбоем.
		res, err := svc.Delete(ctx, client, clientID)
		if err != nil {
			t.Fatalf("Delete вернул технический сбой вместо soft-отказа: %v", err)
		}
		if res.DSLError == "" {
			t.Fatal("DATA-01: удаление контрагента, на которого ссылается ТЧ, НЕ отклонено")
		}
		if _, err := db.GetByID(ctx, "Контрагент", clientID, client); err != nil {
			t.Fatalf("контрагент удалён несмотря на отказ — ссылочная целостность нарушена: %v", err)
		}

		// Контроль: объект без ссылок удаляется штатно — предохранитель не
		// блокирует лишнего.
		freeID := uuid.New()
		if err := db.Upsert(ctx, "Контрагент", freeID, map[string]any{"Наименование": "ООО Свободный"}, client); err != nil {
			t.Fatal(err)
		}
		res2, err := svc.Delete(ctx, client, freeID)
		if err != nil {
			t.Fatalf("Delete без ссылок: %v", err)
		}
		if res2.DSLError != "" {
			t.Fatalf("удаление объекта без ссылок ошибочно отклонено: %s", res2.DSLError)
		}
		if _, err := db.GetByID(ctx, "Контрагент", freeID, client); err == nil {
			t.Fatal("контрагент без ссылок не был удалён")
		}
	})
}
