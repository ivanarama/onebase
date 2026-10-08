package storage

import (
	"context"
	"errors"
	"strings"
)

type readOnlyContextKey struct{}

var ErrReadOnlyContext = errors.New("запись недоступна в обработчике Поиск")

// ReadOnlyContext marks a form search handler's database access as read-only.
// The check lives at the storage boundary so writes through object methods,
// managers, or explicit transactions obey the same rule.
func ReadOnlyContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, readOnlyContextKey{}, true)
}

func writeAllowed(ctx context.Context) error {
	if ctx != nil && ctx.Value(readOnlyContextKey{}) == true {
		return ErrReadOnlyContext
	}
	return nil
}

// CheckWriteAllowed lets higher-level writers reject a search-handler write
// before hooks, number allocation, or other work with side effects begins.
func CheckWriteAllowed(ctx context.Context) error { return writeAllowed(ctx) }

func queryAllowed(ctx context.Context, sqlText string) error {
	if err := writeAllowed(ctx); err != nil {
		// Search handlers only need SELECT. Reject statements that could mutate
		// through a Query/QueryRow path (including CTEs with writable clauses).
		fields := strings.Fields(sqlText)
		if len(fields) > 0 && strings.EqualFold(fields[0], "SELECT") && !strings.Contains(sqlText, ";") {
			return nil
		}
		return err
	}
	return nil
}
