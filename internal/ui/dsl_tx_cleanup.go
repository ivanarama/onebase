package ui

import (
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
)

var errDSLTransactionLeftOpen = interpreter.ErrTransactionLeftOpen

// finishDSLExecution is the transaction boundary for request/offline DSL
// execution. Cleanup is deliberately detached from execution cancellation so a
// timeout cannot strand a database transaction; it is independently bounded.
func finishDSLExecution(state *interpreter.TxState, runErr error) error {
	return interpreter.FinishTxExecution(state, runErr)
}

// rollbackDSLExecution is a best-effort panic/early-return backstop. Normal
// paths call finishDSLExecution synchronously before rendering or reading DB.
func rollbackDSLExecution(state *interpreter.TxState) {
	interpreter.RollbackTxExecution(state)
}
