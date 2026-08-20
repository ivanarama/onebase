# Песочница DSL (профиль ограничений на запуск) — Implementation Plan (план 57)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ввести профиль ограничений на отдельный запуск DSL (сеть/файлы/время/итерации), чтобы будущий сгенерированный ИИ код исполнялся «на поводке», не меняя поведения обычных обработок.

**Architecture:** Возможности (сеть/email/файлы) гейтятся через per-run guard'ы, внедряемые в extraVars (паттерн `NetGuard` из плана 62; `e.get` перекрывает глобальные builtins — `interpreter.go:619` раньше `:650`). Ресурсы (wall-clock, итерации) живут в `execCtx` (план 52) и проверяются в `execBlock`/циклах. Жёсткий таймаут — через `dslStop`, который `execTry` не перехватывает (`interpreter.go:836`).

**Tech Stack:** Go, пакет `internal/dsl/interpreter`; тесты на `testing` + `testify`.

**Дизайн:** [57-stage0-sandbox-profile.md](57-stage0-sandbox-profile.md). **Ветка:** `feature/57-sandbox-profile`.

---

## Структура файлов

- Создать: `internal/dsl/interpreter/sandbox.go` — `SandboxProfile`, `RestrictedProfile`, sentinel-ошибки, `RunSandboxed`, `SandboxProfile.Vars()`.
- Изменить: `internal/dsl/interpreter/file_builtins.go` — `FileGuard`, `checkFile`, `NewFileFunctions(guard)`.
- Изменить: `internal/dsl/interpreter/env.go` — поля `deadline`/`maxLoopIters` в `execCtx`, методы `checkDeadline`/`loopLimit`.
- Изменить: `internal/dsl/interpreter/interpreter.go` — чек дедлайна в `execBlock`; лимиты в `WhileStmt`/`NumericForStmt`.
- Изменить: `internal/dsl/interpreter/builtins.go:414`, `internal/dslvars/dslvars.go:71` — вызов `NewFileFunctions(nil)`.
- Создать: `internal/dsl/interpreter/sandbox_guard_test.go` (package `interpreter`) — guard файлов.
- Создать: `internal/dsl/interpreter/sandbox_test.go` (package `interpreter_test`) — ресурсы + профиль end-to-end.

---

## Task 1: FileGuard + гейт файловых builtins

**Files:**
- Modify: `internal/dsl/interpreter/file_builtins.go`
- Modify: `internal/dsl/interpreter/builtins.go:414`
- Modify: `internal/dslvars/dslvars.go:71`
- Test: `internal/dsl/interpreter/sandbox_guard_test.go` (создать, package `interpreter`)

- [ ] **Step 1: Написать падающий тест**

Создать `internal/dsl/interpreter/sandbox_guard_test.go`:

```go
package interpreter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deny-guard блокирует файловую операцию до реального доступа к ФС.
func TestNewFileFunctions_GuardBlocks(t *testing.T) {
	deny := FileGuard(func() error { return errors.New("файлы запрещены") })
	m := NewFileFunctions(deny)
	msg := callBuiltinExpectPanic(t, m["копироватьфайл"], []any{"a.txt", "b.txt"})
	if !strings.Contains(msg, "файлы запрещены") {
		t.Errorf("ожидалось сообщение guard'а, получено %q", msg)
	}
}

// nil-guard не блокирует: копирование реального файла проходит.
func TestNewFileFunctions_NilGuardAllows(t *testing.T) {
	SetFileSandbox("")
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "b.txt")
	fn, ok := NewFileFunctions(nil)["копироватьфайл"].(BuiltinFunc)
	if !ok {
		t.Fatal("копироватьфайл должна быть BuiltinFunc")
	}
	if _, err := fn([]any{src, dst}, "", 0); err != nil {
		t.Fatalf("nil-guard не должен блокировать: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("файл должен быть скопирован: %v", err)
	}
}
```

- [ ] **Step 2: Запустить тест — убедиться, что не компилируется/падает**

Run: `go test ./internal/dsl/interpreter/ -run TestNewFileFunctions -count=1`
Expected: FAIL — `NewFileFunctions` принимает 0 аргументов, тип `FileGuard` не объявлен, в `m` нет ключа `копироватьфайл`.

