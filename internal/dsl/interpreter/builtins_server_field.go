package interpreter

func init() {
	// Only the native OnWrite execution edge may inject a scoped positive view.
	deny := func([]any, string, int) (any, error) { return false, nil }
	builtins["поленазначеносервером"] = deny
	builtins["serverassignedfield"] = deny
}
