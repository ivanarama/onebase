package ui

import (
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/report/expreval"
)

// newInterpEvaluator строит evaluator условий/показателей компоновки. Раньше
// UI-путь исполнял выражения БЕЗ песочницы (обычный RunWithResult), поэтому
// формула с бесконечным циклом вешала хендлер навсегда. Теперь используется
// общий expreval.Evaluator с обязательным SandboxProfile — та же реализация,
// что и в REST-пути (issue #788).
func newInterpEvaluator(interp *interpreter.Interpreter) *expreval.Evaluator {
	return expreval.New(interp, expreval.DefaultProfile())
}
