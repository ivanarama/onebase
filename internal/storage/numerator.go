package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/ivantit66/onebase/internal/metadata"
)

// EnsureNumeratorSchema creates the _numerators table if it does not exist.
func (db *DB) EnsureNumeratorSchema(ctx context.Context) error {
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _numerators (
			entity_name TEXT    NOT NULL,
			period_key  TEXT    NOT NULL,
			last_number INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (entity_name, period_key)
		)`)
	return err
}

// NextNumber atomically increments and returns the next sequence number
// for (entityName, periodKey). Safe under concurrent requests.
func (db *DB) NextNumber(ctx context.Context, entityName, periodKey string) (int, error) {
	d := db.dialect
	q := fmt.Sprintf(`
		INSERT INTO _numerators (entity_name, period_key, last_number)
		VALUES (%s, %s, 1)
		ON CONFLICT (entity_name, period_key) DO UPDATE
			SET last_number = _numerators.last_number + 1
		RETURNING last_number
	`, d.Placeholder(1), d.Placeholder(2))
	var n int
	err := db.QueryRow(ctx, q, entityName, periodKey).Scan(&n)
	return n, err
}

// FormatNumber formats an integer into a prefixed, zero-padded string.
// FormatNumber("ПОС-", 8, 42) → "ПОС-00000042"
func FormatNumber(prefix string, length, number int) string {
	digits := fmt.Sprintf("%d", number)
	if len(digits) < length {
		digits = strings.Repeat("0", length-len(digits)) + digits
	}
	return prefix + digits
}

// ComputePeriodKey derives the period key from a document date field and
// the numerator's Period setting ("year", "month", "none"). Scope (если задан)
// добавляется к ключу — например, scope: Организация даст у каждой
// организации свой счётчик ( Формат: "<period>|<scopeValue>",
// либо просто "<period>" если scope не задан.
// Deprecated: используйте PeriodKeyFor — он выбирает дату детерминированно, по
// объявленному порядку реквизитов. Здесь дата берётся перебором map, поэтому у
// документа с двумя датами ключ был случайным (Д13 плана 117). Оставлено ради
// совместимости внешних вызовов и делегирует в новую функцию.
func ComputePeriodKey(num *metadata.Numerator, fields map[string]any) string {
	return PeriodKeyFor(nil, num, fields)
}
