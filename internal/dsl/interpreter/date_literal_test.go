package interpreter

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestDateLiteral_EvaluatesAsDate(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want time.Time
	}{
		{"'20260511'", time.Date(2026, 5, 11, 0, 0, 0, 0, time.Local)},
		{"'20260511143045'", time.Date(2026, 5, 11, 14, 30, 45, 0, time.Local)},
	} {
		t.Run(tc.src, func(t *testing.T) {
			got := evalBreakFunc(t, "Функция Тест()\n  Возврат "+tc.src+";\nКонецФункции")
			date, ok := got.(time.Time)
			if !ok || !date.Equal(tc.want) {
				t.Fatalf("%s = %T(%v), want %v", tc.src, got, got, tc.want)
			}
		})
	}
}

func TestDateLiteral_ParticipatesInDateComparisons(t *testing.T) {
	got := evalBreakFunc(t, `Функция Тест()
  Возврат '20260511143045' = Дата(2026, 5, 11, 14, 30, 45)
    И Дата('20260511') < '20260512';
КонецФункции`)
	if got != true {
		t.Fatalf("date literal comparison = %v, want true", got)
	}
}

func TestDateLiteral_EmptyDateIsZeroValue(t *testing.T) {
	got := evalBreakFunc(t, `Функция Тест()
  Если '00010101' <> Дата(0) Тогда
    Возврат "не равны";
  КонецЕсли;
  Возврат Число(Дата('00010101000000'));
КонецФункции`)
	number, ok := got.(decimal.Decimal)
	if !ok || !number.IsZero() {
		t.Fatalf("empty date result = %T(%v), want decimal zero", got, got)
	}
}
