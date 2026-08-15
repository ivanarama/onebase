package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/shopspring/decimal"
)

// Регистр накопления с двумя измерениями (одно ссылочное) и ресурсом — для
// проверки отборов (issue #45). Возвращает store с записанными движениями.
func setupAccumReg(t *testing.T) (*DB, context.Context, *metadata.Register, string, string) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	reg := &metadata.Register{
		Name: "Остатки",
		Dimensions: []metadata.Field{
			{Name: "Номенклатура", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
			{Name: "Склад", Type: metadata.FieldTypeString},
		},
		Resources: []metadata.Field{{Name: "Количество", Type: metadata.FieldTypeNumber}},
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	товарA := uuid.New()
	товарB := uuid.New()

	// recorder1: 01.01.2026 — товарA/Главный = 10
	rec1 := uuid.New()
	p1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.WriteMovements(ctx, "Остатки", "Поступление", rec1, []map[string]any{
		{"ВидДвижения": "Приход", "Номенклатура": товарA.String(), "Склад": "Главный", "Количество": float64(10)},
	}, reg, &p1); err != nil {
		t.Fatal(err)
	}
	// recorder2: 15.06.2026 — товарB/Резервный = 5, товарA/Главный = 3
	rec2 := uuid.New()
	p2 := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	if err := db.WriteMovements(ctx, "Остатки", "Поступление", rec2, []map[string]any{
		{"ВидДвижения": "Приход", "Номенклатура": товарB.String(), "Склад": "Резервный", "Количество": float64(5)},
		{"ВидДвижения": "Приход", "Номенклатура": товарA.String(), "Склад": "Главный", "Количество": float64(3)},
	}, reg, &p2); err != nil {
		t.Fatal(err)
	}

	return db, ctx, reg, товарA.String(), товарB.String()
}

func TestGetMovements_NoFilter(t *testing.T) {
	db, ctx, reg, _, _ := setupAccumReg(t)
	rows, err := db.GetMovements(ctx, "Остатки", reg, RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("ожидалось 3 движения без отбора, получено %d", len(rows))
	}
}

func TestGetMovements_FilterByDimension(t *testing.T) {
	db, ctx, reg, товарA, _ := setupAccumReg(t)
	rows, err := db.GetMovements(ctx, "Остатки", reg, RegFilter{
		Dims: map[string]string{"Номенклатура": товарA},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("ожидалось 2 движения по товарA, получено %d: %v", len(rows), rows)
	}
}

func TestGetMovements_FilterByStringDimension(t *testing.T) {
	db, ctx, reg, _, _ := setupAccumReg(t)
	rows, err := db.GetMovements(ctx, "Остатки", reg, RegFilter{
		Dims: map[string]string{"Склад": "Резервный"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалось 1 движение по складу Резервный, получено %d", len(rows))
	}
}

func TestGetMovements_FilterByPeriod(t *testing.T) {
	db, ctx, reg, _, _ := setupAccumReg(t)
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	rows, err := db.GetMovements(ctx, "Остатки", reg, RegFilter{From: &from})
	if err != nil {
		t.Fatal(err)
	}
	// Только движения recorder2 (15.06) попадают в диапазон from=01.03.
	if len(rows) != 2 {
		t.Fatalf("ожидалось 2 движения с from=01.03, получено %d", len(rows))
	}

	to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	rows, err = db.GetMovements(ctx, "Остатки", reg, RegFilter{To: &to})
	if err != nil {
		t.Fatal(err)
	}
	// Только движение recorder1 (01.01) попадает (to=01.03).
	if len(rows) != 1 {
		t.Fatalf("ожидалось 1 движение с to=01.03, получено %d", len(rows))
	}
}

// TestGetMovements_FilterTo_IncludesWholeDay проверяет, что граница «по дату»
// включает весь день (движение в 12:30 попадает при To = конец того же дня).
// Тест RED до фикса parseRegFilter (To должен быть 23:59:59.999999999, а не полночь).
func TestGetMovements_FilterTo_IncludesWholeDay(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "testday.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	reg := &metadata.Register{
		Name:       "ДневнойРегистр",
		Dimensions: []metadata.Field{{Name: "Склад", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Кол", Type: metadata.FieldTypeNumber}},
	}
	if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
		t.Fatal(err)
	}

	// Движение в 12:30 12-го июня — должно попасть в отбор «по 12.06»
	p := time.Date(2026, 6, 12, 12, 30, 0, 0, time.UTC)
	rec := uuid.New()
	if err := db.WriteMovements(ctx, "ДневнойРегистр", "ПриходТовара", rec, []map[string]any{
		{"ВидДвижения": "Приход", "Склад": "Склад1", "Кол": float64(1)},
	}, reg, &p); err != nil {
		t.Fatal(err)
	}

	// To = 12.06 23:59:59.999999999 — конец дня, как выдаёт parseRegFilter после фикса
	endOfDay := time.Date(2026, 6, 12, 23, 59, 59, 999999999, time.UTC)
	rows, err := db.GetMovements(ctx, "ДневнойРегистр", reg, RegFilter{To: &endOfDay})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("движение в 12:30 должно попадать при To=конец_дня, получено %d строк", len(rows))
	}
}

func TestGetMovements_RejectsUnknownDimension(t *testing.T) {
	db, ctx, reg, _, _ := setupAccumReg(t)
	// Имя измерения, которого нет в reg — должно игнорироваться (защита от
	// инъекции имён колонок), выборка не сужается.
	rows, err := db.GetMovements(ctx, "Остатки", reg, RegFilter{
		Dims: map[string]string{"НетТакого": "значение"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("неизвестное измерение должно игнорироваться, получено %d строк", len(rows))
	}
}

func TestGetBalances_FilterByDimensionAndDate(t *testing.T) {
	db, ctx, reg, товарA, _ := setupAccumReg(t)

	// Остатки товарA на 31.12 — только recorder2 ещё не было, recorder1 (01.01)? Нет:
	// возьмём дату to=01.02.2026 — учитывается только recorder1 (10).
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	rows, err := db.GetBalances(ctx, "Остатки", reg, RegFilter{
		Dims: map[string]string{"Номенклатура": товарA},
		To:   &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 группа остатка товарA на 01.02, получено %d", len(rows))
	}
	if got := normFloat(rows[0]["Количество"]); got != 10 {
		t.Fatalf("остаток на 01.02 должен быть 10, получено %v", rows[0]["Количество"])
	}

	// Без даты (вся история) товарA = 10 + 3 = 13.
	rows, err = db.GetBalances(ctx, "Остатки", reg, RegFilter{
		Dims: map[string]string{"Номенклатура": товарA},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 группа остатка товарA, получено %d", len(rows))
	}
	if got := normFloat(rows[0]["Количество"]); got != 13 {
		t.Fatalf("итоговый остаток товарA должен быть 13, получено %v", rows[0]["Количество"])
	}
}

func normFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case decimal.Decimal:
		f, _ := x.Float64()
		return f
	}
	return -1
}

func setupInfoReg(t *testing.T) (*DB, context.Context, *metadata.InfoRegister) {
	t.Helper()
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ir := &metadata.InfoRegister{
		Name:       "Цены",
		Periodic:   true,
		Dimensions: []metadata.Field{{Name: "Товар", Type: metadata.FieldTypeString}},
		Resources:  []metadata.Field{{Name: "Цена", Type: metadata.FieldTypeNumber}},
	}
	if err := db.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{ir}); err != nil {
		t.Fatal(err)
	}
	p1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := db.InfoRegSet(ctx, ir, map[string]any{"Товар": "Молоко"}, map[string]any{"Цена": float64(50)}, &p1); err != nil {
		t.Fatal(err)
	}
	if err := db.InfoRegSet(ctx, ir, map[string]any{"Товар": "Хлеб"}, map[string]any{"Цена": float64(30)}, &p2); err != nil {
		t.Fatal(err)
	}
	return db, ctx, ir
}

func TestInfoRegList_NoFilter(t *testing.T) {
	db, ctx, ir := setupInfoReg(t)
	rows, err := db.InfoRegList(ctx, ir, RegFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("ожидалось 2 записи без отбора, получено %d", len(rows))
	}
}

func TestInfoRegList_FilterByDimension(t *testing.T) {
	db, ctx, ir := setupInfoReg(t)
	rows, err := db.InfoRegList(ctx, ir, RegFilter{Dims: map[string]string{"Товар": "Молоко"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 запись по Молоко, получено %d", len(rows))
	}
}

func TestInfoRegList_FilterByPeriod(t *testing.T) {
	db, ctx, ir := setupInfoReg(t)
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	rows, err := db.InfoRegList(ctx, ir, RegFilter{From: &from})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ожидалась 1 запись с from=01.03 (Хлеб 01.06), получено %d", len(rows))
	}
}

// Ссылочное измерение с НЕвалидным UUID (например ручной ?Измерение=мусор в URL):
// сырая строка не должна попадать в аргументы запроса. На PostgreSQL колонка
// имеет тип uuid и `col = 'мусор'` падает с 500 (invalid input syntax for uuid).
// Вместо подстановки строки генерируем заведомо ложное условие — пустой
// результат на обоих диалектах (фильтр по несуществующей ссылке = ничего).
func TestDimWhereClause_InvalidRefUUIDYieldsEmpty(t *testing.T) {
	ctx := context.Background()
	db, err := ConnectSQLite(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	dims := []metadata.Field{
		{Name: "Номенклатура", Type: metadata.FieldType("reference:Товары"), RefEntity: "Товары"},
	}
	f := RegFilter{Dims: map[string]string{"Номенклатура": "не-uuid"}}
	where, args, err := dimWhereClause(db.Dialect(), dims, f, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}

	if where == "" {
		t.Fatal("ожидалось сужающее условие для невалидного UUID, получено пусто (вернёт все строки)")
	}
	for _, a := range args {
		if s, ok := a.(string); ok && s == "не-uuid" {
			t.Fatalf("сырая не-UUID строка просочилась в аргументы (на PostgreSQL → 500): %v", args)
		}
	}
}
