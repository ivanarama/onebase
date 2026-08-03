//go:build integration

package storage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
)

// Реструктуризация на PostgreSQL идёт другим путём, чем на SQLite: там
// настоящие ALTER COLUMN … TYPE … USING вместо переноса значений в новую
// колонку, и типы различаются (NUMERIC/BOOLEAN/TIMESTAMPTZ вместо TEXT).
// Юнит-тесты на SQLite этот путь не проверяют вовсе.

func connectPGForSchemaTest(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func pgCatalog(fieldName string, typ metadata.FieldType) *metadata.Entity {
	return &metadata.Entity{
		Name: "СхемаТест",
		Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
			{ID: "f_val", Name: fieldName, Type: typ},
		},
	}
}

func resetSchemaTestTable(ctx context.Context, t *testing.T, db *DB) {
	t.Helper()
	table := metadata.TableName("СхемаТест")
	if _, err := db.Exec(ctx, "DROP TABLE IF EXISTS "+pgQuoteIdent(table)); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM _schema_fields WHERE table_name = $1", table); err != nil &&
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("очистка карты схемы: %v", err)
	}
}

func TestRestructureRenameKeepsData_Postgres(t *testing.T) {
	ctx := context.Background()
	db := connectPGForSchemaTest(t)
	resetSchemaTestTable(ctx, t, db)

	before := pgCatalog("Сумма", metadata.FieldTypeNumber)
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Сумма": "150.50"}, before); err != nil {
		t.Fatal(err)
	}

	after := pgCatalog("СуммаДокумента", metadata.FieldTypeNumber)
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}

	cols, err := db.tableColumns(ctx, metadata.TableName("СхемаТест"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["суммадокумента"]; !ok {
		t.Fatalf("колонка не переименована: %v", cols)
	}
	if _, ok := cols["сумма"]; ok {
		t.Fatalf("старая колонка осталась: %v", cols)
	}
	var got string
	if err := db.QueryRow(ctx, `SELECT суммадокумента::text FROM схематест WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("чтение значения: %v", err)
	}
	if !strings.HasPrefix(got, "150.5") {
		t.Fatalf("данные потеряны: %q", got)
	}
}

// На PostgreSQL смена типа — настоящий ALTER … TYPE: текст превращается в
// число, а не остаётся текстом, как в SQLite.
func TestRestructureRetype_Postgres(t *testing.T) {
	ctx := context.Background()
	db := connectPGForSchemaTest(t)
	resetSchemaTestTable(ctx, t, db)

	before := pgCatalog("Значение", metadata.FieldTypeString)
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Значение": "150.50"}, before); err != nil {
		t.Fatal(err)
	}

	after := pgCatalog("Значение", metadata.FieldTypeNumber)
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatalf("смена типа с преобразуемым значением: %v", err)
	}
	cols, err := db.tableColumns(ctx, metadata.TableName("СхемаТест"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToLower(cols["значение"]); got != "numeric" {
		t.Fatalf("тип колонки в PostgreSQL не изменён: %q", got)
	}
	var sum string
	if err := db.QueryRow(ctx, `SELECT (значение + 1)::text FROM схематест WHERE id = $1`, id).Scan(&sum); err != nil {
		t.Fatalf("значение не стало числом: %v", err)
	}
	if !strings.HasPrefix(sum, "151.5") {
		t.Fatalf("арифметика по преобразованной колонке дала %q", sum)
	}
}

// Непреобразуемое значение отменяет смену типа ДО того, как ALTER испортит
// колонку: на PostgreSQL это особенно важно — там ALTER упал бы сам, но уже
// в середине миграции и без внятного объяснения, сколько значений виноваты.
func TestRestructureRetypeRefuses_Postgres(t *testing.T) {
	ctx := context.Background()
	db := connectPGForSchemaTest(t)
	resetSchemaTestTable(ctx, t, db)

	before := pgCatalog("Значение", metadata.FieldTypeString)
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := db.Upsert(ctx, before.Name, id, map[string]any{"Наименование": "Гвозди", "Значение": "не число"}, before); err != nil {
		t.Fatal(err)
	}

	after := pgCatalog("Значение", metadata.FieldTypeNumber)
	err := db.Migrate(ctx, []*metadata.Entity{after})
	if err == nil {
		t.Fatal("миграция обязана отказаться")
	}
	if !strings.Contains(err.Error(), "не преобразуются") || !strings.Contains(err.Error(), "не число") {
		t.Fatalf("сообщение не объясняет причину: %v", err)
	}

	var got string
	if err := db.QueryRow(ctx, `SELECT значение FROM схематест WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("чтение значения: %v", err)
	}
	if got != "не число" {
		t.Fatalf("значение изменилось при отказе: %q", got)
	}
}

func TestRestructureDrop_Postgres(t *testing.T) {
	ctx := context.Background()
	db := connectPGForSchemaTest(t)
	resetSchemaTestTable(ctx, t, db)

	before := pgCatalog("Комментарий", metadata.FieldTypeString)
	if err := db.Migrate(ctx, []*metadata.Entity{before}); err != nil {
		t.Fatal(err)
	}
	after := &metadata.Entity{Name: "СхемаТест", Kind: metadata.KindCatalog, Fields: []metadata.Field{
		{ID: "f_name", Name: "Наименование", Type: metadata.FieldTypeString},
	}}

	db.SetSchemaOptions(SchemaOptions{})
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}
	cols, err := db.tableColumns(ctx, metadata.TableName("СхемаТест"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["комментарий"]; !ok {
		t.Fatal("без разрешения колонка удаляться не должна")
	}

	db.SetSchemaOptions(SchemaOptions{AllowDestructive: true})
	t.Cleanup(func() { db.SetSchemaOptions(SchemaOptions{}) })
	if err := db.Migrate(ctx, []*metadata.Entity{after}); err != nil {
		t.Fatal(err)
	}
	cols, err = db.tableColumns(ctx, metadata.TableName("СхемаТест"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["комментарий"]; ok {
		t.Fatal("с разрешением колонка должна быть удалена")
	}
}
