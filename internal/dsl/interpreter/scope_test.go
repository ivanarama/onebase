package interpreter

import (
	"strings"
	"testing"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
)

// runScopeFunc parses a function and returns its result. Stays in the
// `interpreter` package so we can access the unexported Run helpers.
func runScopeFunc(t *testing.T, code string) any {
	t.Helper()
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Procedures) == 0 {
		t.Fatal("no procedures parsed")
	}
	i := New()
	this := &MapThis{M: map[string]any{}}
	var result any
	if err := i.RunWithResult(prog.Procedures[0], this, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	return result
}

// Регрессия для замечания #22: присваивание в ветви Если/Иначе должно
// обновлять переменную внешнего scope, а не создавать локальную.
func TestIfElseScope_ThenAssignmentPropagates(t *testing.T) {
	code := `Функция Тест()
  Действие = "?";
  Если 1 = 1 Тогда
    Действие = "Обновлён";
  Иначе
    Действие = "Создан";
  КонецЕсли;
  Возврат Действие;
КонецФункции`
	if got := runScopeFunc(t, code); got != "Обновлён" {
		t.Errorf("expected \"Обновлён\", got %v", got)
	}
}

func TestIfElseScope_ElseAssignmentPropagates(t *testing.T) {
	code := `Функция Тест()
  Действие = "?";
  Если 1 = 2 Тогда
    Действие = "Обновлён";
  Иначе
    Действие = "Создан";
  КонецЕсли;
  Возврат Действие;
КонецФункции`
	if got := runScopeFunc(t, code); got != "Создан" {
		t.Errorf("expected \"Создан\", got %v", got)
	}
}

// Та же история, но переменная объявлена внутри Для-цикла — тоже частый
// сценарий (Эл из коллекции, действие в зависимости от него).
func TestIfElseScope_InsideForEach(t *testing.T) {
	code := `Функция Тест()
  М = Новый Массив;
  М.Добавить(1);
  Лог = "";
  Для Каждого Э Из М Цикл
    Действие = "?";
    Если Э = 1 Тогда
      Действие = "один";
    Иначе
      Действие = "другой";
    КонецЕсли;
    Лог = Лог + Действие;
  КонецЦикла;
  Возврат Лог;
КонецФункции`
	if got := runScopeFunc(t, code); got != "один" {
		t.Errorf("expected \"один\", got %v", got)
	}
}

// параметры по умолчанию.
func TestDefaultParam_UsedWhenOmitted(t *testing.T) {
	code := `Функция Тест()
  Возврат Сумма(10);
КонецФункции

Функция Сумма(А, Б = 20)
  Возврат А + Б;
КонецФункции`
	// Параллельная процедура должна быть доступна через LookupSiblingProc,
	// но для теста проще — вызов внутри одного файла обходится без него:
	// callUserProc находит helper через i.LookupProc. Здесь нет реестра,
	// но parser кладёт обе процедуры в один Program — а RunWithResult
	// исполняет только первую. Сделаем helper через LookupSiblingProc.
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Procedures) != 2 {
		t.Fatalf("ожидалось 2 процедуры, получили %d", len(prog.Procedures))
	}
	main, helper := prog.Procedures[0], prog.Procedures[1]
	i := New()
	i.LookupSiblingProc = func(file, name string) *ast.ProcedureDecl {
		if name == "Сумма" || name == "сумма" {
			return helper
		}
		return nil
	}
	var result any
	if err := i.RunWithResult(main, &MapThis{M: map[string]any{}}, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !numEq(result, 30) {
		t.Errorf("expected 30 (10+20 default), got %v", result)
	}
}

