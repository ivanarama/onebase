package interpreter

import "fmt"

// DSLError is returned by Error() built-in; stops execution and cancels Save.
type DSLError struct {
	File string
	Line int
	Msg  string
}

func (e *DSLError) Error() string {
	return fmt.Sprintf("%s:%d: Error: %s", e.File, e.Line, e.Msg)
}

var builtins = map[string]func(args []any, file string, line int) (any, error){
	"Error": func(args []any, file string, line int) (any, error) {
		msg := ""
		if len(args) > 0 {
			msg = fmt.Sprintf("%v", args[0])
		}
		return nil, &DSLError{File: file, Line: line, Msg: msg}
	},
}
