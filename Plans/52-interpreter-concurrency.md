# План 52 — Потокобезопасность DSL-интерпретатора

**Статус:** ✅ Реализовано (2026-06-10, ветка `fix/interpreter-race`)

> **Как реализовано.** Вариант A: состояние запуска (`curFile/curLine` + debug hook)
> вынесено в `execCtx`, живущий в цепочке `*env` (`env.ec`, наследуется `child()`).
> Поле `Interpreter.DebugHook` заменено на `DebugSource func() DebugHook` —
> устанавливается однократно в `ui.New` и читает текущую сессию из
> `GlobalDebugController` (мьютекс); hook захватывается на старте каждого
> `Run/Call/RunWithResult` в его `execCtx`. `debug_handlers` больше не мутируют
> интерпретатор. Отступление от плана: семантика отладчика осталась глобальной
> (hook видят все запуски, пока отладка включена) — это записанное решение проекта
> («глобальный отладчик, единые брейкпоинты»); per-user-session фильтрация — отдельная
> фича с выбором сессий как в 1С, не входит в фикс гонки. Race-тесты:
> `internal/dsl/interpreter/concurrency_test.go` (4 теста, проверены под `-race`
> локально на Windows — RED на старом коде подтверждён, GREEN после фикса).
**Источник:** `docs/history/АнализПроекта-2026-06-10.md` §2.1 (подтверждено чтением кода).
**Приоритет:** 🔴 Критический — единственная серьёзная техническая проблема.

---

## Контекст

Сервер создаёт **один** интерпретатор на старте и разделяет его между всеми HTTP-запросами:

```go
// cli/run.go:214
interp := interpreter.New()
// ...передаётся в api.New → ui.New → Server.interp и entityservice.Service.Interp
```

При этом `*Interpreter` хранит **изменяемое состояние**, которое пишется на **каждом операторе** без синхронизации:

```go
// internal/dsl/interpreter/interpreter.go:56-74
type Interpreter struct {
    ...
    DebugHook DebugHook  // одно поле на ВСЕ сессии
    curFile   string     // last executed statement location
    curLine   int
}

// :167-178
func (i *Interpreter) execBlock(stmts []ast.Stmt, e *env) {
    for _, s := range stmts {
        if loc := getLocation(s); loc != nil {
            i.curFile = loc.File   // ← data race при конкурентных запросах
            i.curLine = loc.Line
        }
        ...
```

**Путь срабатывания — горячий, не только ИИ.** Общий `interp` исполняет хуки документов
и форм на конкурентных запросах:
- `entityservice.Service.Save` → `s.Interp.Run(proc, obj, vars)` (`service.go:175`) — **проведение/запись документа**;
- `Service.Fill` → `s.Interp.Run(...)` (`service.go:390`) — ввод на основании;
- события управляемых форм (`handlers_managed_events.go:194,619,674`);
- отчёты/обработки/консоль кода (`handlers.go:1472,1640,2147,2200,3887`).

### Последствия

1. **Возможен краш рантайма.** Запись `string` (ptr+len — два слова) не атомарна.
   Конкурентная запись может склеить `ptr` одной записи с `len` другой → чтение за
   границей памяти → паника рантайма, а не просто неверный `File:Line` в `DSLError`.
2. **Утечка отладки между сессиями.** `DebugHook` глобален
   (`debug_handlers.go:55 s.interp.DebugHook = sess`): один пользователь включил
   отладку → брейкпоинты и пошаговое исполнение срабатывают во всех чужих сессиях.

### Почему не поймали

Desktop single-user исполняет запросы строго последовательно; тесты тоже
последовательны; на Windows `-race` требует CGo (которого в сборке нет) → race-детектор
**не запускался ни разу**. Проблема выстрелит при включении многопользовательского режима.

---

## Решение

Состояние исполнения вынести из общего `Interpreter` в **per-call контекст**.
Окружение `*env` уже создаётся заново на каждый `Run/Call/RunWithResult` (`newEnv(this)`)
— это естественное место для изоляции.

### Вариант A (рекомендуемый): execution context в `*env`

1. Завести лёгкую структуру состояния вызова и носить её через `env` (или отдельным
   параметром `execBlock`/`execStmt`):

