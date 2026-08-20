package storage

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
)

// ErrIncompleteWriteLifecycle means a transaction tried to commit an
// intentionally incomplete provisional/prelude snapshot without its ordinary
// required-backed final write.
var ErrIncompleteWriteLifecycle = errors.New("storage: incomplete write lifecycle")

type writeLifecycle struct {
	key           string
	description   string
	original      map[string]any
	tablePartRows []map[string]any
	armed         atomic.Bool
	done          atomic.Bool
}

func entityWriteLifecycleKey(db *DB, kind, entityName string, id uuid.UUID) string {
	return fmt.Sprintf("%p|%s|entity|%s|%s", db, kind, entityName, id)
}

func tablePartWriteLifecycleKey(db *DB, entityName, tablePartName string, parentID uuid.UUID) string {
	return fmt.Sprintf("%p|posting-prelude|tablepart|%s|%s|%s", db, entityName, tablePartName, parentID)
}

func beginWriteLifecycle(ctx context.Context, key, description string) (*writeLifecycle, error) {
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return nil, errors.New("storage: write lifecycle requires a managed transaction")
	}
	state := &writeLifecycle{key: key, description: description}
	hooks.mu.Lock()
	if existing := hooks.writeLifecycles[key]; existing != nil && !existing.done.Load() {
		hooks.mu.Unlock()
		return nil, fmt.Errorf("storage: write lifecycle already active: %s", description)
	}
	hooks.writeLifecycles[key] = state
	hooks.mu.Unlock()

	if !DeferBeforeTxCommit(ctx, func() error {
		if state.done.Load() {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrIncompleteWriteLifecycle, state.description)
	}) {
		cancelWriteLifecycle(ctx, state)
		return nil, errors.New("storage: cannot register write lifecycle commit guard")
	}
	if !DeferUntilTxRollback(ctx, func() { cancelWriteLifecycle(ctx, state) }) {
		cancelWriteLifecycle(ctx, state)
		return nil, errors.New("storage: cannot register write lifecycle rollback cleanup")
	}
	return state, nil
}

func pendingWriteLifecycle(ctx context.Context, key string) (*writeLifecycle, error) {
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return nil, errors.New("storage: final write requires a managed transaction")
	}
	state := lookupWriteLifecycle(ctx, key)
	if state == nil || state.done.Load() {
		return nil, errors.New("storage: final write has no matching provisional/prelude write")
	}
	if !state.armed.Load() {
		return nil, fmt.Errorf("%w: provisional/prelude write did not complete: %s",
			ErrIncompleteWriteLifecycle, state.description)
	}
	return state, nil
}

func lookupWriteLifecycle(ctx context.Context, key string) *writeLifecycle {
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return nil
	}
	hooks.mu.Lock()
	state := hooks.writeLifecycles[key]
	hooks.mu.Unlock()
	if state == nil || state.done.Load() {
		return nil
	}
	return state
}

func armWriteLifecycle(ctx context.Context, state *writeLifecycle) error {
	if state == nil {
		return errors.New("storage: cannot arm a nil write lifecycle")
	}
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return errors.New("storage: write lifecycle requires a managed transaction")
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.writeLifecycles[state.key] != state || state.done.Load() {
		return errors.New("storage: write lifecycle is no longer active")
	}
	state.armed.Store(true)
	return nil
}

func finishWriteLifecycle(ctx context.Context, state *writeLifecycle) error {
	if state == nil {
		return errors.New("storage: cannot finish a nil write lifecycle")
	}
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return errors.New("storage: final write requires a managed transaction")
	}
	hooks.mu.Lock()
	active := hooks.writeLifecycles[state.key] == state && !state.done.Load()
	hooks.mu.Unlock()
	if !active {
		return errors.New("storage: write lifecycle is no longer active")
	}
	if !state.armed.Load() {
		return fmt.Errorf("%w: cannot finish an unsuccessful provisional/prelude write: %s",
			ErrIncompleteWriteLifecycle, state.description)
	}

	// A final write may itself run in a nested WithTxScope (staged entities do
	// this internally). If that savepoint is later rolled back while the prelude
	// lives in an outer scope, the lifecycle must become pending again; otherwise
	// the outer transaction could commit only the incomplete prelude snapshot.
	if !DeferUntilTxRollback(ctx, func() { restoreWriteLifecycle(ctx, state) }) {
		return errors.New("storage: cannot register final write rollback guard")
	}

	state.done.Store(true)
	hooks.mu.Lock()
	if hooks.writeLifecycles[state.key] == state {
		delete(hooks.writeLifecycles, state.key)
	}
	hooks.mu.Unlock()
	return nil
}

func cancelWriteLifecycle(ctx context.Context, state *writeLifecycle) {
	if state == nil {
		return
	}
	state.done.Store(true)
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return
	}
	hooks.mu.Lock()
	if hooks.writeLifecycles[state.key] == state {
		delete(hooks.writeLifecycles, state.key)
	}
	hooks.mu.Unlock()
}

func restoreWriteLifecycle(ctx context.Context, state *writeLifecycle) {
	if state == nil {
		return
	}
	state.done.Store(false)
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return
	}
	hooks.mu.Lock()
	if existing := hooks.writeLifecycles[state.key]; existing == nil || existing == state || existing.done.Load() {
		hooks.writeLifecycles[state.key] = state
	}
	hooks.mu.Unlock()
}
