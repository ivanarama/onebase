package exchange_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Сквозная проверка терпимости к недостающему полю ТЧ (#885).
//
// Сценарий — рекомендованный порядок обновления узлов: ПОЛУЧАТЕЛЯ обновляют
// первым, у него в табличной части появляется новое поле, а отправитель его
// ещё не шлёт. Прежняя проверка «набор полей строки совпадает точно» роняла
// такой пакет целиком — вместе с теми, что уже лежали в очереди.
//
// Матрично: приём пакета пишет строки ТЧ в базу, а вставка ТЧ у диалектов
// разная и разойтись может молча.
func TestApplyPackage_НедостающееПолеТЧПринимается(t *testing.T) {
	sender := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
		}}},
	}
	// У получателя ТЧ шире на одно поле — он обновлён раньше отправителя.
	receiver := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{{Name: "Номер", Type: metadata.FieldTypeString}},
		TableParts: []metadata.TablePart{{Name: "Товары", Fields: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldTypeString},
			{Name: "Количество", Type: metadata.FieldTypeNumber},
			{Name: "Скидка", Type: metadata.FieldTypeNumber},
		}}},
	}

	dbtest.ForEachDialect(t, func(t *testing.T, recvDB *storage.DB) {
		ctx := context.Background()
		if err := recvDB.EnsureExchangeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := recvDB.Migrate(ctx, []*metadata.Entity{receiver}); err != nil {
			t.Fatal(err)
		}

		sendDB, sendCtx := newBase(t, sender)
		plan := &metadata.ExchangePlan{
			Name:    "Обмен",
			Content: []string{"Документ.Заказ"},
			Nodes:   []metadata.ExchangeNode{{Code: "center", Priority: 10}, {Code: "fil01", Priority: 1}},
		}
		plan.Normalize()
		if err := sendDB.SaveExchangeThisNode(sendCtx, "Обмен", "center"); err != nil {
			t.Fatal(err)
		}
		if err := recvDB.SaveExchangeThisNode(ctx, "Обмен", "fil01"); err != nil {
			t.Fatal(err)
		}

		id := uuid.New()
		if err := sendDB.Upsert(sendCtx, sender.Name, id, map[string]any{"Номер": "З-1"}, sender); err != nil {
			t.Fatal(err)
		}
		if err := sendDB.UpsertTablePartRows(sendCtx, sender.Name, "Товары", id,
			[]map[string]any{{"Номенклатура": "Стул", "Количество": 2}}, sender.TableParts[0]); err != nil {
			t.Fatal(err)
		}
		if err := exchange.RegisterOnSave(sendCtx, sendDB, []*metadata.ExchangePlan{plan}, sender, id, false); err != nil {
			t.Fatal(err)
		}

		data, err := exchange.BuildPackage(sendCtx, sendDB, fakeResolver{"Заказ": sender}, plan, "fil01")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := exchange.ApplyPackage(ctx, recvDB, fakeResolver{"Заказ": receiver}, plan, data, exchange.ApplyOptions{}); err != nil {
			t.Fatalf("пакет без нового поля ТЧ отклонён получателем: %v", err)
		}

		rows, err := recvDB.GetTablePartRows(ctx, receiver.Name, "Товары", id, receiver.TableParts[0])
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("строк ТЧ у получателя %d, ожидалась 1", len(rows))
		}
		if got, _ := rows[0]["Номенклатура"].(string); got != "Стул" {
			t.Errorf("номенклатура = %q, ожидался «Стул»", got)
		}
	})
}
