package interpreter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/storage"
)

// TxDB is the minimal storage interface needed for DSL transactions.
// Satisfied by *storage.DB.
type TxDB interface {
	BeginTx(ctx context.Context) (storage.Tx, context.Context, error)
	Exec(ctx context.Context, sql string, args ...any) (storage.CommandTag, error)
}

// TxState is a mutable context holder for DSL transaction management.
// All DSL builtins that write to storage should call Ctx() to get the
// current context — it carries the active transaction if one is open.
type TxState struct {
	ctxStack []context.Context // [0]=base, [N]=current (possibly with tx)
	txs      []storage.Tx      // active transaction per nesting level
	saves    []string          // savepoint names for nested transactions
	db       TxDB              // savepoint executor; set on first begin
}

// txExecutionContext takes transaction/storage values from valueCtx while
// retaining cancellation and deadlines from executionCtx. The transaction is
// opened with a detached context so database/sql cannot auto-rollback it behind
// the hook wrapper when execution is canceled; individual DB calls still stop
// with the execution context. context.WithoutCancel keeps Value(), which makes
// the same transaction available to bounded cleanup.
type txExecutionContext struct {
	valueCtx     context.Context
	executionCtx context.Context
}

func (c txExecutionContext) Deadline() (time.Time, bool) { return c.executionCtx.Deadline() }
func (c txExecutionContext) Done() <-chan struct{}       { return c.executionCtx.Done() }
func (c txExecutionContext) Err() error                  { return c.executionCtx.Err() }
func (c txExecutionContext) Value(key any) any           { return c.valueCtx.Value(key) }

// NewTxState creates a TxState with the given base context.
func NewTxState(ctx context.Context) *TxState {
	return &TxState{ctxStack: []context.Context{ctx}}
}

// Ctx returns the current context (contains the active transaction if any).
func (s *TxState) Ctx() context.Context {
	return s.ctxStack[len(s.ctxStack)-1]
}

// HasOpen reports whether the DSL execution still owns an open transaction or
// savepoint. Execution boundaries use it to prevent a procedure that forgot to
// commit/rollback from leaking a connection past the request.
func (s *TxState) HasOpen() bool {
	return s != nil && len(s.txs) > 0
}

// RollbackOpen unwinds every transaction/savepoint still owned by the DSL,
// from the innermost level to the outermost one. The supplied context should be
// detached from the execution cancellation (and bounded by the caller), while
// retaining the transaction values from Ctx().
//
// Database rollback/release is always attempted before the corresponding hook
// scope is discarded. The state is cleared even when cleanup fails, and all
// cleanup errors are returned instead of panicking.
func (s *TxState) RollbackOpen(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var cleanupErr error
	for len(s.txs) > 0 {
		tx := s.txs[len(s.txs)-1]
		s.txs = s.txs[:len(s.txs)-1]
		if len(s.ctxStack) > 1 {
			s.ctxStack = s.ctxStack[:len(s.ctxStack)-1]
		}

		if len(s.saves) > 0 {
			sp := s.saves[len(s.saves)-1]
			s.saves = s.saves[:len(s.saves)-1]
			if s.db == nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("rollback savepoint %s: transaction database is unavailable", sp))
			} else {
				if _, err := s.db.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp); err != nil {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("rollback savepoint %s: %w", sp, err))
				}
				if _, err := s.db.Exec(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf("release savepoint %s: %w", sp, err))
				}
			}
			storage.RollbackTxHookScope(ctx)
			continue
		}

		if tx == nil {
			cleanupErr = errors.Join(cleanupErr, errors.New("rollback transaction: transaction handle is unavailable"))
			continue
		}
		if err := tx.Rollback(ctx); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("rollback transaction: %w", err))
		}
	}
	return cleanupErr
}

