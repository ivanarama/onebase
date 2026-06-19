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

func TestParser_EmptyModule(t *testing.T) {
	// Пустой модуль обработки — норма (как в 1С): валидный модуль без процедур.
	for _, src := range []string{"", "   \n\t\n", "// только комментарий\n"} {
		prog := parse(t, src)
		if len(prog.Procedures) != 0 {
			t.Fatalf("empty module %q: want 0 procedures, got %d", src, len(prog.Procedures))
		}
	}
}

func TestParser_BOMOnlyModule(t *testing.T) {
	// Файл из одного BOM (часто встречается в выгрузках 1С) должен парситься
	// как пустой модуль, а не падать с "expected Procedure or Function".
	prog := parse(t, "\uFEFF")
	if len(prog.Procedures) != 0 {
		t.Fatalf("BOM-only module: want 0 procedures, got %d", len(prog.Procedures))
	}
}

func TestParser_VarDeclCommaList(t *testing.T) {
	// Перем а, б, в; — объявление нескольких переменных через запятую (как в 1С).
	src := `Процедура Тест()
  Перем а, б, в;
КонецПроцедуры`

	prog := parse(t, src)
	if len(prog.Procedures) != 1 {
		t.Fatalf("want 1 procedure, got %d", len(prog.Procedures))
	}
	body := prog.Procedures[0].Body
	if len(body) != 1 {
		t.Fatalf("want 1 stmt in body, got %d", len(body))
	}
	vd, ok := body[0].(*ast.VarDecl)
	if !ok {
		t.Fatalf("want *ast.VarDecl, got %T", body[0])
	}
	want := []string{"а", "б", "в"}
	if len(vd.Names) != len(want) {
		t.Fatalf("want %d names, got %d", len(want), len(vd.Names))
	}
	for i, n := range want {
		if vd.Names[i].Literal != n {
			t.Fatalf("name[%d]: want %q, got %q", i, n, vd.Names[i].Literal)
		}
	}
}

func TestParser_ModuleVarSection(t *testing.T) {
	// issue #115: модуль объекта обработки из выгрузки 1С начинается с раздела
	// объявления переменных модуля (Перем … [Экспорт];) до процедур. Парсер не
	// должен падать с «expected Procedure or Function, got "Перем"».
	src := `Перем НастройкиСервиса Экспорт;
Перем КэшТокена, ВремяОбновления;

Процедура ПолучитьТокен() Экспорт
КонецПроцедуры`

	prog := parse(t, src)
	if len(prog.ModuleVars) != 2 {
		t.Fatalf("want 2 module var decls, got %d", len(prog.ModuleVars))
	}
	if !prog.ModuleVars[0].Exported {
		t.Fatalf("first module var must be Exported (Перем … Экспорт)")
	}
	if got := prog.ModuleVars[0].Names[0].Literal; got != "НастройкиСервиса" {
		t.Fatalf("module var name: want НастройкиСервиса, got %q", got)
	}
	if n := len(prog.ModuleVars[1].Names); n != 2 {
		t.Fatalf("second decl: want 2 names, got %d", n)
	}
	if prog.ModuleVars[1].Exported {
		t.Fatalf("second module var must not be Exported")
	}
	if len(prog.Procedures) != 1 {
		t.Fatalf("want 1 procedure, got %d", len(prog.Procedures))
	}
}

func TestParser_ModuleVarsOnly(t *testing.T) {
	// Модуль из одних объявлений переменных, без процедур — тоже валиден.
	prog := parse(t, "Перем А;\nПерем Б;\n")
	if len(prog.ModuleVars) != 2 {
		t.Fatalf("want 2 module var decls, got %d", len(prog.ModuleVars))
	}
	if len(prog.Procedures) != 0 {
		t.Fatalf("want 0 procedures, got %d", len(prog.Procedures))
	}
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

// Модификатор «Экспорт» после сигнатуры должен поглощаться парсером, а не
// превращаться в фиктивное выражение-statement в начале тела.
func TestParser_ExportModifier(t *testing.T) {
	for _, src := range []string{
		"Функция F(С) Экспорт\n  Возврат С;\nКонецФункции",
		"Процедура P() Экспорт\n  Возврат;\nКонецПроцедуры",
	} {
		prog := parse(t, src)
		if len(prog.Procedures) != 1 {
			t.Fatalf("ожидалась 1 процедура, получено %d (src=%q)", len(prog.Procedures), src)
		}
		for _, st := range prog.Procedures[0].Body {
			if es, ok := st.(*ast.ExprStmt); ok {
				if id, ok := es.X.(*ast.Ident); ok && id.Tok.Literal == "Экспорт" {
					t.Fatalf("«Экспорт» осталась фиктивным выражением в теле (src=%q)", src)
				}
			}
		}
	}
}
