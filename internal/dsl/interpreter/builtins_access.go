package interpreter

// The authenticated host supplies the effective permission predicate. Bare
// evaluation and hosts without an actor deny instead of guessing permissions.
func init() {
	deny := BuiltinFunc(func(_ []any, _ string, _ int) (any, error) { return false, nil })
	builtins["естьправодоступа"] = deny
	builtins["hasaccessright"] = deny
}
