package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

const dslTransactionCleanupTimeout = 5 * time.Second

var errDSLTransactionLeftOpen = errors.New("DSL-обработчик оставил открытую транзакцию; она отменена")

// finishDSLExecution is the transaction boundary for request/offline DSL
// execution. Cleanup is deliberately detached from execution cancellation so a
// timeout cannot strand a database transaction; it is independently bounded.
func finishDSLExecution(state *interpreter.TxState, runErr error) error {
	if state == nil || !state.HasOpen() {
		return runErr
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(state.Ctx()), dslTransactionCleanupTimeout)
	cleanupErr := state.RollbackOpen(cleanupCtx)
	cancel()

	if runErr == nil {
		runErr = errDSLTransactionLeftOpen
	}
	if cleanupErr != nil {
		// Do not wrap a possible *interpreter.DSLError here: FormatUserError
		// intentionally collapses wrapped DSL errors to their business message
		// and would hide the cleanup failure. Preserve both texts and unwrap the
		// cleanup error, which is the actionable infrastructure failure.
		return fmt.Errorf("%v; не удалось отменить открытую DSL-транзакцию: %w", runErr, cleanupErr)
	}
	return runErr
}

// rollbackDSLExecution is a best-effort panic/early-return backstop. Normal
// paths call finishDSLExecution synchronously before rendering or reading DB.
func rollbackDSLExecution(state *interpreter.TxState) {
	if state == nil || !state.HasOpen() {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(state.Ctx()), dslTransactionCleanupTimeout)
	defer cancel()
	_ = state.RollbackOpen(cleanupCtx)
}