- [ ] **Step 3: Реализовать FileGuard + checkFile + гейт в NewFileFunctions**

В `internal/dsl/interpreter/file_builtins.go` добавить перед `NewFileFunctions` тип и хелпер:

```go
// FileGuard вызывается перед каждой файловой операцией. nil → без ограничений.
type FileGuard func() error

// checkFile паникует userError'ом, если guard запрещает файловые операции.
// Сообщение человеческое и ловится Попыткой (как checkNet, план 62).
func checkFile(guard FileGuard) {
	if guard == nil {
		return
	}
	if err := guard(); err != nil {
		panic(userError{Msg: err.Error()})
	}
}

// guardedFile оборачивает файловый builtin проверкой guard'а.
func guardedFile(guard FileGuard, fn BuiltinFunc) BuiltinFunc {
	return func(args []any, file string, line int) (any, error) {
		checkFile(guard)
		return fn(args, file, line)
	}
}
```

Заменить сигнатуру и тело `NewFileFunctions` на:

```go
func NewFileFunctions(guard FileGuard) map[string]any {
	m := map[string]any{}

	textReaderFactory := func(args []any) any {
		checkFile(guard)
		return &dslTextReader{path: strArg(args, 0)}
	}
	textWriterFactory := func(args []any) any {
		checkFile(guard)
		return &dslTextWriter{path: strArg(args, 0)}
	}
	fileFactory := func(args []any) any {
		checkFile(guard)
		return &dslFile{path: strArg(args, 0)}
	}

	m["__factory_ЧтениеТекста"] = textReaderFactory
	m["__factory_TextReader"] = textReaderFactory
	m["__factory_ЗаписьТекста"] = textWriterFactory
	m["__factory_TextWriter"] = textWriterFactory
	m["__factory_Файл"] = fileFactory
	m["__factory_File"] = fileFactory

	m["декодироватьфайл"] = guardedFile(guard, decodeFileBuiltin)
	m["decodefile"] = guardedFile(guard, decodeFileBuiltin)

	// Процедурные файловые builtins (глобально зарегистрированы в
	// builtins_files.go) перекрываются здесь обёрткой с guard'ом: extraVars
	// разрешаются раньше глобальной карты builtins (interpreter.go:619).
	m["копироватьфайл"] = guardedFile(guard, copyFileFn)
	m["copyfile"] = guardedFile(guard, copyFileFn)
	m["переместитьфайл"] = guardedFile(guard, moveFileFn)
	m["movefile"] = guardedFile(guard, moveFileFn)
	m["удалитьфайлы"] = guardedFile(guard, deleteFileFn)
	m["deletefiles"] = guardedFile(guard, deleteFileFn)
	m["создатькаталог"] = guardedFile(guard, makeDirFn)
	m["createdirectory"] = guardedFile(guard, makeDirFn)
	m["найтифайлы"] = guardedFile(guard, findFilesFn)
	m["findfiles"] = guardedFile(guard, findFilesFn)

	return m
}
```

> Примечание: `decodeFileBuiltin`, `copyFileFn`, `moveFileFn`, `deleteFileFn`,
> `makeDirFn`, `findFilesFn` — существующие функции пакета (`file_builtins.go`,
> `builtins_files.go`), совместимые с `BuiltinFunc`.

- [ ] **Step 4: Обновить двух вызывающих**

В `internal/dsl/interpreter/builtins.go:414` заменить `NewFileFunctions(),` на `NewFileFunctions(nil),`.

В `internal/dslvars/dslvars.go:71` заменить `for k, v := range interpreter.NewFileFunctions() {` на `for k, v := range interpreter.NewFileFunctions(nil) {`.

- [ ] **Step 5: Запустить тест — убедиться, что проходит**

Run: `go test ./internal/dsl/interpreter/ -run TestNewFileFunctions -count=1`
Expected: PASS (оба теста).

- [ ] **Step 6: Прогнать пакет целиком (нет регрессий)**

Run: `go test ./internal/dsl/interpreter/ ./internal/dslvars/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/dsl/interpreter/file_builtins.go internal/dsl/interpreter/builtins.go internal/dslvars/dslvars.go internal/dsl/interpreter/sandbox_guard_test.go
git commit -m "feat(interpreter): FileGuard — per-run гейт файловых builtins (план 57, этап 0)"
```

