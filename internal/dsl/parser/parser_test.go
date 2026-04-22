package parser_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func parse(t *testing.T, src string) *ast.Program {
	t.Helper()
	l := lexer.New(src, "test.os")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return prog
}

func TestParser_OnWriteProcedure(t *testing.T) {
	src := `Procedure OnWrite()
  If this.Number = "" Then
    Error("Number is required");
  EndIf;
EndProcedure`

	prog := parse(t, src)
	if len(prog.Procedures) != 1 {
		t.Fatalf("want 1 procedure, got %d", len(prog.Procedures))
	}
	proc := prog.Procedures[0]
	if proc.Name.Literal != "OnWrite" {
		t.Fatalf("want OnWrite, got %q", proc.Name.Literal)
	}
	if len(proc.Body) != 1 {
		t.Fatalf("want 1 stmt in body, got %d", len(proc.Body))
	}
	ifStmt, ok := proc.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatal("want IfStmt")
	}
	cond, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok {
		t.Fatal("condition should be BinaryExpr")
	}
	member, ok := cond.Left.(*ast.MemberExpr)
	if !ok {
		t.Fatal("left should be MemberExpr")
	}
	if member.Field.Literal != "Number" {
		t.Fatalf("want Number, got %q", member.Field.Literal)
	}
	if len(ifStmt.Then) != 1 {
		t.Fatalf("want 1 then stmt, got %d", len(ifStmt.Then))
	}
	exprStmt, ok := ifStmt.Then[0].(*ast.ExprStmt)
	if !ok {
		t.Fatal("then stmt should be ExprStmt")
	}
	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok {
		t.Fatal("then expr should be CallExpr")
	}
	ident, ok := call.Callee.(*ast.Ident)
	if !ok {
		t.Fatal("callee should be Ident")
	}
	if ident.Tok.Literal != "Error" {
		t.Fatalf("want Error, got %q", ident.Tok.Literal)
	}
}

func TestParser_AssignStmt(t *testing.T) {
	src := `Procedure SetNum()
  Var x;
  x = "hello";
EndProcedure`
	prog := parse(t, src)
	proc := prog.Procedures[0]
	if len(proc.Body) != 2 {
		t.Fatalf("want 2 stmts, got %d", len(proc.Body))
	}
	assign, ok := proc.Body[1].(*ast.AssignStmt)
	if !ok {
		t.Fatal("second stmt should be AssignStmt")
	}
	id, ok := assign.Target.(*ast.Ident)
	if !ok || id.Tok.Literal != "x" {
		t.Fatal("assign target should be Ident x")
	}
}

func TestParser_SyntaxError(t *testing.T) {
	src := `Procedure Broken(
EndProcedure`
	l := lexer.New(src, "bad.os")
	p := parser.New(l)
	_, err := p.ParseProgram()
	if err == nil {
		t.Fatal("expected parse error")
	}
}
