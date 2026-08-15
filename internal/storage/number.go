package storage

import (
	"github.com/shopspring/decimal"

	"github.com/ivantit66/onebase/internal/i18n/i18nerr"
	"github.com/ivantit66/onebase/internal/metadata"
)

// canonicalNumberArg — единственная точка приведения числа перед записью.
//
// До неё представление числа в базе зависело от того, каким API записали
// объект, а не от метаданных поля: количество 100 из REST приезжало float64 и
// ложилось в SQLite как «100.0», а то же 100 из HTML-формы или из строки ТЧ —
// строкой «100» (#912). Колонка number на SQLite — TEXT, поэтому разница видна
// прямо в данных: один документ, два пути записи, два разных текста.
//
// Арифметика от этого не страдала (запросы платформы оборачивают сырые колонки
// в CAST), но страдало всё, что сравнивает представление: прикладной запрос без
// CAST, сверка выгрузок обмена между узлами, тесты и отчёты по сырым значениям.
//
// Приведение к decimal.Decimal и делает представление функцией метаданных:
// драйвер получает одно и то же значение независимо от пути записи, а
// decimal.Value() даёт стабильный текст. Разрядность (number(Length,Scale))
// проверяется здесь же — иначе округление зависело бы от того же пути.
func canonicalNumberArg(f metadata.Field, v any) (any, error) {
	if f.Type != metadata.FieldTypeNumber || v == nil {
		return v, nil
	}
	dec, ok := normalizeNumber(v).(decimal.Decimal)
	if !ok {
		// Нераспознанное значение (например мусорная строка) оставляем как есть:
		// приведение к числу — не место для отклонения записи, за типы отвечает
		// валидация выше по стеку.
		return v, nil
	}
	if f.Scale > 0 {
		dec = dec.Round(int32(f.Scale)) //nolint:gosec // G115: значение приходит из проверенной модели и заведомо укладывается в целевой тип
	}
	if f.Length > 0 {
		intDigits := len(dec.Abs().Truncate(0).String())
		if dec.Abs().Truncate(0).IsZero() {
			intDigits = 0
		}
		if intDigits > f.Length-f.Scale {
			return nil, i18nerr.Errorf("поле %q: число превышает разрядность (%d,%d)", f.Name, f.Length, f.Scale)
		}
	}
	return dec, nil
}

// canonicalRegNumber — тот же приём для регистров, где ошибка разрядности не
// возвращается наверх: движения пишутся внутри проведения, и падение записи
// из-за переполнения меняло бы поведение проведения, а не только текст в базе.
// Разрядность движений — отдельный вопрос (её никто не проверял и до #912).
func canonicalRegNumber(f metadata.Field, v any) any {
	out, err := canonicalNumberArg(f, v)
	if err != nil {
		return v
	}
	return out
}

// normalizeRegField — нормализация значения колонки регистра с учётом её
// метаданных: ссылка → UUID, число → канонический decimal.
//
// Измерения проходят через неё и при записи, и при построении WHERE. Иначе
// канонизация сама сломала бы поиск: запись положила бы «100», а отбор искал бы
// «100.0» по той же колонке TEXT.
func normalizeRegField(d Dialect, f metadata.Field, v any) any {
	return canonicalRegNumber(f, normalizeRegArg(d, v, f.RefEntity != ""))
}
