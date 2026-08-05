package storage

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Сбой посреди плана реструктуризации откатывает уже применённые изменения:
// база остаётся согласованной с картой полей (issue #588). Весь план одной
// таблицы и запись карты идут в одной транзакции; без неё переименование,
// прошедшее до сбойного ретайпа, оставалось бы в базе, а карта (её пишет
// saveSchemaMap последней строкой) о нём бы не знала — следующий прогон строил
// бы план от состояния, которого в базе нет.
//
// На SQLite смена типа string→bool реально меняет хранение (TEXT→INTEGER),
// поэтому непреобразуемое «abc» роняет ретайп — в отличие от string→number,
// который на SQLite остаётся TEXT→TEXT и не падает.
func TestRestructureRollsBackOnMidPlanFailure(t *testing.T) {
	ctx := context.Background()
	db := schemaTestDB(t)

	before := &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_code", Name: "Код", Type: metadata.FieldTypeString},
			{ID: "f_flag", Name: "Флаг", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{
		"Наименование": "Гвозди", "Код": "Г-1", "Флаг": "abc", // «abc» не булево
	}, before); err != nil {
		t.Fatal(err)
	}

	// План: переименовать Код→КодТовара (в плане идёт ПЕРВЫМ — changeOrder) и
	// сменить тип Флаг string→bool (упрётся в непреобразуемое «abc»).
	after := &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_code", Name: "КодТовара", Type: metadata.FieldTypeString},
			{ID: "f_flag", Name: "Флаг", Type: metadata.FieldTypeBool},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err == nil {
		t.Fatal("ожидалась ошибка: «abc» не преобразуется в булево")
	}

	// Переименование откатилось вместе со сбойным ретайпом: колонка осталась
	// «код», а не «кодтовара». На коде без транзакции «кодтовара» уже было бы в
	// базе, а карта считала бы поле по-прежнему «код» — рассогласование.
	cols := columnNames(t, db, "товары")
	if _, ok := cols["кодтовара"]; ok {
		t.Fatalf("переименование не откатилось после сбоя посреди плана: %v", cols)
	}
	if _, ok := cols["код"]; !ok {
		t.Fatalf("исходная колонка пропала: %v", cols)
	}

	// Карта согласована с базой: повторный корректный прогон (только
	// переименование, тип Флаг оставляем строкой) проходит и переименовывает.
	fixed := &metadata.Entity{
		Name: "Товары",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_code", Name: "КодТовара", Type: metadata.FieldTypeString},
			{ID: "f_flag", Name: "Флаг", Type: metadata.FieldTypeString},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{fixed}); err != nil {
		t.Fatalf("повторный корректный прогон не прошёл (карта осталась рассогласованной?): %v", err)
	}
	cols = columnNames(t, db, "товары")
	if _, ok := cols["кодтовара"]; !ok {
		t.Fatalf("повторный прогон не переименовал колонку: %v", cols)
	}
}
