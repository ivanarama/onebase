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

// Вычислить видит процедуры собственного файла (issue #692). Диспетчеризация
// «вызвать обработчик по имени» — основной сценарий Вычислить, и до фикса она
// падала `unknown function`, если обработчики лежали рядом в
// `<обработка>.proc.os`: выражение разбиралось под синтетическим именем файла
// «<Вычислить>», и sibling-резолв не находил ничего. Перенос тех же процедур в
// общий модуль внезапно всё чинил — поведение выглядело случайным.
func TestEval_SeesSiblingProcedures(t *testing.T) {
	src := `Функция Обработчик_Привет(Имя)
  Возврат "привет, " + Имя;
КонецФункции

Функция Выполнить()
  Возврат Вычислить("Обработчик_Привет(""Иван"")");
КонецФункции`

	prog, err := parser.New(lexer.New(src, "приветствие.proc.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var entry, handler *ast.ProcedureDecl
	for _, p := range prog.Procedures {
		switch strings.ToLower(p.Name.Literal) {
		case "выполнить":
			entry = p
		case "обработчик_привет":
			handler = p
		}
	}
	if entry == nil || handler == nil {
		t.Fatal("процедуры не нашлись")
	}

	interp := interpreter.New()
	interp.LookupSiblingProc = func(file, name string) *ast.ProcedureDecl {
		if file == "приветствие.proc.os" && strings.EqualFold(name, "Обработчик_Привет") {
			return handler
		}
		return nil
	}
	var result any
	if err := interp.RunWithResult(entry, nil, &result); err != nil {
		t.Fatalf("выполнение: %v", err)
	}
	if result != "привет, Иван" {
		t.Errorf("Вычислить не дозвался до соседней процедуры: %v", result)
	}
}