---

## Task 2: Ресурсные лимиты (wall-clock + итерации) и RunSandboxed

**Files:**
- Create: `internal/dsl/interpreter/sandbox.go`
- Modify: `internal/dsl/interpreter/env.go`
- Modify: `internal/dsl/interpreter/interpreter.go` (execBlock + WhileStmt + NumericForStmt)
- Test: `internal/dsl/interpreter/sandbox_test.go` (создать, package `interpreter_test`)

- [ ] **Step 1: Написать падающий тест**

Создать `internal/dsl/interpreter/sandbox_test.go`:

```go
package interpreter_test

import (
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
	assert.EqualValues(t, 500500, result)
}
```

- [ ] **Step 2: Запустить тест — убедиться, что не компилируется**

Run: `go test ./internal/dsl/interpreter/ -run TestSandbox_ -count=1`
Expected: FAIL — нет типа `interpreter.SandboxProfile` и метода `RunSandboxed`.

- [ ] **Step 3: Добавить поля и методы в execCtx**

В `internal/dsl/interpreter/env.go` заменить блок импорта `import "strings"` на:

```go
import (
	"strings"
	"time"
)
```

В том же файле в структуру `execCtx` добавить два поля (после `debug`):

```go
	deadline     time.Time // wall-clock запуска; zero = без лимита
	maxLoopIters int       // потолок итераций цикла; 0 = maxWhileIter
```

И добавить методы после определения `execCtx`:

```go
// loopLimit — действующий потолок итераций цикла для запуска.
func (ec *execCtx) loopLimit() int {
	if ec.maxLoopIters > 0 {
		return ec.maxLoopIters
	}
	return maxWhileIter
}

// checkDeadline жёстко останавливает запуск (dslStop, мимо Попытки), если
// исчерпан wall-clock. Дёшево, когда дедлайн не задан.
func (ec *execCtx) checkDeadline() {
	if !ec.deadline.IsZero() && time.Now().After(ec.deadline) {
		panic(dslStop{err: errSandboxTimeout})
	}
}
```

- [ ] **Step 4: Создать sandbox.go с профилем, ошибками и RunSandboxed**

Создать `internal/dsl/interpreter/sandbox.go`:

```go
package interpreter

import (
	"errors"
	"time"

	"github.com/ivantit66/onebase/internal/dsl/ast"
)

var (
	errSandboxTimeout = errors.New("превышено максимальное время выполнения (песочница)")
	errSandboxIters   = errors.New("превышен лимит итераций цикла (песочница)")
)

// SandboxProfile описывает, что разрешено одному запуску DSL. Нулевое значение =
// «всё разрешено» = поведение по умолчанию (без регрессии).
type SandboxProfile struct {
	AllowNet     bool          // сеть: HTTP-клиент и email
	AllowFile    bool          // файловые builtins
	MaxWallClock time.Duration // 0 = без лимита времени
	MaxLoopIters int           // 0 = дефолт (maxWhileIter)
}

// RestrictedProfile — строгий профиль для недоверенного кода (ИИ/marketplace).
func RestrictedProfile() SandboxProfile {
	return SandboxProfile{
		AllowNet:     false,
		AllowFile:    false,
		MaxWallClock: 10 * time.Second,
		MaxLoopIters: 1_000_000,
	}
}

// RunSandboxed исполняет процедуру с ресурсными лимитами профиля (wall-clock и
// итерации). Запреты возможностей (сеть/файлы) подаются вызывающим через
// extraVars (см. SandboxProfile.Vars). Возвращаемое значение — в result.
func (i *Interpreter) RunSandboxed(proc *ast.ProcedureDecl, this This, p SandboxProfile, result *any, extraVars ...map[string]any) (err error) {
	e := i.startEnv(this)
	if p.MaxWallClock > 0 {
		e.ec.deadline = time.Now().Add(p.MaxWallClock)
	}
	e.ec.maxLoopIters = p.MaxLoopIters
	defer func() {
		if r := recover(); r != nil {
			switch s := r.(type) {
			case dslStop:
				err = s.err
			case userError:
				err = &DSLError{File: e.ec.curFile, Line: e.ec.curLine, Msg: s.Msg, Err: s.Err}
			case dslReturn:
				if result != nil {
					*result = s.val
				}
			default:
				panic(r)
			}
		}
	}()
	for _, m := range extraVars {
		for k, v := range m {
			e.set(k, v)
		}
	}
	i.execBlock(proc.Body, e)
	return nil
}
```

