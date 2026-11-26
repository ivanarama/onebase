package storage

import "time"

// sqliteTimeLayout is the canonical UTC representation of time.Time in SQLite.
//
// Зачем: драйвер modernc.org/sqlite по умолчанию биндит time.Time как Go-строку
// `2006-01-02 15:04:05 -0700 MST` (с именем зоны, напр. `… +0300 MSK`). Функции
// дат SQLite (`date`, `strftime`) такой формат распарсить НЕ могут и молча
// возвращают NULL. Из-за этого ломалась группировка по периоду:
// `.Обороты(, , Месяц)`, `НАЧАЛОПЕРИОДА`, `Год/Месяц/День` сваливали все периоды
// в одну пустую корзину, а виджеты/отчёты «по месяцам» считали неверно — при
// зелёном `onebase check`.
//
// SQLite has no timezone-aware timestamp type, while PostgreSQL uses
// TIMESTAMPTZ. Storing UTC makes comparisons, month buckets and rollups mean
// the same thing on both backends. Date-only values are parsed as UTC before
// they reach this boundary, so their calendar date remains unchanged.
const sqliteTimeLayout = "2006-01-02 15:04:05-07:00"

// SQLiteDateComparisonKey gives TEXT operands of register date comparisons
// the same chronological ordering, including fractional seconds.
func SQLiteDateComparisonKey(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000000000")
}

// normalizeSQLiteArgs возвращает копию args, где каждый time.Time (и непустой
// *time.Time) приведён к strftime-совместимой строке; остальные значения
// проходят как есть. Применяется только на SQLite-пути — pgx биндит time.Time
// нативно как timestamptz, там нормализация не нужна и вредна.
func normalizeSQLiteArgs(args []any) []any {
	if len(args) == 0 {
		return args
	}
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case time.Time:
			out[i] = v.UTC().Format(sqliteTimeLayout)
		case *time.Time:
			if v == nil {
				out[i] = nil
			} else {
				out[i] = v.UTC().Format(sqliteTimeLayout)
			}
		default:
			out[i] = a
		}
	}
	return out
}
