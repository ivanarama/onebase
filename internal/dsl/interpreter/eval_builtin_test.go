package interpreter_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

func TestEval_Arithmetic(t *testing.T) {
	src := `Функция Тест()
  Возврат Вычислить("2 + 3 * 4");
КонецФункции`
	if got := runFunc(t, src); !numEq(got, 14) {
		t.Fatalf("Вычислить арифметики: ожидалось 14, got %v", got)
	}
}

// Вычислить видит локальные переменные текущего окружения.
func TestEval_LocalVar(t *testing.T) {
	src := `Функция Тест()
  х = 5;
  Возврат Вычислить("х * 2");
КонецФункции`
	if got := runFunc(t, src); !numEq(got, 10) {
		t.Fatalf("Вычислить с локальной переменной: ожидалось 10, got %v", got)
	}
}

// Вычислить получает lexical identity вызывающего модуля, но сохраняет
// синтетическое диагностическое имя. Благодаря этому соседняя процедура
// находится по настоящему .form.os, а ошибка в динамической строке не лжёт,
// будто выражение написано на первой строке физического файла.
func TestEval_UsesCallerScopeAndSyntheticDiagnostics(t *testing.T) {
	const sourceFile = `forms/заказ/объекта.form.os`
	prog, err := parser.New(lexer.New(`
Функция Локальная()
	Возврат "форма";
КонецФункции

Функция Успех()
	Возврат Вычислить("Локальная()");
КонецФункции

Функция Ошибка()
	Возврат Вычислить("НетТакой()");
КонецФункции
`, sourceFile)).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	byName := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
	for _, proc := range prog.Procedures {
		byName[strings.ToLower(proc.Name.Literal)] = proc
	}

	interp := interpreter.New()
	var resolvedFile string
	interp.LookupSiblingProc = func(file, name string) *ast.ProcedureDecl {
		resolvedFile = file
		if file == sourceFile {
			return byName[strings.ToLower(name)]
		}
		return nil
	}
	var result any
	if err := interp.RunWithResult(byName["успех"], nil, &result); err != nil {
		t.Fatalf("Вычислить локальной процедуры: %v", err)
	}
	if result != "форма" {
		t.Fatalf("result=%v, want форма", result)
	}
	if resolvedFile != sourceFile {
		t.Fatalf("scope identity=%q, want %q", resolvedFile, sourceFile)
	}

	err = interp.RunWithResult(byName["ошибка"], nil, &result)
	if err == nil {
		t.Fatal("ожидалась ошибка неизвестной функции в Вычислить")
	}
	if !strings.Contains(err.Error(), "<Вычислить>:1") {
		t.Fatalf("динамическое выражение потеряло синтетическую диагностику: %v", err)
	}
	if strings.Contains(err.Error(), sourceFile+":1") {
		t.Fatalf("ошибка ложно указывает на line 1 физического модуля: %v", err)
	}
}

// Прямая рекурсия Вычислить не создаёт кадр процедуры, поэтому обычный
// MaxRecursionDepth её не видит. Отдельный per-run guard должен остановить
// самовоспроизводящееся выражение до переполнения стека Go.
func TestEval_DepthGuard(t *testing.T) {
	prog, err := parser.New(lexer.New(`
Функция Тест()
	Текст = "Вычислить(Текст)";
	Возврат Вычислить(Текст);
КонецФункции
`, "eval-depth.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	interp := interpreter.New()
	interp.MaxEvalDepth = 8
	var result any
	err = interp.RunWithResult(prog.Procedures[0], nil, &result)
	if err == nil {
		t.Fatal("ожидалась ошибка глубины Вычислить")
	}
	if !strings.Contains(err.Error(), "глубина Вычислить") {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
}
