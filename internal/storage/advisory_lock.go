package storage

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

var ErrAdvisoryLockRequiresTx = errors.New("postgres advisory transaction lock requires active storage transaction")

// advisoryLockTimeout ограничивает ожидание pg_advisory_xact_lock: без него
// зависшая транзакция-держатель блокировала бы все конкурирующие проведения
// бессрочно и без диагностики. SET LOCAL действует только на текущую
// транзакцию и не влияет на остальные запросы соединения.
const advisoryLockTimeout = "30s"

// AdvisoryXactLock takes PostgreSQL transaction-scoped advisory locks for the
// provided logical keys. SQLite has no equivalent and treats this as a no-op.
func (db *DB) AdvisoryXactLock(ctx context.Context, keys []string) error {
	keys = normalizeAdvisoryKeys(keys)
	if len(keys) == 0 || !db.IsPostgres() {
		return nil
	}
	if !HasTx(ctx) {
		return ErrAdvisoryLockRequiresTx
	}
	if _, err := db.Exec(ctx, "SET LOCAL lock_timeout = '"+advisoryLockTimeout+"'"); err != nil {
		return fmt.Errorf("advisory lock timeout: %w", err)
	}
	for _, key := range keys {
		if _, err := db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryLockKey(key)); err != nil {
			if isLockTimeoutErr(err) {
				return fmt.Errorf("блокировка данных %q не получена за %s — её держит другое проведение: %w", key, advisoryLockTimeout, err)
			}
			return fmt.Errorf("advisory lock %q: %w", key, err)
		}
	}
	// SET LOCAL живёт до конца транзакции — возвращаем безлимит (0), чтобы
	// таймаут advisory-ожидания не распространился на обычные row-локи
	// последующих UPDATE в той же транзакции проведения.
	if _, err := db.Exec(ctx, "SET LOCAL lock_timeout = '0'"); err != nil {
		return fmt.Errorf("advisory lock timeout reset: %w", err)
	}
	return nil
}

// isLockTimeoutErr распознаёт истечение lock_timeout (SQLSTATE 55P03).
func isLockTimeoutErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "55P03") || strings.Contains(strings.ToLower(s), "lock timeout")
}

func normalizeAdvisoryKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func advisoryLockKey(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("onebase:data-lock:"))
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64()) //nolint:gosec // G115: перенос знака намеренный — ключ advisory-блокировки PostgreSQL занимает весь диапазон int64
}
