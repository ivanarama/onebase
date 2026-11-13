package interpreter

import "fmt"

// The UI host replaces this refusal only for a validated processor command.
// Object hooks, background jobs and ordinary evaluation never acquire a context.
func init() {
	blocked := BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return nil, fmt.Errorf("КонтекстФормыРезультата доступен только в команде текущей формы результата")
	})
	builtins["контекстформырезультата"] = blocked
	builtins["resultformcontext"] = blocked
}
