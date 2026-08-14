package entityservice

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
	"github.com/ivantit66/onebase/internal/storage"
)

// Остаток DATA-01 (#855): CheckRefs проверял только реквизиты сущностей и
// строки табличных частей. Ссылки из ИЗМЕРЕНИЙ РЕГИСТРОВ не проверялись вовсе —
// при том что и комментарий в коде, и PR #801 («Closes #774») заявляли полное
// покрытие, а исходный #774 прямо называл измерения регистров одной из двух дыр.
//
// Отказ выглядел так: товар, по которому есть движения, удалялся без единого
// возражения; регистр оставался со строками по несуществующей ссылке —
// остатки и обороты в отчётах «висели» на пустом месте.
//
// Матрично: COUNT-запросы CheckRefs диалектозависимы, а таблицы регистров на
// SQLite и PostgreSQL создаются разным DDL.
func TestDelete_BlockedByRegisterDimension(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()

		goods := &metadata.Entity{
			Name:   "Товар",
			Kind:   metadata.KindCatalog,
			Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
		}
		stock := &metadata.Register{
			Name: "Остатки",
			Dimensions: []metadata.Field{
				{Name: "Товар", Type: metadata.FieldType("reference:Товар"), RefEntity: "Товар"},
			},
			Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		prices := &metadata.InfoRegister{
			Name: "ЦеныНоменклатуры",
			Dimensions: []metadata.Field{
				{Name: "Товар", Type: metadata.FieldType("reference:Товар"), RefEntity: "Товар"},
			},
			Resources: []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{goods}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{stock}); err != nil {
			t.Fatal(err)
		}
		if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{prices}); err != nil {
			t.Fatal(err)
		}
		registry := runtime.NewRegistry()
		registry.Load(runtime.LoadOptions{
			Entities:  []*metadata.Entity{goods},
			Registers: []*metadata.Register{stock},
			InfoRegs:  []*metadata.InfoRegister{prices},
		})
		svc := &Service{Store: db, Reg: registry, Interp: interpreter.New()}

		t.Run("измерение регистра накопления", func(t *testing.T) {
			id := uuid.New()
			if err := db.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Гвозди"}, goods); err != nil {
				t.Fatal(err)
			}
			period := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
			if err := db.WriteMovements(ctx, stock.Name, "Поступление", uuid.New(),
				[]map[string]any{{"Товар": id.String(), "Количество": 10, "тип": "+"}},
				stock, &period); err != nil {
				t.Fatalf("движения: %v", err)
			}

			res, err := svc.Delete(ctx, goods, id)
			if err != nil {
				t.Fatalf("Delete вернул технический сбой вместо soft-отказа: %v", err)
			}
			if res.DSLError == "" {
				t.Fatal("товар с движениями удалён без возражений — регистр остался со ссылкой в никуда")
			}
			if _, err := db.GetByID(ctx, "Товар", id, goods); err != nil {
				t.Fatalf("товар удалён несмотря на отказ: %v", err)
			}
		})

		t.Run("измерение регистра сведений", func(t *testing.T) {
			id := uuid.New()
			if err := db.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Шурупы"}, goods); err != nil {
				t.Fatal(err)
			}
			if err := db.InfoRegSet(ctx, prices,
				map[string]any{"Товар": id.String()}, map[string]any{"Цена": 100}, nil); err != nil {
				t.Fatalf("запись регистра сведений: %v", err)
			}

			res, err := svc.Delete(ctx, goods, id)
			if err != nil {
				t.Fatalf("Delete вернул технический сбой вместо soft-отказа: %v", err)
			}
			if res.DSLError == "" {
				t.Fatal("товар со строкой регистра сведений удалён без возражений")
			}
		})

		// Контроль: товар без единой ссылки удаляется штатно — предохранитель
		// не должен блокировать лишнего.
		t.Run("без ссылок удаляется", func(t *testing.T) {
			id := uuid.New()
			if err := db.Upsert(ctx, "Товар", id, map[string]any{"Наименование": "Свободный"}, goods); err != nil {
				t.Fatal(err)
			}
			res, err := svc.Delete(ctx, goods, id)
			if err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if res.DSLError != "" {
				t.Fatalf("товар без ссылок не удалён: %s", res.DSLError)
			}
		})
	})
}
