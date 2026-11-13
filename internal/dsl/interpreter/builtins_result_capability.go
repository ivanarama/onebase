package interpreter

func init() {
	// A bare/headless/ordinary processor cannot acquire a result capability.
	deny := BuiltinFunc(func(_ []any, _ string, _ int) (any, error) { return false, nil })
	builtins["возможностьформырезультата"] = deny
	builtins["resultformcapability"] = deny
}
