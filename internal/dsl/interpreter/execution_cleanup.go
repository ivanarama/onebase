package interpreter

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const transactionCleanupTimeout = 5 * time.Second

// ErrTransactionLeftOpen is returned when a DSL procedure exits successfully
// while still owning a transaction or savepoint. The scope is rolled back
// before this error is returned.
var ErrTransactionLeftOpen = errors.New("DSL-обработчик оставил открытую транзакцию; она отменена")

// FinishTxExecution closes every transaction/savepoint still owned by state.
// Cleanup keeps transaction values but is detached from execution cancellation
// and independently bounded, so timeout/error paths cannot strand a pool
// connection or a borrowed savepoint.
func FinishTxExecution(state *TxState, runErr error) error {
	if state == nil || !state.HasOpen() {
		return runErr
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(state.Ctx()), transactionCleanupTimeout)
	cleanupErr := state.RollbackOpen(cleanupCtx)
	cancel()

	if runErr == nil {
		runErr = ErrTransactionLeftOpen
	}
	if cleanupErr != nil {
		// Do not wrap a possible *DSLError here: FormatUserError intentionally
		// collapses wrapped DSL errors to their business message and would hide
		// the cleanup failure. Preserve both texts and unwrap cleanup instead.
		return fmt.Errorf("%v; не удалось отменить открытую DSL-транзакцию: %w", runErr, cleanupErr)
	}
	return runErr
}

// RollbackTxExecution is a best-effort panic/early-return backstop. Normal
// execution paths call FinishTxExecution synchronously before continuing.
func RollbackTxExecution(state *TxState) {
	if state == nil || !state.HasOpen() {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(state.Ctx()), transactionCleanupTimeout)
	defer cancel()
	_ = state.RollbackOpen(cleanupCtx)
}
