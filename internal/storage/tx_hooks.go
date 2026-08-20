package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// txHooksKey stores callbacks whose lifetime must follow the database
// transaction carried by the context. The callback stack mirrors DSL
// savepoints: a released savepoint is folded into its parent, while a rolled
// back savepoint runs only its own rollback callbacks.
type txHooksKey struct{}

type txHookScope struct {
	beforeCommit []func() error
	commit       []func()
	rollback     []func()
}

type txHooks struct {
	mu              sync.Mutex
	scopes          []txHookScope
	writeLifecycles map[string]*writeLifecycle
	done            bool
}

func newTxHooks() *txHooks {
	return &txHooks{
		scopes:          []txHookScope{{}},
		writeLifecycles: make(map[string]*writeLifecycle),
	}
}

func txHooksFromContext(ctx context.Context) *txHooks {
	hooks, _ := ctx.Value(txHooksKey{}).(*txHooks)
	return hooks
}

// DeferUntilTxCommit schedules fn after the outer database transaction has
// committed. It returns false when ctx is not managed by a transaction; the
// caller should then perform the action immediately.
func DeferUntilTxCommit(ctx context.Context, fn func()) bool {
	return addTxHook(ctx, fn, true)
}

// DeferUntilTxRollback schedules fn when the current transaction/savepoint is
// rolled back. When a savepoint commits, its rollback callbacks are inherited
// by the parent transaction.
func DeferUntilTxRollback(ctx context.Context, fn func()) bool {
	return addTxHook(ctx, fn, false)
}

// DeferBeforeTxCommit registers an invariant which must hold immediately
// before the outer database transaction commits. Returning an error aborts and
// rolls back that transaction. Savepoint scopes follow the same rules as the
// ordinary hooks: release inherits the check, rollback discards it.
func DeferBeforeTxCommit(ctx context.Context, fn func() error) bool {
	if fn == nil {
		return false
	}
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return false
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.done || len(hooks.scopes) == 0 {
		return false
	}
	scope := &hooks.scopes[len(hooks.scopes)-1]
	scope.beforeCommit = append(scope.beforeCommit, fn)
	return true
}

func addTxHook(ctx context.Context, fn func(), onCommit bool) bool {
	if fn == nil {
		return false
	}
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return false
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.done || len(hooks.scopes) == 0 {
		return false
	}
	scope := &hooks.scopes[len(hooks.scopes)-1]
	if onCommit {
		scope.commit = append(scope.commit, fn)
	} else {
		scope.rollback = append(scope.rollback, fn)
	}
	return true
}

// PushTxHookScope mirrors creation of a database savepoint.
func PushTxHookScope(ctx context.Context) {
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if !hooks.done {
		hooks.scopes = append(hooks.scopes, txHookScope{})
	}
}

// CommitTxHookScope mirrors RELEASE SAVEPOINT: callbacks remain pending until
// the outer transaction finishes.
func CommitTxHookScope(ctx context.Context) {
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if hooks.done || len(hooks.scopes) < 2 {
		return
	}
	child := hooks.scopes[len(hooks.scopes)-1]
	hooks.scopes = hooks.scopes[:len(hooks.scopes)-1]
	parent := &hooks.scopes[len(hooks.scopes)-1]
	parent.beforeCommit = append(parent.beforeCommit, child.beforeCommit...)
	parent.commit = append(parent.commit, child.commit...)
	parent.rollback = append(parent.rollback, child.rollback...)
}

// RollbackTxHookScope mirrors ROLLBACK TO SAVEPOINT. Commit callbacks are
// discarded; rollback callbacks run in reverse registration order.
func RollbackTxHookScope(ctx context.Context) {
	hooks := txHooksFromContext(ctx)
	if hooks == nil {
		return
	}
	hooks.mu.Lock()
	if hooks.done || len(hooks.scopes) < 2 {
		hooks.mu.Unlock()
		return
	}
	child := hooks.scopes[len(hooks.scopes)-1]
	hooks.scopes = hooks.scopes[:len(hooks.scopes)-1]
	hooks.mu.Unlock()
	runTxHooksReverse(child.rollback)
}

func (h *txHooks) commitAll() {
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return
	}
	h.done = true
	var callbacks []func()
	for _, scope := range h.scopes {
		callbacks = append(callbacks, scope.commit...)
	}
	h.scopes = nil
	h.mu.Unlock()
	runTxHooks(callbacks)
}

func (h *txHooks) checkBeforeCommit() error {
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return errors.New("storage: transaction hooks already completed")
	}
	var callbacks []func() error
	for _, scope := range h.scopes {
		callbacks = append(callbacks, scope.beforeCommit...)
	}
	h.mu.Unlock()
	for _, fn := range callbacks {
		if err := runBeforeCommitHook(fn); err != nil {
			return err
		}
	}
	return nil
}

func (h *txHooks) rollbackAll() {
	h.mu.Lock()
	if h.done {
		h.mu.Unlock()
		return
	}
	h.done = true
	var callbacks []func()
	for _, scope := range h.scopes {
		callbacks = append(callbacks, scope.rollback...)
	}
	h.scopes = nil
	h.mu.Unlock()
	runTxHooksReverse(callbacks)
}

func runTxHooks(callbacks []func()) {
	for _, fn := range callbacks {
		runTxHook(fn)
	}
}

func runTxHooksReverse(callbacks []func()) {
	for i := len(callbacks) - 1; i >= 0; i-- {
		runTxHook(callbacks[i])
	}
}

// A callback runs after the database outcome is final, so its panic must not
// turn a successful commit into an apparent failure or suppress other cleanup.
func runTxHook(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

func runBeforeCommitHook(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("storage: before-commit hook panic: %v", recovered)
		}
	}()
	return fn()
}

type hookedTx struct {
	Tx
	hooks *txHooks
}

func (t *hookedTx) Commit(ctx context.Context) error {
	if err := t.hooks.checkBeforeCommit(); err != nil {
		rollbackErr := t.Tx.Rollback(ctx)
		t.hooks.rollbackAll()
		return errors.Join(err, rollbackErr)
	}
	err := t.Tx.Commit(ctx)
	if err != nil {
		t.hooks.rollbackAll()
		return err
	}
	t.hooks.commitAll()
	return nil
}

func (t *hookedTx) Rollback(ctx context.Context) error {
	err := t.Tx.Rollback(ctx)
	t.hooks.rollbackAll()
	return err
}
