package lexer_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/token"
)

func TestLexer_OnWrite(t *testing.T) {
	input := `Procedure OnWrite()
  If this.Number = "" Then
    Error("Number is required");
  EndIf;
EndProcedure`

	expected := []struct {
		typ token.Type
		lit string
	}{
		{token.PROCEDURE, "Procedure"},
		{token.IDENT, "OnWrite"},
		{token.LPAREN, "("},
		{token.RPAREN, ")"},
		{token.IF, "If"},
		{token.IDENT, "this"},
		{token.DOT, "."},
		{token.IDENT, "Number"},
		{token.ASSIGN, "="},
		{token.STRING, ""},
		{token.THEN, "Then"},
		{token.IDENT, "Error"},
		{token.LPAREN, "("},
		{token.STRING, "Number is required"},
		{token.RPAREN, ")"},
		{token.SEMICOLON, ";"},
		{token.ENDIF, "EndIf"},
		{token.SEMICOLON, ";"},
		{token.ENDPROCEDURE, "EndProcedure"},
		{token.EOF, ""},
	}

	l := lexer.New(input, "test.os")
	for i, want := range expected {
		got := l.NextToken()
		if got.Type != want.typ {
			t.Fatalf("token[%d]: type want %v, got %v (literal=%q)", i, want.typ, got.Type, got.Literal)
		}
		if want.lit != "" && got.Literal != want.lit {
			t.Fatalf("token[%d]: literal want %q, got %q", i, want.lit, got.Literal)
		}
	}
}

func TestLexer_Positions(t *testing.T) {
	l := lexer.New("If\nEndIf", "pos.os")
	tok := l.NextToken()
	if tok.Line != 1 || tok.Col != 1 {
		t.Fatalf("want line=1 col=1, got line=%d col=%d", tok.Line, tok.Col)
	}
	tok = l.NextToken()
	if tok.Line != 2 || tok.Col != 1 {
		t.Fatalf("want line=2 col=1, got line=%d col=%d", tok.Line, tok.Col)
	}
}

func TestLexer_Operators(t *testing.T) {
	input := "<> <= >= < >"
	l := lexer.New(input, "ops.os")
	cases := []token.Type{token.NEQ, token.LTE, token.GTE, token.LT, token.GT, token.EOF}
	for i, want := range cases {
		got := l.NextToken()
		if got.Type != want {
			t.Fatalf("op[%d]: want %v, got %v", i, want, got.Type)
		}
	}
}
