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
	for _, key := range keys {
		if _, err := db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", advisoryLockKey(key)); err != nil {
			return fmt.Errorf("advisory lock %q: %w", key, err)
		}
	}
	return nil
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
	return int64(h.Sum64())
}