- [ ] **Step 5: Подключить чеки в execBlock и циклы**

В `internal/dsl/interpreter/interpreter.go`, в `execBlock` (начало тела цикла `for _, s := range stmts {`) добавить первой строкой:

```go
		e.ec.checkDeadline()
```

В `WhileStmt` (после `iter++`) заменить блок проверки на:

```go
			iter++
			e.ec.checkDeadline()
			if iter > e.ec.loopLimit() {
				if e.ec.maxLoopIters > 0 {
					panic(dslStop{err: errSandboxIters})
				}
				RaiseUserError("Цикл «Пока»: превышено максимальное число итераций — вероятно, бесконечный цикл")
			}
```

В `NumericForStmt` (после `iter++`) заменить блок проверки на:

```go
			iter++
			e.ec.checkDeadline()
			if iter > e.ec.loopLimit() {
				if e.ec.maxLoopIters > 0 {
					panic(dslStop{err: errSandboxIters})
				}
				RaiseUserError("Цикл «Для»: превышено максимальное число итераций — вероятно, ошибка в границах цикла")
			}
```

- [ ] **Step 6: Запустить тест — убедиться, что проходит**

Run: `go test ./internal/dsl/interpreter/ -run TestSandbox_ -count=1`
Expected: PASS (три теста).

- [ ] **Step 7: Прогнать пакет целиком**

Run: `go test ./internal/dsl/interpreter/ -count=1`
Expected: PASS (существующие тесты циклов/лимитов зелёные — дефолтное поведение `maxWhileIter` сохранено).

- [ ] **Step 8: Commit**

```bash
git add internal/dsl/interpreter/sandbox.go internal/dsl/interpreter/env.go internal/dsl/interpreter/interpreter.go internal/dsl/interpreter/sandbox_test.go
git commit -m "feat(interpreter): ресурсные лимиты запуска + RunSandboxed (план 57, этап 0)"
```

---

## Task 3: SandboxProfile.Vars() — композиция запретов возможностей

**Files:**
- Modify: `internal/dsl/interpreter/sandbox.go`
- Test: `internal/dsl/interpreter/sandbox_test.go` (дополнить)

- [ ] **Step 1: Дописать падающие тесты**

Добавить в `internal/dsl/interpreter/sandbox_test.go`:

```go
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
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result, p.Vars())
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
	err := interpreter.New().RunSandboxed(parseProc(t, src), nil, p, &result, p.Vars())
	require.NoError(t, err)
	assert.Contains(t, result.(string), "сеть запрещена")
}

// При AllowNet/AllowFile профиль не внедряет запретов — нет регрессии.
func TestSandbox_AllowedNoVars(t *testing.T) {
	p := interpreter.SandboxProfile{AllowNet: true, AllowFile: true}
	v := p.Vars()
	_, hasFile := v["копироватьфайл"]
	_, hasMail := v["отправитьписьмо"]
	assert.False(t, hasFile, "при AllowFile не должно быть файловых запретов")
	assert.False(t, hasMail, "при AllowNet не должно быть сетевых запретов")
}
```

- [ ] **Step 2: Запустить тест — убедиться, что не компилируется**

Run: `go test ./internal/dsl/interpreter/ -run TestSandbox_ -count=1`
Expected: FAIL — у `SandboxProfile` нет метода `Vars`.

- [ ] **Step 3: Реализовать Vars()**

Добавить в `internal/dsl/interpreter/sandbox.go` метод (импорт `errors` уже есть):