```go
// новый тип — состояние одного запуска DSL
type execCtx struct {
    curFile  string
    curLine  int
    debug    DebugHook   // per-call, не глобальный
    depth    int         // глубина рекурсии (тоже per-call, см. ниже)
}
```

2. `Run/Call/RunWithResult` создают `execCtx` локально и прокидывают его. `DSLError`
   берёт `File/Line` из `execCtx`, а не из `i.curFile/i.curLine`.

3. `DebugHook` передаётся в запуск (например, `Interpreter.RunWithDebug(proc, this, hook, vars)`
   или поле `execCtx.debug`), а не присваивается полю общего интерпретатора. Сам
   `Interpreter` становится **неизменяемым** после конфигурирования (только `LookupProc`,
   `MaxRecursionDepth` — read-only при исполнении).

### Что трогаем

| Файл | Изменение |
|---|---|
| `internal/dsl/interpreter/interpreter.go` | убрать `curFile/curLine/DebugHook` из структуры; добавить `execCtx`; прокинуть через `execBlock/execStmt/evalExpr`; `DSLError` из `execCtx` |
| `internal/dsl/interpreter/*.go` (builtins, control flow) | подмена `i.curLine` → `ec.curLine` (механически; ~30-50 точек) |
| `internal/ui/debug_handlers.go:55,68,176` | `s.interp.DebugHook = sess` → передача hook в конкретный запуск/сессию |
| `internal/debugger/controller.go` | привязка hook к сессии исполнения, а не к глобальному интерпретатору |

> **Объём.** Механическая, но широкая правка (интерпретатор пронизывает `i.curLine`).
> Делать атомарным рефакторингом с прогоном всех тестов после.

### Вариант B (дешевле в моменте, хуже архитектурно)

Создавать `Interpreter` на каждый запрос (структура крошечная). Но `LookupProc`/
`MaxRecursionDepth` придётся переустанавливать, а `DebugHook` всё равно надо привязывать
к сессии — то есть половина работы варианта A. **Не рекомендуется.**

---

## Тесты

1. **Конкурентный race-тест** (главный): N=50 горутин гоняют `interp.Run` по одному
   интерпретатору с разными процедурами/файлами; запуск под `-race`. Должен падать на
   текущем коде и проходить после фикса.

```go
// internal/dsl/interpreter/concurrency_test.go
func TestInterpreterConcurrentRun(t *testing.T) {
    interp := New()
    interp.LookupProc = ...
    var wg sync.WaitGroup
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func(n int) { defer wg.Done()
            for j := 0; j < 100; j++ { _ = interp.Run(procs[n%len(procs)], thisN(n)) }
        }(i)
    }
    wg.Wait() // под `go test -race` — ловит гонку curFile/curLine
}
```

2. **Изоляция DebugHook**: включить отладку в «сессии A», проверить, что «сессия B»
   (без hook) не дёргает брейкпоинты.

3. **Регрессия `DSLError`**: ошибка из вложенного вызова сохраняет корректные `File:Line`
   (что и было целью полей) — теперь из `execCtx`.

---

## Verification

1. `go test -race ./internal/dsl/...` — зелёный (на Linux/в CI; локально на Windows без CGo
   race-детектор недоступен — см. план 56, CI).
2. Ручной сценарий: два браузера, оба проводят документы с тяжёлым `ОбработкаПроведения`
   одновременно (нагрузить циклом) — ошибок локации/крашей нет.
3. Отладка: включить точку останова в одной сессии, во второй сессии провести документ —
   вторая не «залипает» на брейкпоинте.

---

## Связанные

- План 56 — добавляет `go test -race` в CI (без него фикс нельзя проверить машинно на Windows).
- Память: «Ограничения рекурсии в DSL — maxCallDepth» — глубину рекурсии тоже стоит
  перенести в `execCtx` (сейчас, если она поле интерпретатора, это вторая гонка).

## Эстимейт

- Рефакторинг `execCtx` + прокидка: **1 день**.
- DebugHook per-session + правка debugger: **0.5 дня**.
- Конкурентный race-тест + регрессии: **0.5 дня**.
- **Итого ≈ 2 дня.**
