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

func TestLexer_LeadingBOM(t *testing.T) {
	// Файлы, выгруженные из 1С или сохранённые внешними редакторами, часто
	// начинаются с BOM (U+FEFF). Лексер должен его проглотить, иначе первый
	// токен ломается с "expected Procedure or Function".
	input := "\uFEFFProcedure Выполнить()\nEndProcedure"
	l := lexer.New(input, "bom.os")

	tok := l.NextToken()
	if tok.Type != token.PROCEDURE {
		t.Fatalf("first token after BOM: want PROCEDURE, got %v (literal=%q)", tok.Type, tok.Literal)
	}
	// Позиция первого токена не должна сдвинуться из-за BOM.
	if tok.Line != 1 || tok.Col != 1 {
		t.Fatalf("want line=1 col=1, got line=%d col=%d", tok.Line, tok.Col)
	}
}

// Строки с директивами препроцессора 1С (#Область и т.п.) пропускаются как
// комментарии — спасает копипаст кода из 1С (issue #48 п.2).
func TestSkipsPreprocessorLines(t *testing.T) {
	src := "#Область Сервис\nПроцедура П()\nКонецПроцедуры\n#КонецОбласти\n"
	l := lexer.New(src, "t.os")
	tok := l.NextToken()
	if tok.Type != token.LookupIdent("Процедура") || tok.Literal != "Процедура" {
		t.Fatalf("первый токен: %+v, ожидалась Процедура", tok)
	}
	for tok.Type != token.EOF {
		if tok.Type == token.ILLEGAL {
			t.Fatalf("ILLEGAL токен: %+v", tok)
		}
		tok = l.NextToken()
	}
}

func TestLexer_MultilineStringPipe(t *testing.T) {
	// 1C-style: '|' at start of continuation line is stripped
	input := "\"ВЫБРАТЬ\n|  Номер,\n|  Дата\n|ИЗ Документ.Заявка\""
	l := lexer.New(input, "test.os")
	tok := l.NextToken()
	if tok.Type != token.STRING {
		t.Fatalf("want STRING, got %v", tok.Type)
	}
	want := "ВЫБРАТЬ\n  Номер,\n  Дата\nИЗ Документ.Заявка"
	if tok.Literal != want {
		t.Fatalf("want %q, got %q", want, tok.Literal)
	}
}

func TestLexer_DateLiterals(t *testing.T) {
	l := lexer.New("'00010101' '20260819153045'", "dates.os")
	for i, want := range []string{"00010101", "20260819153045"} {
		tok := l.NextToken()
		if tok.Type != token.DATE || tok.Literal != want {
			t.Fatalf("token[%d] = %v(%q), want DATE(%q)", i, tok.Type, tok.Literal, want)
		}
	}
	if tok := l.NextToken(); tok.Type != token.EOF {
		t.Fatalf("last token = %v(%q), want EOF", tok.Type, tok.Literal)
	}
}

func TestLexer_InvalidDateLiteralSyntaxStaysIllegal(t *testing.T) {
	for _, src := range []string{
		"''",
		"'202601'",
		"'2026-01-01'",
		"'20260101x'",
		"'20260101",
		"'20260101\nСледующаяСтрока",
	} {
		t.Run(src, func(t *testing.T) {
			tok := lexer.New(src, "bad-date.os").NextToken()
			if tok.Type != token.ILLEGAL {
				t.Fatalf("first token = %v(%q), want ILLEGAL", tok.Type, tok.Literal)
			}
		})
	}
}