```go
// Vars возвращает extraVars, навязывающие запреты возможностей профиля
// (сеть/email/файлы). Мержить ПОСЛЕ обычных переменных запуска, чтобы deny-
// guard'ы перекрыли стандартные функции. Разрешённые возможности не внедряются —
// остаются обычные функции (с глобальным предохранителем сети, план 62).
func (p SandboxProfile) Vars() map[string]any {
	m := map[string]any{}
	if !p.AllowNet {
		deny := NetGuard(func() error { return errors.New("сеть запрещена в этом режиме (песочница)") })
		for k, v := range NewHTTPFunctions(deny) {
			m[k] = v
		}
		for k, v := range NewEmailFunctions(nil, deny) {
			m[k] = v
		}
	}
	if !p.AllowFile {
		deny := FileGuard(func() error { return errors.New("файловые операции запрещены в этом режиме (песочница)") })
		for k, v := range NewFileFunctions(deny) {
			m[k] = v
		}
	}
	return m
}
```

> Ключи `NewFileFunctions`/`NewEmailFunctions` — в нижнем регистре (`копироватьфайл`,
> `отправитьписьмо`), как ожидают тесты `TestSandbox_AllowedNoVars`.

- [ ] **Step 4: Запустить тест — убедиться, что проходит**

Run: `go test ./internal/dsl/interpreter/ -run TestSandbox_ -count=1`
Expected: PASS (все тесты Sandbox_).

- [ ] **Step 5: Commit**

```bash
git add internal/dsl/interpreter/sandbox.go internal/dsl/interpreter/sandbox_test.go
git commit -m "feat(interpreter): SandboxProfile.Vars — запреты сети/файлов по профилю (план 57, этап 0)"
```

---

## Task 4: Верификация и статус плана

**Files:**
- Modify: `Plans/57-stage0-sandbox-profile.md` (отметить статус)
- Modify: `Plans/README.md` (строка плана 57 — отметить этап 0)

- [ ] **Step 1: Полный прогон тестов и vet**

Run: `go test ./... -count=1`
Expected: PASS (без регрессий).

Run: `go vet ./...`
Expected: чисто (нет вывода).

- [ ] **Step 2: Сборка основного бинаря**

Run: `go build -o onebase.exe ./cmd/onebase`
Expected: успешная сборка (предварительно остановить запущенный сервер: `taskkill /IM onebase.exe /F`, если залочен).

- [ ] **Step 3: Обновить статус в дизайн-доке**

В `Plans/57-stage0-sandbox-profile.md` заменить строку `**Статус:** дизайн утверждён, ожидает плана реализации` на `**Статус:** ✅ Реализовано (этап 0)`.

- [ ] **Step 4: Обновить строку плана 57 в индексе**

В `Plans/README.md` в строке плана 57 (направление З) заменить `⬜ Не начато` на `🟡 Этап 0 (песочница) реализован`.

- [ ] **Step 5: Commit**

```bash
git add Plans/57-stage0-sandbox-profile.md Plans/README.md
git commit -m "docs(plans): этап 0 плана 57 (песочница) реализован"
```

---

## Self-Review

**Spec coverage:**
- `SandboxProfile` (AllowNet/AllowFile/MaxWallClock/MaxLoopIters) + `RestrictedProfile` → Task 2.
- `FileGuard` (зеркало `NetGuard`) + per-run гейт файлов → Task 1.
- Ресурсы в `execCtx`, чек в `execBlock`/циклах → Task 2.
- Жёсткий непойманный таймаут (`dslStop`, мимо `Попытки`) → Task 2 (тест `WallClockHardStop`).
- Ловимые запреты возможностей (`userError`) → Task 1 (guard) + Task 3 (`FileDeniedCatchable`/`NetDeniedCatchable`).
- Нулевая регрессия (профиль по умолчанию) → Task 2 (`NoProfileNoRegression`), Task 3 (`AllowedNoVars`), полный прогон Task 4.
- `SandboxProfile.Vars()` (профиль → guard'ы) → Task 3.
- YAGNI: лимит памяти на коллекции не реализуется (соответствует дизайну).

**Placeholder scan:** заглушек нет; весь код приведён целиком, команды и ожидаемый результат указаны.

**Type consistency:** `FileGuard`/`checkFile`/`guardedFile` (Task 1) используются в `NewFileFunctions(guard)` (Task 1) и `Vars()` (Task 3); `SandboxProfile`/`RunSandboxed`/`errSandboxTimeout`/`errSandboxIters` (Task 2) согласованы с чеками `checkDeadline`/`loopLimit` (Task 2). Ключи файловых/почтовых функций — нижний регистр во всех задачах.
