package query_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/query"
	"github.com/ivantit66/onebase/internal/storage"
)

// Денежные суммы в запросах точны на обоих диалектах.
//
// На SQLite число хранится текстом, а встроенный sum() складывал double: приход
// 0.1 и 0.2 против расхода 0.3 давал остаток 2.78e-17, и отбор «<> 0»
// возвращал закрытую позицию. На PostgreSQL (NUMERIC) тот же запрос отвечал
// нулём — один и тот же отчёт показывал разное на разных движках.
func TestMoneySumsAreExact_Matrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, db *storage.DB) {
		ctx := context.Background()
		reg := &metadata.Register{
			Name:       "ДенежныеСредства",
			Dimensions: []metadata.Field{{Name: "Касса", Type: metadata.FieldTypeString}},
			Resources:  []metadata.Field{{Name: "Сумма", Type: metadata.FieldTypeNumber}},
		}
		if err := db.MigrateRegisters(ctx, []*metadata.Register{reg}); err != nil {
			t.Fatalf("миграция: %v", err)
		}
		period := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
		for _, m := range []struct{ kind, sum string }{
			{"Приход", "0.1"}, {"Приход", "0.2"}, {"Расход", "0.3"},
		} {
			rows := []map[string]any{{"Касса": "К1", "Сумма": m.sum, "ВидДвижения": m.kind}}
			if err := db.WriteMovements(ctx, reg.Name, "ПКО", uuid.New(), rows, reg, &period); err != nil {
				t.Fatalf("движение %s %s: %v", m.kind, m.sum, err)
			}
		}

		run := func(text string) []map[string]any {
			t.Helper()
			res, err := query.Compile(text, query.CompileOpts{Registers: []*metadata.Register{reg}, Dialect: db.Dialect()})
			if err != nil {
				t.Fatalf("компиляция %q: %v", text, err)
			}
			rows, _, err := query.Run(ctx, db, &res)
			if err != nil {
				t.Fatalf("исполнение %q: %v\nSQL: %s", text, err, res.SQL)
			}
			return rows
		}
		exact := func(rows []map[string]any, col, want string) {
			t.Helper()
			if len(rows) != 1 {
				t.Fatalf("колонка %s: строк %d, ждали 1: %v", col, len(rows), rows)
			}
			got := moneyDecimal(t, moneyField(rows[0], col))
			if !got.Equal(decimal.RequireFromString(want)) {
				t.Fatalf("%s = %s, ждали ровно %s", col, got, want)
			}
		}

		exact(run(`ВЫБРАТЬ СуммаОстаток ИЗ РегистрНакопления.ДенежныеСредства.Остатки()`), "СуммаОстаток", "0")
		if rows := run(`ВЫБРАТЬ Касса ИЗ РегистрНакопления.ДенежныеСредства.Остатки() ГДЕ СуммаОстаток <> 0`); len(rows) != 0 {
			t.Fatalf("отбор «СуммаОстаток <> 0» вернул закрытую позицию: %v", rows)
		}
		exact(run(`ВЫБРАТЬ СУММА(Сумма) КАК Всего ИЗ РегистрНакопления.ДенежныеСредства`), "Всего", "0.6")
		turnovers := run(`ВЫБРАТЬ СуммаПриход, СуммаРасход ИЗ РегистрНакопления.ДенежныеСредства.Обороты()`)
		exact(turnovers, "СуммаПриход", "0.3")
		exact(turnovers, "СуммаРасход", "0.3")
	})
}

func moneyField(row map[string]any, name string) any {
	for k, v := range row {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

func moneyDecimal(t *testing.T, v any) decimal.Decimal {
	t.Helper()
	switch x := v.(type) {
	case decimal.Decimal:
		return x
	case float64:
		return decimal.NewFromFloat(x)
	case int64:
		return decimal.NewFromInt(x)
	case int:
		return decimal.NewFromInt(int64(x))
	case string:
		return decimal.RequireFromString(x)
	case []byte:
		return decimal.RequireFromString(string(x))
	case nil:
		t.Fatalf("значение NULL")
	}
	d, err := decimal.NewFromString(fmt.Sprint(v))
	if err != nil {
		t.Fatalf("не число: %#v (%T)", v, v)
	}
	return d
}
