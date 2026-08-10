package parser_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func parseProc(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return prog
}

// «ВызватьИсключение Выражение;» — оператор 1С без скобок. Раньше парсер видел
// два выражения подряд, и check сообщал «выражение без эффекта» о рабочем коде.
func TestParse_ВызватьИсключениеСВыражением(t *testing.T) {
	prog := parseProc(t, `Процедура Тест()
		ВызватьИсключение ОписаниеОшибки();
	КонецПроцедуры`)
	body := prog.Procedures[0].Body
	if len(body) != 1 {
		t.Fatalf("операторов в теле: %d, ожидался 1", len(body))
	}
	es, ok := body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("тип оператора %T, ожидался ExprStmt", body[0])
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		t.Fatalf("выражение %T, ожидался CallExpr", es.X)
	}
	if len(call.Args) != 1 {
		t.Errorf("аргументов %d, ожидался 1", len(call.Args))
	}
	if id, ok := call.Callee.(*ast.Ident); !ok || !strings.EqualFold(id.Tok.Literal, "ВызватьИсключение") {
		t.Errorf("вызывается не ВызватьИсключение: %+v", call.Callee)
	}
}

// Без аргумента — «перебросить текущее исключение».
func TestParse_ВызватьИсключениеБезАргумента(t *testing.T) {
	prog := parseProc(t, `Процедура Тест()
		Попытка
			Возврат 1;
		Исключение
			ВызватьИсключение;
		КонецПопытки;
	КонецПроцедуры`)
	if len(prog.Procedures) != 1 {
		t.Fatalf("процедур: %d", len(prog.Procedures))
	}
}

// Форма со скобками должна работать как и раньше.
func TestParse_ВызватьИсключениеСоСкобками(t *testing.T) {
	prog := parseProc(t, `Процедура Тест()
		ВызватьИсключение("сломалось");
	КонецПроцедуры`)
	body := prog.Procedures[0].Body
	es, ok := body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("тип оператора %T", body[0])
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("ожидался вызов с одним аргументом: %+v", es.X)
	}
}
