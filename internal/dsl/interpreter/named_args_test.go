package interpreter_test

import (
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Параметр обработки, объявленный в сигнатуре Выполнить, обязан приходить
// заполненным (issue #706).
//
// Параметры инжектировались только как переменные, а объявленный параметр
// процедуры с тем же именем затенял инжектированный и приходил пустым. Ошибки
// не было: обработка отрабатывала «успешно», просто со значением по умолчанию,
// и выглядело это как «--set не биндится».

func declOf(t *testing.T, src string) *ast.ProcedureDecl {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "t.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return prog.Procedures[0]
}

func TestBindNamedArgs_ОбъявленныйПараметрПолучаетЗначение(t *testing.T) {
	decl := declOf(t, `Функция Выполнить(ModelName = "")
		Возврат ModelName;
	КонецФункции`)

	args := interpreter.BindNamedArgs(decl, map[string]any{"ModelName": "gemma"})
	in := interpreter.New()
	res, err := in.Call(decl, runtime.NewObject("T", metadata.KindCatalog), args)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "gemma" {
		t.Fatalf("параметр пришёл как %#v, ждали \"gemma\"", res)
	}
}

// Регистр имени не должен мешать: в YAML и в модуле его пишут по-разному.
func TestBindNamedArgs_ИмяСверяетсяБезУчётаРегистра(t *testing.T) {
	decl := declOf(t, `Функция Выполнить(modelname = "")
		Возврат modelname;
	КонецФункции`)

	args := interpreter.BindNamedArgs(decl, map[string]any{"ModelName": "gemma"})
	in := interpreter.New()
	res, _ := in.Call(decl, runtime.NewObject("T", metadata.KindCatalog), args)
	if res != "gemma" {
		t.Fatalf("параметр пришёл как %#v", res)
	}
}

// Параметру, которому нет одноимённого значения, ничего не передаётся — иначе
// явный nil затёр бы его значение по умолчанию.
func TestBindNamedArgs_ЗначенияПоУмолчаниюНеЗатираются(t *testing.T) {
	decl := declOf(t, `Функция Выполнить(Первый = "", Второй = "по умолчанию")
		Возврат Первый + "/" + Второй;
	КонецФункции`)

	args := interpreter.BindNamedArgs(decl, map[string]any{"Первый": "задан"})
	if len(args) != 1 {
		t.Fatalf("аргументов %d, ждали 1 (второй должен взять умолчание)", len(args))
	}
	in := interpreter.New()
	res, err := in.Call(decl, runtime.NewObject("T", metadata.KindCatalog), args)
	if err != nil {
		t.Fatal(err)
	}
	if res != "задан/по умолчанию" {
		t.Fatalf("получено %#v", res)
	}
}

// Разреженная сигнатура: отсутствие первого metadata-параметра не должно
// мешать передать второй по имени и не должно затирать DSL-default первого.
func TestBindNamedArgs_РазреженныйВторойПараметрНеЗатираетDefaultПервого(t *testing.T) {
	decl := declOf(t, `Функция Выполнить(Первый = "по умолчанию", Второй = "")
		Возврат Первый + "/" + Второй;
	КонецФункции`)

	args := interpreter.BindNamedArgs(decl, map[string]any{"Второй": "задан"})
	if len(args) != 2 {
		t.Fatalf("аргументов %d, ждали sparse-список до второго formal", len(args))
	}
	res, err := interpreter.New().Call(decl, runtime.NewObject("T", metadata.KindCatalog), args)
	if err != nil {
		t.Fatal(err)
	}
	if res != "по умолчанию/задан" {
		t.Fatalf("получено %#v", res)
	}
}

// Явный nil — значение, а не отсутствие: он обязан затереть DSL-default.
func TestBindNamedArgs_ЯвныйNilОтличаетсяОтОтсутствующего(t *testing.T) {
	decl := declOf(t, `Функция Выполнить(Первый = "по умолчанию")
		Возврат Первый;
	КонецФункции`)
	args := interpreter.BindNamedArgs(decl, map[string]any{"Первый": nil})
	res, err := interpreter.New().Call(decl, runtime.NewObject("T", metadata.KindCatalog), args)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("явный nil заменён default: %#v", res)
	}
}

// Если первый же параметр не совпал, не передаётся ничего: поведение остаётся
// прежним, и процедура с посторонней сигнатурой не получает чужих значений
// по позиции.
func TestBindNamedArgs_ЧужаяСигнатураНеПолучаетЗначенийПоПозиции(t *testing.T) {
	decl := declOf(t, `Функция Выполнить(Постороннее = "цело")
		Возврат Постороннее;
	КонецФункции`)

	if args := interpreter.BindNamedArgs(decl, map[string]any{"ModelName": "gemma"}); args != nil {
		t.Fatalf("переданы аргументы по позиции: %#v", args)
	}
}
