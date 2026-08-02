package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// CheckRefs — предохранитель перед удалением, поэтому он обязан быть
// fail-closed: при невозможности пересчитать ссылки он возвращает ошибку, а не
// пустой список. Раньше ошибки Scan игнорировались, count оставался нулём, и
// вызывающий код видел «ссылок нет» — объект удалялся, ломая целостность.
//
// Ошибку воспроизводим отсутствующей таблицей: сущность объявлена в метаданных,
// но в схеме её нет — ровно то, что бывает при неполной миграции.
func TestCheckRefs_FailsClosedOnQueryError(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "refs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	target := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	// Ссылающаяся сущность НЕ мигрируется — её таблицы в базе нет.
	referrer := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Клиент", Type: metadata.FieldType("reference:Контрагент"), RefEntity: "Контрагент"},
		},
	}
	if err := db.Migrate(ctx, []*metadata.Entity{target}); err != nil {
		t.Fatal(err)
	}

	refs, err := db.CheckRefs(ctx, "Контрагент", uuid.New(),
		[]*metadata.Entity{target, referrer})
	if err == nil {
		t.Fatalf("проверка ссылок скрыла сбой запроса и вернула %v — "+
			"вызывающий код принял бы это за «ссылок нет» и удалил объект", refs)
	}
	if refs != nil {
		t.Errorf("при ошибке список ссылок должен быть nil, получено %v", refs)
	}
}

// На исправной схеме поведение прежнее: реальные ссылки находятся, ошибки нет.
func TestCheckRefs_FindsRealReferences(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "refs2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	target := &metadata.Entity{
		Name: "Контрагент", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{{Name: "Наименование", Type: metadata.FieldTypeString}},
	}
	referrer := &metadata.Entity{
		Name: "Заказ", Kind: metadata.KindDocument,
		Fields: []metadata.Field{
			{Name: "Клиент", Type: metadata.FieldType("reference:Контрагент"), RefEntity: "Контрагент"},
		},
	}
	all := []*metadata.Entity{target, referrer}
	if err := db.Migrate(ctx, all); err != nil {
		t.Fatal(err)
	}

	clientID := uuid.New()
	if err := db.Upsert(ctx, target.Name, clientID,
		map[string]any{"Наименование": "Ромашка"}, target); err != nil {
		t.Fatal(err)
	}

	// Пока ссылок нет — пусто и без ошибки.
	refs, err := db.CheckRefs(ctx, "Контрагент", clientID, all)
	if err != nil {
		t.Fatalf("неожиданная ошибка на исправной схеме: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("ссылок быть не должно, получено %v", refs)
	}

	// Появилась ссылка — она найдена.
	if err := db.Upsert(ctx, referrer.Name, uuid.New(),
		map[string]any{"Клиент": clientID.String()}, referrer); err != nil {
		t.Fatal(err)
	}
	refs, err = db.CheckRefs(ctx, "Контрагент", clientID, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].EntityName != "Заказ" || refs[0].Count != 1 {
		t.Fatalf("ожидалась одна ссылка из Заказ, получено %+v", refs)
	}
}
