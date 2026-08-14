package storage_test

// RLS-предикат Any обязан оставаться одним операндом WHERE (issue #858).
//
// PredicateSQL для Any-группы возвращал `(a) OR (b)` без внешних скобок, а
// List/GetMovements/GetBalances/InfoRegList склеивают WHERE через " AND ":
// получалось `фильтр AND (a) OR (b)` — по приоритету операторов вторая ветка
// политики вырывалась из-под фильтра, и пользователь видел чужие строки.
// В internal/query то же чинил #652, но оборачивал результат снаружи, не
// тронув параллельные storage-билдеры.
//
// Тесты матричные: генерация SQL общая, но исполнение и приведение типов у
// диалектов своё — dbtest.ForEachDialect гоняет SQLite и (при заданном
// TEST_DATABASE_URL) PostgreSQL.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Поиск (AND-условие) + политика «свои ИЛИ публичные» (Any): запись, не
// попавшая под поиск, не должна протекать в выдачу через OR-ветку политики.
func TestListAnyPolicyStaysInsideSearchFilterMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		cat := &metadata.Entity{
			Name: "Товар" + uuid.NewString()[:8],
			Kind: metadata.KindCatalog,
			Fields: []metadata.Field{
				{Name: "Наименование", Type: metadata.FieldTypeString},
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Public", Type: metadata.FieldTypeBool},
			},
		}
		if err := db.Migrate(ctx, []*metadata.Entity{cat}); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		seed := []map[string]any{
			{"Наименование": "красный стул", "Owner": "alice", "Public": false},
			{"Наименование": "красный стол", "Owner": "bob", "Public": true},
			{"Наименование": "зелёный шкаф", "Owner": "bob", "Public": true},
		}
		for _, r := range seed {
			if err := db.Upsert(ctx, cat.Name, uuid.New(), r, cat); err != nil {
				t.Fatalf("upsert %v: %v", r["Наименование"], err)
			}
		}
		params := storage.ListParams{
			Search: "красный",
			RowFilter: &storage.Predicate{Any: []storage.Predicate{
				{Field: "Owner", Op: "eq", Value: "alice"},
				{Field: "Public", Op: "eq", Value: true},
			}},
		}
		rows, err := db.List(ctx, cat.Name, cat, params)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2 (утечка мимо поиска через OR-ветку политики): %#v", len(rows), rows)
		}
		for _, r := range rows {
			if r["Наименование"] == "зелёный шкаф" {
				t.Fatalf("«зелёный шкаф» не под поиском, но в выдаче: %#v", rows)
			}
		}
		total, err := db.CountList(ctx, cat.Name, cat, params)
		if err != nil {
			t.Fatalf("CountList: %v", err)
		}
		if total != 2 {
			t.Fatalf("CountList = %d, want 2", total)
		}
	})
}

// Отбор по измерению (AND) + Any-политика в остатках регистра накопления:
// движение чужого склада не должно попадать в остатки через OR-ветку.
func TestBalancesAnyPolicyStaysInsideDimFilterMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{
			Name: "Остатки" + uuid.NewString()[:8],
			Dimensions: []metadata.Field{
				{Name: "Owner", Type: metadata.FieldTypeString},
				{Name: "Склад", Type: metadata.FieldTypeString},
			},
			Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("MigrateRegisters: %v", err)
		}
		period := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
		if err := db.WriteMovements(ctx, reg.Name, "Док", uuid.New(), []map[string]any{
			{"Owner": "u1", "Склад": "Основной", "Количество": 10},
			{"Owner": "u3", "Склад": "Резервный", "Количество": 20},
			{"Owner": "u2", "Склад": "Основной", "Количество": 30},
		}, reg, &period); err != nil {
			t.Fatalf("WriteMovements: %v", err)
		}
		filter := storage.RegFilter{
			Dims: map[string]string{"Склад": "Основной"},
			RowFilter: &storage.Predicate{Any: []storage.Predicate{
				{Field: "Owner", Op: "eq", Value: "u1"},
				{Field: "Owner", Op: "eq", Value: "u3"},
			}},
		}
		balances, err := db.GetBalances(ctx, reg.Name, reg, filter)
		if err != nil {
			t.Fatalf("GetBalances: %v", err)
		}
		if len(balances) != 1 || balances[0]["Owner"] != "u1" {
			t.Fatalf("balances = %#v, want ровно строка u1/Основной (Резервный протёк через OR-ветку)", balances)
		}
		movements, err := db.GetMovements(ctx, reg.Name, reg, filter)
		if err != nil {
			t.Fatalf("GetMovements: %v", err)
		}
		if len(movements) != 1 || movements[0]["Owner"] != "u1" {
			t.Fatalf("movements = %#v, want ровно строка u1/Основной", movements)
		}
	})
}
