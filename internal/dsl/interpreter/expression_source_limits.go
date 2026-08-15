package interpreter

import (
	"fmt"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

// These limits keep recursive expression parsing bounded for text supplied at
// runtime. They are exported so API/UI validation and report formulas can use
// exactly the same pre-parse gate as the interpreter.
const (
	MaxUntrustedExpressionBytes       = 128 << 10
	MaxUntrustedExpressionTokens      = 1024
	MaxUntrustedExpressionSyntaxDepth = 128
)

// ValidateUntrustedExpressionSource performs only iterative work. It must run
// before ParseStandaloneExpr/ParseProgram, whose recursive descent cannot
// consult a sandbox deadline while parsing deeply nested input.
func ValidateUntrustedExpressionSource(expr string) error {
	if len(expr) > MaxUntrustedExpressionBytes {
		return fmt.Errorf("expression source exceeds the %d byte limit", MaxUntrustedExpressionBytes)
	}
	l := lexer.New(expr, "<bounded-expression>")
	depth := 0
	for count := 0; ; count++ {
		tok := l.NextToken()
		if tok.Type == token.EOF {
			return nil
		}
		if count >= MaxUntrustedExpressionTokens {
			return fmt.Errorf("expression source exceeds the %d token limit", MaxUntrustedExpressionTokens)
		}
		switch tok.Type {
		case token.LPAREN, token.LBRACKET:
			depth++
			if depth > MaxUntrustedExpressionSyntaxDepth {
				return fmt.Errorf("expression source exceeds the maximum syntax depth %d", MaxUntrustedExpressionSyntaxDepth)
			}
		case token.RPAREN, token.RBRACKET:
			if depth > 0 {
				depth--
			}
		}
	}
}
