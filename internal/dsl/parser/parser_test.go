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

func TestParser_StandaloneExpressionConsumesAllInput(t *testing.T) {
	for _, src := range []string{"Сч = 4", "Сч = 4;", "(Сч + 1) > 4 // comment"} {
		p := parser.New(lexer.New(src, "condition.os"))
		if _, err := p.ParseStandaloneExpr(); err != nil {
			t.Errorf("ParseStandaloneExpr(%q): %v", src, err)
		}
	}

	for _, src := range []string{"Сч = 4; Лишнее", "Сч = 4 Лишнее", "Сч = 4;;"} {
		p := parser.New(lexer.New(src, "condition.os"))
		if _, err := p.ParseStandaloneExpr(); err == nil {
			t.Errorf("ParseStandaloneExpr(%q) accepted a trailing token", src)
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

func TestParser_KeywordAsMemberName(t *testing.T) {
	// issue #117: имя свойства/метода после точки может совпадать с
	// зарезервированным словом (XDTO-объект с полями «To»/«По»). В позиции члена
	// это обычные идентификаторы, и синтаксис-контроль не должен на них падать.
	src := `Процедура Тест()
  Запрос.To = КонецПериода;
  Рез = Запрос.По;
  Объект.Если("x");
КонецПроцедуры`

	prog := parse(t, src)
	body := prog.Procedures[0].Body
	if len(body) != 3 {
		t.Fatalf("want 3 stmts, got %d", len(body))
	}

	// Запрос.To = … — присваивание в свойство-ключевое-слово (англ.)
	assign, ok := body[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("stmt 0: want *ast.AssignStmt, got %T", body[0])
	}
	tgt, ok := assign.Target.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("stmt 0 target: want *ast.MemberExpr, got %T", assign.Target)
	}
	if tgt.Field.Literal != "To" {
		t.Fatalf("stmt 0 field: want %q, got %q", "To", tgt.Field.Literal)
	}

	// Рез = Запрос.По — чтение свойства с русским ключевым словом
	read, ok := body[1].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("stmt 1: want *ast.AssignStmt, got %T", body[1])
	}
	m, ok := read.Value.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("stmt 1 value: want *ast.MemberExpr, got %T", read.Value)
	}
	if m.Field.Literal != "По" {
		t.Fatalf("stmt 1 field: want %q, got %q", "По", m.Field.Literal)
	}

	// Объект.Если("x") — вызов метода с именем-ключевым-словом
	es, ok := body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt 2: want *ast.ExprStmt, got %T", body[2])
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		t.Fatalf("stmt 2: want *ast.CallExpr, got %T", es.X)
	}
	callee, ok := call.Callee.(*ast.MemberExpr)
	if !ok {
		t.Fatalf("stmt 2 callee: want *ast.MemberExpr, got %T", call.Callee)
	}
	if callee.Field.Literal != "Если" {
		t.Fatalf("stmt 2 field: want %q, got %q", "Если", callee.Field.Literal)
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

// Парсер должен выставлять ProcedureDecl.Export для процедур/функций с
// модификатором «Экспорт» (англ. «Export») и оставлять его false для приватных
// (внутренних) — на этом строится экспорт-гейт действий страниц (ревью #10).
func TestParser_ExportFlag(t *testing.T) {
	cases := []struct {
		src        string
		wantExport bool
	}{
		{"Процедура P() Экспорт\n  Возврат;\nКонецПроцедуры", true},
		{"Функция F(С) Export\n  Возврат С;\nКонецФункции", true},
		{"Процедура P()\n  Возврат;\nКонецПроцедуры", false},
		{"Функция F()\n  Возврат 1;\nКонецФункции", false},
	}
	for _, c := range cases {
		prog := parse(t, c.src)
		if len(prog.Procedures) != 1 {
			t.Fatalf("ожидалась 1 процедура (src=%q)", c.src)
		}
		if got := prog.Procedures[0].Export; got != c.wantExport {
			t.Errorf("Export = %v, ожидалось %v (src=%q)", got, c.wantExport, c.src)
		}
	}
}

// issue #128 → #171: оператор вне процедуры (исполняемый раздел модуля) раньше
// давал жёсткую ошибку «тело модуля не поддерживается». Теперь парсер его
// принимает и кладёт в Program.Body (а решение о допустимости тела по типу
// модуля принимает загрузчик). Регрессия: старая ошибка #128 больше не возникает.
func TestParser_StatementOutsideProcedure_Issue128(t *testing.T) {
	src := "Процедура Выполнить()\n  Сообщить(\"Привет!\")\nКонецПроцедуры\nф=6;\n"
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	if err != nil {
		t.Fatalf("оператор вне процедуры больше не должен быть ошибкой парсера, получили: %v", err)
	}
	if len(prog.Procedures) != 1 {
		t.Fatalf("ожидали 1 процедуру, получили %d", len(prog.Procedures))
	}
	if len(prog.Body) != 1 {
		t.Fatalf("ожидали 1 оператор в теле модуля (ф=6), получили %d", len(prog.Body))
	}
}

// issue #171: операторы вне процедур (исполняемый раздел / тело модуля) больше
// НЕ отвергаются парсером — они собираются в Program.Body. Допустимость тела
// (только в обработках) проверяет загрузчик по типу модуля, а не парсер.
func TestParser_ModuleBody_Issue171(t *testing.T) {
	src := "Перем Итог;\nИтог = 2 + 3;\nСообщить(Итог);\n"
	prog := parse(t, src)
	if len(prog.ModuleVars) != 1 {
		t.Fatalf("ожидали 1 переменную модуля, получили %d", len(prog.ModuleVars))
	}
	if len(prog.Body) != 2 {
		t.Fatalf("ожидали 2 оператора в теле модуля, получили %d", len(prog.Body))
	}
	if _, ok := prog.Body[0].(*ast.AssignStmt); !ok {
		t.Fatalf("body[0]: ожидали AssignStmt, получили %T", prog.Body[0])
	}
	if len(prog.Procedures) != 0 {
		t.Fatalf("ожидали 0 процедур, получили %d", len(prog.Procedures))
	}
}

// Тело модуля может идти и после процедур (как «раздел основной программы» 1С):
// процедуры → в Procedures, операторы → в Body.
func TestParser_ModuleBodyAfterProcedures_Issue171(t *testing.T) {
	src := "Процедура Готово()\nКонецПроцедуры\nЗапустить();\n"
	prog := parse(t, src)
	if len(prog.Procedures) != 1 {
		t.Fatalf("ожидали 1 процедуру, получили %d", len(prog.Procedures))
	}
	if len(prog.Body) != 1 {
		t.Fatalf("ожидали 1 оператор тела, получили %d", len(prog.Body))
	}
}
