package interpreter_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/interpreter"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseProc(t *testing.T, src string) *ast.ProcedureDecl {
	t.Helper()
	prog, err := parser.New(lexer.New(src, "test.os")).ParseProgram()
	require.NoError(t, err)
	require.NotEmpty(t, prog.Procedures)
	return prog.Procedures[0]
}

// Sandboxed entrypoints обязаны устанавливать ту же lexical source identity,
// что Run/Call: иначе synthetic Вычислить теряет sibling-процедуры файла.
func TestSandbox_EvalKeepsEntrySourceIdentity(t *testing.T) {
	prog, err := parser.New(lexer.New(`
Функция Помощник()
	Возврат "локальная";
КонецФункции

Функция Тест()
	Возврат Вычислить("Помощник()");
КонецФункции
`, "sandbox-local.os")).ParseProgram()
	require.NoError(t, err)
	byName := make(map[string]*ast.ProcedureDecl, len(prog.Procedures))
	for _, proc := range prog.Procedures {
		byName[strings.ToLower(proc.Name.Literal)] = proc
	}

	for _, tc := range []struct {
		name string
		run  func(*interpreter.Interpreter) (any, error)
	}{
		{
			name: "RunSandboxed",
			run: func(in *interpreter.Interpreter) (any, error) {
				var result any
				err := in.RunSandboxed(byName["тест"], nil, interpreter.SandboxProfile{}, &result)
				return result, err
			},
		},
		{
			name: "CallSandboxed",
			run: func(in *interpreter.Interpreter) (any, error) {
				return in.CallSandboxed(byName["тест"], nil, nil, interpreter.SandboxProfile{})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := interpreter.New()
			in.LookupSiblingProc = func(file, name string) *ast.ProcedureDecl {
				if file == "sandbox-local.os" {
					return byName[strings.ToLower(name)]
				}
				return nil
			}
			result, err := tc.run(in)
			require.NoError(t, err)
			assert.Equal(t, "локальная", result)
		})
	}
}

// Бесконечный цикл с пустым телом останавливается по wall-clock,
// и Попытка НЕ перехватывает жёсткий стоп.
func TestSandbox_WallClockHardStop(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    Пока Истина Цикл
    КонецЦикла;
  Исключение
    Возврат "поймано";
  КонецПопытки;
  Возврат "вышли";
КонецПроцедуры`
	p := interpreter.SandboxProfile{MaxWallClock: 50 * time.Millisecond}
	var result any
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result)
	require.Error(t, err)
	assert.NotEqual(t, "поймано", result)
	assert.NotEqual(t, "вышли", result)
}

// Цикл сверх MaxLoopIters останавливается жёстко, минуя Попытку.
func TestSandbox_LoopItersHardStop(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    н = 0;
    Пока н < 100000000 Цикл
      н = н + 1;
    КонецЦикла;
  Исключение
    Возврат "поймано";
  КонецПопытки;
  Возврат "вышли";
КонецПроцедуры`
	p := interpreter.SandboxProfile{MaxLoopIters: 1000}
	var result any
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result)
	require.Error(t, err)
	assert.NotEqual(t, "поймано", result)
	assert.NotEqual(t, "вышли", result)
}

// Без профиля (нулевые лимиты) обычный цикл отрабатывает и возвращает значение.
func TestSandbox_NoProfileNoRegression(t *testing.T) {
	src := `Процедура Тест()
  с = 0;
  Для к = 1 По 1000 Цикл
    с = с + к;
  КонецЦикла;
  Возврат с;
КонецПроцедуры`
	var result any
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.SandboxProfile{}, &result)
	require.NoError(t, err)
	// DSL числа возвращаются как decimal.Decimal — сравниваем через строку.
	assert.Equal(t, "500500", fmt.Sprintf("%v", result))
}

// Строгий профиль запрещает файлы; запрет ловится Попыткой (catchable).
func TestSandbox_FileDeniedCatchable(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    КопироватьФайл("a.txt", "b.txt");
    Возврат "без ошибки";
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	p := interpreter.RestrictedProfile()
	var result any
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result)
	require.NoError(t, err)
	assert.Contains(t, result.(string), "файловые операции запрещены")
}

// Строгий профиль запрещает сеть/почту; запрет ловится Попыткой.
func TestSandbox_NetDeniedCatchable(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    ОтправитьПисьмо("x@y.com", "тема", "текст");
    Возврат "без ошибки";
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	p := interpreter.RestrictedProfile()
	var result any
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result)
	require.NoError(t, err)
	assert.Contains(t, result.(string), "сеть запрещена")
}

// Строгий профиль запрещает ИИ-builtin'ы (сеть + чтение файла); ловится Попыткой.
func TestSandbox_LLMDeniedCatchable(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    ЗапросИИ("привет");
    Возврат "без ошибки";
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	p := interpreter.RestrictedProfile()
	var result any
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result)
	require.NoError(t, err)
	assert.Contains(t, result.(string), "запрещены")
}

// RunSandboxed навязывает запреты профиля сам — вызывающему НЕ нужно вручную
// передавать p.Vars(). Иначе забытый (или неверно упорядоченный) Vars() молча
// открыл бы песочницу: сеть/файлы/ИИ остались бы доступны недоверенному коду.
func TestSandbox_RestrictionsAppliedWithoutManualVars(t *testing.T) {
	src := `Процедура Тест()
  Попытка
    ЗапросИИ("привет");
    Возврат "без ошибки";
  Исключение
    Возврат ОписаниеОшибки();
  КонецПопытки;
КонецПроцедуры`
	p := interpreter.RestrictedProfile()
	var result any
	// БЕЗ p.Vars() в extraVars — запрет должен навязать сам RunSandboxed.
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result)
	require.NoError(t, err)
	assert.Contains(t, result.(string), "запрещены")
}

// Нулевой профиль = «всё разрешено» (см. docstring SandboxProfile): Vars() не
// должен внедрять никаких deny-guard'ов. Иначе RunSandboxed (применяет Vars()
// безусловно) запретил бы сеть/файлы/ИИ коду, для которого ограничения не заданы.
func TestSandbox_ZeroProfileRestrictsNothing(t *testing.T) {
	v := interpreter.SandboxProfile{}.Vars()
	if len(v) != 0 {
		t.Fatalf("нулевой профиль не должен ничего запрещать, получено %d guard'ов: %v", len(v), v)
	}
}
