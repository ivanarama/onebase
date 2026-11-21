package interpreter_test

import (
	"fmt"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func TestExplicitExceptionOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		explicit         bool
	}{
		{"raise", `ВызватьИсключение("SQL access denied");`, "SQL access denied", true},
		{"error", `Error("business");`, "business", true},
		{"bare-rethrow", `Попытка Raise("business"); Исключение Raise; КонецПопытки;`, "business", true},
		{"technical", `x = Число("not-a-number");`, "", false},
		{"technical-rethrow", `Попытка x = Число("not-a-number"); Исключение Raise; КонецПопытки;`, "", false},
		{"replacement", `Попытка x = Число("bad"); Исключение Raise("business"); КонецПопытки;`, "business", true},
		{"caught-does-not-taint", `Попытка Raise("caught"); Исключение x = 1; КонецПопытки; x = Число("bad");`, "", false},
		{"bare-outside-catch", `Raise;`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parser.New(lexer.New("Процедура Test()\n"+tc.body+"\nКонецПроцедуры", "/private/module.os")).ParseProgram()
			if err != nil {
				t.Fatal(err)
			}
			in := interpreter.New()
			err = in.Run(p.Procedures[0], nil)
			if err == nil {
				t.Fatal("expected execution error")
			}
			for _, wrapped := range []error{err, fmt.Errorf("technical wrapper: %w", err), &interpreter.DSLError{Msg: "technical outer", Err: err}} {
				got, explicit := interpreter.ExplicitExceptionMessage(wrapped)
				if got != tc.want || explicit != tc.explicit {
					t.Fatalf("message=%q explicit=%v; want %q %v (%v)", got, explicit, tc.want, tc.explicit, err)
				}
			}
			if interpreter.FormatUserError(err) == "" {
				t.Fatal("ordinary error formatter changed")
			}
		})
	}
	if _, ok := interpreter.ExplicitExceptionMessage(&interpreter.DSLError{Msg: "business"}); ok {
		t.Fatal("unmarked error accepted")
	}
}