func TestDefaultParam_OverriddenByArg(t *testing.T) {
	code := `Функция Тест()
  Возврат Сумма(10, 5);
КонецФункции

Функция Сумма(А, Б = 20)
  Возврат А + Б;
КонецФункции`
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, _ := p.ParseProgram()
	main, helper := prog.Procedures[0], prog.Procedures[1]
	i := New()
	i.LookupSiblingProc = func(_, name string) *ast.ProcedureDecl {
		if name == "Сумма" || name == "сумма" {
			return helper
		}
		return nil
	}
	var result any
	if err := i.RunWithResult(main, &MapThis{M: map[string]any{}}, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !numEq(result, 15) {
		t.Errorf("expected 15, got %v", result)
	}
}

func TestDefaultParam_StringDefault(t *testing.T) {
	code := `Функция Тест()
  Возврат Привет();
КонецФункции

Функция Привет(Имя = "мир")
  Возврат "Hello, " + Имя;
КонецФункции`
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, _ := p.ParseProgram()
	main, helper := prog.Procedures[0], prog.Procedures[1]
	i := New()
	i.LookupSiblingProc = func(_, name string) *ast.ProcedureDecl {
		if name == "Привет" || name == "привет" {
			return helper
		}
		return nil
	}
	var result any
	if err := i.RunWithResult(main, &MapThis{M: map[string]any{}}, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	if result != "Hello, мир" {
		t.Errorf("expected Hello, мир, got %v", result)
	}
}

func TestFunctionParamDoesNotOverwriteCallerVariable(t *testing.T) {
	code := `Функция Тест()
  Значение = "caller";
  Результат = Помощник("arg");
  Возврат Значение + ":" + Результат;
КонецФункции

Функция Помощник(Значение)
  Значение = "local";
  Возврат Значение;
КонецФункции`
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, _ := p.ParseProgram()
	main, helper := prog.Procedures[0], prog.Procedures[1]
	i := New()
	i.LookupSiblingProc = func(_, name string) *ast.ProcedureDecl {
		if name == "Помощник" || name == "помощник" {
			return helper
		}
		return nil
	}
	var result any
	if err := i.RunWithResult(main, &MapThis{M: map[string]any{}}, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	if result != "caller:local" {
		t.Errorf("expected caller:local, got %v", result)
	}
}

func TestFunctionLocalDoesNotOverwriteCallerCollection(t *testing.T) {
	code := `Функция Тест()
  Данные = Новый Массив;
  Данные.Добавить(1);
  Помощник();
  Возврат Данные.Количество();
КонецФункции

Процедура Помощник()
  Данные = Новый Массив;
  Данные.Добавить(2);
КонецПроцедуры`
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, _ := p.ParseProgram()
	main, helper := prog.Procedures[0], prog.Procedures[1]
	i := New()
	i.LookupSiblingProc = func(_, name string) *ast.ProcedureDecl {
		if name == "Помощник" || name == "помощник" {
			return helper
		}
		return nil
	}
	var result any
	if err := i.RunWithResult(main, &MapThis{M: map[string]any{}}, &result); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !numEq(result, 1) {
		t.Errorf("expected caller collection count 1, got %v", result)
	}
}

// ИначеЕсли тоже должен корректно писать во внешний scope.
func TestIfElseScope_ElseIfAssignmentPropagates(t *testing.T) {
	code := `Функция Тест()
  Действие = "?";
  Если 1 = 2 Тогда
    Действие = "первая";
  ИначеЕсли 1 = 1 Тогда
    Действие = "вторая";
  Иначе
    Действие = "третья";
  КонецЕсли;
  Возврат Действие;
КонецФункции`
	if got := runScopeFunc(t, code); got != "вторая" {
		t.Errorf("expected \"вторая\", got %v", got)
	}
}

// П.39: переменная, ВПЕРВЫЕ присвоенная внутри блока (без предобъявления),
// должна быть видна после блока — scope = процедура, как в 1С.

func TestScope_FirstAssignInIfVisibleOutside(t *testing.T) {
	code := `Функция Тест()
  Если 1 = 1 Тогда
    Результат = 42;
  КонецЕсли;
  Возврат Результат;
КонецФункции`
	if got := runScopeFunc(t, code); !numEq(got, 42) {
		t.Errorf("expected 42, got %v", got)
	}
}

func TestScope_FirstAssignInElseVisibleOutside(t *testing.T) {
	code := `Функция Тест()
  Если 1 = 2 Тогда
    Результат = 1;
  Иначе
    Результат = 99;
  КонецЕсли;
  Возврат Результат;
КонецФункции`
	if got := runScopeFunc(t, code); !numEq(got, 99) {
		t.Errorf("expected 99, got %v", got)
	}
}

func TestScope_FirstAssignInForEachVisibleOutside(t *testing.T) {
	code := `Функция Тест()
  М = Новый Массив;
  М.Добавить(3);
  М.Добавить(4);
  Для Каждого Э Из М Цикл
    Сумма = ?(Сумма = Неопределено, 0, Сумма) + Э;
  КонецЦикла;
  Возврат Сумма;
КонецФункции`
	if got := runScopeFunc(t, code); !numEq(got, 7) {
		t.Errorf("expected 7, got %v", got)
	}
}

func TestScope_FirstAssignInNumericForVisibleOutside(t *testing.T) {
	code := `Функция Тест()
  Для Сч = 1 По 5 Цикл
    Итог = Сч;
  КонецЦикла;
  Возврат Итог;
КонецФункции`
	if got := runScopeFunc(t, code); !numEq(got, 5) {
		t.Errorf("expected 5, got %v", got)
	}
}

func TestScope_FirstAssignInWhileVisibleOutside(t *testing.T) {
	code := `Функция Тест()
  Сч = 0;
  Пока Сч < 3 Цикл
    Сч = Сч + 1;
    Последний = Сч;
  КонецЦикла;
  Возврат Последний;
КонецФункции`
	if got := runScopeFunc(t, code); !numEq(got, 3) {
		t.Errorf("expected 3, got %v", got)
	}
}

func TestScope_FirstAssignInExceptVisibleOutside(t *testing.T) {
	code := `Функция Тест()
  Попытка
    Error("сбой");
  Исключение
    Результат = "поймано";
  КонецПопытки;
  Возврат Результат;
КонецФункции`
	if got := runScopeFunc(t, code); got != "поймано" {
		t.Errorf("expected \"поймано\", got %v", got)
	}
}

func runScopeProgramResult(t *testing.T, code string, strict bool, extra map[string]any) (any, error) {
	t.Helper()
	l := lexer.New(code, "<test>")
	p := parser.New(l)
	prog, err := p.ParseProgram()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(prog.Procedures) == 0 {
		t.Fatal("no procedures parsed")
	}
	procs := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
	for _, pr := range prog.Procedures {
		procs[strings.ToLower(pr.Name.Literal)] = pr
	}
	i := New()
	i.StrictLexicalScope = strict
	i.LookupSiblingProc = func(_, name string) *ast.ProcedureDecl {
		return procs[strings.ToLower(name)]
	}
	var result any
	err = i.RunWithResult(prog.Procedures[0], &MapThis{M: map[string]any{}}, &result, extra)
	return result, err
}

func TestStrictLexicalScope_LegacyKeepsCallerLocalVisible(t *testing.T) {
	code := `Функция Тест()
  Секрет = "caller";
  Возврат Помощник();
КонецФункции

Функция Помощник()
  Возврат ?(Секрет = Неопределено, "hidden", Секрет);
КонецФункции`
	got, err := runScopeProgramResult(t, code, false, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "caller" {
		t.Fatalf("legacy scope result = %v, want caller", got)
	}
}

func TestStrictLexicalScope_HidesCallerLocalFromHelper(t *testing.T) {
	code := `Функция Тест()
  Секрет = "caller";
  Возврат Помощник();
КонецФункции

Функция Помощник()
  Возврат ?(Секрет = Неопределено, "hidden", Секрет);
КонецФункции`
	got, err := runScopeProgramResult(t, code, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "hidden" {
		t.Fatalf("strict scope result = %v, want hidden", got)
	}
}

func TestStrictLexicalScope_HelperSeesRootExtraVars(t *testing.T) {
	code := `Функция Тест()
  Глобальная = "caller";
  Возврат Помощник();
КонецФункции

Функция Помощник()
  Возврат Глобальная;
КонецФункции`
	got, err := runScopeProgramResult(t, code, true, map[string]any{"Глобальная": "root"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "root" {
		t.Fatalf("strict helper global = %v, want root", got)
	}
}

func TestStrictLexicalScope_DefaultParamDoesNotReadCallerLocal(t *testing.T) {
	code := `Функция Тест()
  Секрет = "caller";
  Возврат Помощник();
КонецФункции

Функция Помощник(Значение = Секрет)
  Возврат ?(Значение = Неопределено, "hidden", Значение);
КонецФункции`
	got, err := runScopeProgramResult(t, code, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "hidden" {
		t.Fatalf("strict default param result = %v, want hidden", got)
	}
}

func TestStrictLexicalScope_ModuleVarSharedWithHelper(t *testing.T) {
	code := `Перем Счетчик;

Функция Тест()
  Счетчик = 1;
  Помощник();
  Возврат Счетчик;
КонецФункции

Процедура Помощник()
  Счетчик = Счетчик + 1;
КонецПроцедуры`
	got, err := runScopeProgramResult(t, code, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !numEq(got, 2) {
		t.Fatalf("strict module var result = %v, want 2", got)
	}
}

func TestStrictLexicalScope_ModuleVarDoesNotExposeCallerLocal(t *testing.T) {
	code := `Перем Общая;

Функция Тест()
  Общая = "module";
  Секрет = "caller";
  Возврат Помощник();
КонецФункции

Функция Помощник()
  Возврат Общая + ":" + ?(Секрет = Неопределено, "hidden", Секрет);
КонецФункции`
	got, err := runScopeProgramResult(t, code, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "module:hidden" {
		t.Fatalf("strict module/caller scope result = %v, want module:hidden", got)
	}
}

func TestStrictLexicalScope_LocalVarShadowsModuleVar(t *testing.T) {
	code := `Перем Значение;

Функция Тест()
  Перем Значение;
  Значение = "local";
  ЗаписатьМодуль();
  Возврат Значение + ":" + ПрочитатьМодуль();
КонецФункции

Процедура ЗаписатьМодуль()
  Значение = "module";
КонецПроцедуры

Функция ПрочитатьМодуль()
  Возврат Значение;
КонецФункции`
	got, err := runScopeProgramResult(t, code, true, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "local:module" {
		t.Fatalf("strict local/module shadow result = %v, want local:module", got)
	}
}

// Default AST принадлежит вызываемой процедуре B, хотя для legacy-совместимости
// его переменные вычисляются в scope caller A. Резолв процедур обязан идти по
// файлу B; то же правило действует для вложенного synthetic Вычислить.
func TestDefaultExpression_UsesCalleeSourceAndCallerVariables(t *testing.T) {
	const callerSource = `
Функция Помощник(Значение)
	Возврат "A:" + Значение;
КонецФункции

Функция Тест()
	Значение = "caller";
	Возврат Цель();
КонецФункции`

	for _, tc := range []struct {
		name        string
		defaultExpr string
	}{
		{name: "обычный AST", defaultExpr: "Помощник(Значение)"},
		{name: "Вычислить", defaultExpr: `Вычислить("Помощник(Значение)")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, err := parser.New(lexer.New(callerSource, "caller-a.os")).ParseProgram()
			if err != nil {
				t.Fatalf("parse caller: %v", err)
			}
			calleeSource := `
Функция Помощник(Значение)
	Возврат "B:" + Значение;
КонецФункции

Функция Цель(Параметр = ` + tc.defaultExpr + `)
	Возврат Параметр;
КонецФункции`
			callee, err := parser.New(lexer.New(calleeSource, "callee-b.os")).ParseProgram()
			if err != nil {
				t.Fatalf("parse callee: %v", err)
			}

			all := append(append([]*ast.ProcedureDecl{}, caller.Procedures...), callee.Procedures...)
			find := func(file, name string) *ast.ProcedureDecl {
				for _, proc := range all {
					if proc.Name.File == file && strings.EqualFold(proc.Name.Literal, name) {
						return proc
					}
				}
				return nil
			}
			in := New()
			in.LookupSiblingProc = find
			in.LookupProc = func(name string) *ast.ProcedureDecl {
				return find("callee-b.os", name)
			}

			var result any
			if err := in.RunWithResult(find("caller-a.os", "Тест"), nil, &result); err != nil {
				t.Fatalf("run: %v", err)
			}
			if result != "B:caller" {
				t.Fatalf("default разрешился в scope caller: %v", result)
			}
		})
	}
}
