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

func runSandboxEntry(t *testing.T, entry string, proc *ast.ProcedureDecl, p interpreter.SandboxProfile, extra map[string]any) (any, error) {
	t.Helper()
	in := interpreter.New()
	if entry == "RunSandboxed" {
		var result any
		err := in.RunSandboxed(proc, nil, p, &result, extra)
		return result, err
	}
	return in.CallSandboxed(proc, nil, nil, p, extra)
}

type sandboxTrustedObject struct {
	value string
}

func (o *sandboxTrustedObject) CallMethod(_ string, _ []any) any { return o.value }

func sandboxBypassVars(p interpreter.SandboxProfile) map[string]any {
	allowed := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return "ОТКРЫТО", nil
	})
	factory := func(_ []any) any { return &sandboxTrustedObject{value: "ОТКРЫТО"} }
	extra := map[string]any{"РазрешеннаяФункция": allowed}
	for name := range p.Vars() {
		if strings.HasPrefix(strings.ToLower(name), "__factory_") {
			extra[name] = factory
		} else {
			extra[name] = allowed
		}
	}
	return extra
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

// Пути временного каталога — часть файловой capability: строгий профиль не
// должен ни раскрывать путь хоста, ни выдавать имя для последующей записи.
func TestSandbox_TempPathsDeniedCatchable(t *testing.T) {
	for _, call := range []string{"КаталогВременныхФайлов()", `ПолучитьИмяВременногоФайла("txt")`} {
		src := `Процедура Тест()
		Попытка
			Значение = ` + call + `;
			Возврат "без ошибки: " + Строка(Значение);
		Исключение
			Возврат ОписаниеОшибки();
		КонецПопытки;
	КонецПроцедуры`
		var result any
		err := interpreter.New().RunSandboxed(parseProc(t, src), nil, interpreter.RestrictedProfile(), &result)
		require.NoError(t, err)
		assert.Contains(t, result.(string), "файловые операции запрещены", call)
	}
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

// Запреты должны оставаться приоритетными не только при первичном merge
// extraVars, но и после присваивания/Перем из самого DSL. Матрица проходит оба
// sandbox-entrypoint, русские/английские имена и фабрики с/без Новый.
func TestSandbox_DenyOverlayCannotBeShadowed(t *testing.T) {
	p := interpreter.RestrictedProfile()
	tests := []struct {
		name       string
		moduleDecl string
		setup      string
		call       string
		want       string
	}{
		{name: "temp RU", call: `КаталогВременныхФайлов()`, want: "файловые операции запрещены"},
		{name: "temp EN", call: `GetTempFileName()`, want: "файловые операции запрещены"},
		{name: "net RU", call: `HTTPПолучить("http://127.0.0.1:1")`, want: "сеть запрещена"},
		{name: "net EN", call: `HTTPGet("http://127.0.0.1:1")`, want: "сеть запрещена"},
		{name: "safe net RU", call: `HTTPПолучитьБезопасно("http://8.8.8.8", "8.8.8.8")`, want: "сеть запрещена"},
		{name: "safe net EN", call: `HTTPSafeGet("http://8.8.8.8", "8.8.8.8")`, want: "сеть запрещена"},
		{name: "exec RU", call: `ВыполнитьКоманду("sandbox-must-not-run")`, want: "выполнение команд ОС запрещено"},
		{name: "exec EN", call: `ExecuteCommand("sandbox-must-not-run")`, want: "выполнение команд ОС запрещено"},
		{name: "LLM RU", call: `ЗапросИИ("привет")`, want: "ИИ-запросы запрещены"},
		{name: "LLM EN", call: `AIQuery("hello")`, want: "ИИ-запросы запрещены"},
		{
			name:  "assignment cannot reopen",
			setup: `HTTPПолучить = РазрешеннаяФункция;`,
			call:  `HTTPПолучить("http://127.0.0.1:1")`,
			want:  "сеть запрещена",
		},
		{
			name:       "module and local declaration cannot reopen",
			moduleDecl: "Перем AIQuery;\n",
			setup:      "Перем AIQuery;\nAIQuery = РазрешеннаяФункция;",
			call:       `AIQuery("hello")`,
			want:       "ИИ-запросы запрещены",
		},
		{name: "direct factory", call: `ЧтениеТекста("ignored.txt")`, want: "файловые операции запрещены"},
		{name: "new factory", call: `Новый TextReader("ignored.txt")`, want: "файловые операции запрещены"},
	}

	for _, entry := range []string{"RunSandboxed", "CallSandboxed"} {
		for _, tc := range tests {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				src := tc.moduleDecl + `Функция Тест()
					` + tc.setup + `
					Попытка
						Значение = ` + tc.call + `;
						Возврат "ОТКРЫТО";
					Исключение
						Возврат ОписаниеОшибки();
					КонецПопытки;
				КонецФункции`
				result, err := runSandboxEntry(t, entry, parseProc(t, src), p, sandboxBypassVars(p))
				require.NoError(t, err)
				assert.Contains(t, result, tc.want)
				assert.NotEqual(t, "ОТКРЫТО", result)
			})
		}
	}
}

