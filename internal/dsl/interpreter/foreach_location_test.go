package interpreter_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

type locationEntrypoint struct {
	name string
	run  func(*interpreter.Interpreter, *ast.ProcedureDecl, map[string]any) error
}

func locationEntrypoints() []locationEntrypoint {
	return []locationEntrypoint{
		{"Run", func(i *interpreter.Interpreter, p *ast.ProcedureDecl, vars map[string]any) error {
			return i.Run(p, nil, vars)
		}},
		{"RunWithResult", func(i *interpreter.Interpreter, p *ast.ProcedureDecl, vars map[string]any) error {
			var result any
			return i.RunWithResult(p, nil, &result, vars)
		}},
		{"Call", func(i *interpreter.Interpreter, p *ast.ProcedureDecl, vars map[string]any) error {
			_, err := i.Call(p, nil, nil, vars)
			return err
		}},
		{"RunSandboxed", func(i *interpreter.Interpreter, p *ast.ProcedureDecl, vars map[string]any) error {
			var result any
			return i.RunSandboxed(p, nil, interpreter.RestrictedProfile(), &result, vars)
		}},
		{"CallSandboxed", func(i *interpreter.Interpreter, p *ast.ProcedureDecl, vars map[string]any) error {
			_, err := i.CallSandboxed(p, nil, nil, interpreter.RestrictedProfile(), vars)
			return err
		}},
		{"CallEntrySandboxedWithBindings", func(i *interpreter.Interpreter, p *ast.ProcedureDecl, vars map[string]any) error {
			_, err := i.CallEntrySandboxedWithBindings(p, nil, nil, interpreter.RestrictedProfile(), vars)
			return err
		}},
	}
}

func parseLocationProgram(t *testing.T, file, source string) *ast.Program {
	t.Helper()
	program, err := parser.New(lexer.New(source, file)).ParseProgram()
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func locationInterpreter(t *testing.T, strict bool) *interpreter.Interpreter {
	t.Helper()
	module := parseLocationProgram(t, "числа.module.os", `Функция Число()
  Возврат 17;
КонецФункции

Процедура Провал()
  Для Каждого Элемент Из Модуль.Число() Цикл
  КонецЦикла;
КонецПроцедуры`)
	i := interpreter.New()
	i.StrictLexicalScope = strict
	i.LookupModuleProc = func(moduleName, name string) *ast.ProcedureDecl {
		if strings.EqualFold(moduleName, "Модуль") {
			for _, proc := range module.Procedures {
				if strings.EqualFold(name, proc.Name.Literal) {
					return proc
				}
			}
		}
		return nil
	}
	return i
}

func TestForEach_ErrorLocationAcrossEntrypoints(t *testing.T) {
	cases := []struct {
		name, body, file string
		line             int
	}{
		{"literal", "  Для Каждого Элемент Из 17 Цикл\n  КонецЦикла;", "цикл.proc.os", 2},
		{"module-result", "  Для Каждого Элемент Из Модуль.Число() Цикл\n  КонецЦикла;", "цикл.proc.os", 2},
		{"nested-call", "  Модуль.Провал();", "числа.module.os", 6},
		{"rethrow", "  Попытка\n    Модуль.Провал();\n  Исключение\n    Модуль.Число();\n    ВызватьИсключение();\n  КонецПопытки;", "числа.module.os", 6},
	}
	for _, entry := range locationEntrypoints() {
		for _, strict := range []bool{false, true} {
			mode := "legacy"
			if strict {
				mode = "lexical"
			}
			for _, tc := range cases {
				t.Run(entry.name+"/"+mode+"/"+tc.name, func(t *testing.T) {
					program := parseLocationProgram(t, "цикл.proc.os", "Функция Выполнить()\n"+tc.body+"\nКонецФункции")
					err := entry.run(locationInterpreter(t, strict), program.Procedures[0], nil)
					var dslErr *interpreter.DSLError
					if !errors.As(err, &dslErr) {
						t.Fatalf("expected DSLError, got %v", err)
					}
					if dslErr.File != tc.file || dslErr.Line != tc.line {
						t.Fatalf("location = %s:%d, want %s:%d", dslErr.File, dslErr.Line, tc.file, tc.line)
					}
					if dslErr.Msg != "Цикл «Для Каждого»: значение не является коллекцией" {
						t.Fatalf("message = %q", dslErr.Msg)
					}
				})
			}
		}
	}
}

func TestForEach_ErrorInfoKeepsLoopLocation(t *testing.T) {
	for _, entry := range locationEntrypoints() {
		for _, expression := range []string{"17", "Модуль.Число()"} {
			t.Run(entry.name+"/"+expression, func(t *testing.T) {
				program := parseLocationProgram(t, "пойманный.proc.os", `Функция Выполнить()
  Попытка
    Для Каждого Элемент Из `+expression+` Цикл
    КонецЦикла;
  Исключение
    СохранитьИнфо(ИнформацияОбОшибке());
  КонецПопытки;
КонецФункции`)
				var info *interpreter.Struct
				vars := map[string]any{"СохранитьИнфо": interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
					info, _ = args[0].(*interpreter.Struct)
					return nil, nil
				})}
				if err := entry.run(locationInterpreter(t, false), program.Procedures[0], vars); err != nil {
					t.Fatal(err)
				}
				if info == nil || info.Get("Источник") != "пойманный.proc.os" || info.Get("НомерСтроки") != float64(3) {
					t.Fatalf("unexpected error info: %#v", info)
				}
				if info.Get("Описание") != "Цикл «Для Каждого»: значение не является коллекцией" {
					t.Fatalf("description = %v", info.Get("Описание"))
				}
			})
		}
	}
}

func TestUserError_EntrypointsKeepFallbackAndCause(t *testing.T) {
	for _, entry := range locationEntrypoints() {
		t.Run(entry.name, func(t *testing.T) {
			program := parseLocationProgram(t, "внешний.proc.os", "Процедура Выполнить()\n  Сбой();\nКонецПроцедуры")
			cause := errors.New("original error")
			vars := map[string]any{"Сбой": interpreter.BuiltinFunc(func(args []any, file string, line int) (any, error) {
				interpreter.RaiseUserErrorWrap("сбой", cause)
				return nil, nil
			})}
			err := entry.run(interpreter.New(), program.Procedures[0], vars)
			var dslErr *interpreter.DSLError
			if !errors.As(err, &dslErr) || dslErr.File != "внешний.proc.os" || dslErr.Line != 2 || dslErr.Msg != "сбой" {
				t.Fatalf("unexpected error: %v", err)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("original error lost: %v", err)
			}
		})
	}
}
