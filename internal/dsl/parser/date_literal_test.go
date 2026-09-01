package parser_test

import (
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func TestParser_DateLiteral(t *testing.T) {
	tests := []struct {
		src  string
		want time.Time
	}{
		{"'00010101'", time.Time{}},
		{"'00010101000000'", time.Time{}},
		{"'20260228'", time.Date(2026, 2, 28, 0, 0, 0, 0, time.Local)},
		{"'20260228142359'", time.Date(2026, 2, 28, 14, 23, 59, 0, time.Local)},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			expr, err := parser.New(lexer.New(tc.src, "date.os")).ParseStandaloneExpr()
			if err != nil {
				t.Fatal(err)
			}
			lit, ok := expr.(*ast.DateLit)
			if !ok {
				t.Fatalf("expression = %T, want *ast.DateLit", expr)
			}
			if !lit.Value.Equal(tc.want) || lit.Value.IsZero() != tc.want.IsZero() {
				t.Fatalf("value = %v, want %v", lit.Value, tc.want)
			}
		})
	}
}

func TestParser_DateLiteralRejectsInvalidCalendarValue(t *testing.T) {
	for _, src := range []string{
		"'00000101'",
		"'20260229'",
		"'20261301'",
		"'20260101240000'",
		"'20260101125960'",
	} {
		t.Run(src, func(t *testing.T) {
			_, err := parser.New(lexer.New(src, "bad-date.os")).ParseStandaloneExpr()
			if err == nil {
				t.Fatalf("invalid date literal %s was accepted", src)
			}
		})
	}
}