func (s *TxState) begin(db TxDB) {
	s.db = db
	if len(s.txs) == 0 {
		// buildDSLVars may itself run inside entityservice's atomic hook
		// transaction. An explicit НачатьТранзакцию then becomes a savepoint of
		// that borrowed transaction instead of opening a second connection (which
		// deadlocks SQLite's single-connection pool).
		if storage.HasTx(s.Ctx()) {
			sp := fmt.Sprintf("sp%d", len(s.saves)+1)
			if _, err := db.Exec(s.Ctx(), "SAVEPOINT "+sp); err != nil {
				panic(userError{Msg: "НачатьТранзакцию (savepoint): " + err.Error()})
			}
			storage.PushTxHookScope(s.Ctx())
			s.saves = append(s.saves, sp)
			s.txs = append(s.txs, nil) // outer transaction is owned by the caller
			s.ctxStack = append(s.ctxStack, s.Ctx())
			return
		}
		executionCtx := s.Ctx()
		if err := executionCtx.Err(); err != nil {
			panic(userError{Msg: "НачатьТранзакцию: " + err.Error()})
		}
		tx, txValueCtx, err := db.BeginTx(context.WithoutCancel(executionCtx))
		if err != nil {
			panic(userError{Msg: "НачатьТранзакцию: " + err.Error()})
		}
		txCtx := txExecutionContext{valueCtx: txValueCtx, executionCtx: executionCtx}
		s.txs = append(s.txs, tx)
		s.ctxStack = append(s.ctxStack, txCtx)
	} else {
		// Nested: SAVEPOINT
		sp := fmt.Sprintf("sp%d", len(s.saves)+1)
		if _, err := db.Exec(s.Ctx(), "SAVEPOINT "+sp); err != nil {
			panic(userError{Msg: "НачатьТранзакцию (savepoint): " + err.Error()})
		}
		storage.PushTxHookScope(s.Ctx())
		s.saves = append(s.saves, sp)
		s.txs = append(s.txs, s.txs[0])          // same underlying tx
		s.ctxStack = append(s.ctxStack, s.Ctx()) // same ctx
	}
}

func (s *TxState) commit() {
	if len(s.txs) == 0 {
		panic(userError{Msg: "ЗафиксироватьТранзакцию: транзакция не активна"})
	}
	tx := s.txs[len(s.txs)-1]
	txCtx := s.ctxStack[len(s.ctxStack)-1]
	s.txs = s.txs[:len(s.txs)-1]
	s.ctxStack = s.ctxStack[:len(s.ctxStack)-1]

	if len(s.saves) > 0 {
		sp := s.saves[len(s.saves)-1]
		s.saves = s.saves[:len(s.saves)-1]
		if _, err := s.db.Exec(txCtx, "RELEASE SAVEPOINT "+sp); err != nil {
			panic(userError{Msg: "ЗафиксироватьТранзакцию: " + err.Error()})
		}
		storage.CommitTxHookScope(txCtx)
	} else {
		if err := tx.Commit(txCtx); err != nil {
			panic(userError{Msg: "ЗафиксироватьТранзакцию: " + err.Error()})
		}
	}
}

func (s *TxState) rollback() {
	if len(s.txs) == 0 {
		panic(userError{Msg: "ОтменитьТранзакцию: транзакция не активна"})
	}
	tx := s.txs[len(s.txs)-1]
	txCtx := s.ctxStack[len(s.ctxStack)-1]
	s.txs = s.txs[:len(s.txs)-1]
	s.ctxStack = s.ctxStack[:len(s.ctxStack)-1]

	if len(s.saves) > 0 {
		sp := s.saves[len(s.saves)-1]
		s.saves = s.saves[:len(s.saves)-1]
		if _, err := s.db.Exec(txCtx, "ROLLBACK TO SAVEPOINT "+sp); err != nil {
			panic(userError{Msg: "ОтменитьТранзакцию: " + err.Error()})
		}
		if _, err := s.db.Exec(txCtx, "RELEASE SAVEPOINT "+sp); err != nil {
			panic(userError{Msg: "ОтменитьТранзакцию (release savepoint): " + err.Error()})
		}
		storage.RollbackTxHookScope(txCtx)
	} else {
		_ = tx.Rollback(txCtx)
	}
}

// NewTxFunctions returns DSL builtins for transaction control.
// Inject the returned map into interpreter.Run via extraVars.
// All DSL functions that write to storage must call state.Ctx() to get the
// current context so they participate in the active transaction.
func NewTxFunctions(state *TxState, db TxDB) map[string]any {
	begin := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		state.begin(db)
		return nil, nil
	})
	commit := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		state.commit()
		return nil, nil
	})
	rollback := BuiltinFunc(func(args []any, file string, line int) (any, error) {
		state.rollback()
		return nil, nil
	})
	return map[string]any{
		"НачатьТранзакцию":        begin,
		"BeginTransaction":        begin,
		"ЗафиксироватьТранзакцию": commit,
		"CommitTransaction":       commit,
		"ОтменитьТранзакцию":      rollback,
		"RollbackTransaction":     rollback,
	}
}