func TestSandbox_TempDenialCarriesSourceLocation(t *testing.T) {
	profiles := []struct {
		name string
		p    interpreter.SandboxProfile
	}{
		{name: "Restricted", p: interpreter.RestrictedProfile()},
		{name: "DenyFile", p: interpreter.SandboxProfile{DenyFile: true}},
	}
	calls := []string{
		"КаталогВременныхФайлов()",
		"TempFilesDir()",
		"ПолучитьИмяВременногоФайла()",
		"GetTempFileName()",
	}

	for _, entry := range []string{"RunSandboxed", "CallSandboxed"} {
		for _, profile := range profiles {
			for _, call := range calls {
				t.Run(entry+"/"+profile.name+"/"+call, func(t *testing.T) {
					src := `Функция Тест()
						Попытка
							` + call + `;
							Возврат "не поймано";
						Исключение
							Возврат ИнформацияОбОшибке();
						КонецПопытки;
					КонецФункции`
					result, err := runSandboxEntry(t, entry, parseProc(t, src), profile.p, nil)
					require.NoError(t, err)
					info, ok := result.(*interpreter.Struct)
					require.True(t, ok, "ожидалась ИнформацияОбОшибке, получено %#v", result)
					assert.Equal(t, "test.os", info.Get("Источник"))
					assert.Greater(t, info.Get("НомерСтроки").(float64), float64(0))
					assert.Contains(t, info.Get("Описание"), "файловые операции запрещены")
				})
			}
		}
	}
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

func TestSandbox_ZeroProfileHasNoOverlay(t *testing.T) {
	allowed := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return "shadow works", nil
	})
	extra := map[string]any{"HTTPGet": allowed}
	proc := parseProc(t, `Функция Тест()
		Возврат HTTPGet("ignored");
	КонецФункции`)
	for _, entry := range []string{"RunSandboxed", "CallSandboxed"} {
		t.Run(entry, func(t *testing.T) {
			result, err := runSandboxEntry(t, entry, proc, interpreter.SandboxProfile{}, extra)
			require.NoError(t, err)
			assert.Equal(t, "shadow works", result)
		})
	}
}

func TestSandbox_SleepShadowingUnaffectedByOverlay(t *testing.T) {
	allowed := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return "custom sleep", nil
	})
	proc := parseProc(t, `Функция Тест()
		Возврат Sleep(999);
	КонецФункции`)
	for _, entry := range []string{"RunSandboxed", "CallSandboxed"} {
		t.Run(entry, func(t *testing.T) {
			result, err := runSandboxEntry(t, entry, proc, interpreter.RestrictedProfile(), map[string]any{"Sleep": allowed})
			require.NoError(t, err)
			assert.Equal(t, "custom sleep", result)
		})
	}
}

func TestSandbox_NonSandboxShadowingUnaffected(t *testing.T) {
	allowed := interpreter.BuiltinFunc(func(_ []any, _ string, _ int) (any, error) {
		return "ordinary shadow", nil
	})
	extra := map[string]any{"HTTPПолучить": allowed}
	proc := parseProc(t, `Функция Тест()
		Возврат HTTPПолучить("ignored");
	КонецФункции`)
	in := interpreter.New()
	_, sandboxErr := in.CallSandboxed(proc, nil, nil, interpreter.RestrictedProfile(), extra)
	require.Error(t, sandboxErr, "restricted overlay должен сработать в предыдущем execCtx")

	// Тот же Interpreter начинает обычные Run/Call с новым execCtx: overlay
	// sandbox-вызова не протекает между исполнениями.
	var runResult any
	require.NoError(t, in.RunWithResult(proc, nil, &runResult, extra))
	assert.Equal(t, "ordinary shadow", runResult)
	callResult, err := in.Call(proc, nil, nil, extra)
	require.NoError(t, err)
	assert.Equal(t, "ordinary shadow", callResult)
}

// Overlay защищает только известные глобальные capability-имена. Уже готовый
// объект с возможностями под произвольным именем — явная граница доверия
// вызывающего Go-кода, а не то, что SandboxProfile может классифицировать.
func TestSandbox_ArbitraryInjectedObjectRemainsTrustedCallerBoundary(t *testing.T) {
	proc := parseProc(t, `Функция Тест()
		Возврат ГотовыйОбъект.Вызвать();
	КонецФункции`)
	extra := map[string]any{"ГотовыйОбъект": &sandboxTrustedObject{value: "trusted object"}}
	for _, entry := range []string{"RunSandboxed", "CallSandboxed"} {
		t.Run(entry, func(t *testing.T) {
			result, err := runSandboxEntry(t, entry, proc, interpreter.RestrictedProfile(), extra)
			require.NoError(t, err)
			assert.Equal(t, "trusted object", result)
		})
	}
}
