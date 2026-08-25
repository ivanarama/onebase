package printform

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Числовые поля приходят из хранилища как decimal.Decimal (storage.normalizeNumber).
// Пока printform их не распознавал, «| number:2» молча не применялся, а
// Итог.<ТЧ>.<Поле> всегда был 0 — на поставляемом примере АктСписания печать
// показывала «Итого: 0.00» при непустой табличной части.
func TestDecimalValuesFormatAndSum(t *testing.T) {
	ctx := &RenderContext{TableParts: map[string][]map[string]any{
		"Товары": {
			{"Цена": decimal.RequireFromString("620"), "Сумма": decimal.RequireFromString("6200")},
			{"Цена": decimal.RequireFromString("260.5"), "Сумма": decimal.RequireFromString("5210")},
		},
	}}
	row := ctx.TableParts["Товары"][0]

	if got := InterpolateText("{{Цена | number:2}}", ctx, row, 1); got != "620.00" {
		t.Errorf("формат числа строки: got %q, want 620.00", got)
	}
	if got := InterpolateText("{{Итог.Товары.Сумма | number:2}}", ctx, row, 1); got != "11410.00" {
		t.Errorf("итог по табличной части: got %q, want 11410.00", got)
	}
	// Без формата значение остаётся как есть — поведение не менялось.
	if got := InterpolateText("{{Цена}}", ctx, row, 1); got != "620" {
		t.Errorf("значение без формата: got %q, want 620", got)
	}
}
