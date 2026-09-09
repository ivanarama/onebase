package interpreter_test

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/runtime"
)

// Пропуск аргумента («Метод(А,,Б)») означает «значение не передано», а не
// «передано Неопределено»: у параметра со значением по умолчанию берётся
// умолчание — как в 1С (issue #1160). Разницу между двумя случаями и проверяют
// тесты ниже: подстановка литерала Неопределено вместо пропуска прошла бы
// парсер, но сломала бы ровно тот сценарий, ради которого пропуск пишут.

// runWithSibling исполняет первую процедуру модуля, разрешая вызовы остальных
// процедур того же файла — тот же путь, которым идёт вызов помощника в модуле
// конфигурации.
func runWithSibling(t *testing.T, src string, obj *runtime.Object) {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.proc.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Procedures) < 2 {
		t.Fatalf("ждали точку входа и помощника, получили %d процедур", len(prog.Procedures))
	}
	interp := interpreter.New()
	interp.LookupSiblingProc = func(file, name string) *ast.ProcedureDecl {
		if file != "test.proc.os" {
			return nil
		}
		for _, proc := range prog.Procedures[1:] {
			if strings.EqualFold(proc.Name.Literal, name) {
				return proc
			}
		}
		return nil
	}
	if err := interp.Run(prog.Procedures[0], obj); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestИнтерпретатор_ПропускБерётЗначениеПоУмолчанию(t *testing.T) {
	src := `Процедура Выполнить()
  Помощник(1,, 3);
  Помощник(9,);
КонецПроцедуры

Процедура Помощник(Первый, Второй = 10, Третий = 20)
  ЭтотОбъект.Результат = "" + ЭтотОбъект.Результат + Первый + "/" + Второй + "/" + Третий + ";";
КонецПроцедуры`

	obj := runtime.NewObject("Тест", metadata.KindDocument)
	obj.Set("Результат", "")
	runWithSibling(t, src, obj)

	if got := obj.Get("Результат"); got != "1/10/3;9/10/20;" {
		t.Fatalf("получили %q, ждали \"1/10/3;9/10/20;\"", got)
	}
}

func TestИнтерпретатор_ПропускБезУмолчанияДаётНеопределено(t *testing.T) {
	src := `Процедура Выполнить()
  Помощник(1,, 3);
КонецПроцедуры

Процедура Помощник(Первый, Второй, Третий)
  ЭтотОбъект.Пусто = (Второй = Неопределено);
  ЭтотОбъект.Третий = Третий;
КонецПроцедуры`

	obj := runtime.NewObject("Тест", metadata.KindDocument)
	runWithSibling(t, src, obj)

	if obj.Get("Пусто") != true {
		t.Fatalf("параметр без умолчания пришёл как %#v, ждали Неопределено", obj.Get("Пусто"))
	}
	if !numEq(obj.Get("Третий"), 3) {
		t.Fatalf("третий параметр %#v, ждали 3 — пропуск не должен сдвигать позиции", obj.Get("Третий"))
	}
}

func TestИнтерпретатор_ЯвноеНеопределеноНеБерётУмолчание(t *testing.T) {
	// Обратная сторона правила и главная причина, по которой пропуск — узел AST,
	// а не литерал: переданное явно Неопределено обязано остаться Неопределено.
	src := `Процедура Выполнить()
  Помощник(1, Неопределено);
КонецПроцедуры

Процедура Помощник(Первый, Второй = 10)
  ЭтотОбъект.Второй = Второй;
  ЭтотОбъект.Пусто = (Второй = Неопределено);
КонецПроцедуры`

	obj := runtime.NewObject("Тест", metadata.KindDocument)
	runWithSibling(t, src, obj)

	if obj.Get("Пусто") != true {
		t.Fatalf("явное Неопределено подменилось умолчанием: Второй = %#v", obj.Get("Второй"))
	}
}

func TestИнтерпретатор_ПропускВоВстроеннойФункцииЭтоНеопределено(t *testing.T) {
	// Инвариант границы: sentinel пропуска не выходит за callUserProc. Внешняя
	// функция обязана увидеть nil (Неопределено), а не внутренний тип
	// интерпретатора — иначе он утёк бы в builtins и свалил бы вызов.
	src := `Процедура Выполнить()
  ВнешняяФункция(1,, 3);
КонецПроцедуры`

	prog, err := parser.New(lexer.New(src, "test.proc.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got []any
	interp := interpreter.New()
	obj := runtime.NewObject("Тест", metadata.KindDocument)
	err = interp.Run(prog.Procedures[0], obj, map[string]any{
		"ВнешняяФункция": interpreter.BuiltinFunc(func(args []any, _ string, _ int) (any, error) {
			got = args
			return nil, nil
		}),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("аргументов %d, ждали 3 — пропуск занимает позицию", len(got))
	}
	if got[1] != nil {
		t.Fatalf("на месте пропуска %#v (%T), ждали nil (Неопределено)", got[1], got[1])
	}
}

func TestИнтерпретатор_ПропускВМетодеИКонструктореЭтоНеопределено(t *testing.T) {
	// Тот же инвариант для двух других приёмников аргументов: метода объекта и
	// «Новый Тип(…)». Если бы sentinel утёк, в массиве и структуре лежало бы
	// значение внутреннего типа, и сравнение с Неопределено дало бы Ложь.
	src := `Процедура Выполнить()
  М = Новый Массив;
  М.Вставить(0,);
  ЭтотОбъект.ЭлементПуст = (М[0] = Неопределено);
  ЭтотОбъект.Количество = М.Количество();

  С = Новый Структура("А,Б", 1,);
  ЭтотОбъект.ПолеПусто = (С.Б = Неопределено);
КонецПроцедуры`

	prog, err := parser.New(lexer.New(src, "test.proc.os")).ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	obj := runtime.NewObject("Тест", metadata.KindDocument)
	if err := interpreter.New().Run(prog.Procedures[0], obj); err != nil {
		t.Fatalf("run: %v", err)
	}
	if obj.Get("ЭлементПуст") != true {
		t.Errorf("Массив.Вставить(0,): элемент не Неопределено")
	}
	if !numEq(obj.Get("Количество"), 1) {
		t.Errorf("в массиве %v элементов, ждали 1", obj.Get("Количество"))
	}
	if obj.Get("ПолеПусто") != true {
		t.Errorf("Новый Структура(\"А,Б\", 1,): поле Б не Неопределено")
	}
}
